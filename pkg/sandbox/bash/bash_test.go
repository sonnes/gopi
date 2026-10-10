package bash

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/sandbox/internal/tooltest"
)

func TestBash(t *testing.T) {
	tests := []struct {
		name    string
		input   map[string]any
		want    string
		wantErr string
	}{
		{
			name:  "stdout",
			input: map[string]any{"command": "echo hello"},
			want:  "hello",
		},
		{
			name:  "stdout then stderr",
			input: map[string]any{"command": "echo out; echo err >&2"},
			want:  "out\nerr",
		},
		{
			name:  "stdout without a newline, then stderr",
			input: map[string]any{"command": "printf out; printf err >&2"},
			want:  "out\nerr",
		},
		{
			name:  "pipe",
			input: map[string]any{"command": "printf 'a b c' | wc -w | tr -d ' '"},
			want:  "3",
		},
		{
			name:  "no output",
			input: map[string]any{"command": "true"},
			want:  "(no output)",
		},
		{
			name:    "exit code",
			input:   map[string]any{"command": "echo failed; exit 2"},
			wantErr: "failed\n\nCommand exited with code 2",
		},
		{
			name:    "no command",
			input:   map[string]any{},
			wantErr: "command is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(tooltest.Local(t, t.TempDir())), tt.input)

			if tt.wantErr != "" {
				require.True(t, isErr, got)
				assert.Equal(t, tt.wantErr, got)
				return
			}
			require.False(t, isErr, got)
			assert.Equal(t, tt.want, got)
		})
	}
}

// fullOutput matches the note of a truncated result, and captures the
// path of the full output.
var fullOutput = regexp.MustCompile(`Full output: (\S+)\]$`)

func TestBashKeepsTheTailOfALongOutput(t *testing.T) {
	var lines []string
	for i := 1; i <= 3000; i++ {
		lines = append(lines, fmt.Sprint(i))
	}
	wideLine := strings.Repeat("x", 1000)

	tests := []struct {
		name     string
		command  string
		full     string
		wantHead string
	}{
		{
			name:     "line limit",
			command:  "seq 1 3000",
			full:     strings.Join(lines, "\n"),
			wantHead: strings.Join(lines[1000:], "\n") + "\n\n[Showing lines 1001-3000 of 3000. Full output: ",
		},
		{
			name:     "byte limit",
			command:  "for i in $(seq 1 60); do printf '%01000d\\n' 0 | tr 0 x; done",
			full:     strings.TrimSuffix(strings.Repeat(wideLine+"\n", 60), "\n"),
			wantHead: strings.TrimSuffix(strings.Repeat(wideLine+"\n", 51), "\n") + "\n\n[Showing lines 10-60 of 60 (50KB limit). Full output: ",
		},
		{
			name:     "one long line",
			command:  "head -c 60000 /dev/zero | tr '\\0' x",
			full:     strings.Repeat("x", 60000),
			wantHead: strings.Repeat("x", maxBytes) + "\n\n[Showing the last 50KB of line 1 (the line is 60000 bytes). Full output: ",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isErr := tooltest.Call(t, New(tooltest.Local(t, t.TempDir())), map[string]any{"command": tt.command})

			require.False(t, isErr)
			assert.True(t, strings.HasPrefix(got, tt.wantHead), "got the head %q", got[:min(len(got), 200)])
			m := fullOutput.FindStringSubmatch(got)
			require.NotNil(t, m, "no path of the full output")
			data, err := os.ReadFile(m[1])
			require.NoError(t, err)
			assert.Equal(t, tt.full, string(data))
		})
	}
}

func TestBashTimeout(t *testing.T) {
	tool := New(tooltest.Local(t, t.TempDir()))

	start := time.Now()
	got, isErr := tooltest.Call(t, tool, map[string]any{
		"command": "echo started; sleep 10",
		"timeout": 1,
	})

	require.True(t, isErr)
	assert.Equal(t, "started\n\nCommand timed out after 1 seconds", got)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestBashReportsTheDeadlineOfTheCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	_, err := New(tooltest.Local(t, t.TempDir())).Run(ctx, ai.ToolCall{
		ID:    "call_1",
		Name:  Name,
		Input: json.RawMessage(`{"command": "sleep 10"}`),
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, err.Error(), "timed out after")
}

func TestBashReturnsAtOnceForABackgroundProgramWithItsOutputInAFile(t *testing.T) {
	dir := t.TempDir()
	tool := New(tooltest.Local(t, dir))

	start := time.Now()
	got, isErr := tooltest.Call(t, tool, map[string]any{"command": "(sleep 1; echo late) > bg.log 2>&1 &"})

	require.False(t, isErr, got)
	assert.Less(t, time.Since(start), 900*time.Millisecond)
	assert.Eventually(t, func() bool {
		data, _ := os.ReadFile(filepath.Join(dir, "bg.log"))
		return string(data) == "late\n"
	}, 5*time.Second, 50*time.Millisecond)
}

func TestBashTimeoutDefault(t *testing.T) {
	seconds := func(n int) *int { return &n }

	tests := []struct {
		name    string
		timeout *int
		want    time.Duration
	}{
		{name: "omitted", want: defaultTimeout},
		{name: "zero", timeout: seconds(0), want: defaultTimeout},
		{name: "given", timeout: seconds(30), want: 30 * time.Second},
		{name: "over the maximum", timeout: seconds(99999), want: maxTimeout},
		{name: "over the range of a duration", timeout: seconds(math.MaxInt), want: maxTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, bashInput{Timeout: tt.timeout}.timeout())
		})
	}
}

func TestBashIsNotInteractive(t *testing.T) {
	got, isErr := tooltest.Call(t, New(tooltest.Local(t, t.TempDir())), map[string]any{
		"command": "echo $GIT_TERMINAL_PROMPT $GIT_EDITOR; (: < /dev/tty) 2>/dev/null && echo tty || echo no tty",
	})

	require.False(t, isErr, got)
	assert.Equal(t, "0 true\nno tty", got)
}

func TestBashStartsEachCommandInTheSandboxDirectory(t *testing.T) {
	dir := t.TempDir()
	tooltest.WriteFiles(t, dir, map[string]string{"sub/f.txt": ""})
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	tool := New(tooltest.Local(t, dir))

	_, isErr := tooltest.Call(t, tool, map[string]any{"command": "cd sub"})
	require.False(t, isErr)
	got, isErr := tooltest.Call(t, tool, map[string]any{"command": "pwd -P"})

	require.False(t, isErr)
	assert.Equal(t, realDir, got)
}

func TestBashLoadsTheUserProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.WriteFile(
		filepath.Join(home, ".bash_profile"),
		[]byte("export GOPI_PROFILE_MARKER=loaded\n"),
		0o644,
	))

	got, isErr := tooltest.Call(t, New(tooltest.Local(t, t.TempDir())), map[string]any{
		"command": `printf %s "$GOPI_PROFILE_MARKER"`,
	})

	require.False(t, isErr)
	assert.Equal(t, "loaded", got)
}
