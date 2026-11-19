package claude

import (
	"context"
	"os/exec"
	"regexp"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/cliproc"
	"github.com/sonnes/gopi/pkg/agent/internal/tail"
)

// hyperlinkURL matches the target of a terminal hyperlink (OSC 8). Claude
// Code prints its sign-in link as one. The target is whole even when the
// terminal wraps the text of a long link.
var hyperlinkURL = regexp.MustCompile(`\x1b\]8;[^;\x07\x1b]*;(https://[^\x07\x1b]+)(?:\x07|\x1b\\)`)

// signInURL matches the sign-in link that `claude auth login` prints,
// such as "If the browser didn't open, visit: https://claude.com/...",
// in a terminal with no hyperlinks. The space or the line end after the
// link shows that the link is whole.
var signInURL = regexp.MustCompile(`(https://\S+)\s`)

// Login returns `claude auth login --claudeai`, with the binary and the
// environment of the driver. The CLI opens a browser, prints the link
// of the sign-in, and waits for the callback. If the browser shows a
// code, the person types it in the terminal of the CLI.
func (d driver) Login(ctx context.Context) *exec.Cmd {
	c := newOptions(d.opts)
	return cliproc.LoginCmd(ctx, c.binary, c.environ(), "auth", "login", "--claudeai")
}

// ReadLink finds the sign-in link in the output of Login: the target of
// a terminal hyperlink, or else the first whole link of the text. The
// link has no code.
func (d driver) ReadLink(output []byte) (agent.Link, bool) {
	m := hyperlinkURL.FindSubmatch(output)
	if m == nil {
		m = signInURL.FindSubmatch(tail.StripANSI(output))
	}
	if m == nil {
		return agent.Link{}, false
	}
	return agent.Link{URL: string(m[1])}, true
}
