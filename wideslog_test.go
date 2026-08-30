package wideslog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func decodeLog(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &value); err != nil {
		t.Fatal(err)
	}

	return value
}

type logValuerFunc func() slog.Value

func (f logValuerFunc) LogValue() slog.Value {
	return f()
}

func TestWithGroup(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(context.Background(), logger, "op")

	customer := logger.WithGroup("customer").With(
		"id", "customer-1",
		"name", "Alice",
	)

	customer.InfoContext(ctx, "customer loaded")

	event.End(ctx)

	root := decodeLog(t, &buf)
	events := root["events"].([]any)
	got := events[0].(map[string]any)
	customerValue := got["customer"].(map[string]any)

	if customerValue["id"] != "customer-1" {
		t.Fatalf("unexpected customer.id: %#v", customerValue["id"])
	}

	if customerValue["name"] != "Alice" {
		t.Fatalf("unexpected customer.name: %#v", customerValue["name"])
	}
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

	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["event_count"] != float64(1) {
		t.Fatalf("unexpected event count: %#v", root["event_count"])
	}
	got := root["events"].([]any)[0].(map[string]any)

	customer := got["customer"].(map[string]any)
	debt := customer["debt"].(map[string]any)

	if customer["id"] != "customer-1" {
		t.Fatalf("unexpected customer.id: %#v", customer["id"])
	}

	if debt["id"] != "debt-1" {
		t.Fatalf("unexpected debt.id: %#v", debt["id"])
	}
}

func TestWithDoesNotLeakToParent(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(context.Background(), logger, "op")

	child := logger.With("customer_id", "123")

	child.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")

	event.End(ctx)

	root := decodeLog(t, &buf)
	events := root["events"].([]any)

	childEvent := events[0].(map[string]any)
	parentEvent := events[1].(map[string]any)

	if childEvent["customer_id"] != "123" {
		t.Fatalf("child lost customer_id: %#v", childEvent)
	}

	if _, ok := parentEvent["customer_id"]; ok {
		t.Fatalf("attribute leaked into parent: %#v", parentEvent)
	}
}

func TestHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := NewHandler(base)
	child := handler.WithAttrs([]slog.Attr{slog.String("source", "child")})
	logger := slog.New(child)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "child event")

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)
	if got["source"] != "child" {
		t.Fatalf("missing handler attribute: %#v", got)
	}
}

func TestHandlerWithGroup(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := NewHandler(base)
	child := handler.WithGroup("request").WithAttrs([]slog.Attr{
		slog.String("method", "GET"),
	})
	logger := slog.New(child)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "request received", "path", "/users")

	event.End(ctx)

	root := decodeLog(t, &buf)
	rootRequest := root["request"].(map[string]any)
	got := rootRequest["events"].([]any)[0].(map[string]any)
	request := got["request"].(map[string]any)
	if request["method"] != "GET" {
		t.Fatalf("missing grouped handler attribute: %#v", request)
	}
	if request["path"] != "/users" {
		t.Fatalf("missing grouped record attribute: %#v", request)
	}
}

func TestHandlerChildrenDoNotModifyParent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewJSONHandler(&buf, nil)
	handler := NewHandler(base)
	child := handler.WithAttrs([]slog.Attr{slog.String("scope", "child")})

	logger := slog.New(handler)
	childLogger := slog.New(child)
	ctx, event := NewEvent(context.Background(), logger, "op")

	childLogger.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")

	event.End(ctx)

	events := decodeLog(t, &buf)["events"].([]any)
	childEvent := events[0].(map[string]any)
	parentEvent := events[1].(map[string]any)
	if childEvent["scope"] != "child" {
		t.Fatalf("child lost attribute: %#v", childEvent)
	}
	if _, ok := parentEvent["scope"]; ok {
		t.Fatalf("parent received child attribute: %#v", parentEvent)
	}
}

