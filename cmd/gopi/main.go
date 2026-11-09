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
// of JSON instead. During a run, an interrupt stops the run.
//
// The agent has the tools of gopi on a local sandbox in the root
// folder. By default, the sandbox has the fence.Workspace policy: a
// command can write only to the root, the temp folder, and the cache
// folder, and it has no network. The file tools obey the same rules.
// ask_user_question asks on the terminal.
//
// gopi runs the agent in a session of package host, and package harness
// is the driver. The agent gets the CLAUDE.md and AGENTS.md files, the
// skills, and the subagents of each --dir folder, lowest first. Without
// --dir, it gets those of ~/.claude, <root>/.claude, and <root>. --agent
// names an agent file of those folders. The file fills the flags that
// are unset, and its prompt goes in the system prompt.
//
// Each tool call obeys --permission-mode and the --allow and --deny
// rules. A call that needs an approval asks on the terminal. The answer
// "always" keeps the rule of the call as a grant of the session.
//
// gopi finds the providers from ANTHROPIC_API_KEY, OPENAI_API_KEY,
// OPENROUTER_API_KEY, and FIREWORKS_API_KEY, with the detectors of
// package catalog/env. Before those, it finds the chatgpt provider if
// $GOPI_HOME/auth.json, or ~/.gopi/auth.json, has a signed-in account.
//
// "gopi login chatgpt" signs in with a ChatGPT account in the browser,
// and saves the account to that file. Then the chatgpt provider runs on
// the ChatGPT plan of the account, with no API key. "gopi logout
// chatgpt" revokes the token and removes it from the file.
// --model names the model by its spec, such as
// anthropic/claude-opus-5-5. If --model is unset, gopi uses the model
// of the resumed session, then the model of the agent file, then the
// first model of the first provider that it finds. If no provider
// registered the model, gopi asks the provider of the spec for its
// models once. A bare model ID asks every provider, so two providers
// can list the ID, and gopi then asks for the full spec.
//
// gopi reads the models of each provider from models.json: the path or
// the URL in $GOPI_MODELS, or $GOPI_HOME/models.json, or
// ~/.gopi/models.json. Without the file, a spec resolves after gopi asks
// the provider for its models.
//
// With --session-id, gopi keeps the session in a JSONL file of that
// name in $GOPI_HOME/sessions, or ~/.gopi/sessions. The session keeps
// the conversation, the config, and the grants. The first run creates
// the session, and each later run with the same ID resumes it. A
// resumed session keeps its config. A flag of a later run changes the
// config, and the rules of --allow and --deny add to the rules of the
// session. A later run in another root, or with another agent file,
// fails.
//
// "gopi models [provider...]" lists the spec of each model that
// --model takes, with the models that each provider lists from its API.
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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/permission"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
)

// prompt asks for the next line on stderr, so that stdout holds only
// the tree.
const prompt = "> "

// errShown is the error of a run that the tree shows. A run that starts
// always sends agent_end with its error, so main exits with no message.
var errShown = errors.New("the run failed")

// instructionFiles are the instruction files that gopi reads in each
// folder, in order: the file of Claude Code, then the file of the other
// agents.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md"}

// claudeDir is the folder of Claude Code, with agents/ and skills/,
// that gopi reads without --dir.
const claudeDir = ".claude"

// options holds the flags of gopi.
type options struct {
	// model is the model spec. Empty means the first model of the
	// first provider that gopi finds.
	model string
	turns int
	root  string
	fence bool
	// tools names the tools of gopi that the agent gets. Nil means each
	// tool.
	tools []string
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
	// dirs are the folders of the definitions, lowest first. Empty
	// means the folders of [defDirs].
	dirs []string
	// agent names an agent file in the dirs. Empty means no agent file.
	agent string
}

