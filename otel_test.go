package wideslog

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func spanContext() context.Context {
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanID:     trace.SpanID{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18},
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestEventRootCarriesTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := NewEvent(spanContext(), logger, "checkout")
	logger.InfoContext(ctx, "step")
	event.End()

	root := decodeLog(t, &buf)
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", root[TraceIDKey])
	assert.Equal(t, "1112131415161718", root[SpanIDKey])
}

func TestEventWithoutSpanHasNoTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	_, event := NewEvent(context.Background(), logger, "checkout")
	event.End()

	root := decodeLog(t, &buf)
	assert.NotContains(t, root, TraceIDKey)
	assert.NotContains(t, root, SpanIDKey)
}

func TestPassthroughCarriesTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	logger.InfoContext(spanContext(), "outside an event")

	root := decodeLog(t, &buf)
	assert.Equal(t, "0102030405060708090a0b0c0d0e0f10", root[TraceIDKey])
	assert.Equal(t, "1112131415161718", root[SpanIDKey])
}

func TestPassthroughWithoutSpanHasNoTraceIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	logger.InfoContext(context.Background(), "outside an event")

	root := decodeLog(t, &buf)
	assert.NotContains(t, root, TraceIDKey)
	assert.NotContains(t, root, SpanIDKey)
}

func TestEndIsIdempotentWithTrace(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	_, event := NewEvent(spanContext(), logger, "checkout")
	event.End()
	event.End()

	require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("\n")))
}
