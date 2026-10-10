package fence

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeatbeltArgs(t *testing.T) {
	c := newConfig(
		Write("/w"),
		ReadOnly("/w/.git/hooks"),
		Hide("/h"),
	)

	args := seatbeltArgs(c)

	require.Equal(t, "-p", args[0])
	profile := args[1]
	assert.Contains(t, profile, "(deny default)")
	assert.Contains(t, profile, "(allow file-read*)")
	assert.Contains(t, profile, `(allow file-write* (subpath (param "WRITE_0")))`)
	assert.Contains(t, profile, `(deny file-write* (literal (param "READONLY_0")) (subpath (param "READONLY_0")))`)
	assert.Contains(t, profile, `(deny file-write-unlink (literal (param "READONLY_0_PARENT_0")))`)
	assert.Contains(t, profile, `(deny file-read* file-write* (literal (param "HIDE_0")) (subpath (param "HIDE_0")))`)
	assert.NotContains(t, profile, "(allow network*)")
	assert.Equal(t, []string{
		"-DWRITE_0=/w",
		"-DREADONLY_0=/w/.git/hooks",
		"-DREADONLY_0_PARENT_0=/w/.git",
		"-DHIDE_0=/h",
		"--",
	}, args[2:])
}

func TestSeatbeltArgsNetwork(t *testing.T) {
	args := seatbeltArgs(newConfig(Network(true)))

	assert.Contains(t, args[1], "(allow network*)")
	assert.Contains(t, args[1], "(deny network-outbound (remote unix-socket))")
	assert.Contains(t, args[1], `(remote unix-socket (path-literal "/private/var/run/mDNSResponder"))`)
}

func TestSeatbeltRefusesAQuoteInARoot(t *testing.T) {
	_, err := Seatbelt(ReadOnlyNames(`/w/a"b`, ".bashrc"))

	require.Error(t, err)
}

func TestReadOnlyParents(t *testing.T) {
	tests := []struct {
		name  string
		write []string
		path  string
		want  []string
	}{
		{
			name:  "a nested path",
			write: []string{"/w"},
			path:  "/w/a/b/c",
			want:  []string{"/w/a/b", "/w/a"},
		},
		{
			name:  "a child of the write directory",
			write: []string{"/w"},
			path:  "/w/a",
			want:  nil,
		},
		{
			name:  "a path outside the write directories",
			write: []string{"/w"},
			path:  "/x/a/b",
			want:  nil,
		},
		{
			name:  "a write directory in another write directory",
			write: []string{"/t", "/t/proj"},
			path:  "/t/proj/.git/hooks",
			want:  []string{"/t/proj/.git", "/t/proj"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, readOnlyParents(tt.write, tt.path))
		})
	}
}

func TestSeatbeltReportsAMissingBinary(t *testing.T) {
	old := seatbeltPath
	seatbeltPath = "/nonexistent/sandbox-exec"
	t.Cleanup(func() { seatbeltPath = old })

	_, err := Seatbelt(Write(t.TempDir()))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox-exec")
}

// The tests below run commands inside the fence. Each command must fail
// where the fence denies it.

func needSeatbelt(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("Seatbelt runs only on macOS")
	}
}

// run runs script with bash inside f, in dir.
func run(t *testing.T, f Fence, dir, script string) (string, error) {
	t.Helper()

	cmd, err := f.Command(t.Context(), dir, "/bin/bash", "-c", script)
	require.NoError(t, err)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestSeatbeltWritesOnlyToWriteDirs(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	outside := t.TempDir()
	f, err := Seatbelt(Write(ws))
	require.NoError(t, err)

	out, err := run(t, f, ws, "echo in > in.txt")
	require.NoError(t, err, out)
	out, err = run(t, f, ws, fmt.Sprintf("echo out > %q", filepath.Join(outside, "out.txt")))
	require.Error(t, err, out)

	assert.FileExists(t, filepath.Join(ws, "in.txt"))
	assert.NoFileExists(t, filepath.Join(outside, "out.txt"))
}

func TestSeatbeltWritesToStdio(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	f, err := Seatbelt(Write(ws))
	require.NoError(t, err)

	out, err := run(t, f, ws, "echo out > /dev/stdout; echo err > /dev/stderr; echo fd > /dev/fd/1")

	require.NoError(t, err, out)
	assert.NotContains(t, out, "not permitted")
}

func TestSeatbeltDoesNotWriteThroughASymlink(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "link")))
	f, err := Seatbelt(Write(ws))
	require.NoError(t, err)

	out, err := run(t, f, ws, "echo out > link/out.txt")

	require.Error(t, err, out)
	assert.NoFileExists(t, filepath.Join(outside, "out.txt"))
}