func TestGroupAndDirectAttrs(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.WithGroup("http").InfoContext(
		ctx,
		"request",
		"method", "GET",
		"status", 200,
	)

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)
	httpValue := got["http"].(map[string]any)

	if httpValue["method"] != "GET" {
		t.Fatalf("unexpected method: %#v", httpValue["method"])
	}

	if httpValue["status"].(float64) != 200 {
		t.Fatalf("unexpected status: %#v", httpValue["status"])
	}
}

func TestTimestampOffset(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(
		context.Background(),
		logger,
		"op",
		WithTimestampMode(TimestampOffset),
		WithOffsetUnit(OffsetMicroseconds),
	)

	logger.InfoContext(ctx, "hello")

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)

	if _, ok := got["offset_us"]; !ok {
		t.Fatalf("missing offset_us: %#v", got)
	}

	if _, ok := got["timestamp"]; ok {
		t.Fatalf("unexpected absolute timestamp: %#v", got)
	}
}

func TestTimestampNone(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(
		context.Background(),
		logger,
		"op",
		WithTimestampMode(TimestampNone),
	)

	logger.InfoContext(ctx, "hello")

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)

	if _, ok := got["timestamp"]; ok {
		t.Fatalf("unexpected timestamp: %#v", got)
	}

	if _, ok := got["offset_us"]; ok {
		t.Fatalf("unexpected offset: %#v", got)
	}
}

func TestRootTimestampAlwaysPresent(t *testing.T) {
	tests := []struct {
		name string
		mode TimestampMode
	}{
		{"none", TimestampNone},
		{"absolute", TimestampAbsolute},
		{"offset", TimestampOffset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(slog.NewJSONHandler(&buf, nil))
			ctx, event := NewEvent(context.Background(), logger, "op", WithTimestampMode(tt.mode))

			logger.InfoContext(ctx, "hello")
			event.End(ctx)

			root := decodeLog(t, &buf)
			if _, ok := root["timestamp"]; !ok {
				t.Fatalf("missing root timestamp: %#v", root)
			}
		})
	}
}

func TestOffsetUnits(t *testing.T) {
	tests := []struct {
		unit OffsetUnit
		key  string
	}{
		{OffsetNanoseconds, "offset_ns"},
		{OffsetMicroseconds, "offset_us"},
		{OffsetMilliseconds, "offset_ms"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			var buf bytes.Buffer
			logger := New(slog.NewJSONHandler(&buf, nil))
			ctx, event := NewEvent(context.Background(), logger, "op", WithOffsetUnit(tt.unit))

			logger.InfoContext(ctx, "hello")
			event.End(ctx)

			root := decodeLog(t, &buf)
			got := root["events"].([]any)[0].(map[string]any)
			if _, ok := got[tt.key]; !ok {
				t.Fatalf("missing %q: %#v", tt.key, got)
			}
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
	event.End(ctx)

	root := decodeLog(t, &buf)
	if _, ok := root["duration_ms"]; !ok {
		t.Fatalf("missing root duration_ms: %#v", root)
	}
	if _, ok := root["duration"]; ok {
		t.Fatalf("unexpected root duration: %#v", root)
	}
	if root["request_id"] != "req-1" {
		t.Fatalf("unexpected request_id: %#v", root["request_id"])
	}
	if _, ok := root["success"]; ok {
		t.Fatalf("final step attribute leaked into root: %#v", root)
	}
	eventAttrs := root["events"].([]any)[0].(map[string]any)
	if _, ok := eventAttrs["request_id"]; ok {
		t.Fatalf("root attribute leaked into event: %#v", eventAttrs)
	}
	lastEvent := root["events"].([]any)[1].(map[string]any)
	if lastEvent["success"] != true {
		t.Fatalf("end attribute missing from final event: %#v", lastEvent)
	}
}

func TestNewConfigDefaultsAndOptions(t *testing.T) {
	defaults := NewConfig()
	if defaults.TimestampMode != TimestampOffset {
		t.Fatalf("unexpected default timestamp mode: %v", defaults.TimestampMode)
	}
	if defaults.OffsetUnit != OffsetMicroseconds {
		t.Fatalf("unexpected default offset unit: %v", defaults.OffsetUnit)
	}

	configured := NewConfig(
		WithTimestampMode(TimestampAbsolute),
		WithOffsetUnit(OffsetMilliseconds),
	)
	if configured.TimestampMode != TimestampAbsolute {
		t.Fatalf("unexpected timestamp mode: %v", configured.TimestampMode)
	}
	if configured.OffsetUnit != OffsetMilliseconds {
		t.Fatalf("unexpected offset unit: %v", configured.OffsetUnit)
	}
}

func TestNilContextsAreSupported(t *testing.T) {
	logger := New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	ctx, event := NewEvent(nil, logger, "op")
	if ctx == nil || event == nil {
		t.Fatal("Start returned a nil value")
	}
	event.End(nil)
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "hello")
	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["msg"] != "op" {
		t.Fatalf("unexpected root message, got %#v", root["msg"])
	}
	events := root["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(events))
	}
	if events[0].(map[string]any)["msg"] != "hello" {
		t.Fatalf("unexpected buffered message: %#v", events[0])
	}
}

func TestNewHandlerPanicsOnNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewHandler did not panic")
		}
	}()

	NewHandler(nil)
}

