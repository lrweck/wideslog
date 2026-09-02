package wideslog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var value map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &value))

	return value
}

func step(t *testing.T, root map[string]any, i int) map[string]any {
	t.Helper()
	events, ok := root["events"].([]any)
	require.True(t, ok, "root has events array")
	require.Less(t, i, len(events), "events index in range")
	return events[i].(map[string]any)
}

type logValuerFunc func() slog.Value

func (f logValuerFunc) LogValue() slog.Value {
	return f()
}

func TestWithGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	customer := logger.WithGroup("customer").With("id", "customer-1", "name", "Alice")
	customer.InfoContext(ctx, "customer loaded")
	event.End()

	got := step(t, decodeLog(t, &buf), 0)["customer"].(map[string]any)
	assert.Equal(t, "customer-1", got["id"])
	assert.Equal(t, "Alice", got["name"])
}

func TestNestedWithGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	debtLogger := logger.
		WithGroup("customer").
		With("id", "customer-1").
		WithGroup("debt").
		With("id", "debt-1")
	debtLogger.InfoContext(ctx, "debt loaded")
	event.End()

	root := decodeLog(t, &buf)
	assert.EqualValues(t, 1, root["event_count"])

	got := step(t, root, 0)
	customer := got["customer"].(map[string]any)
	debt := customer["debt"].(map[string]any)
	assert.Equal(t, "customer-1", customer["id"])
	assert.Equal(t, "debt-1", debt["id"])
}

func TestWithDoesNotLeakToParent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	child := logger.With("customer_id", "123")
	child.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")
	event.End()

	childEvent := step(t, decodeLog(t, &buf), 0)
	parentEvent := step(t, decodeLog(t, &buf), 1)
	assert.Equal(t, "123", childEvent["customer_id"])
	assert.NotContains(t, parentEvent, "customer_id")
}

func TestHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	hd, err := NewHandler(base)
	require.NoError(t, err)
	handler := hd.WithAttrs([]slog.Attr{slog.String("source", "child")})
	logger := slog.New(handler)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "child event")
	event.End()

	// The handler attribute is captured as shared context, written once on
	// the root record and not repeated in the step.
	root := decodeLog(t, &buf)
	assert.Equal(t, "child", root["source"])
	assert.NotContains(t, step(t, root, 0), "source")
}

func TestHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	hd, err := NewHandler(base)
	require.NoError(t, err)
	handler := hd.
		WithGroup("request").
		WithAttrs([]slog.Attr{slog.String("method", "GET")})
	logger := slog.New(handler)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "request received", "path", "/users")
	event.End()

	// The shared group keeps its skeleton on the root record, wrapping the
	// root attributes including the events list itself.
	root := decodeLog(t, &buf)
	rootRequest := root["request"].(map[string]any)
	assert.Equal(t, "GET", rootRequest["method"])
	assert.Contains(t, rootRequest, "events")

	// The step keeps the group skeleton with only its own attribute; the
	// shared method attribute is not written again.
	events := rootRequest["events"].([]any)
	require.Len(t, events, 1)
	request := events[0].(map[string]any)["request"].(map[string]any)
	assert.Equal(t, "/users", request["path"])
	assert.NotContains(t, request, "method")
}

func TestHandlerChildrenDoNotModifyParent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler, err := NewHandler(base)
	require.NoError(t, err)
	child := handler.WithAttrs([]slog.Attr{slog.String("scope", "child")})

	logger := slog.New(handler)
	childLogger := slog.New(child)
	ctx, event := NewEvent(context.Background(), logger, "op")

	childLogger.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")
	event.End()

	childEvent := step(t, decodeLog(t, &buf), 0)
	parentEvent := step(t, decodeLog(t, &buf), 1)
	assert.Equal(t, "child", childEvent["scope"])
	assert.NotContains(t, parentEvent, "scope")
}

func TestLoggerAttrsNotDuplicatedInEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil)).With("request_id", "req-1")

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step")
	logger.With("region", "br").InfoContext(ctx, "child step")
	event.End()

	root := decodeLog(t, &buf)
	assert.Equal(t, "req-1", root["request_id"])

	events := root["events"].([]any)
	for i := range events {
		assert.NotContains(t, events[i].(map[string]any), "request_id", "step %d", i)
	}
	// The child-only attribute stays on its own step.
	assert.Equal(t, "br", step(t, root, 1)["region"])
}

func TestGroupAndDirectAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.WithGroup("http").InfoContext(ctx, "request", "method", "GET", "status", 200)
	event.End()

	httpValue := step(t, decodeLog(t, &buf), 0)["http"].(map[string]any)
	assert.Equal(t, "GET", httpValue["method"])
	assert.EqualValues(t, 200, httpValue["status"])
}

func TestAttrOrderPreserved(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "ordered", "z", 1, "a", 2, "m", 3)
	event.End()

	raw := buf.Bytes()

	// The flags come first, then the attributes in logged order.
	flag := bytes.Index(raw, []byte(`"level":"INFO"`))
	z := bytes.Index(raw, []byte(`"z":1`))
	a := bytes.Index(raw, []byte(`"a":2`))
	m := bytes.Index(raw, []byte(`"m":3`))
	require.True(t, flag != -1 && z != -1 && a != -1 && m != -1, "missing keys in output: %q", raw)
	assert.True(t, flag < z && z < a && a < m, "attribute order not preserved: %q", raw)
}

func TestTimeOffset(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(
		context.Background(), logger, "op",
		WithTimeMode(TimeOffset),
		WithOffsetUnit(OffsetMicroseconds),
	)

	logger.InfoContext(ctx, "hello")
	event.End()

	got := step(t, decodeLog(t, &buf), 0)
	assert.Contains(t, got, "offset_us")
	assert.NotContains(t, got, "time")
}

func TestTimeNone(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op", WithTimeMode(TimeNone))

	logger.InfoContext(ctx, "hello")
	event.End()

	got := step(t, decodeLog(t, &buf), 0)
	assert.NotContains(t, got, "time")
	assert.NotContains(t, got, "offset_us")
}

func TestRootTimeAlwaysPresent(t *testing.T) {
	tests := []struct {
		name string
		mode TimeMode
	}{
		{"none", TimeNone},
		{"absolute", TimeAbsolute},
		{"offset", TimeOffset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(slog.NewJSONHandler(&buf, nil))
			ctx, event := NewEvent(context.Background(), logger, "op", WithTimeMode(tt.mode))

			logger.InfoContext(ctx, "hello")
			event.End()

			root := decodeLog(t, &buf)
			assert.Contains(t, root, "time")
			assert.NotContains(t, root, "timestamp")
		})
	}
}

func TestOffsetUnits(t *testing.T) {
	tests := []struct {
		name string
		unit OffsetUnit
		key  string
	}{
		{"ns", OffsetNanoseconds, "offset_ns"},
		{"us", OffsetMicroseconds, "offset_us"},
		{"ms", OffsetMilliseconds, "offset_ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(slog.NewJSONHandler(&buf, nil))
			ctx, event := NewEvent(context.Background(), logger, "op", WithOffsetUnit(tt.unit))

			logger.InfoContext(ctx, "hello")
			event.End()

			assert.Contains(t, step(t, decodeLog(t, &buf), 0), tt.key)
		})
	}
}

func TestRootAttributesAndEndAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")
	event.Add(slog.String("request_id", "req-1"))

	logger.InfoContext(ctx, "hello")
	logger.InfoContext(ctx, "completed", slog.Bool("success", true))
	event.End()

	root := decodeLog(t, &buf)
	assert.Contains(t, root, "duration_ms")
	assert.NotContains(t, root, "duration")
	assert.Equal(t, "req-1", root["request_id"])
	assert.NotContains(t, root, "success")
	assert.NotContains(t, step(t, root, 0), "request_id")
	assert.Equal(t, true, step(t, root, 1)["success"])
}

func TestNewConfigDefaultsAndOptions(t *testing.T) {
	defaults := NewConfig()
	assert.Equal(t, TimeOffset, defaults.TimeMode)
	assert.Equal(t, OffsetMicroseconds, defaults.OffsetUnit)

	configured := NewConfig(WithTimeMode(TimeAbsolute), WithOffsetUnit(OffsetMilliseconds))
	assert.Equal(t, TimeAbsolute, configured.TimeMode)
	assert.Equal(t, OffsetMilliseconds, configured.OffsetUnit)
}

