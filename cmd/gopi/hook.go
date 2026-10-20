package main

import (
	"context"
	"errors"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/agent/codex"
)

// hookCommand returns the "gopi hook" command. Codex runs it for each
// hook of a gopi agent: the command sends the hook input on stdin to
// the agent that listens on the socket, and writes the answer to
// stdout. If the agent is not reachable, the command writes the answer
// that stops the action of the event. [codex.HookShim] has the rules.
func hookCommand() *cli.Command {
	return &cli.Command{
		Name:      "hook",
		Usage:     "relay a hook of the Codex CLI to the gopi agent on a socket",
		ArgsUsage: "<socket>",
		Action: func(ctx context.Context, c *cli.Command) error {
			if c.Args().Len() != 1 {
				return errors.New("hook: give the path of the socket")
			}
			return codex.HookShim(ctx, c.Args().First(), c.Reader, c.Writer)
		},
	}
}