func TestSeatbeltReadOnlyInsideAWriteDir(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git"), 0o755))
	hooks := filepath.Join(ws, ".git", "hooks")
	f, err := Seatbelt(Write(ws), ReadOnly(hooks))
	require.NoError(t, err)

	tests := []struct {
		name   string
		script string
		denied bool
	}{
		{name: "create the protected directory", script: "mkdir .git/hooks", denied: true},
		{name: "rename its parent", script: "mv .git .git2", denied: true},
		{name: "write next to it", script: "echo x > .git/index"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, f, ws, tt.script)

			if tt.denied {
				require.Error(t, err, out)
				return
			}
			require.NoError(t, err, out)
		})
	}
	assert.NoDirExists(t, hooks)
}

func TestSeatbeltHidesPaths(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "key"), []byte("s3cret"), 0o600))
	f, err := Seatbelt(Write(ws), Hide(secret))
	require.NoError(t, err)

	for _, script := range []string{
		fmt.Sprintf("cat %q", filepath.Join(secret, "key")),
		fmt.Sprintf("ls %q", secret),
	} {
		out, err := run(t, f, ws, script)

		require.Error(t, err, out)
		assert.NotContains(t, out, "s3cret")
	}
}

func TestSeatbeltNetwork(t *testing.T) {
	needSeatbelt(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	script := fmt.Sprintf("exec 3<>/dev/tcp/127.0.0.1/%d", port)
	ws := t.TempDir()

	tests := []struct {
		name   string
		opts   []Option
		denied bool
	}{
		{name: "off by default", opts: nil, denied: true},
		{name: "on", opts: []Option{Network(true)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := Seatbelt(append([]Option{Write(ws)}, tt.opts...)...)
			require.NoError(t, err)

			out, err := run(t, f, ws, script)

			if tt.denied {
				require.Error(t, err, out)
				return
			}
			require.NoError(t, err, out)
		})
	}
}

func TestSeatbeltEnv(t *testing.T) {
	needSeatbelt(t)
	t.Setenv("FENCE_TEST_KEEP", "kept")
	t.Setenv("FENCE_TEST_DROP", "dropped")
	ws := t.TempDir()
	f, err := Seatbelt(Write(ws), Env("FENCE_TEST_KEEP"))
	require.NoError(t, err)

	out, err := run(t, f, ws, "env | grep '^FENCE_TEST_' | sort")

	require.NoError(t, err, out)
	assert.Equal(t, "FENCE_TEST_KEEP=kept\n", out)
}

func TestSeatbeltStopsALaunchdJob(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	marker := filepath.Join(t.TempDir(), "escaped")
	label := fmt.Sprintf("dev.gopi.fence-test.%d", os.Getpid())
	t.Cleanup(func() { _ = exec.Command("launchctl", "remove", label).Run() })
	f, err := Seatbelt(Write(ws))
	require.NoError(t, err)

	out, err := run(t, f, ws, fmt.Sprintf("launchctl submit -l %s -- /usr/bin/touch %q", label, marker))

	require.Error(t, err, out)
	assert.NoFileExists(t, marker)
}

func TestWorkspaceRunsGit(t *testing.T) {
	needSeatbelt(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	ws := t.TempDir()
	require.NoError(t, exec.Command("git", "init", "-q", ws).Run())
	f, err := New(Workspace(ws)...)
	require.NoError(t, err)

	script := strings.Join([]string{
		"echo x > a.txt",
		"git add a.txt",
		"git -c user.name=a -c user.email=a@b -c commit.gpgsign=false commit -qm first",
		"git log --format=%s",
	}, " && ")
	out, err := run(t, f, ws, script)

	require.NoError(t, err, out)
	assert.Equal(t, "first\n", out)
}

func TestWorkspaceProtectsGit(t *testing.T) {
	needSeatbelt(t)

	tests := []struct {
		name   string
		gitDir bool
		script string
		target string
	}{
		{
			name:   "a hook",
			gitDir: true,
			script: "echo 'touch /tmp/pwned' > .git/hooks/pre-commit",
			target: ".git/hooks/pre-commit",
		},
		{
			name:   "a hook of a submodule",
			gitDir: true,
			script: "mkdir -p .git/modules/sub/hooks && echo 'touch /tmp/pwned' > .git/modules/sub/hooks/post-checkout",
			target: ".git/modules",
		},
		{
			name:   "a .git file in a directory without .git",
			script: "echo 'gitdir: evil' > .git",
			target: ".git",
		},
		{
			name:   "a common directory",
			gitDir: true,
			script: "echo evil > .git/commondir",
			target: ".git/commondir",
		},
		{
			name:   "a submodule list",
			gitDir: true,
			script: "printf '[submodule \"s\"]\\n' > .gitmodules",
			target: ".gitmodules",
		},
		{
			name:   "a hook of a nested repository",
			gitDir: true,
			script: "mkdir -p sub/.git/hooks && echo x > sub/.git/hooks/pre-commit",
			target: "sub/.git/hooks/pre-commit",
		},
		{
			name:   "a hook after a rename of the root",
			gitDir: true,
			script: "cd .. && mv \"$OLDPWD\" \"$OLDPWD.2\" && echo x > \"$OLDPWD.2/.git/hooks/pre-commit\"; mv \"$OLDPWD.2\" \"$OLDPWD\" 2>/dev/null; false",
			target: ".git/hooks/pre-commit",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := t.TempDir()
			if tt.gitDir {
				require.NoError(t, os.MkdirAll(filepath.Join(ws, ".git", "hooks"), 0o755))
			}
			f, err := New(Workspace(ws)...)
			require.NoError(t, err)

			out, err := run(t, f, ws, tt.script)

			require.Error(t, err, out)
			assert.NoFileExists(t, filepath.Join(ws, tt.target))
			assert.NoDirExists(t, filepath.Join(ws, tt.target))
		})
	}
}

func TestSeatbeltReadOnlyNames(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ws, "sub", ".git", "hooks"), 0o755))
	f, err := Seatbelt(Write(ws), ReadOnlyNames(ws, ".bashrc", ".gitmodules", ".git/hooks"))
	require.NoError(t, err)

	tests := []struct {
		name   string
		script string
		denied bool
	}{
		{name: "a name at the root", script: "echo x > .bashrc", denied: true},
		{name: "a name in a subdirectory", script: "echo x > sub/.gitmodules", denied: true},
		{name: "a name in another case", script: "echo x > sub/.GITMODULES", denied: true},
		{name: "a path under a name", script: "echo x > sub/.git/hooks/pre-commit", denied: true},
		{name: "a rename of a directory on the name", script: "mv sub/.git sub/g", denied: true},
		{name: "another file", script: "echo x > sub/.git/index"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, f, ws, tt.script)

			if tt.denied {
				require.Error(t, err, out)
				return
			}
			require.NoError(t, err, out)
		})
	}
	assert.NoFileExists(t, filepath.Join(ws, "sub", ".git", "hooks", "pre-commit"))
}

