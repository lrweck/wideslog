// Package wideslog collects slog records from one operation into a single
// structured record: a wide event.
//
// Set up the logger once with New or JSONHandler, then wrap each operation:
//
//	ctx, event := wideslog.NewEvent(ctx, logger, "checkout")
//	event.Add(slog.String("request_id", "req-1"))
//	logger.InfoContext(ctx, "payment authorized")
//	event.End()
//
// Log calls made with the returned context are buffered instead of being
// written immediately. End emits them as one record whose root carries the
// shared context, and the buffered lines live in the events field. Logs made
// without an active event pass through to the wrapped handler unchanged.
// Abort discards the buffered lines without emitting anything.
//
// See the README and the example program for a full walkthrough.
package wideslog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// TimeMode controls how timestamps are recorded for individual events.
type TimeMode uint8

const (
	// TimeNone omits timestamps from individual events.
	TimeNone TimeMode = iota

	// TimeAbsolute records each event's wall-clock timestamp.
	TimeAbsolute

	// TimeOffset records each event's elapsed time from the wide event start.
	TimeOffset
)

// OffsetUnit selects the unit used for relative event timestamps.
type OffsetUnit uint8

const (
	// OffsetNanoseconds records offsets in nanoseconds.
	OffsetNanoseconds OffsetUnit = iota

	// OffsetMicroseconds records offsets in microseconds.
	OffsetMicroseconds

	// OffsetMilliseconds records offsets in milliseconds.
	OffsetMilliseconds
)

// Config defines the timestamp behavior of an Event.
type Config struct {
	TimeMode   TimeMode
	OffsetUnit OffsetUnit
}

// Option configures an Event created by NewEvent.
type Option func(*Config)

// WithTimeMode sets how individual event timestamps are represented.
func WithTimeMode(mode TimeMode) Option {
	return func(c *Config) {
		c.TimeMode = mode
	}
}

// WithOffsetUnit sets the unit used when TimeOffset is enabled.
func WithOffsetUnit(unit OffsetUnit) Option {
	return func(c *Config) {
		c.OffsetUnit = unit
	}
}

// NewConfig returns the default configuration with options applied.
func NewConfig(options ...Option) Config {
	cfg := Config{
		TimeMode:   TimeOffset,
		OffsetUnit: OffsetMicroseconds,
	}

	for _, option := range options {
		option(&cfg)
	}

	return cfg
}

type contextKey struct{}

// Event collects request-scoped slog records into one wide event.
//
// An Event is safe for concurrent use. This is useful when multiple
// goroutines contribute logs to the same request.
type Event struct {
	mu     sync.Mutex
	output slog.Handler
	config Config
	start  time.Time
	ctx    context.Context
	scoped [][]slog.Attr
	groups []string
	msg    string
	attrs  []slog.Attr
	events *[]eventRecord
	ended  bool
}

type eventRecord struct {
	time    time.Time
	offset  time.Duration
	level   slog.Level
	message string
	attrs   *[]slog.Attr
}

// cap 32 covers the typical 5-50 record range in one capped slice without
// per-append growth; larger events grow amortized.
const eventCap = 32

// eventRecordPool reuses the per-Event buffering slice across events. The
// pool holds a pointer so Put never re-boxes the slice into an interface.
// Returns go back in End/Abort; NewEvent pulls one to avoid a fresh
// allocation per request.
var eventRecordPool = sync.Pool{
	New: func() any { s := make([]eventRecord, 0, eventCap); return &s },
}

// eventEntryPool reuses the per-End eventEntry slice. Entries are rebuilt on
// every End, so the backing array is returned once marshaling completes.
// Stored as a pointer to avoid Put boxing allocations.
var eventEntryPool = sync.Pool{
	New: func() any { s := make([]eventEntry, 0, eventCap); return &s },
}

// attrCap covers a typical record's attributes in one capped slice.
const attrCap = 16

// attrSlicePool reuses the []slog.Attr backing each buffered record. A
// pointer is pooled so Put never re-boxes the slice. Slices are owned by the
// event until End, which returns them once marshaling completes. Empty
// records allocate nothing and are never pooled.
var attrSlicePool = sync.Pool{
	New: func() any { s := make([]slog.Attr, 0, attrCap); return &s },
}

