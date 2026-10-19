// Command gopi is the CLI of gopi. It is a test drive of the SDK.
//
// Usage:
//
//	gopi [options] [prompt]
//
// If you give a prompt, gopi runs it and stops. If you give no prompt,
// gopi reads stdin line by line. Each line is one run of the same
// conversation. gopi prints each run as a tree of its turns, its text,
// and its tool calls. With --json, gopi prints each event as one line
// of JSON instead. During a run, an interrupt cancels the run.
//
// The agent has the tools of gopi on a local sandbox in the root
// folder. By default, the sandbox has the fence.Workspace policy: a
// command can write only to the root, the temp folder, and the cache
// folder, and it has no network. The file tools obey the same rules.
// ask_user_question asks on the terminal.
//
// gopi builds the agent with package harness. It reads the instruction
// files, the skills, and the subagents of the home folder and of the
// root folder, in the layouts of Claude Code, Codex, and gopi. Each
// tool call obeys --permission-mode and the --allow and --deny rules. A
// call that needs an approval asks on the terminal.
//
// With --session-id, gopi keeps the conversation in a JSONL file of
// that name in $GOPI_HOME/sessions, or ~/.gopi/sessions. The first run
// creates the session, and each later run with the same ID resumes it.
//
// With --mcp, gopi also starts the MCP servers of an mcp.json file, in
// the format of Claude Code, and gives their tools to the agent. The
// fence does not confine them. A stdio server runs as a normal process
// of the user, with full access to the files and the network.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/bash"
	"github.com/sonnes/gopi/pkg/sandbox/edit"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/find"
	"github.com/sonnes/gopi/pkg/sandbox/grep"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/sandbox/write"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/session/jsonl"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
	"github.com/urfave/cli/v3"
)

// prompt asks for the next line on stderr, so that stdout holds only
// the tree.
const prompt = "> "

// errShown is the error of a run that the tree shows. A run that starts
// always sends agent_end with its error, so main exits with no message.
var errShown = errors.New("the run failed")

// defaultTools names every tool of gopi, in the order that the model
// gets them.
const defaultTools = "read_file,write_file,edit_file,bash,grep,find,todo_write,ask_user_question"

// options holds the flags of gopi.
type options struct {
	provider string
	model    string
	turns    int
	root     string
	fence    bool
	tools    string
	// thinking is the thinking level of each model call. Empty means
	// the default of the model.
	thinking string
	// json prints the events as JSON lines instead of the tree.
	json bool
	// mcp is the path of an mcp.json file. Empty means no MCP server.
	mcp string
	// sessionID is the ID of the session that keeps the conversation.
	// Empty means no session: the conversation ends with gopi.
	sessionID string
	// mode is the permission mode of the tool calls.
	mode string
	// allow and deny are the permission rules of the tool calls.
	allow []string
	deny  []string
}