func TestNilContextsSupported(t *testing.T) {
	logger := New(slog.NewTextHandler(&bytes.Buffer{}, nil))

	// FromContext on a nil context is nil, like slog tolerating nil ctx.
	assert.Nil(t, FromContext(nil))

	// NewEvent(nil) falls back to a background context.
	ctx, event := NewEvent(nil, logger, "op")
	assert.NotNil(t, ctx)
	assert.NotNil(t, event)

	// Logs through the returned context are still buffered.
	var buf bytes.Buffer
	jsonLogger := JSONHandler(&buf, nil)
	ctx, ev := NewEvent(nil, jsonLogger, "op")
	jsonLogger.InfoContext(ctx, "hello")
	ev.End()
	assert.Equal(t, "hello", step(t, decodeLog(t, &buf), 0)["msg"])

	// A nil context cannot reach the event (slog normalizes it away), so the
	// record falls through instead of panicking.
	buf.Reset()
	jsonLogger.InfoContext(nil, "stray")
	assert.NotPanics(t, func() { jsonLogger.InfoContext(nil, "stray") })
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "hello")
	event.End()

	root := decodeLog(t, &buf)
	assert.Equal(t, "op", root["msg"])
	assert.Len(t, root["events"].([]any), 1)
	assert.Equal(t, "hello", step(t, root, 0)["msg"])
}

func TestNewHandlerNilReturnsError(t *testing.T) {
	handler, err := NewHandler(nil)
	assert.Nil(t, handler)
	assert.Error(t, err)
}

func TestNewPanicsOnNilHandler(t *testing.T) {
	assert.Panics(t, func() { New(nil) })
}

func TestNewEventPanicsOnNonWideLogger(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	assert.Panics(t, func() {
		NewEvent(context.Background(), logger, "op")
	})
}

func TestWithGroupEmptyReturnsSameHandler(t *testing.T) {
	handler, _ := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil))
	assert.Same(t, handler, handler.WithGroup(""))
}

func TestFromContextNil(t *testing.T) {
	assert.Nil(t, FromContext(context.Background()))
}

func TestEndIsIdempotentAndIgnoresLateAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	_, event := NewEvent(context.Background(), logger, "op")

	event.End()
	event.Add(slog.String("late", "ignored"))
	event.End()

	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("\n")), "one output record")
	root := decodeLog(t, &buf)
	assert.Equal(t, "op", root["msg"])
	assert.NotContains(t, root, "late")
}

func TestLogAfterEventEndedFallsThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "buffered")
	event.End()
	logger.InfoContext(ctx, "after end")

	decoded := func() []map[string]any {
		out := make([]map[string]any, 0)
		for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
			var v map[string]any
			require.NoError(t, json.Unmarshal(line, &v))
			out = append(out, v)
		}
		return out
	}()

	require.Len(t, decoded, 2, "post-end log is emitted, not dropped")
	assert.Equal(t, "op", decoded[0]["msg"])
	assert.Equal(t, "buffered", step(t, decoded[0], 0)["msg"])
	assert.Equal(t, "after end", decoded[1]["msg"])
	assert.NotContains(t, decoded[1], "events")
}

func TestNestedEventOuterStillCollects(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx1, outer := NewEvent(context.Background(), logger, "outer")
	ctx2, inner := NewEvent(ctx1, logger, "inner")

	logger.InfoContext(ctx2, "inner step")
	inner.End()
	logger.InfoContext(ctx1, "outer step")
	outer.End()

	lines := decodeLines(t, &buf)
	require.Len(t, lines, 2, "outer and inner events each emit one record")

	// The inner event carries its own step.
	assert.Equal(t, "inner", lines[0]["msg"])
	assert.Equal(t, "inner step", step(t, lines[0], 0)["msg"])
	// The outer event carries its own step; the inner record belongs to the
	// inner event and is not duplicated.
	assert.Equal(t, "outer", lines[1]["msg"])
	assert.Equal(t, "outer step", step(t, lines[1], 0)["msg"])
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	out := make([]map[string]any, 0)
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var v map[string]any
		require.NoError(t, json.Unmarshal(line, &v))
		out = append(out, v)
	}
	return out
}

func TestConcurrentLogsToSameEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	const goroutines = 16
	const logsPerGoroutine = 25

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < logsPerGoroutine; i++ {
				logger.InfoContext(ctx, "step",
					slog.Int("g", g), slog.Int("i", i))
			}
		}()
	}
	wg.Wait()
	event.End()

	root := decodeLog(t, &buf)
	assert.EqualValues(t, goroutines*logsPerGoroutine, root["event_count"])
	events := root["events"].([]any)
	assert.Len(t, events, goroutines*logsPerGoroutine)

	// Every step survived, regardless of interleaving.
	seen := make(map[string]bool)
	for _, e := range events {
		m := e.(map[string]any)
		seen[fmt.Sprintf("%v/%v", m["g"], m["i"])] = true
	}
	assert.Len(t, seen, goroutines*logsPerGoroutine)
}

// TestPooledBuffersDoNotAlias stresses the sync.Pool reuse across many
// concurrent events with varying step counts. Any aliasing bug in the pooled
// event/entry/attr buffers corrupts an unrelated event's contents, which this
// test catches by verifying every emitted event intact after heavy reuse.
func TestPooledBuffersDoNotAlias(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	const workers = 24
	const eventsPerWorker = 60

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		w := w
		go func() {
			defer wg.Done()
			for i := 0; i < eventsPerWorker; i++ {
				ctx, ev := NewEvent(context.Background(), logger, fmt.Sprintf("op-%d-%d", w, i))
				steps := (i % 20) + 1
				for j := 0; j < steps; j++ {
					logger.InfoContext(ctx, fmt.Sprintf("step-%d", j),
						"w", w, "i", i, "j", j)
				}
				if i%5 == 0 {
					ctx2, ev2 := NewEvent(context.Background(), logger, fmt.Sprintf("abt-%d-%d", w, i))
					logger.InfoContext(ctx2, "to-abort", "w", w, "i", i)
					ev2.Abort()
				}
				ev.End()
			}
		}()
	}
	wg.Wait()

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	byMsg := make(map[string][]map[string]any)
	for _, line := range lines {
		var v map[string]any
		require.NoError(t, json.Unmarshal(line, &v))
		byMsg[v["msg"].(string)] = append(byMsg[v["msg"].(string)], v)
	}

	for w := 0; w < workers; w++ {
		for i := 0; i < eventsPerWorker; i++ {
			key := fmt.Sprintf("op-%d-%d", w, i)
			evs, ok := byMsg[key]
			require.Truef(t, ok, "missing event %s", key)
			require.Len(t, evs, 1, "event %s emitted %d times", key, len(evs))
			steps := (i % 20) + 1
			children := evs[0]["events"].([]any)
			require.Len(t, children, steps, "event %s has wrong step count", key)
			for _, c := range children {
				m := c.(map[string]any)
				require.Equal(t, w, int(m["w"].(float64)), "event %s content corrupted", key)
				require.Equal(t, i, int(m["i"].(float64)), "event %s content corrupted", key)
			}
			// Aborted events must not appear in output.
			_, aborted := byMsg[fmt.Sprintf("abt-%d-%d", w, i)]
			assert.False(t, aborted, "aborted event should not be emitted")
		}
	}
}

func TestAbort(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "step")
	event.Abort()
	event.Abort()
	assert.Zero(t, buf.Len(), "aborted event wrote output")

	// Add after abort is ignored, and End stays silent on the buffered
	// records. A log after abort is not lost: it falls through to the
	// wrapped handler as a standalone record.
	event.Add(slog.String("late", "ignored"))
	logger.InfoContext(ctx, "post-abort log")
	event.End()

	root := decodeLog(t, &buf)
	assert.Equal(t, "post-abort log", root["msg"])
	assert.NotContains(t, root, "late")
	assert.NotContains(t, root, "events")
}

func TestEndRespectsHandlerLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	// Nothing acceptable is buffered (Info is filtered at the handler),
	// so the root has no level to emit at and nothing is written.
	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "filtered")
	event.End()
	assert.Zero(t, buf.Len(), "filtered event wrote output")

	ctx, event = NewEvent(context.Background(), logger, "op")
	logger.ErrorContext(ctx, "boom")
	event.End()

	assert.Equal(t, 1, strings.Count(buf.String(), "\n"), "one output record")
	root := decodeLog(t, &buf)
	assert.Equal(t, "op", root["msg"])
	assert.Equal(t, "ERROR", root["level"])
	assert.Len(t, root["events"].([]any), 1)
	assert.Equal(t, "boom", step(t, root, 0)["msg"])
}

func TestNewEventLoggerAttrsOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil)).With("service", "accounts")

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step")
	event.End()

	assert.Equal(t, "accounts", decodeLog(t, &buf)["service"])
}

func TestNewEventLoggerGroupOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil)).WithGroup("request").With("method", "GET")

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step")
	event.End()

	request := decodeLog(t, &buf)["request"].(map[string]any)
	assert.Equal(t, "GET", request["method"])
}

func TestValueConversions(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "values",
		"bool", true,
		"duration", time.Second,
		"float", 1.5,
		"int", int64(2),
		"string", "value",
		"time", time.Unix(10, 0).UTC(),
		"uint", uint64(3),
	)
	event.End()

	got := step(t, decodeLog(t, &buf), 0)
	assert.Equal(t, true, got["bool"])
	assert.Equal(t, 1.5, got["float"])
	assert.EqualValues(t, 2, got["int"])
	assert.Equal(t, "value", got["string"])
	assert.EqualValues(t, 3, got["uint"])
}

func TestInvalidEnumValuesUseDefaults(t *testing.T) {
	assert.Equal(t, "us", OffsetUnit(99).String())
	require.Equal(t, int64(time.Second/time.Microsecond), OffsetUnit(99).convert(time.Second))
}

func TestNoEventFallsThrough(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	logger.WithGroup("http").Info("hello", "status", 200)

	httpValue := decodeLog(t, &buf)["http"].(map[string]any)
	assert.EqualValues(t, 200, httpValue["status"])
}

func TestLogValuerResolvedAtHandle(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	type state struct{ value string }
	s := &state{value: "before"}

	logger.InfoContext(ctx, "valued",
		"value", slog.AnyValue(logValuerFunc(func() slog.Value {
			return slog.StringValue(s.value)
		})),
	)
	s.value = "after"
	event.End()

	// Resolution happens while Handle is processing the record.
	assert.Equal(t, "before", step(t, decodeLog(t, &buf), 0)["value"])
}

func TestLogValuerReturningGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "valued",
		"group", slog.AnyValue(logValuerFunc(func() slog.Value {
			return slog.GroupValue(slog.String("inner", "resolved"), slog.Bool("flag", true))
		})),
	)
	event.End()

	group := step(t, decodeLog(t, &buf), 0)["group"].(map[string]any)
	assert.Equal(t, "resolved", group["inner"])
	assert.Equal(t, true, group["flag"])
}

func TestNewEventMsgOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "checkout order ord_8F31A2")

	logger.InfoContext(ctx, "hello")
	event.End()

	assert.Equal(t, "checkout order ord_8F31A2", decodeLog(t, &buf)["msg"])
}

func TestEndNoImplicitEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	_, event := NewEvent(context.Background(), logger, "op")
	event.Add(slog.String("request_id", "req-1"))
	event.End()

	root := decodeLog(t, &buf)
	assert.Equal(t, "req-1", root["request_id"])
	assert.EqualValues(t, 0, root["event_count"])
	assert.Empty(t, root["events"].([]any))
}

func TestFinalLogIsPartOfEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "step one")
	logger.WarnContext(ctx, "final")
	event.End()

	root := decodeLog(t, &buf)
	assert.EqualValues(t, 2, root["event_count"])
	assert.Equal(t, "WARN", root["level"])
	assert.Len(t, root["events"].([]any), 2)

	last := step(t, root, 1)
	assert.Equal(t, "final", last["msg"])
	assert.Equal(t, "WARN", last["level"])
}
