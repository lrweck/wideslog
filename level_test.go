package wideslog

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetLevelOverridesRoot(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	_, event := NewEvent(context.Background(), logger, "op")
	event.SetLevel(slog.LevelError)
	event.End()

	assert.Equal(t, "ERROR", decodeLog(t, &buf)["level"])
}

func TestSetLevelWarnAboveInfoSteps(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "a step")
	event.SetLevel(slog.LevelWarn)
	event.End()

	assert.Equal(t, "WARN", decodeLog(t, &buf)["level"])
}

func TestSetLevelAfterEndIsNoop(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	_, event := NewEvent(context.Background(), logger, "op")
	event.End()
	n := buf.Len()
	event.SetLevel(slog.LevelError)

	require.Equal(t, n, buf.Len(), "SetLevel after End must not re-emit")
	assert.Equal(t, "INFO", decodeLog(t, &buf)["level"], "root keeps the step-derived level")
}
