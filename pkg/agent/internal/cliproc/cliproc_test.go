package cliproc

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fake is a process that runs in a goroutine. It reads stdin until
// stdin closes, then writes out to stdout and exits.
func fake(out []byte) Pipes {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		io.Copy(io.Discard, inR)
		outW.Write(out)
		outW.Close()
	}()
	return Pipes{
		Stdin:  inW,
		Stdout: outR,
		Wait: func() error {
			<-exited
			return nil
		},
		Kill: func() error {
			inR.Close()
			outW.Close()
			return nil
		},
	}
}

func TestStopReadsStdout(t *testing.T) {
	// No caller reads the lines, and stdout has no buffer, so the
	// process exits only if Stop reads them.
	out := bytes.Repeat([]byte("{}\n"), 100)
	p := Open(fake(out))

	begin := time.Now()
	require.NoError(t, p.Stop())
	assert.Less(t, time.Since(begin), exitGrace, "the CLI exits without a signal")
}

func TestReadErr(t *testing.T) {
	long := append(bytes.Repeat([]byte("a"), maxLine+1), '\n')
	p := Open(fake(long))
	p.Write([]byte("{}\n"))
	p.pipes.Stdin.Close()

	for range p.Lines() {
	}
	assert.ErrorIs(t, p.ReadErr(), bufio.ErrTooLong)

	begin := time.Now()
	require.NoError(t, p.Stop())
	assert.Less(t, time.Since(begin), exitGrace, "the reader reads stdout after the error")
}
