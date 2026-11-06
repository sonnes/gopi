package permission_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness/permission"
)

// folders makes a root and a folder outside it, each with a.txt.
func folders(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	for _, dir := range []string{root, outside} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600))
	}
	return root, outside
}

func TestTheModeChecksTheRoots(t *testing.T) {
	root, outside := folders(t)
	calls := map[string]ai.ToolCall{
		"read in":        pathCall(t, "read_file", filepath.Join(root, "a.txt")),
		"read out":       pathCall(t, "read_file", filepath.Join(outside, "a.txt")),
		"grep out":       pathCall(t, "grep", outside),
		"edit in":        pathCall(t, "edit_file", "a.txt"),
		"edit out":       pathCall(t, "write_file", filepath.Join(outside, "b.txt")),
		"edit protected": pathCall(t, "edit_file", ".git/hooks/pre-commit"),
		"read protected": pathCall(t, "read_file", ".git/config"),
	}
	tests := []struct {
		mode permission.Mode
		want map[string]outcome
	}{
		{
			mode: permission.Default,
			want: map[string]outcome{
				"read in":        ran,
				"read out":       asked,
				"grep out":       asked,
				"edit in":        asked,
				"edit out":       asked,
				"edit protected": asked,
				"read protected": ran,
			},
		},
		{
			mode: permission.AcceptEdits,
			want: map[string]outcome{
				"read in":        ran,
				"read out":       asked,
				"grep out":       asked,
				"edit in":        ran,
				"edit out":       asked,
				"edit protected": asked,
				"read protected": ran,
			},
		},
		{
			mode: permission.Plan,
			want: map[string]outcome{
				"read in":        ran,
				"read out":       asked,
				"grep out":       asked,
				"edit in":        denied,
				"edit out":       denied,
				"edit protected": denied,
				"read protected": ran,
			},
		},
		{
			mode: permission.Bypass,
			want: map[string]outcome{
				"read in":        ran,
				"read out":       ran,
				"grep out":       ran,
				"edit in":        ran,
				"edit out":       ran,
				"edit protected": ran,
				"read protected": ran,
			},
		},
	}
	for _, tt := range tests {
		for name, want := range tt.want {
			t.Run(string(tt.mode)+"/"+name, func(t *testing.T) {
				p := &permission.Policy{Mode: tt.mode, Dir: root}
				assert.Equal(t, want, decide(t, p, calls[name]))
			})
		}
	}
}

func TestEachRootActsAsDir(t *testing.T) {
	root, extra := folders(t)
	p := &permission.Policy{
		Mode:  permission.AcceptEdits,
		Dir:   root,
		Roots: []string{root, extra},
	}

	assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", filepath.Join(extra, "a.txt"))))
	assert.Equal(t, ran, decide(t, p, pathCall(t, "edit_file", filepath.Join(extra, "a.txt"))))
}

func TestRootsReplaceDir(t *testing.T) {
	root, extra := folders(t)
	p := &permission.Policy{
		Dir:   root,
		Roots: []string{extra},
	}

	assert.Equal(t, asked, decide(t, p, pathCall(t, "read_file", "a.txt")))
	assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", filepath.Join(extra, "a.txt"))))
}

