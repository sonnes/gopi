package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHookCommandDeniesWithoutAHost(t *testing.T) {
	var out bytes.Buffer
	cmd := hookCommand()
	cmd.Reader = strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`)
	cmd.Writer = &out
	socket := filepath.Join(t.TempDir(), "missing.sock")

	err := cmd.Run(context.Background(), []string{"hook", socket})
	require.NoError(t, err)
	assert.Contains(t, out.String(), `"permissionDecision":"deny"`)
}

func TestHookCommandNeedsASocket(t *testing.T) {
	cmd := hookCommand()
	cmd.Reader = strings.NewReader(`{}`)
	cmd.Writer = &bytes.Buffer{}

	err := cmd.Run(context.Background(), []string{"hook"})
	assert.EqualError(t, err, "hook: give the path of the socket")
}
