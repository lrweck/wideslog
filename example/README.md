# wideslog examples

The executable in this directory compares standard `slog` with `wideslog`.
It also demonstrates multiple timestamp modes and uses short pauses between
logs so the differences are visible.

Run it from the repository root:

```sh
go run ./example
```

## Standard `slog`

Without `wideslog`, each log call produces a separate JSON record:

```go
logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

logger.Info("request started", "method", "GET")
time.Sleep(25 * time.Millisecond)
logger.Info("user loaded", "user_id", 42)
time.Sleep(25 * time.Millisecond)
logger.Info("request completed")
```

The example uses `time.Sleep` only to make the timestamps easy to compare.

## Wide event

`wideslog` buffers multiple logs and writes one final record. Attributes added
with `Event.Add` belong to the final record:

```go
logger := wideslog.JSONHandler(os.Stdout, nil)
ctx, event := wideslog.NewEvent(context.Background(), logger, "request completed")
event.Add(slog.String("service", "accounts"))

logger.InfoContext(ctx, "request started", "method", "GET")
time.Sleep(25 * time.Millisecond)
logger.InfoContext(ctx, "user loaded", "user_id", 42)
time.Sleep(25 * time.Millisecond)
logger.WarnContext(ctx, "slow dependency", "dependency", "profile-api")

event.End(ctx)
```

The final record contains the event attributes, duration, and all buffered
logs. The message passed to `NewEvent` identifies the operation on the root
record:

```json
{
    "timestamp": "2026-08-26T20:31:42.100Z",
    "msg": "request completed",
    "service": "accounts",
    "duration_ms": 50,
    "event_count": 3,
    "events": [
        {"offset_us": 5, "level": "INFO", "msg": "request started", "method": "GET"},
        {"offset_us": 25000, "level": "INFO", "msg": "user loaded", "user_id": 42},
        {"offset_us": 50000, "level": "WARN", "msg": "slow dependency", "dependency": "profile-api"}
    ]
}
```

The root `timestamp` is always present and marks when the wide event started.
The exact timestamps and durations vary on every run. `duration_ms` is the
elapsed root-event duration in milliseconds.

### Scoped attributes

Logger attributes remain scoped to the logger that created them:

```go
customerLogger := logger.With("customer_id", "123")
customerLogger.InfoContext(ctx, "customer loaded")
logger.InfoContext(ctx, "root event")
```

Groups are preserved as nested objects:

```go
customerLogger := logger.WithGroup("customer").With("id", "customer-1")
customerLogger.InfoContext(ctx, "customer loaded")
```

## Timestamp modes

Choose how timestamps are stored on each buffered log. The default is
`TimestampOffset` with `OffsetMicroseconds`. These options affect only the
items inside `events`; the root `timestamp` is always emitted:

```go
ctx, event := wideslog.NewEvent(context.Background(), logger, "step completed",
    wideslog.WithTimestampMode(wideslog.TimestampNone),
)

// No timestamp on individual logs.
logger.InfoContext(ctx, "step completed")
event.End(ctx)
```

Use `TimestampAbsolute` like this:

```go
ctx, event := wideslog.NewEvent(context.Background(), logger, "step completed",
    wideslog.WithTimestampMode(wideslog.TimestampAbsolute),
)
logger.InfoContext(ctx, "step completed")
event.End(ctx)
```

Use `TimestampOffset` like this:

```go
ctx, event := wideslog.NewEvent(context.Background(), logger, "step completed",
    wideslog.WithTimestampMode(wideslog.TimestampOffset),
)
logger.InfoContext(ctx, "step completed")
event.End(ctx)
```

The modes produce these per-event fields:

| Mode | Field |
| --- | --- |
| `TimestampNone` | no timestamp field |
| `TimestampAbsolute` | `timestamp` |
| `TimestampOffset` | `offset_ns`, `offset_us`, or `offset_ms` |

For offset timestamps, choose the unit independently:

```go
wideslog.WithOffsetUnit(wideslog.OffsetNanoseconds)
wideslog.WithOffsetUnit(wideslog.OffsetMicroseconds) // default
wideslog.WithOffsetUnit(wideslog.OffsetMilliseconds)
```

Example:

```go
ctx, event := wideslog.NewEvent(context.Background(), logger, "step completed",
    wideslog.WithTimestampMode(wideslog.TimestampOffset),
    wideslog.WithOffsetUnit(wideslog.OffsetMilliseconds),
)

logger.InfoContext(ctx, "first step")
time.Sleep(25 * time.Millisecond)
logger.InfoContext(ctx, "second step")
event.End(ctx)
```

`main.go` runs all of these cases, including standard `slog`, `TimestampNone`,
`TimestampAbsolute`, and `TimestampOffset`.