func main() {
	cmd := &cli.Command{
		Name:      "gopi",
		Usage:     "a test drive of the gopi SDK",
		ArgsUsage: "[prompt]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "provider",
				Usage: "the provider: anthropic, openai, or openaichat. If unset, gopi picks it from ANTHROPIC_API_KEY or OPENAI_API_KEY",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "the model ID. If unset, gopi uses the first model of the provider",
			},
			&cli.IntFlag{
				Name:  "turns",
				Usage: "the most turns of one run, or 0 for no limit",
			},
			&cli.StringFlag{
				Name:  "root",
				Value: ".",
				Usage: "the folder of the sandbox. The tools start in it",
			},
			&cli.BoolFlag{
				Name:  "fence",
				Value: true,
				Usage: "confine the tools with the fence.Workspace policy of the root. The fence runs only on macOS",
			},
			&cli.StringFlag{
				Name:  "tools",
				Value: defaultTools,
				Usage: "the tools of the agent, with commas. An empty value gives no tool",
			},
			&cli.StringFlag{
				Name:  "thinking",
				Usage: "the thinking level: off, low, medium, high, xhigh, or max. If unset, the model uses its default",
			},
			&cli.BoolFlag{
				Name:  "json",
				Usage: "print each event as one line of JSON instead of the tree",
			},
			&cli.StringFlag{
				Name:    "session-id",
				Aliases: []string{"s"},
				Usage:   "keep the conversation in the session with this ID. The first run creates it, and a later run resumes it",
			},
			&cli.StringFlag{
				Name:  "permission-mode",
				Value: string(permission.Default),
				Usage: "the permission mode of the tool calls: default, acceptEdits, plan, or bypassPermissions. A call that needs an approval asks on the terminal",
			},
			&cli.StringSliceFlag{
				Name:  "allow",
				Usage: "a rule for the tool calls that run without a question, such as 'bash(go test *)'. Give the flag again to add a rule",
			},
			&cli.StringSliceFlag{
				Name:  "deny",
				Usage: "a rule for the tool calls that never run, such as 'bash(git push *)'. Give the flag again to add a rule",
			},
			&cli.StringFlag{
				Name:  "mcp",
				Usage: "the path of an mcp.json file. gopi starts its servers and gives their tools to the agent. The fence does not confine the servers",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			opts := options{
				provider:  c.String("provider"),
				model:     c.String("model"),
				turns:     c.Int("turns"),
				root:      c.String("root"),
				fence:     c.Bool("fence"),
				tools:     c.String("tools"),
				thinking:  c.String("thinking"),
				json:      c.Bool("json"),
				mcp:       c.String("mcp"),
				sessionID: c.String("session-id"),
				mode:      c.String("permission-mode"),
				allow:     c.StringSlice("allow"),
				deny:      c.StringSlice("deny"),
			}
			return run(ctx, opts, c.Args().Slice())
		},
	}

	err := cmd.Run(context.Background(), os.Args)
	if errors.Is(err, errShown) {
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gopi:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options, args []string) error {
	lm, name, err := newModel(o.provider, o.model, os.Getenv)
	if err != nil {
		return err
	}

	// The prompt loop and ask_user_question read the same input.
	in := readInput(os.Stdin, isTerminal(os.Stdin))
	shown := &shownCalls{}
	answers := terminal{
		w:     os.Stderr,
		in:    in,
		shown: shown,
	}
	tools, err := newTools(o.root, o.tools, o.fence, answers)
	if err != nil {
		return fenceError(err)
	}
	if o.mcp != "" {
		mcpTools, closeMCP, err := loadMCP(ctx, o.mcp, os.Environ(), os.Stderr)
		if err != nil {
			return err
		}
		defer closeMCP()
		tools = append(tools, mcpTools...)
	}

	mode, err := permission.ParseMode(o.mode)
	if err != nil {
		return err
	}
	cat, spec := newCatalog(lm, name)
	opts := []harness.Option{
		harness.Catalog(cat),
		harness.Model(spec),
		harness.Dir(o.root),
		harness.Builtin(harness.NoBuiltins),
		harness.Toolset(harness.Tools(tools...)),
		harness.Mode(mode),
		harness.Allow(o.allow...),
		harness.Deny(o.deny...),
		harness.Approve(answers.Approve),
	}
	if o.turns > 0 {
		opts = append(opts, harness.AgentDefaults(maxTurns(o.turns)))
	}
	if o.thinking != "" {
		level, err := parseThinking(o.thinking)
		if err != nil {
			return err
		}
		opts = append(opts, harness.AgentDefaults(agent.Thinking(level)))
	}
	dir, err := sessionsDir(os.Getenv)
	if err != nil {
		return err
	}
	a, err := newAgent(ctx, opts, o.sessionID, dir)
	if err != nil {
		return err
	}
	var t printer = jsonLines{w: os.Stdout}
	if !o.json {
		tr := newTree(os.Stdout, spec, isTerminal(os.Stdout))
		tr.shown = shown
		t = tr
	}

	if len(args) > 0 {
		if err := runPrompt(ctx, a, t, strings.Join(args, " ")); err != nil {
			return errShown
		}
		return nil
	}

	fmt.Fprint(os.Stderr, prompt)
	for {
		line, ok := in.next()
		if !ok {
			break
		}
		line = strings.TrimSpace(line)
		if line != "" {
			// The tree shows the error of a failed run, and the
			// conversation continues.
			_ = runPrompt(ctx, a, t, line)
		}
		fmt.Fprint(os.Stderr, prompt)
	}
	return nil
}

// newAgent returns the agent of gopi. With a session ID, the agent
// keeps the conversation in the JSONL store in dir. Without one, the
// conversation ends with gopi.
// newAgent assembles the agent of gopi with the harness. With a
// session ID, the agent keeps the conversation in a JSONL file in dir,
// and a later run with the same ID resumes it.
func newAgent(
	ctx context.Context,
	opts []harness.Option,
	sessionID string,
	dir string,
) (*agent.Agent, error) {
	if sessionID == "" {
		return harness.New(opts...).New(ctx)
	}
	store, err := jsonl.New(dir)
	if err != nil {
		return nil, err
	}
	opts = append(slices.Clip(opts), harness.Store(store))
	return harness.New(opts...).Durable(ctx, sessionID)
}

// newCatalog returns a catalog that serves lm under the provider name,
// and the spec of lm in it.
func newCatalog(lm ai.LanguageModel, name string) (*catalog.Catalog, string) {
	cat := catalog.New()
	cat.RegisterTextProvider(name, lm.Provider, lm.Model)
	return cat, name + "/" + lm.Model.ID
}

