package permission_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
)

func TestExact(t *testing.T) {
	dir := t.TempDir()
	in := func(name string) string { return filepath.Join(dir, name) }
	tests := []struct {
		name     string
		call     ai.ToolCall
		want     string
		neighbor ai.ToolCall
	}{
		{
			name:     "a command",
			call:     bashCall(t, "go test ./pkg/host/..."),
			want:     "Bash(go test ./pkg/host/...)",
			neighbor: bashCall(t, "go test ./..."),
		},
		{
			name:     "a command with a star",
			call:     bashCall(t, "rm *.tmp"),
			want:     `Bash(rm \*.tmp)`,
			neighbor: bashCall(t, "rm a.tmp"),
		},
		{
			name:     "a command with parentheses",
			call:     bashCall(t, "echo (a)"),
			want:     "Bash(echo (a))",
			neighbor: bashCall(t, "echo (b)"),
		},
		{
			name:     "a command with a backslash",
			call:     bashCall(t, `printf 'a\nb'`),
			want:     `Bash(printf 'a\nb')`,
			neighbor: bashCall(t, `printf 'anb'`),
		},
		{
			name:     "a command with a backslash before a star",
			call:     bashCall(t, `echo \*`),
			want:     `Bash(echo \\\\\*)`,
			neighbor: bashCall(t, `echo *`),
		},
		{
			name:     "a command that ends in a backslash",
			call:     bashCall(t, `echo a\`),
			want:     `Bash(echo a\\)`,
			neighbor: bashCall(t, `echo a`),
		},
		{
			name:     "a command that ends in the prefix mark",
			call:     bashCall(t, "echo a:*"),
			want:     `Bash(echo a:\*)`,
			neighbor: bashCall(t, "echo a:b"),
		},
		{
			name:     "a compound command",
			call:     bashCall(t, "ls && rm -rf build"),
			want:     "Bash(ls && rm -rf build)",
			neighbor: bashCall(t, "ls"),
		},
		{
			name:     "an edit",
			call:     pathCall(t, "edit_file", in("a.go")),
			want:     "Edit(/" + in("a.go") + ")",
			neighbor: pathCall(t, "edit_file", in("b.go")),
		},
		{
			name:     "an edit of Claude Code",
			call:     call(t, "write_file", map[string]any{"file_path": in("a.go")}),
			want:     "Edit(/" + in("a.go") + ")",
			neighbor: call(t, "write_file", map[string]any{"file_path": in("a.go.bak")}),
		},
		{
			name:     "a relative read",
			call:     pathCall(t, "read_file", "a.txt"),
			want:     "Read(/" + in("a.txt") + ")",
			neighbor: pathCall(t, "read_file", "b.txt"),
		},
		{
			name:     "a path with glob characters",
			call:     pathCall(t, "edit_file", in("[a]*.go")),
			want:     "Edit(/" + in(`\[a\]\*.go`) + ")",
			neighbor: pathCall(t, "edit_file", in("ab.go")),
		},
		{
			name:     "a patch with one path",
			call:     call(t, "apply_patch", map[string]any{"changes": []map[string]string{{"path": in("a.go")}}}),
			want:     "Edit(/" + in("a.go") + ")",
			neighbor: call(t, "apply_patch", map[string]any{"changes": []map[string]string{{"path": in("a.go")}, {"path": in("b.go")}}}),
		},
		{
			name:     "a web fetch",
			call:     call(t, "web_fetch", map[string]any{"url": "https://go.dev/doc", "prompt": "x"}),
			want:     "WebFetch(domain:go.dev)",
			neighbor: call(t, "web_fetch", map[string]any{"url": "https://pkg.go.dev/fmt"}),
		},
		{
			name:     "a tool with no content",
			call:     call(t, "mcp__lw__tasks_create", map[string]any{"title": "x"}),
			want:     "mcp__lw__tasks_create",
			neighbor: call(t, "mcp__lw__tasks_delete", map[string]any{}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := permission.Exact(tt.call, dir)

			require.True(t, ok)
			assert.Equal(t, tt.want, got)
			rules := permission.Rules{Allow: []string{got}}
			match, _, err := rules.Check(tt.call, dir)
			require.NoError(t, err)
			assert.Equal(t, permission.AllowMatch, match, "the rule allows the call")
			match, _, err = rules.Check(tt.neighbor, dir)
			require.NoError(t, err)
			assert.Equal(t, permission.NoMatch, match, "the rule does not allow the neighbor")
		})
	}
}

func TestExactHasNoRule(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink(t.TempDir(), link))
	tests := []struct {
		name string
		call ai.ToolCall
	}{
		{
			name: "an input that does not decode",
			call: ai.ToolCall{Name: "bash", Input: []byte("not json")},
		},
		{
			name: "an empty command",
			call: bashCall(t, "  "),
		},
		{
			name: "a URL with no host",
			call: call(t, "web_fetch", map[string]any{"url": "::not a url"}),
		},
		{
			name: "a patch with two paths",
			call: call(t, "apply_patch", map[string]any{"changes": []map[string]string{
				{"path": filepath.Join(dir, "a.go")},
				{"path": filepath.Join(dir, "b.go")},
			}}),
		},
		{
			name: "a path through a symlink",
			call: pathCall(t, "edit_file", filepath.Join(link, "a.go")),
		},
		{
			name: "the root folder",
			call: pathCall(t, "read_file", "/"),
		},
		{
			name: "a tool of the caller with a Claude Code name",
			call: call(t, "Read", map[string]any{}),
		},
		{
			name: "the name of an MCP server",
			call: call(t, "mcp__lw", map[string]any{}),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := permission.Exact(tt.call, dir)

			assert.False(t, ok)
		})
	}
}

func TestExactCommandRoundTrip(t *testing.T) {
	dir := t.TempDir()
	commands := []string{
		`a\(b`,
		`echo \)`,
		`echo (\\)`,
		`*\\*`,
		`echo \\\*`,
		`printf '%s\n' "(a)" \`,
		`grep -E 'a|b' *.go`,
		`git commit -m "x:*"`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			rule, ok := permission.Exact(bashCall(t, command), dir)
			require.True(t, ok)

			rules := permission.Rules{Allow: []string{rule}}
			match, _, err := rules.Check(bashCall(t, command), dir)
			require.NoError(t, err)
			assert.Equal(t, permission.AllowMatch, match, "the rule %s allows the command", rule)
			match, _, err = rules.Check(bashCall(t, command+"x"), dir)
			require.NoError(t, err)
			assert.Equal(t, permission.NoMatch, match, "the rule %s allows a longer command", rule)
		})
	}
}
