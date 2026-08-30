// Package wideslog collects slog records into request-scoped wide events.
package wideslog

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// TimestampMode controls how timestamps are recorded for individual events.
type TimestampMode uint8

const (
	// TimestampNone omits timestamps from individual events.
	TimestampNone TimestampMode = iota

	// TimestampAbsolute records each event's wall-clock timestamp.
	TimestampAbsolute

	// TimestampOffset records each event's elapsed time from the wide event start.
	TimestampOffset
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
	TimestampMode TimestampMode
	OffsetUnit    OffsetUnit
}

// Option configures an Event created by Start.
type Option func(*Config)

// WithTimestampMode sets how individual event timestamps are represented.
func WithTimestampMode(mode TimestampMode) Option {
	return func(c *Config) {
		c.TimestampMode = mode
	}
}

// WithOffsetUnit sets the unit used when TimestampOffset is enabled.
func WithOffsetUnit(unit OffsetUnit) Option {
	return func(c *Config) {
		c.OffsetUnit = unit
	}
}

// NewConfig returns the default configuration with options applied.
func NewConfig(options ...Option) Config {
	cfg := Config{
		TimestampMode: TimestampOffset,
		OffsetUnit:    OffsetMicroseconds,
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
	scoped [][]slog.Attr
	groups []string
	attrs  []slog.Attr
	events []eventRecord
	ended  bool
}

type eventRecord struct {
	time    time.Time
	offset  time.Duration
	level   slog.Level
	message string
	attrs   []slog.Attr
}

// Start begins collecting records for a request-scoped wide event.
//
// The returned context contains the Event. Any slog call made with this
// context is captured by a wideslog Handler.
func Start(
	ctx context.Context,
	logger *slog.Logger,
	options ...Option,
) (context.Context, *Event) {
	if ctx == nil {
		ctx = context.Background()
	}

	if logger == nil {
		logger = slog.Default()
	}

	handler := logger.Handler()

	event := &Event{
		output: unwrap(handler),
		config: NewConfig(options...),
		start:  time.Now(),
	}

	if wide, ok := handler.(*Handler); ok {
		event.scoped = wide.attrs
		event.groups = wide.groups
	} else {
		event.scoped = [][]slog.Attr{nil}
	}

	return context.WithValue(ctx, contextKey{}, event), event
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

// End emits the accumulated wide event.
//
// End is idempotent. Only the first call emits the event.
func (e *Event) End(
	ctx context.Context,
	level slog.Level,
	msg string,
	attrs ...slog.Attr,
) error {
	if ctx == nil {
		ctx = context.Background()
	}

	e.mu.Lock()

	if e.ended {
		e.mu.Unlock()
		return nil
	}

	e.ended = true

	rootAttrs := make([]slog.Attr, 0, len(e.attrs)+len(attrs)+3)
	rootAttrs = append(rootAttrs, e.attrs...)
	rootAttrs = append(rootAttrs, attrs...)

	events := slices.Clone(e.events)
	start := e.start

	rootAttrs = append(
		rootAttrs,
		slog.Time("timestamp", start),
		slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		slog.Int("event_count", len(events)),
	)

	e.mu.Unlock()

	if !e.output.Enabled(ctx, level) {
		return nil
	}

	rootAttrs = append(
		rootAttrs,
		slog.Any("events", e.eventsValue(events)),
	)

	record := slog.NewRecord(time.Now(), level, msg, 0)
	record.AddAttrs(scopedAttrs(e.scoped, e.groups, rootAttrs)...)

	return e.output.Handle(ctx, record)
}

func (e *Event) eventsValue(events []eventRecord) []any {
	values := make([]any, 0, len(events))

	for _, event := range events {
		attrs := make([]slog.Attr, 0, len(event.attrs)+3)

		switch e.config.TimestampMode {
		case TimestampAbsolute:
			attrs = append(
				attrs,
				slog.Time("timestamp", event.time),
			)

		case TimestampOffset:
			attrs = append(
				attrs,
				slog.Int64(
					"offset_"+e.config.OffsetUnit.String(),
					e.config.OffsetUnit.convert(event.offset),
				),
			)
		}

		attrs = append(
			attrs,
			slog.String("level", event.level.String()),
			slog.String("msg", event.message),
		)

		attrs = append(attrs, event.attrs...)

		values = append(values, attrsToMap(attrs))
	}

	return values
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
func NewHandler(next slog.Handler) *Handler {
	if next == nil {
		panic("wideslog: nil handler")
	}

	return &Handler{
		next:     next,
		fallback: next,
		attrs:    [][]slog.Attr{nil},
	}
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
// Levels the wrapped handler rejects are not buffered; filtering is enforced
// when the record arrives, matching what slog would do without an Event.
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

	attrs := scopedAttrs(h.attrs, h.groups, recordAttrs(record))

	// Resolve LogValuer values while the event's context is still active.
	// This mirrors slog's normal record processing semantics and avoids
	// storing a LogValuer whose value might change later.
	attrs = resolveAttrs(attrs)

	event.mu.Lock()

	if event.ended {
		event.mu.Unlock()
		return nil
	}

	event.events = append(event.events, eventRecord{
		time:    record.Time,
		offset:  time.Since(event.start),
		level:   record.Level,
		message: record.Message,
		attrs:   attrs,
	})

	event.mu.Unlock()

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
func New(next slog.Handler) *slog.Logger {
	return slog.New(NewHandler(next))
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

func appendScopedAttrs(scopes [][]slog.Attr, attrs []slog.Attr) [][]slog.Attr {
	result := make([][]slog.Attr, len(scopes))
	for i, scope := range scopes {
		result[i] = slices.Clone(scope)
	}
	result[len(result)-1] = append(result[len(result)-1], attrs...)
	return result
}

func scopedAttrs(attrs [][]slog.Attr, groups []string, record []slog.Attr) []slog.Attr {
	for i, group := range slices.Backward(groups) {
		groupAttrs := append(slices.Clone(attrs[i+1]), record...)
		record = []slog.Attr{slog.Group(group, attrsToAny(groupAttrs)...)}
	}
	return append(slices.Clone(attrs[0]), record...)
}

func recordAttrs(record slog.Record) []slog.Attr {
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	return attrs
}

func attrsToAny(attrs []slog.Attr) []any {
	out := make([]any, len(attrs))

	for i, attr := range attrs {
		out[i] = attr
	}

	return out
}

func resolveAttrs(
	attrs []slog.Attr,
) []slog.Attr {
	resolved := make([]slog.Attr, 0, len(attrs))

	for _, attr := range attrs {
		resolved = append(
			resolved,
			resolveAttr(attr),
		)
	}

	return resolved
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

func attrsToMap(attrs []slog.Attr) map[string]any {
	values := make(map[string]any, len(attrs))

	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}

		values[attr.Key] = valueToAny(attr.Value)
	}

	return values
}

func valueToAny(value slog.Value) any {
	value = value.Resolve()

	switch value.Kind() {
	case slog.KindBool:
		return value.Bool()

	case slog.KindDuration:
		return value.Duration()

	case slog.KindFloat64:
		return value.Float64()

	case slog.KindInt64:
		return value.Int64()

	case slog.KindString:
		return value.String()

	case slog.KindTime:
		return value.Time()

	case slog.KindUint64:
		return value.Uint64()

	case slog.KindGroup:
		return attrsToMap(value.Group())

	default:
		return value.Any()
	}
}
