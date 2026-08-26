# wideslog

Wide events for Go, built on top of [`log/slog`](https://pkg.go.dev/log/slog).

`wideslog` collects the logs produced during one operation and emits them as a
single structured record. The operation context stays explicit, while the
logger keeps normal `slog` behavior such as `With`, `WithGroup`, and
`InfoContext`.

## Why wide events?

Traditional logging emits one record per step and repeats request context:

```text
INFO request started       request_id=req-123 tenant_id=tenant-42
INFO customer loaded       request_id=req-123 tenant_id=tenant-42 customer_id=456
INFO debt loaded           request_id=req-123 tenant_id=tenant-42 debt_id=789
INFO request completed     request_id=req-123 tenant_id=tenant-42
```

A wide event writes the shared context once and keeps the steps together:

```json
{
  "timestamp": "2026-08-26T20:31:42.100Z",
  "duration": 17000000,
  "event_count": 3,
  "request_id": "req-123",
  "tenant_id": "tenant-42",
  "events": [
    {"offset_us": 0, "level": "INFO", "msg": "request started"},
    {"offset_us": 1200, "level": "INFO", "msg": "customer loaded", "customer_id": 456},
    {"offset_us": 5800, "level": "INFO", "msg": "debt loaded", "debt_id": 789}
  ]
}
```

The root `timestamp` is always emitted. Timestamp options affect only the
items inside `events`.

## Savings model

The following simulations use compact JSON, one trailing newline per record,
and raw UTF-8 bytes before compression, indexing, retention, or transport
overhead. Standard logs repeat shared fields on every event. Wide logs put
shared fields at the root and store only event-specific data in `events`.

### Payloads

API read, five events at 100 requests per second:

```json
{"time":"2026-08-26T20:31:42.100Z","level":"INFO","msg":"step 1","service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456}
{"time":"2026-08-26T20:31:42.101Z","level":"INFO","msg":"step 2","service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456}
{"time":"2026-08-26T20:31:42.102Z","level":"INFO","msg":"step 3","service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456}
{"time":"2026-08-26T20:31:42.103Z","level":"INFO","msg":"step 4","service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456}
{"time":"2026-08-26T20:31:42.104Z","level":"INFO","msg":"step 5","service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456}
```

The equivalent wide payload is:

```json
{"timestamp":"2026-08-26T20:31:42.100Z","duration":17000000,"event_count":5,"service":"accounts","request_id":"req-123456","tenant_id":"tenant-42","user_id":123456,"events":[{"offset_us":0,"level":"INFO","msg":"step 1"},{"offset_us":1200,"level":"INFO","msg":"step 2"},{"offset_us":2400,"level":"INFO","msg":"step 3"},{"offset_us":3600,"level":"INFO","msg":"step 4"},{"offset_us":4800,"level":"INFO","msg":"step 5"}]}
```

The other simulated payload inputs are:

```json
{
  "checkout": {
    "events": 20,
    "throughput_per_second": 25,
    "shared": {
      "service": "checkout",
      "request_id": "req-123456",
      "tenant_id": "tenant-42",
      "user_id": 123456,
      "order_id": "order-987654"
    }
  },
  "background_worker": {
    "events": 8,
    "throughput_per_second": 5,
    "shared": {
      "service": "billing-worker",
      "job_id": "job-123456",
      "tenant_id": "tenant-42",
      "attempt": 2
    }
  }
}
```

### Per-operation savings

| Scenario | Events | Throughput | Standard | `wideslog` | Byte savings |
| --- | ---: | ---: | ---: | ---: | ---: |
| API read | 5 | 100 req/s | 770 B | 418 B | 45.7% |
| Checkout | 20 | 25 req/s | 3,611 B | 1,202 B | 66.7% |
| Background worker | 8 | 5 jobs/s | 1,208 B | 562 B | 53.5% |

### Volume projections

Assumptions: 86,400 seconds per day, 30 days per month, and 365 days per
year. `GB` and `TB` are decimal units.

| Scenario | Period | Standard | `wideslog` | Bytes saved |
| --- | --- | ---: | ---: | ---: |
| API read | day | 6.65 GB | 3.61 GB | 3.04 GB |
| API read | month | 199.58 GB | 108.35 GB | 91.24 GB |
| API read | year | 2.43 TB | 1.32 TB | 1.11 TB |
| Checkout | day | 7.80 GB | 2.60 GB | 5.20 GB |
| Checkout | month | 233.99 GB | 77.89 GB | 156.10 GB |
| Checkout | year | 2.85 TB | 947.66 GB | 1.90 TB |
| Background worker | day | 0.52 GB | 0.24 GB | 0.28 GB |
| Background worker | month | 15.66 GB | 7.28 GB | 8.37 GB |
| Background worker | year | 190.48 GB | 88.62 GB | 101.86 GB |

Line reduction is direct: `events per operation` standard records become one
wide record. Savings vary with event count, field sizes, handler options, and
backend compression. The basic calculations are:

```text
bytes saved = (standard bytes/operation - wide bytes/operation) * operations
lines saved = (events/operation - 1) * operations
```

## Quick start

Create a normal `slog` handler and wrap it once during application startup:

```go
handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelInfo,
})
logger := wideslog.New(handler)
```

For JSON output, use the convenience constructor:

```go
logger := wideslog.JSONHandler(os.Stdout, nil)
```

For each request or operation, create a context-local event:

```go
func process(ctx context.Context, logger *slog.Logger) error {
    ctx, event := wideslog.Start(ctx, logger)
    defer func() {
        _ = event.End(ctx, slog.LevelInfo, "request completed")
    }()

    event.Add(
        slog.String("request_id", "req-123"),
        slog.String("tenant_id", "tenant-42"),
    )

    logger.InfoContext(ctx, "customer loaded", "customer_id", 456)
    logger.InfoContext(ctx, "debt loaded", "debt_id", 789)
    return nil
}
```

`Event.Add` puts attributes on the root. Attributes passed to a log call stay
on that event. `End` is idempotent, so it is safe to use in a deferred cleanup.

## Logger hierarchy

`With` and `WithGroup` keep their normal `slog` scope:

```go
customerLogger := logger.With("customer_id", 456)
debtLogger := customerLogger.With("debt_id", 789)

customerLogger.InfoContext(ctx, "customer loaded")
debtLogger.InfoContext(ctx, "debt loaded")
logger.InfoContext(ctx, "request completed")
```

Only the first event receives `customer_id`; the second receives both
`customer_id` and `debt_id`; the root logger receives neither. Groups become
nested objects:

```go
httpLogger := logger.WithGroup("http")
httpLogger.InfoContext(ctx, "request", "method", "GET", "status", 200)
```

Logs without an active event pass through immediately to the wrapped handler.
The logger can be shared globally, but the event context must be created once
per request or operation.

## Timestamp modes

The root record always includes `timestamp` and `duration`. Configure only the
per-event timestamp representation when calling `Start`:

```go
wideslog.WithTimestampMode(wideslog.TimestampNone)
// events have no individual timestamp

wideslog.WithTimestampMode(wideslog.TimestampAbsolute)
// events have a timestamp field

wideslog.WithTimestampMode(wideslog.TimestampOffset)
// events have offset_ns, offset_us, or offset_ms
```

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

Create the event from the request context and pass the returned context
forward. Each request receives an independent event:

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

Do not reuse an event across requests. A shared logger is safe; request state is
stored in the context and the `Event`.

## When to use it

Use wide events for operations with a clear lifecycle:

- HTTP requests
- background jobs
- message processing
- scheduled tasks
- workflows
- database operations

Traditional `slog` may be a better fit when every log line must be independently
searchable, or when work runs for a very long time without useful checkpoints.
A wide event is a summary of an operation, not a replacement for traces or
fine-grained debugging logs.

## Example

See [example/README.md](example/README.md) and run:

```sh
go run ./example
```

## Inspiration

`wideslog` is inspired by [Wide Events](https://lfdubiela.github.io/wide-events/),
written by my friend [Luiz Dubiela](https://github.com/lfdubiela).

## Status

Early-stage and experimental. The API may change while the project evolves.

## License

This project is licensed under the [MIT License](LICENSE).
