# wideslog

Wide events for Go, built on top of [`log/slog`](https://pkg.go.dev/log/slog).

`wideslog` collects logs from one operation and emits one structured record.
Shared context is written once at the root; individual steps remain available
inside `events`.

## Checkout example

A real checkout request may produce these seven standard JSON records:

```json
{"time":"2026-08-26T20:31:42.100Z","level":"INFO","msg":"request received","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","method":"POST","path":"/v1/orders/ord_8F31A2"}
{"time":"2026-08-26T20:31:42.101Z","level":"INFO","msg":"customer loaded","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","customer_id":"cus_42A19C"}
{"time":"2026-08-26T20:31:42.102Z","level":"INFO","msg":"order loaded","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","items":3,"total_cents":12990}
{"time":"2026-08-26T20:31:42.103Z","level":"INFO","msg":"inventory reserved","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","warehouse":"sp_01","items":3}
{"time":"2026-08-26T20:31:42.104Z","level":"INFO","msg":"payment authorized","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","authorization_id":"auth_7D91","amount_cents":12990}
{"time":"2026-08-26T20:31:42.105Z","level":"INFO","msg":"order persisted","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","status":"confirmed"}
{"time":"2026-08-26T20:31:42.106Z","level":"INFO","msg":"response sent","service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","status":201}
```

The equivalent `wideslog` output is one JSON record:

```json
{"timestamp":"2026-08-26T20:31:42.100Z","duration_ms":84,"event_count":7,"service":"checkout-api","request_id":"req_01J8X7","tenant_id":"tenant_acme","user_id":"usr_9021","order_id":"ord_8F31A2","events":[{"offset_ms":0,"level":"INFO","msg":"request received","method":"POST","path":"/v1/orders/ord_8F31A2"},{"offset_ms":12,"level":"INFO","msg":"customer loaded","customer_id":"cus_42A19C"},{"offset_ms":24,"level":"INFO","msg":"order loaded","items":3,"total_cents":12990},{"offset_ms":36,"level":"INFO","msg":"inventory reserved","warehouse":"sp_01","items":3},{"offset_ms":48,"level":"INFO","msg":"payment authorized","authorization_id":"auth_7D91","amount_cents":12990},{"offset_ms":60,"level":"INFO","msg":"order persisted","status":"confirmed"},{"offset_ms":72,"level":"INFO","msg":"response sent","status":201}]}
```

The seven standard records become one line. Request metadata is written once,
while event-specific attributes stay with the event that produced them.

## Payment worker example

A payment worker can use the same shape:

```json
{"time":"2026-08-26T20:31:42.100Z","level":"INFO","msg":"message received","service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1}
{"time":"2026-08-26T20:31:42.101Z","level":"INFO","msg":"payload decoded","service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1,"schema_version":3}
{"time":"2026-08-26T20:31:42.102Z","level":"INFO","msg":"payment loaded","service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1,"authorization_id":"auth_7D91"}
{"time":"2026-08-26T20:31:42.103Z","level":"INFO","msg":"invoice persisted","service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1,"invoice_id":"inv_51C2"}
{"time":"2026-08-26T20:31:42.104Z","level":"INFO","msg":"receipt queued","service":"payment-worker","job_id":"job_01J8Y8","queue":"receipts.email","attempt":1}
{"time":"2026-08-26T20:31:42.105Z","level":"INFO","msg":"job completed","service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1,"result":"success"}
```

With `wideslog`, those six records become:

```json
{"timestamp":"2026-08-26T20:31:42.100Z","duration_ms":84,"event_count":6,"service":"payment-worker","job_id":"job_01J8Y8","queue":"payments.confirmed","attempt":1,"events":[{"offset_ms":0,"level":"INFO","msg":"message received","queue":"payments.confirmed"},{"offset_ms":12,"level":"INFO","msg":"payload decoded","schema_version":3},{"offset_ms":24,"level":"INFO","msg":"payment loaded","authorization_id":"auth_7D91"},{"offset_ms":36,"level":"INFO","msg":"invoice persisted","invoice_id":"inv_51C2"},{"offset_ms":48,"level":"INFO","msg":"receipt queued","queue":"receipts.email"},{"offset_ms":60,"level":"INFO","msg":"job completed","result":"success"}]}
```

## Savings simulation

The payloads above were serialized as compact JSON with one trailing newline per
record. The byte counts are raw UTF-8 output before compression, indexing, or
transport overhead.

| Scenario | Standard | `wideslog` | Throughput | Byte savings |
| --- | ---: | ---: | ---: | ---: |
| Checkout API | 7 lines / 1,601 B | 1 line / 823 B | 75 req/s | 48.6% |
| Payment worker | 6 lines / 1,078 B | 1 line / 659 B | 20 jobs/s | 38.9% |

Using 86,400 seconds per day, 30 days per month, and 365 days per year:

| Scenario | Period | Standard | `wideslog` | Bytes saved |
| --- | --- | ---: | ---: | ---: |
| Checkout API | day | 10.37 GB | 5.33 GB | 5.04 GB |
| Checkout API | month | 311.23 GB | 159.99 GB | 151.24 GB |
| Checkout API | year | 3.79 TB | 1.95 TB | 1.84 TB |
| Payment worker | day | 1.86 GB | 1.14 GB | 0.72 GB |
| Payment worker | month | 55.88 GB | 34.16 GB | 21.72 GB |
| Payment worker | year | 679.92 GB | 415.64 GB | 264.27 GB |

These are simulations, not universal benchmarks. Savings depend on event count,
repeated context, field sizes, handler options, compression, and backend
pricing. The line reduction is deterministic: `n` standard records become one
wide record per operation.

## Quick start

Create the logger once during application startup:

```go
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
})
logger := wideslog.New(handler)
```

Or use the JSON convenience constructor:

```go
logger := wideslog.JSONHandler(os.Stdout, nil)
```

Create one event per request or operation:

```go
func process(ctx context.Context, logger *slog.Logger) error {
    ctx, event := wideslog.Start(ctx, logger)
    defer func() {
        _ = event.End(ctx, slog.LevelInfo, "operation completed")
    }()

    event.Add(
        slog.String("request_id", "req_01J8X7"),
        slog.String("tenant_id", "tenant_acme"),
    )

    logger.InfoContext(ctx, "customer loaded", "customer_id", "cus_42A19C")
    logger.InfoContext(ctx, "payment authorized", "amount_cents", 12990)
    return nil
}
```

`Event.Add` writes root attributes. Attributes passed to a log call remain on
that event. Logs without an active event pass through to the wrapped handler.

## Timestamp modes

The root record always includes `timestamp` and `duration_ms`. Configure only the
fields inside `events`:

```go
ctx, event := wideslog.Start(ctx, logger,
    wideslog.WithTimestampMode(wideslog.TimestampNone),
)
```

Available modes:

- `TimestampNone`: no timestamp on individual events.
- `TimestampAbsolute`: an ISO-8601 `timestamp` on each event.
- `TimestampOffset`: an elapsed `offset_ns`, `offset_us`, or `offset_ms`.

The default is `TimestampOffset` with `OffsetMicroseconds`:

```go
ctx, event := wideslog.Start(ctx, logger,
    wideslog.WithTimestampMode(wideslog.TimestampOffset),
    wideslog.WithOffsetUnit(wideslog.OffsetMilliseconds),
)

logger.InfoContext(ctx, "started")
time.Sleep(25 * time.Millisecond)
logger.InfoContext(ctx, "finished")
_ = event.End(ctx, slog.LevelInfo, "completed")
```

## HTTP middleware

Create the event from the request context and pass the returned context forward:

```go
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, event := wideslog.Start(r.Context(), logger)
        defer func() {
            _ = event.End(ctx, slog.LevelInfo, "request completed",
                slog.Int("http.status", 200),
            )
        }()

        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Each request receives its own event. The logger can be shared; request state is
stored in the context and the `Event`.

## When not to use it

Use standard `slog` when each line must be independently searchable or when an
operation runs for a long time without useful checkpoints. Wide events are a
summary of an operation, not a replacement for traces or fine-grained debug
logs.

## Example

See [example/README.md](example/README.md) and run:

```sh
go run ./example
```

## Inspiration

`wideslog` is inspired by [Wide Events](https://lfdubiela.github.io/wide-events/),
written by my friend [Luiz Dubiela](https://github.com/lfdubiela).

## License

This project is licensed under the [MIT License](LICENSE).