// NewEvent begins collecting records for a request-scoped wide event.
//
// msg identifies the operation and becomes the message of the root wide
// record. The returned context contains the Event. Any slog call made with
// this context is captured by a wideslog Handler.
//
// NewEvent panics when logger is not backed by a wideslog Handler (use
// wideslog.New or wideslog.JSONHandler). A logger created with plain slog
// would silently emit records instead of buffering them.
//
// NewEvent must be called on a context without an existing Event. Calling
// it twice stores the inner event in the context and the outer event stops
// collecting records.
func NewEvent(
	ctx context.Context,
	logger *slog.Logger,
	msg string,
	options ...Option,
) (context.Context, *Event) {
	if ctx == nil {
		ctx = context.Background()
	}

	if logger == nil {
		logger = slog.Default()
	}

	handler := logger.Handler()

	wide, ok := handler.(*Handler)
	if !ok {
		panic("wideslog: NewEvent requires a logger backed by a wideslog handler")
	}

	event := &Event{
		output: unwrap(handler),
		config: NewConfig(options...),
		start:  time.Now(),
		msg:    msg,
		events: eventRecordPool.Get().(*[]eventRecord),
	}
	*event.events = (*event.events)[:0]
	event.scoped = wide.attrs
	event.groups = wide.groups

	ctx = context.WithValue(ctx, contextKey{}, event)
	event.ctx = ctx

	return ctx, event
}

// FromContext returns the Event stored in ctx, or nil when none is present.
func FromContext(ctx context.Context) *Event {
	if ctx == nil {
		return nil
	}

	event, _ := ctx.Value(contextKey{}).(*Event)
	return event
}

// Add attaches attributes to the root of the final wide event.
//
// These attributes are not added to individual events.
func (e *Event) Add(attrs ...slog.Attr) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.ended {
		return
	}

	e.attrs = append(e.attrs, attrs...)
}

// End emits the accumulated wide event, using the context captured at
// NewEvent.
//
// End is idempotent. Only the first call emits the event, and no error is
// returned: an output handler that fails to write is treated the same way
// slog treats handler errors, silently.
//
// The root record carries the message passed to NewEvent. Steps logged
// through the event's context, including the final one, live in the events
// field. The root record uses the highest level found in the buffered steps
// (Info when there are none), so handlers that only accept serious levels
// still receive it.
func (e *Event) End() {
	ctx := e.ctx

	e.mu.Lock()

	if e.ended {
		e.mu.Unlock()
		return
	}

	e.ended = true

	msg := e.msg
	scoped := e.scoped
	groups := e.groups

	rootAttrs := make([]slog.Attr, 0, len(e.attrs)+3)
	rootAttrs = append(rootAttrs, e.attrs...)

	eventsPtr := e.events
	records := *eventsPtr
	e.events = nil
	start := e.start

	rootAttrs = append(
		rootAttrs,
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.Int("event_count", len(records)),
	)

	e.mu.Unlock()

	level := maxEventLevel(records)
	if !e.output.Enabled(ctx, level) {
		releaseEvents(records)
		eventRecordPool.Put(eventsPtr)
		return
	}

	// The entries slice is handed to the eventsArray value and marshaled
	// synchronously by the wrapped handler during Handle; all pooled buffers
	// (entries, events, and each record's attrs) are returned once Handle
	// returns.
	entriesPtr := e.eventsValue(records)
	rootAttrs = append(
		rootAttrs,
		slog.Any("events", eventsArray{entries: entriesPtr}),
	)

	record := slog.NewRecord(start, level, msg, 0)
	record.AddAttrs(scopedAttrs(scoped, groups, rootAttrs)...)

	_ = e.output.Handle(ctx, record)

	eventEntryPool.Put(entriesPtr)
	releaseEvents(records)
	eventRecordPool.Put(eventsPtr)
}

// releaseEvents returns each buffered record's attribute slice to the pool.
// Called only from End/Abort, after Handle has finished marshaling.
func releaseEvents(records []eventRecord) {
	for i := range records {
		if attrs := records[i].attrs; attrs != nil {
			attrSlicePool.Put(attrs)
		}
	}
}

// Abort discards the event without emitting it, releasing the buffered
// records. Like End it is idempotent; any Add after it is ignored, and logs
// made through the event's context fall through to the wrapped handler.
func (e *Event) Abort() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.ended = true
	if e.events != nil {
		releaseEvents(*e.events)
		eventRecordPool.Put(e.events)
		e.events = nil
	}
	e.attrs = nil
}

func maxEventLevel(events []eventRecord) slog.Level {
	level := slog.LevelInfo

	for _, event := range events {
		if event.level > level {
			level = event.level
		}
	}

	return level
}

func (e *Event) eventsValue(records []eventRecord) *[]eventEntry {
	entries := eventEntryPool.Get().(*[]eventEntry)
	*entries = (*entries)[:0]

	for _, record := range records {
		*entries = append(*entries, eventEntry{
			record: record,
			config: e.config,
		})
	}

	return entries
}

func reservedEventKey(key string) bool {
	switch key {
	case "level", "msg", "time":
		return true
	}
	return strings.HasPrefix(key, "offset_")
}