func main() {
	cmd := &cli.Command{
		Name:      "gopi",
		Usage:     "a test drive of the gopi SDK",
		ArgsUsage: "[prompt]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "model",
				Aliases: []string{"m"},
				Usage:   "the model spec, such as anthropic/claude-opus-5-5 or openrouter/qwen/qwen3-coder, or a model ID that one provider serves. If unset, gopi uses the model of the resumed session, then the model of the agent file, then the first model of the first provider that it finds",
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
				Usage: "the tools of gopi that the agent gets, with commas, such as read_file,grep. If unset, the agent gets each tool: read_file, write_file, edit_file, bash, grep, find, todo_write, and ask_user_question. An empty value gives none of them. The skill tool, the agent tool, and the MCP tools stay",
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
				Usage:   "keep the conversation, the config, and the grants in the session with this ID. The first run creates it, and a later run resumes it",
			},
			&cli.StringFlag{
				Name:  "permission-mode",
				Value: string(permission.Default),
				Usage: "the permission mode of the tool calls: default, acceptEdits, or bypassPermissions. A call that needs an approval asks on the terminal",
			},
			&cli.StringSliceFlag{
				Name:  "allow",
				Usage: "a rule for the tool calls that run without a question, such as 'bash(go test *)'. Give the flag again to add a rule",
			},
			&cli.StringSliceFlag{
				Name:  "deny",
				Usage: "a rule for the tool calls that never run, such as 'bash(git push *)'. Give the flag again to add a rule",
			},
			&cli.StringSliceFlag{
				Name:  "dir",
				Usage: "a folder with agents/, skills/, CLAUDE.md, and AGENTS.md. Give the flag again to add a folder. A later folder wins for a name. If unset, gopi reads ~/.claude, <root>/.claude, and <root>",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "the name of an agent file in agents/ of the --dir folders. Its model, thinking level, permission mode, tools, and disallowed tools fill the flags that are unset when gopi creates the session. gopi reads its prompt before each run",
			},
			&cli.StringFlag{
				Name:  "mcp",
				Usage: "the path of an mcp.json file. gopi starts its servers and gives their tools to the agent. The fence does not confine the servers",
			},
		},
		Commands: []*cli.Command{
			modelsCommand(),
			hookCommand(),
			loginCommand(),
			logoutCommand(),
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			opts := options{
				model:     c.String("model"),
				turns:     c.Int("turns"),
				root:      c.String("root"),
				fence:     c.Bool("fence"),
				thinking:  c.String("thinking"),
				json:      c.Bool("json"),
				mcp:       c.String("mcp"),
				sessionID: c.String("session-id"),
				mode:      setString(c, "permission-mode"),
				allow:     c.StringSlice("allow"),
				deny:      c.StringSlice("deny"),
				dirs:      c.StringSlice("dir"),
				agent:     c.String("agent"),
			}
			if c.IsSet("tools") {
				opts.tools = toolNames(c.String("tools"))
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
	m, err := newModels(homeDetectors(loadModels(ctx, os.Stderr, os.Getenv)))
	if err != nil {
		return err
	}

	// The prompt loop, ask_user_question, and the approvals read the
	// same input. Only the tree marks the calls that it shows.
	in := readInput(os.Stdin, isTerminal(os.Stdin))
	var shown *shownCalls
	if !o.json {
		shown = &shownCalls{}
	}
	answers := terminal{
		w:     os.Stderr,
		in:    in,
		shown: shown,
	}
	tools, err := newTools(o.tools, answers)
	if err != nil {
		return err
	}
	tools = append(tools, harness.SkillTool(), harness.AgentTool())
	if o.mcp != "" {
		mcpTools, closeMCP, err := loadMCP(ctx, o.mcp, os.Environ(), os.Stderr)
		if err != nil {
			return err
		}
		defer closeMCP()
		tools = append(tools, mcpTools...)
	}

	h, err := openHost(ctx, o, m, tools)
	if err != nil {
		return err
	}
	defer h.Close()
	sess, err := openSession(ctx, h, o, m)
	if err != nil {
		return fenceError(err)
	}

	var t printer = jsonLines{w: os.Stdout}
	if !o.json {
		tr := newTree(os.Stdout, sess.Model, isTerminal(os.Stdout))
		tr.shown = shown
		t = tr
	}
	send := func(text string) error {
		err := runPrompt(ctx, h, sess.ID, t, answers.Approve, m, text)
		return fenceError(err)
	}

	if len(args) > 0 {
		return send(strings.Join(args, " "))
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
			err := send(line)
			if err != nil && !errors.Is(err, errShown) {
				fmt.Fprintln(os.Stderr, "gopi:", err)
			}
		}
		fmt.Fprint(os.Stderr, prompt)
	}
	return nil
}

