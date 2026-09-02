package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/lrweck/wideslog"
)

const logPause = 25 * time.Millisecond

func main() {
	plainSlog()
	wideEvent()
	timestampModes()
}

func plainSlog() {
	fmt.Println("--- standard slog: one record per log ---")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	logger.Info("request started", "method", "GET")
	time.Sleep(logPause)
	logger.Info("user loaded", "user_id", 42)
	time.Sleep(logPause)
	logger.Info("request completed")
}

func wideEvent() {
	fmt.Println("--- wideslog: multiple logs in one record ---")
	logger := wideslog.JSONHandler(os.Stdout, nil)
	ctx, event := wideslog.NewEvent(context.Background(), logger, "request completed")
	event.Add(slog.String("service", "accounts"))

	logger.InfoContext(ctx, "request started", "method", "GET")
	time.Sleep(logPause)
	logger.InfoContext(ctx, "user loaded", "user_id", 42)
	time.Sleep(logPause)
	logger.WarnContext(ctx, "slow dependency", "dependency", "profile-api")

	event.End()
}

func timestampModes() {
	fmt.Println("--- timestamp modes ---")

	showTimeMode("none", wideslog.TimeNone)
	showTimeMode("absolute", wideslog.TimeAbsolute)
	showTimeMode("offset", wideslog.TimeOffset)
}

func showTimeMode(name string, mode wideslog.TimeMode) {
	logger := wideslog.JSONHandler(os.Stdout, nil)
	ctx, event := wideslog.NewEvent(context.Background(), logger,
		name+" example",
		wideslog.WithTimeMode(mode),
		wideslog.WithOffsetUnit(wideslog.OffsetMilliseconds),
	)

	logger.InfoContext(ctx, name+" timestamp", "step", 1)
	time.Sleep(logPause)
	logger.LogAttrs(ctx, slog.LevelInfo, "finished", slog.Int("step", 2))

	event.End()
}
