package codex

import (
	"context"
	"os/exec"
	"regexp"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/agent/internal/tail"
)

// deviceURL and deviceCode match the link and the code that
// `codex login --device-auth` prints, such as
// "https://auth.openai.com/codex/device" and "QBVZ-JYHN6".
var (
	deviceURL  = regexp.MustCompile(`https://auth\.openai\.com/\S+`)
	deviceCode = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4,5}\b`)
)

// Login returns `codex login --device-auth`, with the binary and the
// environment of the driver. The flow is beta in Codex 0.163. The
// person turns on device codes in the security settings of ChatGPT
// first.
func (d driver) Login(ctx context.Context) *exec.Cmd {
	c := newOptions(d.opts)
	return cliproc.LoginCmd(ctx, c.binary, c.environ(), "login", "--device-auth")
}

// ReadLink finds the link and the code in the output of Login, after it
// removes the escape sequences of the terminal. It reports false until
// the output holds both.
func (d driver) ReadLink(output []byte) (agent.Link, bool) {
	text := tail.StripANSI(output)
	url, code := deviceURL.Find(text), deviceCode.Find(text)
	if url == nil || code == nil {
		return agent.Link{}, false
	}
	return agent.Link{URL: string(url), Code: string(code)}, true
}
