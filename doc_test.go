package wideslog_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"

	"github.com/lrweck/wideslog"
)

func Example() {
	var buf bytes.Buffer
	logger := wideslog.JSONHandler(&buf, nil)

	ctx, event := wideslog.NewEvent(context.Background(), logger, "checkout")
	event.Add(slog.String("request_id", "req-1"))

	logger.InfoContext(ctx, "customer loaded", "customer_id", "cus_42A19C")
	logger.InfoContext(ctx, "payment authorized", "amount_cents", 12990)

	event.End()

	fmt.Println(buf.String())
}
