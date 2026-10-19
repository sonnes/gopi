package permission_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sonnes/gopi/pkg/harness/permission"
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
		{rule: "Read(.env)", tool: "read_file", path: ".env", want: true},
		{rule: "Read(.env)", tool: "read_file", path: "web/.env", want: true},
		{rule: "Read(.env)", tool: "read_file", path: "/work/project/web/.env", want: true},
		{rule: "Read(.env)", tool: "read_file", path: "/elsewhere/.env", want: false},
		{rule: "Read(*.key)", tool: "read_file", path: "certs/a.key", want: true},
		{rule: "Read(./.env)", tool: "read_file", path: "web/.env", want: true},

		// A pattern with a slash is anchored to Dir.
		{rule: "Read(src/*.go)", tool: "read_file", path: "src/main.go", want: true},
		{rule: "Read(src/*.go)", tool: "read_file", path: "src/sub/main.go", want: false},
		{rule: "Read(src/*.go)", tool: "read_file", path: "lib/src/main.go", want: false},
		{rule: "Read(src/**)", tool: "read_file", path: "src/sub/main.go", want: true},
		{rule: "Read(/src/**)", tool: "read_file", path: "src/sub/main.go", want: true},
		{rule: "Read(/src/**)", tool: "read_file", path: "lib/src/main.go", want: false},

		// A pattern that matches a directory matches each path in it.
		{rule: "Read(secrets)", tool: "read_file", path: "a/secrets/key.pem", want: true},
		{rule: "Read(/secrets/)", tool: "read_file", path: "secrets/key.pem", want: true},

		// // is the root of the file system, and ~/ is the home directory.
		{rule: "Read(//etc/**)", tool: "read_file", path: "/etc/passwd", want: true},
		{rule: "Read(//etc/**)", tool: "read_file", path: "etc/passwd", want: false},
		{rule: "Read(~/.ssh/**)", tool: "read_file", path: "/home/user/.ssh/id_rsa", want: true},
		{rule: "Read(~/.ssh/**)", tool: "read_file", path: "~/.ssh/id_rsa", want: true},
		{rule: "Read(~/.ssh/**)", tool: "read_file", path: ".ssh/id_rsa", want: false},

		// A relative path in the input resolves against Dir.
		{rule: "Read(//work/project/a.txt)", tool: "read_file", path: "a.txt", want: true},
		{rule: "Read(//work/project/a.txt)", tool: "read_file", path: "sub/../a.txt", want: true},

		// The aliases cover the tools of their kind.
		{rule: "Read(src/**)", tool: "grep", path: "src", want: true},
		{rule: "Read(src/**)", tool: "find", path: "src/pkg", want: true},
		{rule: "Edit(src/**)", tool: "write_file", path: "src/a.go", want: true},
		{rule: "Edit(src/**)", tool: "edit_file", path: "src/a.go", want: true},
		{rule: "Write(src/**)", tool: "edit_file", path: "src/a.go", want: false},
		{rule: "read_file(src/**)", tool: "grep", path: "src", want: false},
		{rule: "grep(src/**)", tool: "grep", path: "src/a", want: true},

		// An empty path of grep and find is Dir.
		{rule: "Read(//work/project)", tool: "grep", path: "", want: true},
		{rule: "Read(/**)", tool: "find", path: "", want: true},
		{rule: "Read(src/**)", tool: "find", path: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.rule+"/"+tt.tool+"/"+tt.path, func(t *testing.T) {
			t.Setenv("HOME", testHome)
			p := &permission.Policy{
				Mode: permission.Bypass,
				Dir:  testDir,
				Deny: []string{tt.rule},
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
		Allow: []string{"Edit(docs/**)"},
	}
	assert.Equal(t, ran, decide(t, p, pathCall(t, "write_file", "docs/a.md")))
	assert.Equal(t, asked, decide(t, p, pathCall(t, "write_file", "src/a.go")))
}
