package wideslog

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// Attribute keys wideslog uses when the context carries an OpenTelemetry
// span. They match the keys Grafana (Loki derived fields) and most log
// backends look for, so a log line links straight to its trace.
const (
	TraceIDKey = "trace_id"
	SpanIDKey  = "span_id"
)

// traceAttrs returns trace_id/span_id for the span carried by ctx, or nil
// when there is no active span.
//
// Only go.opentelemetry.io/otel/trace is required (no SDK): without a
// configured OpenTelemetry provider there is no span and this is a no-op,
// so non-OpenTelemetry users pay nothing.
func traceAttrs(ctx context.Context) []slog.Attr {
	if ctx == nil {
		return nil
	}

	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}

	return []slog.Attr{
		slog.String(TraceIDKey, sc.TraceID().String()),
		slog.String(SpanIDKey, sc.SpanID().String()),
	}
}