func TestSeatbeltHideExcept(t *testing.T) {
	needSeatbelt(t)
	ws := t.TempDir()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "go"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "go", "mod.txt"), []byte("module"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zsh_history"), []byte("s3cret"), 0o600))
	f, err := Seatbelt(Write(ws), HideExcept(home, filepath.Join(home, "go")))
	require.NoError(t, err)

	out, err := run(t, f, ws, fmt.Sprintf("cat %q", filepath.Join(home, "go", "mod.txt")))
	require.NoError(t, err, out)
	assert.Equal(t, "module", out)

	for _, script := range []string{
		fmt.Sprintf("cat %q", filepath.Join(home, ".zsh_history")),
		fmt.Sprintf("ls %q", home),
	} {
		out, err := run(t, f, ws, script)

		require.Error(t, err, out)
		assert.NotContains(t, out, "s3cret")
	}
}

func TestSeatbeltSetEnv(t *testing.T) {
	needSeatbelt(t)
	t.Setenv("FENCE_TEST_SET", "outside")
	ws := t.TempDir()
	f, err := Seatbelt(Write(ws), Env("PATH"), SetEnv("FENCE_TEST_SET", "inside"))
	require.NoError(t, err)

	out, err := run(t, f, ws, "env | grep '^FENCE_TEST_'")

	require.NoError(t, err, out)
	assert.Equal(t, "FENCE_TEST_SET=inside\n", out)
}

func TestSeatbeltDeniesAUnixSocket(t *testing.T) {
	needSeatbelt(t)
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	ws := t.TempDir()
	f, err := Seatbelt(Write(ws), Network(true))
	require.NoError(t, err)

	out, err := run(t, f, ws, fmt.Sprintf("/usr/bin/nc -U %q < /dev/null", sock))

	require.Error(t, err, out)
}

func TestWorkspaceHidesTheHomeDirectory(t *testing.T) {
	needSeatbelt(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.NoError(t, os.WriteFile(filepath.Join(home, ".zsh_history"), []byte("s3cret"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]"), 0o644))
	ws := filepath.Join(home, "work", "app")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	f, err := New(Workspace(ws)...)
	require.NoError(t, err)

	out, err := run(t, f, ws, "cat ~/.zsh_history")
	require.Error(t, err, out)
	assert.NotContains(t, out, "s3cret")

	out, err = run(t, f, ws, "cat ~/.gitconfig && echo x > a.txt")
	require.NoError(t, err, out)
	assert.FileExists(t, filepath.Join(ws, "a.txt"))
}
