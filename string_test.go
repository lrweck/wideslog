package wideslog

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A consumer that formats the events value with fmt (the OpenTelemetry log
// bridge does `fmt.Sprintf("%+v", v)`) must get JSON, not the Go struct.
func TestEventsArrayStringIsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := JSONHandler(&buf, nil)

	ctx, event := NewEvent(context.Background(), logger, "op")
	logger.InfoContext(ctx, "step one")
	event.End()

	var root map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &root))
	require.NotNil(t, root["events"])

	assert.Equal(t, "[]", fmt.Sprintf("%+v", eventsArray{}))

	rendered := fmt.Sprintf("%+v", eventsArray{entries: []eventEntry{{record: eventRecord{message: "step one"}}}})
	assert.JSONEq(t, `[{"level":"INFO","msg":"step one"}]`, rendered)
	assert.NotContains(t, rendered, "entries:")
}
