package claude

import (
	"context"
	"os/exec"
	"sync"
	"testing"

	"github.com/sonnes/gopi/internal/stdiorr"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
)

// Starts records the commands that the agents of a test started.
type Starts struct {
	mu   sync.Mutex
	cmds []*exec.Cmd
}

// Cmds returns the started commands, in order.
func (s *Starts) Cmds() []*exec.Cmd {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*exec.Cmd(nil), s.cmds...)
}

// Replay makes each process start of the test replay the next trace in
// files. A start after the last file fails the test. In record mode,
// stdiorr runs the real command and writes the trace.
func Replay(t *testing.T, scrubs []stdiorr.Scrub, files ...string) *Starts {
	t.Helper()
	s := &Starts{}
	old := start
	t.Cleanup(func() { start = old })

	start = func(cmd *exec.Cmd) (cliproc.Pipes, error) {
		s.mu.Lock()
		n := len(s.cmds)
		s.cmds = append(s.cmds, cmd)
		s.mu.Unlock()
		if n >= len(files) {
			t.Errorf("claude: start %d has no trace", n+1)
			return cliproc.Pipes{}, exec.ErrNotFound
		}
		p, err := stdiorr.Open(t, files[n], cmd, scrubs...)
		if err != nil {
			return cliproc.Pipes{}, err
		}
		return cliproc.Pipes{
			Stdin:  p.Stdin,
			Stdout: p.Stdout,
			Wait:   p.Wait,
			Kill:   p.Kill,
		}, nil
	}
	return s
}

// Start starts the CLI of a, as the first run does, with no run.
func Start(ctx context.Context, a *Agent) error {
	_, err := a.ensure(ctx)
	return err
}

// SessionID returns the stored session ID of the CLI of a, or "".
func SessionID(a *Agent) (string, error) {
	return conversation.Get(a.log)
}
