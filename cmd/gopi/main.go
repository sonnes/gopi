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
// gopi builds the agent with package harness. It reads the CLAUDE.md
// and AGENTS.md files, the skills, and the subagents of each --dir
// folder, lowest first. Without --dir, it reads ~/.claude,
// <root>/.claude, and <root>. --agent names an agent file of those
// folders, which fills the unset flags. Each tool call obeys --permission-mode and the --allow and
// --deny rules. A call that needs an approval asks on the terminal.
//
// gopi finds the providers from ANTHROPIC_API_KEY, OPENAI_API_KEY,
// OPENROUTER_API_KEY, and FIREWORKS_API_KEY, with the detectors of
// package catalog/env.
// --model names the model by its spec, such as
// anthropic/claude-opus-5-5. If --model is unset, gopi uses the first
// model of the first provider that it finds. If no provider registered
// the model, gopi asks the provider of the spec for its models once. A
// bare model ID asks every provider, so two providers can list the ID,
// and gopi then asks for the full spec.
//
// gopi reads the models of each provider from models.json: the path or
// the URL in $GOPI_MODELS, or $GOPI_HOME/models.json, or
// ~/.gopi/models.json. Without the file, a spec resolves after gopi asks
// the provider for its models.
//
// With --session-id, gopi keeps the conversation in a JSONL file of
// that name in $GOPI_HOME/sessions, or ~/.gopi/sessions. The first run
// creates the session, and each later run with the same ID resumes it.
// A resumed session keeps its model. --model changes it.
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
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/env"
	"github.com/sonnes/gopi/pkg/def"
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
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
	"github.com/sonnes/gopi/pkg/tool/askquestion"
	"github.com/sonnes/gopi/pkg/tool/todowrite"
)

// prompt asks for the next line on stderr, so that stdout holds only
// the tree.
const prompt = "> "

// errShown is the error of a run that the tree shows. A run that starts
// always sends agent_end with its error, so main exits with no message.
var errShown = errors.New("the run failed")

// inheritModel is the model of an agent file that keeps the model of
// the flags.
const inheritModel = "inherit"

// instructionFiles are the instruction files that gopi reads in each
// folder, in order: the file of Claude Code, then the file of the other
// agents.
var instructionFiles = []string{"CLAUDE.md", "AGENTS.md"}

// claudeDir is the folder of Claude Code, with agents/ and skills/,
// that gopi reads without --dir.
const claudeDir = ".claude"

// defaultTools names every tool of gopi, in the order that the model
// gets them.
const defaultTools = "read_file,write_file,edit_file,bash,grep,find,todo_write,ask_user_question"

