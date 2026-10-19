package harness_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/skill"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/tool"
)

// script is a [ai.TextProvider] that answers each call with the next
// scripted message. It records what it received.
type script struct {
	mu        sync.Mutex
	responses []ai.Message
	calls     [][]ai.Message
	opts      []ai.Options
	models    []string
}

func (p *script) StreamText(
	_ context.Context,
	m ai.Model,
	messages []ai.Message,
	opts ai.Options,
) *ai.EventStream {
	p.mu.Lock()
	p.calls = append(p.calls, messages)
	p.opts = append(p.opts, opts)
	p.models = append(p.models, m.ID)
	var next *ai.Message
	if len(p.responses) > 0 {
		r := p.responses[0]
		p.responses = p.responses[1:]
		next = &r
	}
	p.mu.Unlock()

	return ai.NewEventStream(func(push func(ai.Event)) (*ai.Message, error) {
		push(ai.Event{Type: ai.EventStart})
		if next == nil {
			return nil, errors.New("script: no response left")
		}
		return next, nil
	})
}

// called returns the model IDs of the calls so far.
func (p *script) called() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.models...)
}

func reply(s string) ai.Message {
	m := ai.AssistantMessage(ai.Text{Text: s})
	m.StopReason = ai.StopReasonStop
	m.Usage = ai.Usage{Input: 10, Output: 5}
	return m
}

func callTool(id, name, input string) ai.Message {
	m := ai.AssistantMessage(ai.ToolCall{ID: id, Name: name, Input: json.RawMessage(input)})
	m.StopReason = ai.StopReasonToolUse
	m.Usage = ai.Usage{Input: 10, Output: 5}
	return m
}

// newCatalog returns a catalog with the models fake/one and fake/two,
// both served by p.
func newCatalog(p *script) *catalog.Catalog {
	cat := catalog.New()
	cat.RegisterTextProvider(
		"fake",
		p,
		ai.Model{ID: "one"},
		ai.Model{ID: "two"},
	)
	return cat
}

// base returns the options of a harness for a test: the catalog of p,
// the model fake/one, no built-in tools, no seed, and a project and a
// home in empty temporary folders.
func base(t *testing.T, p *script) []harness.Option {
	t.Helper()
	return []harness.Option{
		harness.Catalog(newCatalog(p)),
		harness.Model("fake/one"),
		harness.Builtin(harness.NoBuiltins),
		harness.Seed(harness.NoSeed),
		harness.Dir(t.TempDir()),
		harness.Home(t.TempDir()),
	}
}

// with returns opts, then more.
func with(opts []harness.Option, more ...harness.Option) []harness.Option {
	return append(append([]harness.Option(nil), opts...), more...)
}

// run runs a with input and fails t on an error.
func run(t *testing.T, a *agent.Agent, input string) []ai.Message {
	t.Helper()
	msgs, err := a.Run(context.Background(), session.Text(input)).Wait()
	require.NoError(t, err)
	return msgs
}

// toolNames returns the names of the tools that the model can call now.
func toolNames(a *agent.Agent) []string {
	var out []string
	for _, info := range a.State().Tools() {
		out = append(out, info.Name)
	}
	return out
}

// section returns the text of the section name, or "".
func section(a *agent.Agent, name string) string {
	for _, s := range a.State().Sections() {
		if s.Name == name {
			return s.Text
		}
	}
	return ""
}

// toolResult returns the first tool result message of the log of a.
func toolResult(t *testing.T, a *agent.Agent) ai.Message {
	t.Helper()
	for _, m := range a.Log().Messages() {
		if m.Role == ai.RoleToolResult {
			return m
		}
	}
	t.Fatal("no tool result in the log")
	return ai.Message{}
}

// assemblyOf returns the assembly of one call of h.New.
func assemblyOf(t *testing.T, opts []harness.Option) *harness.Assembly {
	t.Helper()
	var seen *harness.Assembly
	capture := func(_ context.Context, asm *harness.Assembly) ([]session.Entry, error) {
		seen = asm
		return nil, nil
	}
	h := harness.New(with(opts, harness.Seed(capture))...)
	_, err := h.New(context.Background())
	require.NoError(t, err)
	require.NotNil(t, seen)
	return seen
}

// writeFile writes text to dir/name, and makes the folders of name.
func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(text), 0o644))
}

// skillFile returns a SKILL.md file with description and body.
func skillFile(description, body string) string {
	return "---\ndescription: " + description + "\n---\n" + body + "\n"
}

// deploy is the skill deploy, with a support file.
var deploy = skill.Skill{
	Name:        "deploy",
	Description: "Deploy the service.",
	Body:        "Run scripts/run.sh.",
	Files: fstest.MapFS{
		"SKILL.md":       {Data: []byte("---\ndescription: Deploy the service.\n---\n")},
		"scripts/run.sh": {Data: []byte("echo deploy\n")},
	},
}

type echoInput struct {
	S string `json:"s"`
}

var echo = tool.New("echo", func(_ context.Context, in echoInput) (string, error) {
	return in.S, nil
}, tool.Description("Echo the input."))

// loadCounter counts the loads that reach the store.
type loadCounter struct {
	*session.MemoryStore
	mu    sync.Mutex
	loads int
}

func (s *loadCounter) Load(ctx context.Context, id string) (session.Snapshot, error) {
	s.mu.Lock()
	s.loads++
	s.mu.Unlock()
	return s.MemoryStore.Load(ctx, id)
}
