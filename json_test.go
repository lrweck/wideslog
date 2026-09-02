package wideslog

import (
	"bufio"
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// encodeJSON encodes a MarshalerTo the same way slog's JSONHandler does:
// through the encoding/json/v2 path, allowing duplicate names and disabling
// HTML escaping. The trailing newline the streaming handler emits is trimmed
// so snapshots compare the object itself.
func encodeJSON(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf,
		jsontext.AllowDuplicateNames(true),
		jsontext.EscapeForHTML(false),
	)
	require.NoError(t, json.MarshalEncode(enc, v))
	return strings.TrimSuffix(buf.String(), "\n")
}

func newEntry(msg string, level slog.Level, mode TimeMode, attrs ...slog.Attr) eventEntry {
	return eventEntry{
		record: eventRecord{level: level, message: msg, attrs: &attrs},
		config: Config{TimeMode: mode, OffsetUnit: OffsetMicroseconds},
	}
}

func decodeEntryJSON(t *testing.T, entry eventEntry) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(encodeJSON(t, entry)), &got))
	return got
}

// TestMarshalJSONExactSnapshot pins down ordering, duplicate keys, reserved
// key filtering, escaping, and every slog kind at once.
func TestMarshalJSONExactSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 30, 9, 15, 42, 123456789, time.UTC)

	entry := eventEntry{
		record: eventRecord{
			time:    now,
			offset:  12_345_678 * time.Nanosecond,
			level:   slog.LevelWarn,
			message: `pay "ok" \done`,
			attrs: &[]slog.Attr{
				slog.String("request_id", "req"),
				slog.String(`a"b`, "x"),
				slog.String("level", "reserved"),
				slog.String("msg", "reserved"),
				slog.String("time", "reserved"),
				slog.Int64("offset_us", 1),
				slog.String("", "no key"),
				slog.String("quote", "say \"hi\""),
				slog.Int64("big", math.MinInt64),
				slog.Float64("nan", math.NaN()),
				slog.Duration("dur", 1500*time.Nanosecond),
				slog.Time("when", now),
				slog.Bool("ok", true),
				slog.Uint64("u", math.MaxUint64),
				slog.Int64("dup", 1),
				slog.Int64("dup", 2),
				slog.String("chars", "a<b&c"),
			},
		},
		config: Config{TimeMode: TimeAbsolute, OffsetUnit: OffsetMicroseconds},
	}

	want := `{"time":"2026-08-30T09:15:42.123456789Z","level":"WARN",` +
		`"msg":"pay \"ok\" \\done","request_id":"req","a\"b":"x",` +
		`"quote":"say \"hi\"","big":-9223372036854775808,"nan":null,` +
		`"dur":1500,"when":"2026-08-30T09:15:42.123456789Z","ok":true,` +
		`"u":18446744073709551615,"dup":1,"dup":2,"chars":"a<b&c"}`

	assert.Equal(t, want, encodeJSON(t, entry))
}

func TestMarshalJSONGroupValidated(t *testing.T) {
	entry := newEntry("step", slog.LevelInfo, TimeNone,
		slog.Group("g", slog.String("z", "1"), slog.String("a", "2")),
	)

	got := decodeEntryJSON(t, entry)
	g := got["g"].(map[string]any)
	assert.Equal(t, "1", g["z"])
	assert.Equal(t, "2", g["a"])
}

// TestMarshalJSONGroupOrderPreserved pins down that a group's members are
// written in their original order and that duplicate keys survive, matching
// the top-level entry behavior rather than round-tripping through a map.
func TestMarshalJSONGroupOrderPreserved(t *testing.T) {
	entry := newEntry("step", slog.LevelInfo, TimeNone,
		slog.Group("g",
			slog.String("first", "1"),
			slog.String("dup", "a"),
			slog.String("dup", "b"),
			slog.String("last", "2"),
		),
	)

	raw := encodeJSON(t, entry)
	first := strings.Index(raw, `"first":`)
	dupA := strings.Index(raw, `"dup":"a"`)
	dupB := strings.Index(raw, `"dup":"b"`)
	last := strings.Index(raw, `"last":`)
	require.True(t, first != -1 && dupA != -1 && dupB != -1 && last != -1, "missing keys: %q", raw)
	assert.True(t, first < dupA && dupA < dupB && dupB < last,
		"group member order not preserved: %q", raw)
}