// openHost returns the host of gopi on the catalog of m, with tools and
// the definitions of [defs]. The harness is the default driver of the
// catalog. With --session-id, the host keeps the sessions in the
// sessions folder of the gopi home. Without it, the sessions end with
// gopi.
func openHost(ctx context.Context, o options, m models, tools []tool.Tool) (*host.Host, error) {
	hopts := []harness.Option{harness.Catalog(m.cat)}
	if o.fence {
		hopts = append(hopts, harness.Fence())
	}
	if o.turns > 0 {
		hopts = append(hopts, harness.AgentDefaults(agent.Hooks(agent.MaxTurns(o.turns))))
	}
	m.cat.DefaultDriver(harness.New(hopts...))

	opts := []host.Option{
		host.Tools(tools...),
		host.Defs(defs(o)),
	}
	if o.sessionID != "" {
		dir, err := sessionsDir(os.Getenv)
		if err != nil {
			return nil, err
		}
		store, err := storefs.Sessions(dir)
		if err != nil {
			return nil, err
		}
		opts = append(opts, host.Store(store))
	}
	return host.Open(ctx, m.cat, opts...)
}

// specProvider returns the provider of spec if found or ds has it, or
// an empty string for a bare model ID.
func specProvider(spec string, found []catalog.Detection, ds []catalog.Detector) string {
	id, _, hasProvider := strings.Cut(spec, "/")
	inFound := slices.ContainsFunc(found, func(d catalog.Detection) bool {
		return d.Provider == id
	})
	inDetectors := slices.ContainsFunc(ds, func(d catalog.Detector) bool {
		return d.Provider == id
	})
	if hasProvider && (inFound || inDetectors) {
		return id
	}
	return ""
}

// fetchModels asks the provider id of cat for its models, or each
// provider if id is empty. It returns the error of each list that
// failed. The error of a provider names the provider.
func fetchModels(ctx context.Context, cat *catalog.Catalog, id string) []error {
	var ids []string
	if id != "" {
		ids = []string{id}
	}
	var errs []error
	for _, r := range cat.FetchModels(ctx, ids...) {
		if r.Err != nil {
			errs = append(errs, r.Err)
		}
	}
	return errs
}

// defaultSpec returns the spec of the first model of the first provider
// in found that has models, or an empty string. The models of a
// detection come from the first detector in ds with its provider and
// its source.
func defaultSpec(ds []catalog.Detector, found []catalog.Detection) string {
	for _, f := range found {
		i := slices.IndexFunc(ds, func(d catalog.Detector) bool {
			return d.Provider == f.Provider && d.Source == f.Source
		})
		if i >= 0 && len(ds[i].Models) > 0 {
			return f.Provider + "/" + ds[i].Models[0].ID
		}
	}
	return ""
}

// sources returns the credential source of each detector in ds, once,
// in order, as a list for a person to read: "A", "A or B", or
// "A, B, or C".
func sources(ds []catalog.Detector) string {
	var names []string
	for _, d := range ds {
		if !slices.Contains(names, d.Source) {
			names = append(names, d.Source)
		}
	}
	switch len(names) {
	case 0:
		return "a credential"
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	}
	last := len(names) - 1
	return strings.Join(names[:last], ", ") + ", or " + names[last]
}

