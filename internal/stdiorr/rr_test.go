package stdiorr

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dirScrub replaces a home directory with a fixed value.
func dirScrub(line []byte) []byte {
	return bytes.ReplaceAll(line, []byte("/Users/someone/work"), []byte("/home/me/work"))
}

func TestReplay(t *testing.T) {
	p, err := Open(t, "testdata/echo.trace", nil, dirScrub)
	require.NoError(t, err)
	out := bufio.NewScanner(p.Stdout)

	require.True(t, out.Scan())
	assert.Equal(t, `{"hello":true}`, out.Text())

	_, err = io.WriteString(p.Stdin, `{"id":1,"dir":"/Users/someone/work"}`+"\n")
	require.NoError(t, err)
	require.True(t, out.Scan())
	assert.Equal(t, `{"id":1,"ok":true}`, out.Text())
	require.True(t, out.Scan())
	assert.Equal(t, `{"note":"after 1"}`, out.Text())

	_, err = io.WriteString(p.Stdin, `{"id":2}`)
	require.NoError(t, err)
	_, err = io.WriteString(p.Stdin, "\n")
	require.NoError(t, err)
	require.True(t, out.Scan())
	assert.Equal(t, `{"id":2,"ok":true}`, out.Text())

	assert.False(t, out.Scan(), "stdout ends at the end of the trace")
	assert.NoError(t, out.Err())
	assert.NoError(t, p.Wait())
}

// failer is a [testing.TB] that records a failure and does not fail
// the test that runs it.
type failer struct {
	testing.TB
	failed bool
}

func (f *failer) Helper() {}

func (f *failer) Errorf(string, ...any) { f.failed = true }

func TestReplayMismatch(t *testing.T) {
	fake := &failer{TB: t}
	p, err := Open(fake, "testdata/echo.trace", nil)
	require.NoError(t, err)

	_, err = io.WriteString(p.Stdin, `{"id":9}`+"\n")
	assert.ErrorIs(t, err, errMismatch)
	assert.True(t, fake.failed)

	_, err = io.ReadAll(p.Stdout)
	assert.ErrorIs(t, err, errMismatch, "stdout ends so that the client stops")
}

func TestReplayKill(t *testing.T) {
	p, err := Open(t, "testdata/echo.trace", nil)
	require.NoError(t, err)

	require.NoError(t, p.Kill())
	assert.NoError(t, p.Wait())
}

func TestRecordThenReplay(t *testing.T) {
	cat, err := exec.LookPath("cat")
	if err != nil {
		t.Skip("cat is not installed")
	}
	file := filepath.Join(t.TempDir(), "cat.trace")

	*record = "cat"
	t.Cleanup(func() { *record = "" })
	p, err := Open(t, file, exec.Command(cat))
	require.NoError(t, err)

	out := bufio.NewScanner(p.Stdout)
	_, err = io.WriteString(p.Stdin, "ping\n")
	require.NoError(t, err)
	require.True(t, out.Scan())
	assert.Equal(t, "ping", out.Text())
	require.NoError(t, p.Stdin.Close())
	assert.False(t, out.Scan())
	require.NoError(t, p.Wait())

	data, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, "stdiorr trace v1\n> ping\n< ping\n", string(data))

	*record = ""
	p, err = Open(t, file, nil)
	require.NoError(t, err)
	out = bufio.NewScanner(p.Stdout)
	_, err = io.WriteString(p.Stdin, "ping\n")
	require.NoError(t, err)
	require.True(t, out.Scan())
	assert.Equal(t, "ping", out.Text())
}

func TestReadTraceRejectsBadLines(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "no header", data: "> x\n"},
		{name: "no direction", data: "stdiorr trace v1\nx\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bad.trace")
			require.NoError(t, os.WriteFile(file, []byte(tt.data), 0o600))
			_, err := readTrace(file)
			assert.Error(t, err)
		})
	}
}