func (u OffsetUnit) String() string {
	switch u {
	case OffsetNanoseconds:
		return "ns"
	case OffsetMicroseconds:
		return "us"
	case OffsetMilliseconds:
		return "ms"
	default:
		return "us"
	}
}

func (u OffsetUnit) convert(d time.Duration) int64 {
	switch u {
	case OffsetNanoseconds:
		return d.Nanoseconds()
	case OffsetMicroseconds:
		return d.Microseconds()
	case OffsetMilliseconds:
		return d.Milliseconds()
	default:
		return d.Microseconds()
	}
}

// Handler buffers slog records when an Event is present in the context.
//
// When no Event is present, it behaves like the wrapped slog.Handler.
type Handler struct {
	next     slog.Handler
	fallback slog.Handler
	attrs    [][]slog.Attr
	groups   []string
}

var _ slog.Handler = (*Handler)(nil)

// NewHandler returns a Handler that wraps next.
//
// It returns an error when next is nil. Use New or JSONHandler for the
// convenience APIs, which panic on a nil handler the way slog.New does.
func NewHandler(next slog.Handler) (*Handler, error) {
	if next == nil {
		return nil, errors.New("wideslog: nil handler")
	}

	return &Handler{
		next:     next,
		fallback: next,
		attrs:    [][]slog.Attr{nil},
	}, nil
}

// Enabled reports whether the wrapped handler accepts level.
//
// This preserves normal slog filtering behavior.
func (h *Handler) Enabled(
	ctx context.Context,
	level slog.Level,
) bool {
	return h.fallback.Enabled(ctx, level)
}

// Handle buffers record in the active Event or forwards it to the wrapped
// handler when no Event exists.
//
// A record reaching an Event that has already ended (via End or Abort) is
// forwarded to the wrapped handler so it is emitted as a standalone log
// instead of being silently discarded.
//
// Levels the wrapped handler rejects are not buffered; filtering is enforced
// when the record arrives, matching what slog would do without an Event.
//
// Only the attributes the logging handler adds beyond the Event's root scopes
// are attached to the buffered record: shared attributes are stripped from the
// matching scopes, so the group skeleton stays but the shared context is
// written once at the root.
func (h *Handler) Handle(
	ctx context.Context,
	record slog.Record,
) error {
	event := FromContext(ctx)

	if event == nil {
		return h.fallback.Handle(ctx, record)
	}

	if !h.fallback.Enabled(ctx, record.Level) {
		return nil
	}

	raw := recordAttrs(record)

	var rawSlice []slog.Attr
	if raw != nil {
		rawSlice = *raw
	}

	scopes := h.attrs
	if len(event.scoped) > 0 {
		scopes = make([][]slog.Attr, len(h.attrs))
		for i := range h.attrs {
			if i < len(event.scoped) && len(event.scoped[i]) > 0 {
				scopes[i] = minusShared(h.attrs[i], event.scoped[i])
			} else {
				scopes[i] = h.attrs[i]
			}
		}
	}
	attrsSlice := scopedAttrs(scopes, h.groups, rawSlice)

	// Resolve LogValuer values while the event's context is still active.
	// This mirrors slog's normal record processing semantics and avoids
	// storing a LogValuer whose value might change later.
	resolveAttrs(attrsSlice)

	// Store as a pool-backed pointer. When scopedAttrs returned the raw
	// buffer untouched (the common case), reuse its pointer; when it rebuilt,
	// wrap the rebuilt slice in a fresh pooled pointer.
	var stored *[]slog.Attr
	if raw != nil && sameBacking(*raw, attrsSlice) {
		stored = raw
		*stored = attrsSlice
	} else {
		stored = attrSlicePool.Get().(*[]slog.Attr)
		*stored = attrsSlice
	}

	event.mu.Lock()

	if event.ended {
		event.mu.Unlock()
		// The event is no longer collecting. Fall through to the wrapped
		// handler, releasing any pooled attribute buffers we just built.
		putAttrs(raw, stored)
		return h.fallback.Handle(ctx, record)
	}

	*event.events = append(*event.events, eventRecord{
		time:    record.Time,
		offset:  time.Since(event.start),
		level:   record.Level,
		message: record.Message,
		attrs:   stored,
	})

	event.mu.Unlock()

	// If scopedAttrs rebuilt the buffer, the pooled raw buffer is now
	// orphaned; the stored attrs buffer is returned to the pool in End.
	if raw != nil && stored != raw {
		attrSlicePool.Put(raw)
	}

	return nil
}

