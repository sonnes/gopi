package permission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/permission"
)

const (
	testDir  = "/work/project"
	testHome = "/home/user"
)

func TestPathRules(t *testing.T) {
	tests := []struct {
		rule string
		tool string
		path string
		want bool
	}{
		// A bare pattern without a slash matches at any depth under Dir.
		{rule: "read_file(.env)", tool: "read_file", path: ".env", want: true},
		{rule: "read_file(.env)", tool: "read_file", path: "web/.env", want: true},
		{rule: "read_file(.env)", tool: "read_file", path: "/work/project/web/.env", want: true},
		{rule: "read_file(.env)", tool: "read_file", path: "/elsewhere/.env", want: false},
		{rule: "read_file(*.key)", tool: "read_file", path: "certs/a.key", want: true},
		{rule: "read_file(./.env)", tool: "read_file", path: "web/.env", want: true},

		// A pattern with a slash is anchored to Dir.
		{rule: "read_file(src/*.go)", tool: "read_file", path: "src/main.go", want: true},
		{rule: "read_file(src/*.go)", tool: "read_file", path: "src/sub/main.go", want: false},
		{rule: "read_file(src/*.go)", tool: "read_file", path: "lib/src/main.go", want: false},
		{rule: "read_file(src/**)", tool: "read_file", path: "src/sub/main.go", want: true},
		{rule: "read_file(/src/**)", tool: "read_file", path: "src/sub/main.go", want: true},
		{rule: "read_file(/src/**)", tool: "read_file", path: "lib/src/main.go", want: false},

		// A pattern that matches a directory matches each path in it.
		{rule: "read_file(secrets)", tool: "read_file", path: "a/secrets/key.pem", want: true},
		{rule: "read_file(/secrets/)", tool: "read_file", path: "secrets/key.pem", want: true},

		// // is the root of the file system, and ~/ is the home directory.
		{rule: "read_file(//etc/**)", tool: "read_file", path: "/etc/passwd", want: true},
		{rule: "read_file(//etc/**)", tool: "read_file", path: "etc/passwd", want: false},
		{rule: "read_file(~/.ssh/**)", tool: "read_file", path: "/home/user/.ssh/id_rsa", want: true},
		{rule: "read_file(~/.ssh/**)", tool: "read_file", path: "~/.ssh/id_rsa", want: true},
		{rule: "read_file(~/.ssh/**)", tool: "read_file", path: ".ssh/id_rsa", want: false},

		// A relative path in the input resolves against Dir.
		{rule: "read_file(//work/project/a.txt)", tool: "read_file", path: "a.txt", want: true},
		{rule: "read_file(//work/project/a.txt)", tool: "read_file", path: "sub/../a.txt", want: true},

		// A rule names one tool.
		{rule: "write_file(src/**)", tool: "edit_file", path: "src/a.go", want: false},
		{rule: "read_file(src/**)", tool: "grep", path: "src", want: false},
		{rule: "grep(src/**)", tool: "grep", path: "src/a", want: true},

		// An empty path of grep and find is Dir.
		{rule: "grep(//work/project)", tool: "grep", path: "", want: true},
		{rule: "find(/**)", tool: "find", path: "", want: true},
		{rule: "find(src/**)", tool: "find", path: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.tool+"/"+tt.path, func(t *testing.T) {
			t.Setenv("HOME", testHome)
			p := &permission.Policy{
				Mode:  permission.Bypass,
				Dir:   testDir,
				Rules: permission.Rules{Deny: []string{tt.rule}},
			}
			input := map[string]any{"pattern": "x"}
			if tt.path != "" {
				input["path"] = tt.path
			}
			got := decide(t, p, call(t, tt.tool, input)) == denied
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPathAllow(t *testing.T) {
	p := &permission.Policy{
		Mode:  permission.Default,
		Dir:   testDir,
		Rules: permission.Rules{Allow: []string{"write_file(docs/**)"}},
	}
	assert.Equal(t, ran, decide(t, p, pathCall(t, "write_file", "docs/a.md")))
	assert.Equal(t, asked, decide(t, p, pathCall(t, "write_file", "src/a.go")))
}

// patch is the text of an apply_patch call that changes each path.
func patch(paths ...string) string {
	text := "*** Begin Patch\n"
	for _, p := range paths {
		text += "*** Update File: " + p + "\n@@\n-a\n+b\n"
	}
	return text + "*** End Patch\n"
}

func TestApplyPatchPaths(t *testing.T) {
	tests := []struct {
		name  string
		rule  string
		input map[string]any
		want  outcome
	}{
		{
			name:  "deny matches one path of the patch text",
			rule:  "apply_patch(src/**)",
			input: map[string]any{"command": patch("docs/a.md", "src/a.go")},
			want:  denied,
		},
		{
			name:  "deny matches no path of the patch text",
			rule:  "apply_patch(src/**)",
			input: map[string]any{"command": patch("docs/a.md")},
			want:  ran,
		},
		{
			name: "deny matches one path of the changes",
			rule: "apply_patch(src/**)",
			input: map[string]any{"changes": []map[string]any{
				{"path": "/work/project/src/a.go"},
			}},
			want: denied,
		},
		{
			name:  "deny matches a patch with no path",
			rule:  "apply_patch(src/**)",
			input: map[string]any{"command": "garbage"},
			want:  denied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &permission.Policy{
				Mode:  permission.Bypass,
				Dir:   testDir,
				Rules: permission.Rules{Deny: []string{tt.rule}},
			}
			assert.Equal(t, tt.want, decide(t, p, call(t, "apply_patch", tt.input)))
		})
	}
}

func TestApplyPatchAllowNeedsEachPath(t *testing.T) {
	p := &permission.Policy{
		Mode:  permission.Default,
		Dir:   testDir,
		Rules: permission.Rules{Allow: []string{"apply_patch(docs/**)"}},
	}
	one := call(t, "apply_patch", map[string]any{"command": patch("docs/a.md")})
	two := call(t, "apply_patch", map[string]any{"command": patch("docs/a.md", "src/a.go")})

	assert.Equal(t, ran, decide(t, p, one))
	assert.Equal(t, asked, decide(t, p, two))
}
