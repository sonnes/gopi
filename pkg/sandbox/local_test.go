package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/sandbox/fence"
)

func TestLocalDirIsAbsolute(t *testing.T) {
	box := newLocal(t, ".")

	assert.True(t, filepath.IsAbs(box.Dir()))
}

func TestLocalWriteFileCreatesParents(t *testing.T) {
	dir := t.TempDir()
	box := newLocal(t, dir)

	err := box.WriteFile(t.Context(), "a/b/c.txt", []byte("hello"))
	require.NoError(t, err)

	assert.Equal(t, "hello", readFile(t, dir, "a/b/c.txt"))
}

func TestLocalReadFile(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"f.txt": "data"})
	box := newLocal(t, dir)

	tests := []struct {
		name string
		path string
	}{
		{name: "relative", path: "f.txt"},
		{name: "absolute", path: filepath.Join(dir, "f.txt")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := box.ReadFile(t.Context(), tt.path)

			require.NoError(t, err)
			assert.Equal(t, "data", string(got))
		})
	}
}

func TestLocalPaths(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	writeFiles(t, parent, map[string]string{
		"root/f.txt":         "inside",
		"outside/secret.txt": "outside",
	})
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "escape")))
	fenced := Policy(fence.Write(root), fence.Hide(outside))

	tests := []struct {
		name    string
		opts    []Option
		path    string
		want    string
		wantErr string
	}{
		{name: "the default allows a parent path", path: "../outside/secret.txt", want: "outside"},
		{name: "the default allows a symlink that points outside", path: "escape/secret.txt", want: "outside"},
		{name: "a policy allows a path that it does not hide", opts: []Option{fenced}, path: "f.txt", want: "inside"},
		{name: "a policy rejects a hidden path", opts: []Option{fenced}, path: "../outside/secret.txt", wantErr: "hidden"},
		{name: "a policy rejects a symlink to a hidden path", opts: []Option{fenced}, path: "escape/secret.txt", wantErr: "hidden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newLocal(t, root, tt.opts...).ReadFile(t.Context(), tt.path)

			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestLocalPolicyLimitsWrites(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	require.NoError(t, os.Mkdir(root, 0o755))
	box := newLocal(t, root, Policy(fence.Write(root), fence.ReadOnly(filepath.Join(root, ".git"))))

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "a writable directory", path: "f.txt"},
		{name: "outside the writable directories", path: "../escape.txt", wantErr: true},
		{name: "a read-only path", path: ".git/config", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := box.WriteFile(t.Context(), tt.path, []byte("x"))

			if tt.wantErr {
				require.ErrorContains(t, err, "not writable")
				assert.NoFileExists(t, filepath.Join(root, tt.path))
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestLocalExec(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"sub/f.txt": ""})
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	box := newLocal(t, dir)

	tests := []struct {
		name string
		cmd  Command
		want Result
	}{
		{
			name: "stdout",
			cmd:  Command{Line: "echo hello"},
			want: Result{Stdout: []byte("hello\n")},
		},
		{
			name: "stderr",
			cmd:  Command{Line: "echo oops >&2"},
			want: Result{Stderr: []byte("oops\n")},
		},
		{
			name: "exit code",
			cmd:  Command{Line: "exit 3"},
			want: Result{ExitCode: 3},
		},
		{
			name: "runs in the sandbox directory",
			cmd:  Command{Line: "pwd -P"},
			want: Result{Stdout: []byte(realDir + "\n")},
		},
		{
			name: "resolves a relative directory",
			cmd:  Command{Line: "pwd -P", Dir: "sub"},
			want: Result{Stdout: []byte(filepath.Join(realDir, "sub") + "\n")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := box.Exec(t.Context(), tt.cmd)

			require.NoError(t, err)
			assert.Equal(t, string(tt.want.Stdout), string(got.Stdout))
			assert.Equal(t, string(tt.want.Stderr), string(got.Stderr))
			assert.Equal(t, tt.want.ExitCode, got.ExitCode)
		})
	}
}

func TestLocalExecStopsWhenTheContextEnds(t *testing.T) {
	box := newLocal(t, t.TempDir())
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := box.Exec(ctx, Command{Line: "sleep 10; echo done"})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestLocalExecReturnsWhileABackgroundChildRuns(t *testing.T) {
	box := newLocal(t, t.TempDir())

	start := time.Now()
	got, err := box.Exec(t.Context(), Command{Line: "echo started; sleep 5 &"})

	require.NoError(t, err)
	assert.Equal(t, "started\n", string(got.Stdout))
	assert.Equal(t, 0, got.ExitCode)
	assert.Less(t, time.Since(start), 4*time.Second)
}

func TestLocalExecKeepsTheEnvironment(t *testing.T) {
	t.Setenv("GOPI_TEST_API_KEY", "k")
	box := newLocal(t, t.TempDir())

	got, err := box.Exec(t.Context(), Command{Line: "echo $GOPI_TEST_API_KEY"})

	require.NoError(t, err)
	assert.Equal(t, "k\n", string(got.Stdout))
}

func TestLocalPolicyDoesNotLoadTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFiles(t, home, map[string]string{".bash_profile": "export GOPI_TEST_API_KEY=k\n"})
	root := t.TempDir()
	box := newLocal(t, root, Policy(fence.Workspace(root)...))

	got, err := box.Exec(t.Context(), Command{Line: `printf %s "${GOPI_TEST_API_KEY-unset}"`})

	require.NoError(t, err)
	assert.Equal(t, "unset", string(got.Stdout))
}

func TestLocalFencesCommands(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	require.NoError(t, os.Mkdir(root, 0o755))
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	inHome := filepath.Join(home, ".gopi-fence-test")
	inTemp := filepath.Join(parent, "escape.txt")
	inRoot := filepath.Join(root, "f.txt")

	tests := []struct {
		name   string
		opts   []Option
		target string
		want   bool
	}{
		{name: "the default writes in the home directory", target: inHome, want: true},
		{name: "the workspace policy writes in the directory", opts: []Option{Policy(fence.Workspace(root)...)}, target: inRoot, want: true},
		{name: "the workspace policy writes in the temp directory", opts: []Option{Policy(fence.Workspace(root)...)}, target: inTemp, want: true},
		{name: "the workspace policy does not write in the home directory", opts: []Option{Policy(fence.Workspace(root)...)}, target: inHome},
		{name: "a policy with one directory does not write in the temp directory", opts: []Option{Policy(fence.Write(root))}, target: inTemp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			box := newLocal(t, root, tt.opts...)
			t.Cleanup(func() { _ = os.Remove(tt.target) })

			res, err := box.Exec(t.Context(), Command{Line: "echo x > '" + tt.target + "'"})

			require.NoError(t, err)
			if tt.want {
				assert.Equal(t, 0, res.ExitCode, string(res.Stderr))
				assert.FileExists(t, tt.target)
				return
			}
			assert.NotEqual(t, 0, res.ExitCode)
			assert.NoFileExists(t, tt.target)
		})
	}
}

func TestLocalPolicyWithoutAFenceIsAnError(t *testing.T) {
	if _, err := fence.New(); err == nil {
		t.Skip("this OS has a fence")
	}

	_, err := Local(t.TempDir(), Policy(fence.Workspace(t.TempDir())...))

	require.ErrorIs(t, err, errors.ErrUnsupported)
}

// remote is a sandbox without host paths.
type remote struct{ Sandbox }

func TestScratchDir(t *testing.T) {
	tests := []struct {
		name string
		box  Sandbox
		want string
	}{
		{name: "a local sandbox", box: newLocal(t, t.TempDir()), want: filepath.Join(os.TempDir(), "gopi")},
		{name: "a remote sandbox", box: remote{}, want: "/tmp/gopi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ScratchDir(tt.box))
		})
	}
}
