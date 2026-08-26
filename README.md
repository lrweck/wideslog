# wideslog

> Wide events for Go, built on top of `log/slog`.

## Are you tired of...

- Scrolling through hundreds of log lines just to understand a single request?
- Repeating `request_id`, `tenant_id`, `user_id` and other context on every log entry?
- Correlating dozens of independent log records just to reconstruct what happened?
- Paying to ingest and store the same context over and over again?
- Choosing between useful application logs and an ever-growing logging bill?

### Meet wideslog.

`wideslog` turns all the logs generated during an operation into a single **wide event**.

## Fewer lines, fewer repeated bytes

Traditional logging emits one record for every step. A five-step operation
therefore produces five log lines, often repeating the same request context.
`wideslog` emits one root record and keeps the steps inside `events`:

| | Records | Lines | Shared request context |
| --- | ---: | ---: | --- |
| Standard `slog` | 5 | 5 | repeated in each record |
| `wideslog` | 1 | 1 | written once at the root |

The byte reduction depends on the attributes, JSON handler options, and log
transport. `wideslog` reduces bytes when shared context is moved to the root;
the `events` array itself adds a small amount of JSON structure. Measure both
forms with the same handler and representative payloads.

For a quick local comparison, write each version to its own file and measure
the serialized output:

```sh
wc -l -c standard.log wide-event.log
```

Compare the same operation, the same attributes, and the same handler options.
The useful comparison is the total bytes and lines emitted per operation, not
the size of an individual event object.

Instead of scattering a request across multiple log entries:

```text
INFO request started
INFO customer loaded      customer_id=456
INFO debt loaded          debt_id=789
INFO negotiation created  installments=12
INFO request completed
```

You get a single structured record containing the entire operation. The root
`timestamp` is always present; timestamp options apply only to items in
`events`:

```json
{
  "timestamp": "2026-08-26T20:31:42.100Z",
  "request_id": "abc",
  "tenant_id": "123",
  "duration": 17000000,
  "events": [
    {
      "offset_us": 1200,
      "level": "INFO",
      "msg": "customer loaded",
      "customer_id": "456"
    },
    {
      "offset_us": 5800,
      "level": "INFO",
      "msg": "debt loaded",
      "debt_id": "789"
    },
    {
      "offset_us": 17000,
      "level": "INFO",
      "msg": "negotiation created",
      "installments": 12
    }
  ]
}
```

The context can be attached hierarchically with `slog.With()`, so attributes only appear in the events where they actually apply.

**One operation. One log entry. All the context.**

And because `wideslog` is built on `slog`, context remains hierarchical:

```go
customerLogger := logger.With("customer_id", customerID)

customerLogger.InfoContext(ctx, "customer loaded")

debtLogger := customerLogger.With("debt_id", debtID)

debtLogger.InfoContext(ctx, "debt loaded")

debtLogger.InfoContext(ctx, "negotiation created",
    "installments", 12,
)
```

`customer_id` is automatically included in events produced by `customerLogger` and its children. `debt_id` is included only from `debtLogger` downward.


## Why wideslog?

### Less noise

A single request can generate dozens of log entries. `wideslog` collects them into one structured record.

### Less duplication

Request-level context belongs at the request level. It doesn't need to be repeated in every event.

Put shared context on the root with `Event.Add`:

```go
ctx, event := wideslog.Start(ctx, logger)
event.Add(
  slog.String("request_id", requestID),
  slog.String("tenant_id", tenantID),
)

logger.InfoContext(ctx, "customer loaded", "customer_id", customerID)
logger.InfoContext(ctx, "debt loaded", "debt_id", debtID)

if err := event.End(ctx, slog.LevelInfo, "request completed"); err != nil {
  return err
}
```

The request and tenant identifiers are serialized once, instead of being
repeated on every event. Event-specific attributes remain on their own event.

### Better observability

Instead of reconstructing a request from scattered log lines, you get the entire execution as one document.

### Performance timing built in

Events can use relative offsets instead of repeating full timestamps:

```json
{
  "offset_us": 1200,
  "msg": "customer loaded"
}
```

Want absolute timestamps instead? That's configurable too.

### It's still `slog`

You don't need to learn a new logging API.

This:

```go
slog.InfoContext(ctx, "customer loaded",
    "customer_id", customerID,
)
```

still works.

`wideslog` is a `slog.Handler`.

That means you keep the familiar `slog` ecosystem, including:

- `With`
- `WithGroup`
- `InfoContext`
- `WarnContext`
- `ErrorContext`
- structured attributes
- custom `slog.Handler`s

## How it works

At the beginning of an operation, create a wide event:

```go
ctx, event := wideslog.Start(ctx, logger)
```

From that point on, any `slog` call using that context is captured:

```go
slog.InfoContext(ctx, "customer loaded",
    "customer_id", customerID,
)

slog.InfoContext(ctx, "debt loaded",
    "debt_id", debtID,
)
```

At the end:

```go
event.End(ctx, slog.LevelInfo, "request completed")
```

And `wideslog` emits a single structured log entry containing the entire operation.

## `With` still works the way you expect

`wideslog` preserves `slog`'s hierarchical logger semantics.

```go
customerLogger := logger.With(
    "customer_id", customerID,
)

customerLogger.InfoContext(ctx, "customer loaded")

logger.InfoContext(ctx, "something else")
```

