package wideslog

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"log/slog"
	"math"
	"strconv"
	"time"
)

type eventEntry struct {
	record eventRecord
	config Config
}

// MarshalJSONTo writes one buffered step as a JSON object directly onto the
// encoder prepared by the wrapped handler. Implementing MarshalerTo instead
// of Marshaler avoids handing back a new []byte that encoding/json/v2 would
// have to copy again into its own output buffer.
//
// Keys keep their original order and duplicates are preserved, unlike a map.
// Values that do not fit the common slog kinds fall back to encoding/json/v2
// via AppendFormat, which buffers without allocating.
func (e eventEntry) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}

	switch e.config.TimeMode {
	case TimeAbsolute:
		if err := writeEntryTime(enc, "time", e.record.time); err != nil {
			return err
		}
	case TimeOffset:
		if err := writeEntryInt(enc, "offset_"+e.config.OffsetUnit.String(),
			e.config.OffsetUnit.convert(e.record.offset)); err != nil {
			return err
		}
	}

	if err := writeEntryString(enc, "level", e.record.level.String()); err != nil {
		return err
	}
	if err := writeEntryString(enc, "msg", e.record.message); err != nil {
		return err
	}

	events := e.record.attrs
	if events != nil {
		for _, attr := range *events {
			attr.Value = attr.Value.Resolve()
			if attr.Key == "" || reservedEventKey(attr.Key) {
				continue
			}
			if err := writeSlogValue(enc, attr.Key, attr.Value); err != nil {
				return err
			}
		}
	}

	return enc.WriteToken(jsontext.EndObject)
}

// eventsArray is the group of buffered steps emitted as a single value. One
// MarshalJSONTo call writes the whole array, so the wrapped handler marshals
// and flushes it as one block instead of per entry.
type eventsArray struct {
	entries *[]eventEntry
}

func (v eventsArray) MarshalJSONTo(enc *jsontext.Encoder) error {
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for i := range *v.entries {
		if err := (*v.entries)[i].MarshalJSONTo(enc); err != nil {
			return err
		}
	}
	return enc.WriteToken(jsontext.EndArray)
}

// writeSlogValue emits an object member `key: value` where value is a slog
// value. Writing the key as a jsontext String token lets the encoder manage
// the name/value separator and member commas for us.
func writeSlogValue(enc *jsontext.Encoder, key string, value slog.Value) error {
	if err := enc.WriteToken(jsontext.String(key)); err != nil {
		return err
	}
	return writeValue(enc, value)
}

// writeValue emits a single slog value as its JSON representation, reusing
// the encoder's scratch buffer so nested members do not allocate.
func writeValue(enc *jsontext.Encoder, value slog.Value) error {
	switch value.Kind() {
	case slog.KindBool:
		if value.Bool() {
			return enc.WriteValue(jsontext.Value("true"))
		}
		return enc.WriteValue(jsontext.Value("false"))
	case slog.KindDuration:
		return writeValueInt(enc, int64(value.Duration()))
	case slog.KindFloat64:
		return writeValueFloat(enc, value.Float64())
	case slog.KindInt64:
		return writeValueInt(enc, value.Int64())
	case slog.KindString:
		return writeValueString(enc, value.String())
	case slog.KindTime:
		return writeValueTime(enc, value.Time())
	case slog.KindUint64:
		return writeValueUint(enc, value.Uint64())
	case slog.KindGroup:
		return writeAnyWithFormat(enc, attrsToMap(value.Group()))
	default:
		return writeValueAny(enc, value.Any())
	}
}

// writeValueAny mirrors slog's JSON handler for arbitrary values: an error is
// written as its Error string, everything else goes through encoding/json/v2.
// Matching the handler keeps an Event's output identical to plain slog.
func writeValueAny(enc *jsontext.Encoder, v any) error {
	if err, ok := v.(error); ok {
		return writeValueString(enc, err.Error())
	}
	return writeAnyWithFormat(enc, v)
}

func writeValueInt(enc *jsontext.Encoder, n int64) error {
	return enc.WriteValue(strconv.AppendInt(enc.AvailableBuffer(), n, 10))
}

func writeValueUint(enc *jsontext.Encoder, n uint64) error {
	return enc.WriteValue(strconv.AppendUint(enc.AvailableBuffer(), n, 10))
}

func writeValueFloat(enc *jsontext.Encoder, f float64) error {
	// NaN and Inf cannot be represented as JSON numbers and the jsontext
	// encoder rejects them, so emit null (what encoding/json v1 does).
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return enc.WriteValue(jsontext.Value("null"))
	}
	return enc.WriteValue(jsontext.AppendFloat(enc.AvailableBuffer(), f, 64))
}

func writeValueString(enc *jsontext.Encoder, s string) error {
	b, _ := jsontext.AppendQuote(enc.AvailableBuffer(), s)
	return enc.WriteValue(b)
}

func writeValueTime(enc *jsontext.Encoder, t time.Time) error {
	b := enc.AvailableBuffer()
	b = append(b, '"')
	b = t.AppendFormat(b, time.RFC3339Nano)
	b = append(b, '"')
	return enc.WriteValue(b)
}

// writeEntryString and friends handle the fixed event fields (time/offset,
// level, msg), which are always present.
func writeEntryString(enc *jsontext.Encoder, key, s string) error {
	return writeSlogValue(enc, key, slog.StringValue(s))
}

func writeEntryInt(enc *jsontext.Encoder, key string, n int64) error {
	return writeSlogValue(enc, key, slog.Int64Value(n))
}

func writeEntryTime(enc *jsontext.Encoder, key string, t time.Time) error {
	return writeSlogValue(enc, key, slog.TimeValue(t))
}

// writeAnyWithFormat marshals a value that is not a simple slog kind. It falls
// back to encoding/json/v2, writing directly into the encoder's own buffer
// without an intermediate []byte allocation.
func writeAnyWithFormat(enc *jsontext.Encoder, v any) error {
	return json.MarshalEncode(enc, v)
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