func TestARelativeRootIsInDir(t *testing.T) {
	root, _ := folders(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
	p := &permission.Policy{
		Dir:   root,
		Roots: []string{"sub"},
	}

	assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", "sub/a.txt")))
	assert.Equal(t, asked, decide(t, p, pathCall(t, "read_file", "a.txt")))
}

func TestAToolWithNoPathIsInTheRoots(t *testing.T) {
	root, _ := folders(t)
	p := &permission.Policy{Dir: root}

	assert.Equal(t, ran, decide(t, p, call(t, "todo_write", map[string]any{"path": "/etc/passwd"})))
}

func TestSymlinks(t *testing.T) {
	root, outside := folders(t)
	link := filepath.Join(root, "key")
	require.NoError(t, os.Symlink(filepath.Join(outside, "a.txt"), link))
	folder := filepath.Join(root, "out")
	require.NoError(t, os.Symlink(outside, folder))
	hop := filepath.Join(root, "hop")
	require.NoError(t, os.Symlink(link, hop))
	loop := filepath.Join(root, "loop")
	require.NoError(t, os.Symlink(loop, loop))

	tests := []struct {
		name   string
		policy permission.Policy
		call   ai.ToolCall
		want   outcome
	}{
		{
			name: "a read through a link to a file outside asks",
			call: pathCall(t, "read_file", "key"),
			want: asked,
		},
		{
			name: "a read through two links asks",
			call: pathCall(t, "read_file", "hop"),
			want: asked,
		},
		{
			name:   "a new file in a linked folder outside asks",
			policy: permission.Policy{Mode: permission.AcceptEdits},
			call:   pathCall(t, "write_file", "out/new.txt"),
			want:   asked,
		},
		{
			name:   "a new file in the root runs",
			policy: permission.Policy{Mode: permission.AcceptEdits},
			call:   pathCall(t, "write_file", "new/new.txt"),
			want:   ran,
		},
		{
			name:   "a deny rule matches the target of a link",
			policy: permission.Policy{Mode: permission.Bypass, Deny: []string{"read_file(//" + outside[1:] + "/**)"}},
			call:   pathCall(t, "read_file", "hop"),
			want:   denied,
		},
		{
			name:   "an allow rule needs each spelling",
			policy: permission.Policy{Allow: []string{"edit_file(/**)"}},
			call:   pathCall(t, "edit_file", "key"),
			want:   asked,
		},
		{
			name:   "an allow rule runs a file with one spelling",
			policy: permission.Policy{Allow: []string{"edit_file(/**)"}},
			call:   pathCall(t, "edit_file", "a.txt"),
			want:   ran,
		},
		{
			name:   "a link loop is denied in bypass",
			policy: permission.Policy{Mode: permission.Bypass},
			call:   pathCall(t, "read_file", "loop"),
			want:   denied,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.policy
			p.Dir = root
			assert.Equal(t, tt.want, decide(t, &p, tt.call))
		})
	}
}

func TestTheRealPathOfARootIsInTheRoot(t *testing.T) {
	root, _ := folders(t)
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	p := &permission.Policy{Dir: root}

	assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", filepath.Join(real, "a.txt"))))
}

func TestTheAliasesOfMacOSAreOnePath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the aliases exist only on macOS")
	}
	dir, err := os.MkdirTemp("/tmp", "permission-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o600))
	p := &permission.Policy{
		Dir:   dir,
		Allow: []string{"write_file(//tmp/**)"},
	}

	assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", "/private"+dir+"/a.txt")))
	assert.Equal(t, ran, decide(t, p, pathCall(t, "write_file", filepath.Join(dir, "b.txt"))))
}

func TestProtectedPaths(t *testing.T) {
	root, _ := folders(t)
	tests := []struct {
		path      string
		protected bool
	}{
		{path: "main.go"},
		{path: ".git/config", protected: true},
		{path: ".git/hooks/pre-commit", protected: true},
		{path: "sub/.git/HEAD", protected: true},
		{path: ".GIT/config", protected: true},
		{path: ".claude/settings.json", protected: true},
		{path: ".claude/skills/x/SKILL.md", protected: true},
		{path: ".claude/worktrees/a/main.go"},
		{path: ".vscode/settings.json", protected: true},
		{path: ".husky/pre-commit", protected: true},
		{path: ".devcontainer/devcontainer.json", protected: true},
		{path: "home/.config/git/config", protected: true},
		{path: ".config/other"},
		{path: ".bashrc", protected: true},
		{path: "web/.envrc", protected: true},
		{path: ".mcp.json", protected: true},
		{path: "lefthook.yml", protected: true},
		{path: "gradle/wrapper/gradle-wrapper.properties", protected: true},
		{path: "bashrc"},
		{path: ".bashrc.bak"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			p := &permission.Policy{Mode: permission.AcceptEdits, Dir: root}
			want := ran
			if tt.protected {
				want = asked
			}

			assert.Equal(t, want, decide(t, p, pathCall(t, "edit_file", tt.path)))
			assert.Equal(t, ran, decide(t, p, pathCall(t, "read_file", tt.path)), "a read is never protected")
		})
	}
}