Only the first event receives `customer_id`.

This:

```text
logger
  │
  ├── Info("A")
  │
  └── With(customer_id)
        │
        ├── Info("B")
        └── With(debt_id)
              │
              └── Info("C")
```

becomes:

```json
{
  "events": [
    {
      "msg": "A"
    },
    {
      "msg": "B",
      "customer_id": "123"
    },
    {
      "msg": "C",
      "customer_id": "123",
      "debt_id": "456"
    }
  ]
}
```

No magic. Just normal `slog` semantics.

## Timestamp modes

Choose how timing is represented for each buffered event. The root event always
includes `timestamp` and `duration`; these options do not remove or change the
root timestamp.

### No timestamp

```go
ctx, event := wideslog.Start(ctx, logger,
    wideslog.WithTimestampMode(wideslog.TimestampNone),
)
logger.InfoContext(ctx, "customer loaded")
event.End(ctx, slog.LevelInfo, "completed")
```

```json
{
  "msg": "customer loaded"
}
```

### Absolute timestamp

```go
ctx, event := wideslog.Start(ctx, logger,
  wideslog.WithTimestampMode(wideslog.TimestampAbsolute),
)
logger.InfoContext(ctx, "customer loaded")
event.End(ctx, slog.LevelInfo, "completed")
```

```json
{
  "timestamp": "2026-08-26T20:31:42.101Z",
  "msg": "customer loaded"
}
```

### Relative offset

```go
ctx, event := wideslog.Start(ctx, logger,
  wideslog.WithTimestampMode(wideslog.TimestampOffset),
  wideslog.WithOffsetUnit(wideslog.OffsetMicroseconds),
)
logger.InfoContext(ctx, "customer loaded")
event.End(ctx, slog.LevelInfo, "completed")
```

```json
{
  "offset_us": 1200,
  "msg": "customer loaded"
}
```

Offsets can be expressed in:

```go
wideslog.OffsetNanoseconds
wideslog.OffsetMicroseconds
wideslog.OffsetMilliseconds
```

The default is **microseconds**.

For example, with `OffsetMilliseconds`, a pause between two logs can produce:

```json
{
  "timestamp": "2026-08-26T20:31:42.100Z",
  "duration": 25000000,
  "events": [
    {"offset_ms": 0, "level": "INFO", "msg": "started"},
    {"offset_ms": 25, "level": "INFO", "msg": "finished"}
  ]
}
```

## Getting started

Create your normal `slog` handler:

```go
handler := slog.NewJSONHandler(
    os.Stdout,
    &slog.HandlerOptions{
        Level: slog.LevelInfo,
    },
)
```

Wrap it with `wideslog`:

```go
logger := wideslog.New(handler)
```

Start an event:

```go
ctx, event := wideslog.Start(ctx, logger)
```

Use `slog` normally:

```go
logger.InfoContext(ctx, "loading customer")

// ...

logger.InfoContext(ctx, "customer loaded",
    "customer_id", customerID,
)

// ...

logger.InfoContext(ctx, "debt loaded",
    "debt_id", debtID,
)
```

Finish the event:

```go
event.End(ctx, slog.LevelInfo, "request completed")
```

That's it.

## Echo example

`wideslog` works particularly well as an HTTP middleware.

```go
func LoggingMiddleware(logger *slog.Logger) echo.MiddlewareFunc {
    return func(next echo.HandlerFunc) echo.HandlerFunc {
        return func(c echo.Context) error {
            ctx, event := wideslog.Start(
                c.Request().Context(),
                logger,
            )

            c.SetRequest(
                c.Request().WithContext(ctx),
            )

            err := next(c)

            event.End(
                ctx,
                slog.LevelInfo,
                "request completed",
                slog.Int("http.status", c.Response().Status),
            )

            return err
        }
    }
}
```

Now your handler, services and repositories can all contribute to the same wide event without passing a logger or event object around explicitly.

```text
HTTP middleware
      │
      ▼
  Wide Event
      │
      ├── Handler
      │
      ├── Service
      │
      ├── Repository
      │
      └── External API
             │
             ▼
        One log entry
```

## Why not just use `slog.With`?

`slog.With` is great for attaching context to a logger.

But it doesn't solve the problem of **correlating an entire operation**.

With traditional logging:

```text
log 1 ─────┐
log 2 ─────┤
log 3 ─────┼── request_id ──> reconstruct the request
log 4 ─────┤
log 5 ─────┘
```

With `wideslog`:

```text
             ┌─────────────────────┐
             │     Wide Event      │
             │                     │
             │  request metadata   │
             │  event 1            │
             │  event 2            │
             │  event 3            │
             │  event 4            │
             └─────────────────────┘
```

The correlation happens **before the log leaves your application**.

## When should you use it?

`wideslog` is particularly useful when an operation naturally has a lifecycle:

- HTTP requests
- background jobs
- message processing
- scheduled tasks
- workflows
- database operations
- asynchronous jobs

If you want every individual log line to be independently searchable and independently indexed, traditional `slog` may be a better fit.

If you want **one complete record describing what happened during an operation**, that's where `wideslog` shines.

## Status

🚧 Early-stage / experimental.

The API may change while the project evolves.

## License

[Add license here]