func TestMarshalJSONTimeModes(t *testing.T) {
	base := eventRecord{
		time:    time.Date(2026, 8, 30, 9, 15, 42, 0, time.UTC),
		offset:  12_345_678 * time.Nanosecond,
		level:   slog.LevelInfo,
		message: "step",
	}

	cases := []struct {
		name string
		mode TimeMode
		unit OffsetUnit
		want string
	}{
		{"none", TimeNone, OffsetMicroseconds, `{"level":"INFO","msg":"step"}`},
		{"absolute", TimeAbsolute, OffsetMicroseconds, `{"time":"2026-08-30T09:15:42Z","level":"INFO","msg":"step"}`},
		{"offset_ns", TimeOffset, OffsetNanoseconds, `{"offset_ns":12345678,"level":"INFO","msg":"step"}`},
		{"offset_us", TimeOffset, OffsetMicroseconds, `{"offset_us":12345,"level":"INFO","msg":"step"}`},
		{"offset_ms", TimeOffset, OffsetMilliseconds, `{"offset_ms":12,"level":"INFO","msg":"step"}`},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			entry := eventEntry{record: base, config: Config{TimeMode: tt.mode, OffsetUnit: tt.unit}}
			assert.Equal(t, tt.want, encodeJSON(t, entry))
		})
	}
}

func TestWriteFloatNaNInfAreNull(t *testing.T) {
	for _, in := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		entry := newEntry("", slog.LevelInfo, TimeNone, slog.Float64("f", in))
		assert.Equal(t, `{"level":"INFO","msg":"","f":null}`, encodeJSON(t, entry), "value %v", in)
	}
}

// TestErrorAndDurationMatchSlogHandler verifies the value-level behavior for
// arbitrary values matches slog's JSON handler: errors become their Error
// string and durations become an integer number of nanoseconds.
func TestErrorAndDurationMatchSlogHandler(t *testing.T) {
	err := errors.New("boom")

	var wide bytes.Buffer
	logger := New(slog.NewJSONHandler(&wide, nil))
	ctx, event := NewEvent(t.Context(), logger, "op", WithTimeMode(TimeNone))
	logger.InfoContext(ctx, "x", slog.Duration("dur", 1500*time.Nanosecond), slog.Any("err", err))
	event.End()

	events := decodeLog(t, &wide)["events"].([]any)
	step := events[0].(map[string]any)
	assert.EqualValues(t, 1500, step["dur"])
	assert.Equal(t, "boom", step["err"])
}

func TestMarshalJSONValidatedByHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))

	ctx, event := NewEvent(t.Context(), logger, "op", WithTimeMode(TimeAbsolute))
	logger.InfoContext(ctx, "plain")
	logger.InfoContext(ctx, "esca\x00pe\"", slog.String("ctrl", "tab\there"), slog.String("chars", "a<b&c"))
	event.End()

	events := decodeLog(t, &buf)["events"].([]any)
	require.Len(t, events, 2)

	second := events[1].(map[string]any)
	assert.Equal(t, "esca\u0000pe\"", second["msg"])
	assert.Equal(t, "tab\there", second["ctrl"])
	assert.Equal(t, "a<b&c", second["chars"])
	assert.Contains(t, second, "time")
}

func TestEventsArraySingleJSONBlock(t *testing.T) {
	base := eventRecord{
		time:    time.Date(2026, 8, 30, 9, 15, 42, 0, time.UTC),
		offset:  1000 * time.Nanosecond,
		level:   slog.LevelInfo,
		message: "step",
	}
	arr := eventsArray{entries: &[]eventEntry{
		{record: base, config: Config{TimeMode: TimeOffset, OffsetUnit: OffsetMilliseconds}},
		{record: base, config: Config{TimeMode: TimeNone}},
	}}

	var got []map[string]any
	require.NoError(t, json.Unmarshal([]byte(encodeJSON(t, arr)), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "step", got[1]["msg"])
	assert.Contains(t, got[0], "offset_ms")
}

// TestBufferedJSONIsStreamParseable ensures the events array remains a single
// valid document even when parsed incrementally through bufio.
func TestBufferedJSONIsStreamParseable(t *testing.T) {
	var buf bytes.Buffer
	logger := New(slog.NewJSONHandler(&buf, nil))
	ctx, event := NewEvent(t.Context(), logger, "op")
	logger.InfoContext(ctx, "one")
	logger.InfoContext(ctx, "two")
	event.End()

	reader := bufio.NewReader(&buf)
	final, err := reader.ReadBytes('\n')
	require.NoError(t, err)

	var v map[string]any
	require.NoError(t, json.Unmarshal(final, &v))
	assert.Len(t, v["events"].([]any), 2)
}
