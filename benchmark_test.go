package wideslog

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

var benchCtx = context.Background()

var benchSlogBase = slog.New(slog.NewJSONHandler(io.Discard, nil))

func chargeFive(logger *slog.Logger, ctx context.Context) {
	logger.InfoContext(ctx, "request received", "method", "POST", "path", "/v1/payments")
	logger.InfoContext(ctx, "customer loaded", "customer_id", "cus_42A19C", "customer_plan", "pro")
	logger.InfoContext(ctx, "payment method verified", "brand", "visa", "last4", "4242", "risk_score", 12)
	logger.InfoContext(ctx, "payout authorized", "payout_id", "pay_7D91B4", "amount_cents", 12990, "currency", "BRL")
	logger.InfoContext(ctx, "payment completed", "payment_id", "pay_7D91B4", "status", "confirmed")
}

// BenchmarkSlogFiveLines measures five independent JSON records emitted by
// plainly configured log/slog, the standard logging shape. The per-request
// request context is attached to a new logger inside the loop, mirroring
// event.Add inside the wideslog loop.
func BenchmarkSlogFiveLines(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		logger := benchSlogBase.With(
			"service", "payments-api",
			"request_id", "req_01J9Y7K2",
			"tenant_id", "tenant_acme",
			"user_id", "usr_4821",
		)
		chargeFive(logger, benchCtx)
	}
}

// BenchmarkWideslogFiveLogsOneLine measures the same five records buffered
// into one wide event and emitted once, the wideslog shape.
func BenchmarkWideslogFiveLogsOneLine(b *testing.B) {
	base := New(slog.NewJSONHandler(io.Discard, nil))
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		ctx, event := NewEvent(benchCtx, base, "charge payment pay_7D91B4")
		event.Add(
			slog.String("service", "payments-api"),
			slog.String("request_id", "req_01J9Y7K2"),
			slog.String("tenant_id", "tenant_acme"),
			slog.String("user_id", "usr_4821"),
		)
		chargeFive(base, ctx)
		event.End()
	}
}
