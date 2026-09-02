# wideslog

[![PkgGoDev](https://pkg.go.dev/badge/github.com/lrweck/wideslog)](https://pkg.go.dev/github.com/lrweck/wideslog)

Wide events for Go, built on top of [`log/slog`](https://pkg.go.dev/log/slog).

`wideslog` collects logs from one operation and emits one structured record.
Shared context is written once at the root; individual steps remain available
inside `events`.

## Payment API example

One payment request logs five steps. Each standard slog record repeats the
full request context (`service`, `request_id`, `tenant_id`, `user_id`):

```json
{"time":"2026-08-30T09:15:42.100Z","level":"INFO","msg":"request received","service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","method":"POST","path":"/v1/payments"}
{"time":"2026-08-30T09:15:42.101Z","level":"INFO","msg":"customer loaded","service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","customer_id":"cus_42A19C","customer_plan":"pro"}
{"time":"2026-08-30T09:15:42.103Z","level":"INFO","msg":"payment method verified","service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","brand":"visa","last4":"4242","risk_score":12}
{"time":"2026-08-30T09:15:42.105Z","level":"INFO","msg":"payout authorized","service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","payout_id":"pay_7D91B4","amount_cents":12990,"currency":"BRL"}
{"time":"2026-08-30T09:15:42.108Z","level":"INFO","msg":"payment completed","service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","payment_id":"pay_7D91B4","status":"confirmed"}
```

The same request through `wideslog` emits one record. `NewEvent` names the
operation on the root; the shared context is written once as root attributes;
each step keeps only its own fields inside `events`:

```json
{"time":"2026-08-30T09:15:42.100Z","level":"INFO","msg":"charge payment pay_7D91B4","duration_ms":8,"event_count":5,"service":"payments-api","request_id":"req_01J9Y7K2","tenant_id":"tenant_acme","user_id":"usr_4821","events":[{"offset_ms":0,"level":"INFO","msg":"request received","method":"POST","path":"/v1/payments"},{"offset_ms":12,"level":"INFO","msg":"customer loaded","customer_id":"cus_42A19C","customer_plan":"pro"},{"offset_ms":32,"level":"INFO","msg":"payment method verified","brand":"visa","last4":"4242","risk_score":12},{"offset_ms":54,"level":"INFO","msg":"payout authorized","payout_id":"pay_7D91B4","amount_cents":12990,"currency":"BRL"},{"offset_ms":78,"level":"INFO","msg":"payment completed","payment_id":"pay_7D91B4","status":"confirmed"}]}
```

Five standard records become one line. Request metadata and the operation name
are written once, while event-specific attributes stay with the event that
produced them.

## Savings simulation

Serialized as compact JSON with one trailing newline per record, the payment
request above produces five records of 214, 224, 229, 240, and 224 bytes —
1,131 B per request. The `wideslog` output is one record of 763 bytes: 368 B
saved per request (32.5%), before compression, indexing, or transport overhead.

| Scenario | Standard | `wideslog` | Byte savings |
| --- | ---: | ---: | ---: |
| Payment API | 5 lines / 1,131 B | 1 line / 763 B | 368 B (32.5%) |

Using 86,400 seconds per day, 30 days per month, and 365 days per year at
50 requests per second:

| Period | Standard | `wideslog` | Bytes saved |
| --- | ---: | ---: | ---: |
| day | 4.89 GB | 3.30 GB | 1.59 GB |
| month | 146.58 GB | 98.88 GB | 47.69 GB |
| year | 1,783.36 GB | 1,203.10 GB | 580.26 GB |

These are simulations, not universal benchmarks. Savings depend on event count,
repeated context, field sizes, handler options, compression, and backend
pricing. The line reduction is deterministic: five standard records become one
wide record per operation. The byte savings come mostly from writing the shared
request context and the per-line `time`/`level`/`msg` fields only once, at the
root.

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
    ctx, event := wideslog.NewEvent(ctx, logger, "checkout order "+orderID)
    defer event.End()

    event.Add(
        slog.String("request_id", "req_01J8X7"),
        slog.String("tenant_id", "tenant_acme"),
    )

    logger.InfoContext(ctx, "customer loaded", "customer_id", "cus_42A19C")
    logger.InfoContext(ctx, "payment authorized", "amount_cents", 12990)
    return nil
}
```

The message passed to `NewEvent` identifies the operation and becomes the
message of the root record. `Event.Add` writes root attributes. Attributes
passed to a log call remain on that event; steps logged through the logger
appear as entries in `events`. Logs without an active event pass through to
the wrapped handler.

## Time modes

The root record always includes `time` and `duration_ms`. `time` is the moment
the operation started; the `level` and `msg` are the ones slog always writes for
the emitted record. Configure only the fields inside `events`:

```go
ctx, event := wideslog.NewEvent(ctx, logger, "operation completed",
    wideslog.WithTimeMode(wideslog.TimeNone),
)
```

The per-step `level` and, in absolute mode, `time` live inside each `events`
entry.

Available modes:

- `TimeNone`: no timestamp on individual events.
- `TimeAbsolute`: an ISO-8601 `time` on each event.
- `TimeOffset`: an elapsed `offset_ns`, `offset_us`, or `offset_ms`.

The default is `TimeOffset` with `OffsetMicroseconds`:

```go
ctx, event := wideslog.NewEvent(ctx, logger, "operation completed",
    wideslog.WithTimeMode(wideslog.TimeOffset),
    wideslog.WithOffsetUnit(wideslog.OffsetMilliseconds),
)

logger.InfoContext(ctx, "started")
time.Sleep(25 * time.Millisecond)
logger.InfoContext(ctx, "finished")
event.End()
```

## What gets buffered

slog filters at the source: a step below the handler's configured level is
never buffered and does not count toward `event_count`. `End` emits the root
record at the highest level found among the buffered steps (Info when there
are none), so a handler that only accepts serious levels still receives the
summary.

Attributes attached to the logger itself (`With`, `WithGroup`) are shared
context: they are written once on the root record, never repeated inside each
step. Attributes passed to an individual log call stay with that step, in
their original order.

`event.Abort()` discards the buffered steps and emits nothing — handy when
the operation turns out not to need logging after all.

## HTTP middleware

Create the event from the request context and pass the returned context forward:

```go
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        ctx, event := wideslog.NewEvent(r.Context(), logger, "request completed")
        defer event.End()

        next.ServeHTTP(w, r.WithContext(ctx))
        event.Add(slog.Int("http.status", 200))
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
