package wideslog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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

	ctx, event := Start(context.Background(), logger)

	customer := logger.WithGroup("customer").With(
		"id", "customer-1",
		"name", "Alice",
	)

	customer.InfoContext(ctx, "customer loaded")

	if err := event.End(ctx, slog.LevelInfo, "request completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(context.Background(), logger)

	debtLogger := logger.
		WithGroup("customer").
		With("id", "customer-1").
		WithGroup("debt").
		With("id", "debt-1")

	debtLogger.InfoContext(ctx, "debt loaded")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

	root := decodeLog(t, &buf)
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

	ctx, event := Start(context.Background(), logger)

	child := logger.With("customer_id", "123")

	child.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(context.Background(), logger)
	logger.InfoContext(ctx, "child event")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(context.Background(), logger)
	logger.InfoContext(ctx, "request received", "path", "/users")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)
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
	ctx, event := Start(context.Background(), logger)

	childLogger.InfoContext(ctx, "child")
	logger.InfoContext(ctx, "parent")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(context.Background(), logger)

	logger.WithGroup("http").InfoContext(
		ctx,
		"request",
		"method", "GET",
		"status", 200,
	)

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(
		context.Background(),
		logger,
		WithTimestampMode(TimestampOffset),
		WithOffsetUnit(OffsetMicroseconds),
	)

	logger.InfoContext(ctx, "hello")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(
		context.Background(),
		logger,
		WithTimestampMode(TimestampNone),
	)

	logger.InfoContext(ctx, "hello")

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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
			ctx, event := Start(context.Background(), logger, WithTimestampMode(tt.mode))

			logger.InfoContext(ctx, "hello")
			if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
				t.Fatal(err)
			}

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
			ctx, event := Start(context.Background(), logger, WithOffsetUnit(tt.unit))

			logger.InfoContext(ctx, "hello")
			if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
				t.Fatal(err)
			}

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
	ctx, event := Start(context.Background(), logger)
	event.Add(slog.String("request_id", "req-1"))

	logger.InfoContext(ctx, "hello")
	if err := event.End(ctx, slog.LevelInfo, "completed", slog.Bool("success", true)); err != nil {
		t.Fatal(err)
	}

	root := decodeLog(t, &buf)
	if root["request_id"] != "req-1" {
		t.Fatalf("unexpected request_id: %#v", root["request_id"])
	}
	if root["success"] != true {
		t.Fatalf("unexpected success: %#v", root["success"])
	}
	eventAttrs := root["events"].([]any)[0].(map[string]any)
	if _, ok := eventAttrs["request_id"]; ok {
		t.Fatalf("root attribute leaked into event: %#v", eventAttrs)
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
	ctx, event := Start(nil, logger)
	if ctx == nil || event == nil {
		t.Fatal("Start returned a nil value")
	}
	if err := event.End(nil, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}
}

func TestJSONHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := Start(context.Background(), logger)
	logger.InfoContext(ctx, "hello")
	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

	root := decodeLog(t, &buf)
	if root["msg"] != "completed" {
		t.Fatalf("unexpected message: %#v", root["msg"])
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
	ctx, event := Start(context.Background(), logger)

	if err := event.End(ctx, slog.LevelInfo, "first"); err != nil {
		t.Fatal(err)
	}
	event.Add(slog.String("late", "ignored"))
	if err := event.End(ctx, slog.LevelError, "second"); err != nil {
		t.Fatal(err)
	}

	if lines := bytes.Count(buf.Bytes(), []byte("\n")); lines != 1 {
		t.Fatalf("expected one output record, got %d", lines)
	}
	root := decodeLog(t, &buf)
	if root["msg"] != "first" {
		t.Fatalf("unexpected message: %#v", root["msg"])
	}
	if _, ok := root["late"]; ok {
		t.Fatalf("late attribute was emitted: %#v", root)
	}
}

func TestValueConversions(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := Start(context.Background(), logger)

	logger.InfoContext(ctx, "values",
		"bool", true,
		"duration", time.Second,
		"float", 1.5,
		"int", int64(2),
		"string", "value",
		"time", time.Unix(10, 0).UTC(),
		"uint", uint64(3),
	)
	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

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

	ctx, event := Start(context.Background(), logger)

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

	if err := event.End(ctx, slog.LevelInfo, "completed"); err != nil {
		t.Fatal(err)
	}

	root := decodeLog(t, &buf)
	got := root["events"].([]any)[0].(map[string]any)

	// Resolution happens while Handle is processing the record.
	if got["value"] != "before" {
		t.Fatalf("unexpected resolved value: %#v", got["value"])
	}
}
