package fence

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions(t *testing.T) {
	c := newConfig(
		Write("/a"),
		Write("/b"),
		ReadOnly("/a/.git/hooks"),
		Hide("/secret"),
		Network(true),
		Network(false),
		Env("PATH"),
		Env("HOME"),
	)

	assert.Equal(t, []string{"/a", "/b"}, c.write)
	assert.Equal(t, []string{"/a/.git/hooks"}, c.readOnly)
	assert.Equal(t, []string{"/secret"}, c.hide)
	assert.False(t, c.network)
	assert.Equal(t, []string{"PATH", "HOME"}, c.env)
}

func TestWorkspace(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	cache := filepath.Join(os.TempDir(), "gopi-cache")

	c := newConfig(Workspace("/work/app")...)

	assert.Equal(t, []string{"/work/app", os.TempDir()}, c.write)
	assert.Equal(t, []string{
		"/work/app/.git/modules",
		"/work/app/.git",
	}, c.readOnly)
	require.Len(t, c.names, 1)
	assert.Equal(t, "/work/app", c.names[0].root)
	assert.Contains(t, c.names[0].names, ".git/hooks")
	assert.Contains(t, c.names[0].names, ".git/config")
	assert.Contains(t, c.names[0].names, ".git/commondir")
	assert.Contains(t, c.names[0].names, ".gitmodules")
	assert.Contains(t, c.names[0].names, ".bashrc")
	require.Len(t, c.hideExcept, 1)
	assert.Equal(t, home, c.hideExcept[0].dir)
	assert.Contains(t, c.hideExcept[0].keep, "/work/app")
	assert.Contains(t, c.hideExcept[0].keep, filepath.Join(home, "go"))
	assert.Contains(t, c.hide, filepath.Join(home, ".ssh"))
	assert.Contains(t, c.hide, filepath.Join(home, ".aws"))
	assert.False(t, c.network)
	assert.Equal(t, []string{"PATH", "HOME", "USER", "LANG", "TERM", "SHELL", "TMPDIR"}, c.env)
	assert.Contains(t, c.setEnv, "XDG_CACHE_HOME="+cache)
	assert.Contains(t, c.setEnv, "GOCACHE="+filepath.Join(cache, "go-build"))
}

func TestWorkspaceWithAGitDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))

	c := newConfig(Workspace(root)...)

	assert.Equal(t, []string{filepath.Join(root, ".git", "modules")}, c.readOnly)
}

func TestWorkspaceThenAnOption(t *testing.T) {
	opts := append(Workspace("/work/app"), Network(true))

	c := newConfig(opts...)

	assert.True(t, c.network)
}

func TestKeepEnv(t *testing.T) {
	env := []string{"PATH=/bin", "HOME=/home/a", "API_KEY=k", "PATHX=/x", "NOVALUE"}

	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{
			name:  "no names keeps all",
			names: nil,
			want:  env,
		},
		{
			name:  "names keep only these",
			names: []string{"PATH", "HOME"},
			want:  []string{"PATH=/bin", "HOME=/home/a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, keepEnv(env, tt.names))
		})
	}
}

func TestRealPath(t *testing.T) {
	dir := t.TempDir()
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink(filepath.Join(dir, "target", "f"), filepath.Join(dir, "dangling")))
	require.NoError(t, os.Symlink("loop", filepath.Join(dir, "loop")))

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "a directory", path: dir, want: real},
		{name: "a symlink", path: filepath.Join(dir, "link"), want: real},
		{name: "a path that does not exist", path: filepath.Join(dir, "link", "new", "f"), want: filepath.Join(real, "new", "f")},
		{name: "a symlink to a path that does not exist", path: filepath.Join(dir, "dangling"), want: filepath.Join(real, "target", "f")},
		{name: "a symlink loop", path: filepath.Join(dir, "loop"), want: filepath.Join(real, "loop")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, realPath(tt.path))
		})
	}
}

func TestRules(t *testing.T) {
	root := t.TempDir()
	rules := NewRules(
		Write(root),
		ReadOnly(filepath.Join(root, ".git")),
		Hide(filepath.Join(root, "secret")),
	)

	tests := []struct {
		name      string
		path      string
		wantRead  bool
		wantWrite bool
	}{
		{name: "a file in a writable directory", path: filepath.Join(root, "a.txt"), wantRead: true, wantWrite: true},
		{name: "a read-only path", path: filepath.Join(root, ".git", "config"), wantRead: true},
		{name: "a hidden path", path: filepath.Join(root, "secret", "key")},
		{name: "a path outside every directory", path: "/etc/hosts", wantRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantRead, rules.CanRead(tt.path))
			assert.Equal(t, tt.wantWrite, rules.CanWrite(tt.path))
		})
	}
}

func TestRulesFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	hidden := t.TempDir()
	require.NoError(t, os.Symlink(hidden, filepath.Join(root, "link")))
	rules := NewRules(Write(root), Hide(hidden))

	assert.False(t, rules.CanRead(filepath.Join(root, "link", "key")))
	assert.False(t, rules.CanWrite(filepath.Join(root, "link", "key")))
}

func TestRulesIgnoreTheCaseOfADeniedPathOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the default disk of macOS ignores case")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secret"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	rules := NewRules(
		Write(root),
		ReadOnly(filepath.Join(root, ".git")),
		Hide(filepath.Join(root, "secret")),
	)

	assert.False(t, rules.CanRead(filepath.Join(root, "SECRET", "key")))
	assert.False(t, rules.CanWrite(filepath.Join(root, "SECRET", "key")))
	assert.False(t, rules.CanWrite(filepath.Join(root, ".GIT", "config")))
	assert.False(t, rules.CanWrite(strings.ToUpper(filepath.Join(root, "a.txt"))))
}

func TestNameHideAndEnvOptions(t *testing.T) {
	c := newConfig(
		ReadOnlyNames("/w", ".bashrc", ".git/hooks"),
		HideExcept("/home/a", "/home/a/go"),
		SetEnv("GOCACHE", "/tmp/c"),
	)

	assert.Equal(t, []nameRule{{root: "/w", names: []string{".bashrc", ".git/hooks"}}}, c.names)
	assert.Equal(t, []hideExceptRule{{dir: "/home/a", keep: []string{"/home/a/go"}}}, c.hideExcept)
	assert.Equal(t, []string{"GOCACHE=/tmp/c"}, c.setEnv)
}

func TestRulesReadOnlyNames(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	rules := NewRules(
		Write(root),
		ReadOnlyNames(root, ".bashrc", ".git/hooks"),
	)

	tests := []struct {
		name      string
		path      string
		wantWrite bool
	}{
		{name: "a name at the root", path: filepath.Join(root, ".bashrc")},
		{name: "a name in a subdirectory", path: filepath.Join(root, "a", "b", ".bashrc")},
		{name: "a path under a name", path: filepath.Join(root, "sub", ".git", "hooks", "pre-commit")},
		{name: "a name in another case", path: filepath.Join(root, "sub", ".GIT", "Hooks", "x")},
		{name: "a name through the real path", path: filepath.Join(real, ".bashrc")},
		{name: "a name as a suffix", path: filepath.Join(root, "x.bashrc"), wantWrite: true},
		{name: "another file", path: filepath.Join(root, ".git", "index"), wantWrite: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, rules.CanRead(tt.path))
			assert.Equal(t, tt.wantWrite, rules.CanWrite(tt.path))
		})
	}
}

func TestRulesHideExcept(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "work", "app")
	rules := NewRules(
		Write(root),
		HideExcept(home, root, filepath.Join(home, "go"), filepath.Join(home, ".gitconfig")),
	)

	tests := []struct {
		name      string
		path      string
		wantRead  bool
		wantWrite bool
	}{
		{name: "a file in the hidden directory", path: filepath.Join(home, ".zsh_history")},
		{name: "a kept directory", path: filepath.Join(home, "go", "pkg", "mod", "x"), wantRead: true},
		{name: "a kept file", path: filepath.Join(home, ".gitconfig"), wantRead: true},
		{name: "a file in the root", path: filepath.Join(root, "a.txt"), wantRead: true, wantWrite: true},
		{name: "a sibling of the root", path: filepath.Join(home, "work", "other", ".envrc")},
		{name: "a path outside the hidden directory", path: "/etc/hosts", wantRead: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantRead, rules.CanRead(tt.path))
			assert.Equal(t, tt.wantWrite, rules.CanWrite(tt.path))
		})
	}
}

func TestRulesRefuseAliasesOfMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the aliases exist only on macOS")
	}
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	ssh := filepath.Join(home, ".ssh")
	rules := NewRules(Write(home), Hide(ssh))

	for _, p := range []string{
		filepath.Join("/System/Volumes/Data", ssh, "id_rsa"),
		"/.vol/16777220/2",
	} {
		assert.False(t, rules.CanRead(p), p)
		assert.False(t, rules.CanWrite(p), p)
	}
}