// WithAttrs returns a child handler with attrs attached to subsequent records.
//
// The parent handler is never modified, matching slog.Handler semantics.
func (h *Handler) WithAttrs(
	attrs []slog.Attr,
) slog.Handler {
	return &Handler{
		next:     h.next,
		fallback: h.fallback.WithAttrs(attrs),
		attrs:    appendScopedAttrs(h.attrs, attrs),
		groups:   slices.Clone(h.groups),
	}
}

// WithGroup returns a child handler that nests subsequent attributes under name.
//
// Groups are scoped to this logger and its descendants.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &Handler{
		next:     h.next,
		fallback: h.fallback.WithGroup(name),
		attrs:    append(slices.Clone(h.attrs), nil),
		groups:   append(slices.Clone(h.groups), name),
	}
}

// New returns a logger backed by a wide-event Handler.
//
// New panics when next is nil, mirroring slog.New which also requires a
// handler. NewHandler, which New wraps, reports the invalid handler as an
// error instead.
func New(next slog.Handler) *slog.Logger {
	h, err := NewHandler(next)
	if err != nil {
		panic(err)
	}

	return slog.New(h)
}

// JSONHandler returns a logger backed by slog's JSONHandler.
func JSONHandler(
	w io.Writer,
	opts *slog.HandlerOptions,
) *slog.Logger {
	return New(slog.NewJSONHandler(w, opts))
}

func unwrap(h slog.Handler) slog.Handler {
	for {
		if wide, ok := h.(*Handler); ok {
			h = wide.next
			continue
		}

		return h
	}
}

// minusShared returns scope without the attributes that also appear in shared
// (compared by key and resolved value), so those attributes are not written
// again inside every buffered step.
func minusShared(scope, shared []slog.Attr) []slog.Attr {
	if len(shared) == 0 || len(scope) == 0 {
		return scope
	}

	out := make([]slog.Attr, 0, len(scope))
	for _, attr := range scope {
		value := attr.Value.Resolve()
		keep := true

		for _, s := range shared {
			if attr.Key == s.Key && reflect.DeepEqual(value, s.Value.Resolve()) {
				keep = false
				break
			}
		}

		if keep {
			out = append(out, attr)
		}
	}

	return out
}

func appendScopedAttrs(scopes [][]slog.Attr, attrs []slog.Attr) [][]slog.Attr {
	result := make([][]slog.Attr, len(scopes))
	for i, scope := range scopes {
		result[i] = slices.Clone(scope)
	}
	result[len(result)-1] = append(result[len(result)-1], attrs...)
	return result
}

func scopedAttrs(attrs [][]slog.Attr, groups []string, record []slog.Attr) []slog.Attr {
	if len(groups) == 0 && len(attrs[0]) == 0 {
		return record
	}

	for i, group := range slices.Backward(groups) {
		groupAttrs := append(slices.Clone(attrs[i+1]), record...)
		record = []slog.Attr{slog.Group(group, attrsToAny(groupAttrs)...)}
	}
	return append(slices.Clone(attrs[0]), record...)
}

func recordAttrs(record slog.Record) *[]slog.Attr {
	if record.NumAttrs() == 0 {
		return nil
	}

	p := attrSlicePool.Get().(*[]slog.Attr)
	*p = (*p)[:0]
	for attr := range record.Attrs {
		*p = append(*p, attr)
	}
	return p
}

// sameBacking reports whether two non-empty slices share their backing array,
// used to tell whether scopedAttrs returned the pooled raw buffer untouched.
func sameBacking(a, b []slog.Attr) bool {
	return len(a) > 0 && len(b) > 0 && &a[0] == &b[0]
}

// putAttrs returns pooled attribute buffers to the pool when an event no
// longer collects, releasing raw (orphaned when scopedAttrs rebuilt) and the
// stored attrs buffer without double-returning the same pointer.
func putAttrs(raw, attrs *[]slog.Attr) {
	if raw != nil && raw != attrs {
		attrSlicePool.Put(raw)
	}
	if attrs != nil {
		attrSlicePool.Put(attrs)
	}
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))

	for i, attr := range attrs {
		out[i] = attr
	}

	return out
}

func resolveAttrs(attrs []slog.Attr) {
	for i := range attrs {
		attrs[i] = resolveAttr(attrs[i])
	}
}

func resolveAttr(
	attr slog.Attr,
) slog.Attr {
	value := attr.Value.Resolve()

	if value.Kind() != slog.KindGroup {
		return slog.Attr{
			Key:   attr.Key,
			Value: value,
		}
	}

	group := value.Group()
	resolved := make([]slog.Attr, 0, len(group))

	for _, child := range group {
		resolved = append(
			resolved,
			resolveAttr(child),
		)
	}

	return slog.Attr{
		Key:   attr.Key,
		Value: slog.GroupValue(resolved...),
	}
}