func TestAProtectedPathInAPatchAsks(t *testing.T) {
	root, _ := folders(t)
	p := &permission.Policy{Mode: permission.AcceptEdits, Dir: root}
	patch := "*** Begin Patch\n*** Update File: a.txt\n*** Add File: .husky/pre-push\n*** End Patch"

	assert.Equal(t, asked, decide(t, p, call(t, "apply_patch", map[string]any{"command": patch})))
}

func TestHardLimits(t *testing.T) {
	root, outside := folders(t)
	readOut := pathCall(t, "read_file", filepath.Join(outside, "a.txt"))
	editOut := pathCall(t, "edit_file", filepath.Join(outside, "a.txt"))
	readIn := pathCall(t, "read_file", "a.txt")
	editIn := pathCall(t, "edit_file", "a.txt")

	tests := []struct {
		name   string
		policy permission.Policy
		call   ai.ToolCall
		want   outcome
	}{
		{name: "reads: a read outside is denied in bypass", policy: permission.Policy{Mode: permission.Bypass, BlockReadsOutside: true}, call: readOut, want: denied},
		{name: "reads: a read outside is denied in default", policy: permission.Policy{BlockReadsOutside: true}, call: readOut, want: denied},
		{name: "reads: an allow rule does not beat the limit", policy: permission.Policy{BlockReadsOutside: true, Allow: []string{"read_file"}}, call: readOut, want: denied},
		{name: "reads: a read inside runs", policy: permission.Policy{Mode: permission.Bypass, BlockReadsOutside: true}, call: readIn, want: ran},
		{name: "reads: an edit outside runs in bypass", policy: permission.Policy{Mode: permission.Bypass, BlockReadsOutside: true}, call: editOut, want: ran},
		{name: "writes: an edit outside is denied in bypass", policy: permission.Policy{Mode: permission.Bypass, BlockWritesOutside: true}, call: editOut, want: denied},
		{name: "writes: an edit outside is denied in default", policy: permission.Policy{BlockWritesOutside: true}, call: editOut, want: denied},
		{name: "writes: an edit inside runs in bypass", policy: permission.Policy{Mode: permission.Bypass, BlockWritesOutside: true}, call: editIn, want: ran},
		{name: "writes: a read outside runs in bypass", policy: permission.Policy{Mode: permission.Bypass, BlockWritesOutside: true}, call: readOut, want: ran},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.policy
			p.Dir = root
			assert.Equal(t, tt.want, decide(t, &p, tt.call))
		})
	}
}

func TestTheReasonOfALimitNamesThePath(t *testing.T) {
	root, outside := folders(t)
	p := &permission.Policy{Mode: permission.Bypass, Dir: root, BlockWritesOutside: true}
	path := filepath.Join(outside, "a.txt")

	reason := denyReason(t, p, pathCall(t, "write_file", path))

	assert.Contains(t, reason, path)
}

func TestADenyRuleIgnoresTheLetterCase(t *testing.T) {
	root, _ := folders(t)
	t.Setenv("HOME", root)
	p := &permission.Policy{
		Mode: permission.Bypass,
		Dir:  root,
		Deny: []string{"read_file(~/.ssh/**)"},
	}

	assert.Equal(t, denied, decide(t, p, pathCall(t, "read_file", "~/.SSH/id_ed25519")))
}

func TestAnAllowRuleDoesNotApproveAProtectedEdit(t *testing.T) {
	root, _ := folders(t)
	c := pathCall(t, "edit_file", ".git/hooks/pre-commit")
	tests := []struct {
		mode permission.Mode
		want outcome
	}{
		{mode: permission.Default, want: asked},
		{mode: permission.AcceptEdits, want: asked},
		{mode: permission.Bypass, want: ran},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			p := &permission.Policy{Mode: tt.mode, Dir: root, Allow: []string{"Edit"}}
			assert.Equal(t, tt.want, decide(t, p, c))
		})
	}
}