// gopiHome returns $GOPI_HOME, or ~/.gopi.
func gopiHome(getenv func(string) string) (string, error) {
	if home := getenv("GOPI_HOME"); home != "" {
		return home, nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("no home folder: set HOME or GOPI_HOME")
	}
	return filepath.Join(home, ".gopi"), nil
}

func sessionsDir(getenv func(string) string) (string, error) {
	home, err := gopiHome(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "sessions"), nil
}

// modelsSource returns where gopi reads models.json: $GOPI_MODELS, a
// path or a URL, or $GOPI_HOME/models.json, or ~/.gopi/models.json.
func modelsSource(getenv func(string) string) (string, error) {
	if src := getenv("GOPI_MODELS"); src != "" {
		return src, nil
	}
	home, err := gopiHome(getenv)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "models.json"), nil
}

// loadModels returns models.json. The CLI still runs without it,
// because a spec resolves after a fetch of the model list of its
// provider. So loadModels writes an error to errOut and returns the
// zero file. A missing default file is not an error.
func loadModels(ctx context.Context, errOut io.Writer, getenv func(string) string) catalog.ModelFile {
	src, err := modelsSource(getenv)
	if err != nil {
		fmt.Fprintf(errOut, "gopi: %v\n", err)
		return catalog.ModelFile{}
	}
	file, err := catalog.ReadModelFile(ctx, src)
	missing := errors.Is(err, fs.ErrNotExist) && getenv("GOPI_MODELS") == ""
	if err != nil && !missing {
		fmt.Fprintf(errOut, "gopi: %v\n", err)
	}
	return file
}

// fenceError explains an error of a fence that has no backend on this
// OS, and names the flag that runs the tools without one.
func fenceError(err error) error {
	if errors.Is(err, errors.ErrUnsupported) {
		return fmt.Errorf("%w: the fence runs only on macOS. Run gopi with --fence=false to run the tools without a sandbox", err)
	}
	return err
}

// setString returns the value of the flag name, or "" if the command
// line does not set it. A flag with a default then cannot hide the
// value of an agent file.
func setString(c *cli.Command, name string) string {
	if !c.IsSet(name) {
		return ""
	}
	return c.String(name)
}

// defs returns the definitions of the folders of [defDirs], lowest
// first.
func defs(o options) def.Source {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	dirs := defDirs(o, home)
	sources := make([]def.Source, len(dirs))
	for i, d := range dirs {
		sources[i] = storefs.Dir(d, storefs.InstructionFiles(instructionFiles...))
	}
	return def.Layers(sources...)
}

// defDirs returns the folders of the definitions: the --dir flags, or
// the folders of Claude Code in home and in the root, then the root. An
// empty home adds no folder.
func defDirs(o options, home string) []string {
	if len(o.dirs) > 0 {
		return o.dirs
	}
	var dirs []string
	if home != "" {
		dirs = append(dirs, filepath.Join(home, claudeDir))
	}
	return append(dirs, filepath.Join(o.root, claudeDir), o.root)
}

// newTools returns the tools of gopi that names keeps, as [tool.Select]
// reads names: the tools of [harness.Coding], then ask_user_question,
// which asks with answers. If names is nil, newTools returns each tool.
func newTools(names []string, answers askquestion.Responder) ([]tool.Tool, error) {
	all := append(harness.Coding(), askquestion.New(answers))
	return tool.Select(all, names)
}

// toolNames returns the names of the --tools value s, with commas. An
// empty value gives an empty list, so the agent gets no tool.
func toolNames(s string) []string {
	names := []string{}
	for n := range strings.SplitSeq(s, ",") {
		n = strings.TrimSpace(n)
		if n != "" {
			names = append(names, n)
		}
	}
	return names
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