// options holds the flags of gopi.
type options struct {
	// model is the model spec. Empty means the first model of the
	// first provider that gopi finds.
	model string
	turns int
	root  string
	fence bool
	tools string
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
	// system is the base text of the system prompt: the prompt of the
	// agent file. Empty means no base text.
	system string
	// agentModel is the model of the agent file. --model and the model
	// of a resumed session win over it.
	agentModel string
	// skills names the skills that the agent loads at the start: the
	// skills of the agent file.
	skills []string
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
				Usage:   "the model spec, such as anthropic/claude-opus-5-5 or openrouter/qwen/qwen3-coder, or a model ID that one provider serves. If unset, gopi uses the model of the resumed session, or the first model of the first provider that it finds",
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
			&cli.StringSliceFlag{
				Name:  "dir",
				Usage: "a folder with agents/, skills/, CLAUDE.md, and AGENTS.md. Give the flag again to add a folder. A later folder wins for a name. If unset, gopi reads ~/.claude, <root>/.claude, and <root>",
			},
			&cli.StringFlag{
				Name:  "agent",
				Usage: "the name of an agent file in agents/ of the --dir folders. Its model, thinking level, permission mode, prompt, and disallowed tools fill the flags that are unset. gopi reads the file at each start",
			},
			&cli.StringFlag{
				Name:  "mcp",
				Usage: "the path of an mcp.json file. gopi starts its servers and gives their tools to the agent. The fence does not confine the servers",
			},
		},
		Commands: []*cli.Command{
			modelsCommand(),
			hookCommand(),
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			opts := options{
				model:     c.String("model"),
				turns:     c.Int("turns"),
				root:      c.String("root"),
				fence:     c.Bool("fence"),
				tools:     c.String("tools"),
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
	dir, err := sessionsDir(os.Getenv)
	if err != nil {
		return err
	}
	if o.agent != "" {
		a, err := findAgent(ctx, o)
		if err != nil {
			return err
		}
		o = withAgent(o, a)
	}
	// A resumed session keeps its model unless --model changes it, so
	// gopi resolves the model that the harness uses.
	stored, err := storedSpec(ctx, dir, o.sessionID)
	if err != nil {
		return err
	}
	set := loadModels(ctx, os.Stderr, os.Getenv)
	cat, spec, err := newCatalog(ctx, modelSpec(o, stored), env.Detectors(set))
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
		harness.Defs(defReaders(o)...),
	}
	if o.system != "" {
		opts = append(opts, harness.System(o.system))
	}
	if len(o.skills) > 0 {
		opts = append(opts, harness.Preload(o.skills...))
	}
	if o.turns > 0 {
		opts = append(opts, harness.AgentDefaults(maxTurns(o.turns)))
	}
	if o.thinking != "" {
		level, err := ai.ParseThinking(o.thinking)
		if err != nil {
			return err
		}
		opts = append(opts, harness.AgentDefaults(agent.Thinking(level)))
	}
	a, err := newAgent(ctx, opts, spec, o.sessionID, dir)
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

// newAgent assembles the agent of gopi with the harness, on the model
// spec. With a session ID, the agent keeps the conversation in a JSONL
// file in dir, and a later run with the same ID resumes it. The spec
// becomes the model of a resumed session. Without a session ID, the
// conversation ends with gopi.
func newAgent(
	ctx context.Context,
	opts []harness.Option,
	spec string,
	sessionID string,
	dir string,
) (*agent.Agent, error) {
	if sessionID == "" {
		return harness.New(opts...).New(ctx, harness.Model(spec))
	}
	store, err := storefs.Sessions(dir)
	if err != nil {
		return nil, err
	}
	opts = append(slices.Clip(opts), harness.Store(store))
	// A Model on the call, and not in the options of New, changes the
	// model of a resumed session.
	return harness.New(opts...).Durable(ctx, sessionID, harness.Model(spec))
}

// storedSpec returns the model spec that the session id in dir keeps,
// or an empty string if id is empty or the session does not exist.
func storedSpec(ctx context.Context, dir, id string) (string, error) {
	if id == "" {
		return "", nil
	}
	store, err := storefs.Sessions(dir)
	if err != nil {
		return "", err
	}
	snap, err := store.Load(ctx, id)
	if errors.Is(err, session.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	spec, _, err := harness.SessionModel.Read(snap)
	if err != nil {
		return "", fmt.Errorf("session %s: %w", id, err)
	}
	return spec, nil
}

// newCatalog returns a catalog of the providers that ds find, and the
// spec of the model of gopi. If spec is empty, newCatalog uses the first
// model of the first provider that ds find with models, because the
// first model of a detector is the default of its provider.
//
// If no provider registered the model of spec, newCatalog asks the
// provider of spec for its models, because a provider such as
// openrouter registers none. A bare model ID asks every provider.
func newCatalog(ctx context.Context, spec string, ds []catalog.Detector) (*catalog.Catalog, string, error) {
	cat := catalog.New()
	found := cat.Detect(ds...)
	if len(found) == 0 {
		return nil, "", fmt.Errorf("no provider: set %s", sources(ds))
	}
	if spec == "" {
		spec = defaultSpec(ds, found)
	}
	if spec == "" {
		return nil, "", fmt.Errorf(
			"no default model: give a spec with --model, such as %s/<model-id>",
			found[0].Provider,
		)
	}
	// The harness resolves the spec too, but gopi checks it here, before
	// it starts the MCP servers.
	_, err := cat.LanguageModel(spec)
	var listErrs []error
	if errors.Is(err, catalog.ErrUnknownModel) {
		listErrs = fetchModels(ctx, cat, specProvider(spec, found, nil))
		_, err = cat.LanguageModel(spec)
	}
	if err != nil {
		// A spec of a provider with no credential fails as an unknown
		// model. The credential is the fix, so the error names it. The
		// check comes after the resolve, because a bare ID such as
		// anthropic/claude-sonnet-5.5 can resolve to another provider.
		if id := specProvider(spec, nil, ds); id != "" {
			if perr := providerErr(ds, found, id); perr != nil {
				return nil, "", fmt.Errorf("model %q: %w", spec, perr)
			}
		}
		errs := append([]error{withHint(err)}, listErrs...)
		return nil, "", fmt.Errorf("model %q: %w", spec, errors.Join(errs...))
	}
	return cat, spec, nil
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

// findAgent returns the agent file o.agent of the folders of
// [defDirs]. A later folder wins.
func findAgent(ctx context.Context, o options) (def.Agent, error) {
	dirs := defReaders(o)
	readers := make([]def.AgentsReader, len(dirs))
	for i, r := range dirs {
		readers[i] = r
	}
	return def.FindAgent(ctx, o.agent, readers...)
}

// withAgent fills each unset flag of o from the agent file a. The
// prompt of a becomes the base text of the system prompt, its
// disallowed tools go after --deny, and gopi preloads its skills.
func withAgent(o options, a def.Agent) options {
	if !strings.EqualFold(a.Model, inheritModel) {
		o.agentModel = a.Model
	}
	o.thinking = cmp.Or(o.thinking, string(a.Effort))
	o.mode = cmp.Or(o.mode, a.Mode)
	o.system = a.Prompt
	o.deny = append(slices.Clip(o.deny), a.Disallowed...)
	o.skills = a.Skills
	return o
}

// modelSpec returns the model of the run: --model, then the model of a
// resumed session, then the model of the agent file.
func modelSpec(o options, stored string) string {
	return cmp.Or(o.model, stored, o.agentModel)
}

// defReaders returns a reader of each folder of [defDirs].
func defReaders(o options) []def.Reader {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	dirs := defDirs(o, home)
	readers := make([]def.Reader, len(dirs))
	for i, d := range dirs {
		readers[i] = storefs.Dir(d, storefs.InstructionFiles(instructionFiles...))
	}
	return readers
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
