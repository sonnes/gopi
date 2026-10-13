package claude_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
)

// scrubLine keeps a recorded line small and free of the data of the
// machine that recorded it: the initialize response lists the account,
// and the system lines list the paths, tools, and plugins of the CLI.
func scrubLine(line []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		return line
	}
	switch m["type"] {
	case "system":
		m = map[string]any{
			"type":       m["type"],
			"subtype":    m["subtype"],
			"session_id": m["session_id"],
		}
	case "rate_limit_event":
		m = map[string]any{"type": m["type"]}
	case "control_response":
		resp, _ := m["response"].(map[string]any)
		if resp == nil || resp["request_id"] != "req_1" {
			return line
		}
		m = map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    resp["subtype"],
				"request_id": resp["request_id"],
				"response":   map[string]any{},
			},
		}
	default:
		return line
	}
	out, err := json.Marshal(m)
	if err != nil {
		return line
	}
	return out
}

// scrubDir replaces dir with a fixed value.
func scrubDir(dir, with string) stdiorr.Scrub {
	return func(line []byte) []byte {
		if dir == "" {
			return line
		}
		return bytes.ReplaceAll(line, []byte(dir), []byte(with))
	}
}

// TestRecorded runs the real CLI. Record it with
// `make record-cli AGENT=claude RUN=TestRecorded`.
func TestRecorded(t *testing.T) {
	dir := t.TempDir()
	home, _ := os.UserHomeDir()
	claude.Replay(t, []stdiorr.Scrub{
		scrubLine,
		// macOS reports a temporary directory under /private.
		scrubDir("/private"+dir, "/work"),
		scrubDir(dir, "/work"),
		scrubDir(home, "/home/me"),
	}, "testdata/TestRecorded.trace")

	a := claude.New(
		claude.Dir(dir),
		claude.Model("haiku"),
		claude.PermissionMode(claude.PermissionDontAsk),
	)
	defer closeAgent(t, a)

	events, msgs, err := run(context.Background(), a, "Reply with the word hi and nothing else.")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	require.NotEmpty(t, msgs)
	last := msgs[len(msgs)-1]
	assert.Equal(t, ai.RoleAssistant, last.Role)
	assert.Contains(t, strings.ToLower(last.Text()), "hi")
	checkLog(t, a)

	id, err := claude.SessionID(a)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
}