func TestNewEventPanicsOnNonWideLogger(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewEvent did not panic")
		}
	}()

	logger := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	NewEvent(context.Background(), logger, "op")
}

func TestWithGroupEmptyReturnsSameHandler(t *testing.T) {
	handler := NewHandler(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if handler.WithGroup("") != handler {
		t.Fatal("empty group created a new handler")
	}
}

func TestFromContextNil(t *testing.T) {
	if event := FromContext(nil); event != nil {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestEndIsIdempotentAndIgnoresLateAttributes(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	event.End(ctx)
	event.Add(slog.String("late", "ignored"))
	event.End(ctx)

	if lines := bytes.Count(buf.Bytes(), []byte("\n")); lines != 1 {
		t.Fatalf("expected one output record, got %d", lines)
	}
	root := decodeLog(t, &buf)
	if root["msg"] != "op" {
		t.Fatalf("unexpected root message: %#v", root["msg"])
	}
	if _, ok := root["late"]; ok {
		t.Fatalf("late attribute was emitted: %#v", root)
	}
}

func TestEndRespectsHandlerLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError}))

	// Nothing acceptable is buffered (Info is filtered at the handler),
	// so the root has no level to emit at and nothing is written.
	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "filtered")
	event.End(ctx)
	if buf.Len() != 0 {
		t.Fatalf("expected no output, got: %q", buf.String())
	}

	ctx, event = NewEvent(context.Background(), logger, "op")
	logger.ErrorContext(ctx, "boom")
	event.End(ctx)

	if lines := strings.Count(buf.String(), "\n"); lines != 1 {
		t.Fatalf("expected 1 line, got %d: %q", lines, buf.String())
	}
	root := decodeLog(t, &buf)
	if root["msg"] != "op" {
		t.Fatalf("unexpected root message: %#v", root["msg"])
	}
	if root["level"] != "ERROR" {
		t.Fatalf("root should reflect the buffered severity: %#v", root["level"])
	}
	events := root["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(events))
	}
	if events[0].(map[string]any)["msg"] != "boom" {
		t.Fatalf("unexpected buffered message: %#v", events[0])
	}
}

func TestStartLoggerAttrsOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil)).With("service", "accounts")

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step")
	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["service"] != "accounts" {
		t.Fatalf("root lost logger attribute: %#v", root)
	}
}

func TestStartLoggerGroupOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil)).WithGroup("request").With("method", "GET")

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step")
	event.End(ctx)

	root := decodeLog(t, &buf)
	request := root["request"].(map[string]any)
	if request["method"] != "GET" {
		t.Fatalf("root lost grouped attribute: %#v", root)
	}
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
	event.End(ctx)

	got := decodeLog(t, &buf)["events"].([]any)[0].(map[string]any)
	if got["bool"] != true || got["float"] != 1.5 || got["int"] != float64(2) ||
		got["string"] != "value" || got["uint"] != float64(3) {
		t.Fatalf("unexpected converted values: %#v", got)
	}
}

func TestInvalidEnumValuesUseDefaults(t *testing.T) {
	if got := OffsetUnit(99).String(); got != "us" {
		t.Fatalf("unexpected offset suffix: %q", got)
	}
	if got := OffsetUnit(99).convert(time.Second); got != int64(time.Second/time.Microsecond) {
		t.Fatalf("unexpected offset conversion: %d", got)
	}
}

func TestNoEventFallsThrough(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	logger.WithGroup("http").Info(
		"hello",
		"status", 200,
	)

	root := decodeLog(t, &buf)
	httpValue := root["http"].(map[string]any)

	if httpValue["status"].(float64) != 200 {
		t.Fatalf("unexpected status: %#v", httpValue["status"])
	}
}

func TestLogValuerResolvedAtHandle(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(context.Background(), logger, "op")

	type state struct {
		value string
	}

	s := &state{value: "before"}

	logger.InfoContext(ctx, "valued",
		"value", slog.AnyValue(logValuerFunc(func() slog.Value {
			return slog.StringValue(s.value)
		})),
	)

	s.value = "after"

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)

	// Resolution happens while Handle is processing the record.
	if got["value"] != "before" {
		t.Fatalf("unexpected resolved value: %#v", got["value"])
	}
}

func TestLogValuerReturningGroup(t *testing.T) {
	var buf bytes.Buffer

	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "valued",
		"group", slog.AnyValue(logValuerFunc(func() slog.Value {
			return slog.GroupValue(
				slog.String("inner", "resolved"),
				slog.Bool("flag", true),
			)
		})),
	)

	event.End(ctx)

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)
	group := got["group"].(map[string]any)

	if group["inner"] != "resolved" {
		t.Fatalf("unexpected resolved group member: %#v", group)
	}
	if group["flag"] != true {
		t.Fatalf("unexpected resolved group flag: %#v", group)
	}
}

func TestNewEventMsgOnRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "checkout order ord_8F31A2")

	logger.InfoContext(ctx, "hello")
	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["msg"] != "checkout order ord_8F31A2" {
		t.Fatalf("unexpected root message: %#v", root["msg"])
	}
}

func TestEndNoImplicitEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")
	event.Add(slog.String("request_id", "req-1"))

	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["request_id"] != "req-1" {
		t.Fatalf("root attribute missing: %#v", root)
	}
	if root["event_count"] != float64(0) {
		t.Fatalf("unexpected event count: %#v", root["event_count"])
	}
	if events := root["events"].([]any); len(events) != 0 {
		t.Fatalf("expected no events, got %#v", events)
	}
}

func TestFinalLogIsPartOfEvents(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(context.Background(), logger, "op")

	logger.InfoContext(ctx, "step one")
	logger.WarnContext(ctx, "final")
	event.End(ctx)

	root := decodeLog(t, &buf)
	if root["event_count"] != float64(2) {
		t.Fatalf("unexpected event count: %#v", root["event_count"])
	}
	if root["level"] != "WARN" {
		t.Fatalf("root should reflect the highest buffered severity: %#v", root["level"])
	}
	events := root["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %#v", events)
	}
	last := events[1].(map[string]any)
	if last["msg"] != "final" {
		t.Fatalf("final step missing from events: %#v", last)
	}
	if last["level"] != "WARN" {
		t.Fatalf("unexpected final step level: %#v", last["level"])
	}
}