func sessionsDir(getenv func(string) string) (string, error) {
	if home := getenv("GOPI_HOME"); home != "" {
		return filepath.Join(home, "sessions"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("no home folder: set HOME or GOPI_HOME")
	}
	return filepath.Join(home, ".gopi", "sessions"), nil
}

// fenceError explains an error of a fence that has no backend on this
// OS, and names the flag that runs the tools without one.
func fenceError(err error) error {
	if errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("%w: the fence runs only on macOS. Run gopi with --fence=false to run the tools without a sandbox", err)
	}
	return err
}

// newTools returns the tools that names lists, in its order. The
// sandbox tools share one local sandbox on root. If fenced is true, the
// sandbox has the [fence.Workspace] policy of root.
func newTools(root, names string, fenced bool, answers askquestion.Responder) ([]tool.Tool, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var opts []sandbox.Option
	if fenced {
		opts = append(opts, sandbox.Policy(fence.Workspace(root)...))
	}
	box, err := sandbox.Local(root, opts...)
	if err != nil {
		return nil, err
	}

	all := map[string]tool.Tool{
		read.Name:        read.New(box),
		write.Name:       write.New(box),
		edit.Name:        edit.New(box),
		bash.Name:        bash.New(box),
		grep.Name:        grep.New(box),
		find.Name:        find.New(box),
		todowrite.Name:   todowrite.New(),
		askquestion.Name: askquestion.New(answers),
	}

	var tools []tool.Tool
	for n := range strings.SplitSeq(names, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		t, ok := all[n]
		if !ok {
			return nil, fmt.Errorf("unknown tool %q: use %s", n, defaultTools)
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// runPrompt runs one prompt and prints its events. An interrupt during
// the run cancels the run. Outside a run, an interrupt stops gopi.
func runPrompt(ctx context.Context, a *agent.Agent, t printer, prompt string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	// After the first interrupt, a second one stops gopi, in case the
	// cancel of the run waits.
	context.AfterFunc(ctx, stop)

	for e, err := range a.Run(ctx, session.Text(prompt)).Events() {
		if err != nil {
			return err
		}
		t.print(e)
	}
	return nil
}

// newModel binds a model to a provider. It returns the binding and the
// name of the provider. If provider is empty, the first key that is set
// picks it. If model is empty, newModel uses the first model of the
// provider. A model that the provider does not list has no prices, so
// its runs show no cost.
func newModel(provider, model string, getenv func(string) string) (ai.LanguageModel, string, error) {
	if provider == "" {
		switch {
		case getenv("ANTHROPIC_API_KEY") != "":
			provider = "anthropic"
		case getenv("OPENAI_API_KEY") != "":
			provider = "openai"
		default:
			return ai.LanguageModel{}, "", errors.New("no provider: set ANTHROPIC_API_KEY or OPENAI_API_KEY, or use --provider")
		}
	}

	var p ai.TextProvider
	var models []ai.Model
	switch provider {
	case "anthropic":
		p = anthropic.New()
		models = anthropic.Models()
	case "openai":
		p = openai.New()
		models = openai.Models()
	case "openaichat":
		p = openaichat.New()
	default:
		return ai.LanguageModel{}, "", fmt.Errorf("unknown provider %q: use anthropic, openai, or openaichat", provider)
	}

	m, err := pickModel(models, model)
	if err != nil {
		return ai.LanguageModel{}, "", fmt.Errorf("%s: %w", provider, err)
	}
	lm := ai.LanguageModel{
		Model:    m,
		Provider: p,
	}
	return lm, provider, nil
}

// pickModel returns the model with the ID from models. If id is empty,
// it returns the first model, because a provider lists its default
// model first.
func pickModel(models []ai.Model, id string) (ai.Model, error) {
	if id == "" {
		if len(models) == 0 {
			return ai.Model{}, errors.New("the provider has no default model: use --model")
		}
		return models[0], nil
	}
	for _, m := range models {
		if m.ID == id {
			return m, nil
		}
	}
	return ai.Model{ID: id}, nil
}

// maxTurns stops a run before the model call of turn n+1. The agent
// has no limit on the turns of a run, so the limit is a hook.
func maxTurns(n int) agent.Option {
	return agent.BeforeCall(func(ctx context.Context, s agent.State, r agent.Request) (agent.Request, error) {
		if s.Turn() > n {
			return r, fmt.Errorf("the run used more than %d turns", n)
		}
		return r, nil
	})
}

// isTerminal reports if f is a terminal, so that the tree writes colors
// only to a screen.
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
