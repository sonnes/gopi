package harness_test

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/session"
)

func TestNewAssemblesTheAgent(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(p),
		harness.System("You are a test agent."),
		skillTool,
		harness.Toolset(harness.Tools(echo)),
	)...)
	c := config(t)
	c.Defs = def.Set{Skills: []def.Skill{deploy}}

	a, err := h.New(context.Background(), c)
	require.NoError(t, err)
	run(t, a, "hello")

	sections := a.State().Sections()
	require.Len(t, sections, 2)
	assert.Equal(t, ai.Section{Name: harness.SystemSection, Text: "You are a test agent."}, sections[0])
	assert.Equal(t, "skills", sections[1].Name)
	assert.Contains(t, sections[1].Text, "- deploy: Deploy the service.")
	assert.Equal(t, []string{"skill", "echo"}, toolNames(a), "the order of the tool list")
	assert.Equal(t, []string{"one"}, p.called())
}

func TestNewSeedsTheLog(t *testing.T) {
	h := harness.New(with(base(&script{}), harness.Seed(harness.DefaultSeed))...)
	c := config(t)

	a, err := h.New(context.Background(), c)
	require.NoError(t, err)

	entries := a.Log().Entries()
	require.Len(t, entries, 1)
	seed := entries[0]
	assert.Equal(t, harness.SeedAuthor, seed.Author)
	assert.Contains(t, seed.Message.Text(), "Working directory: "+c.Dir)
	assert.Contains(t, seed.Message.Text(), runtime.GOOS)
}

func TestNewRunsTheSeedersInOrder(t *testing.T) {
	note := func(text string) harness.Seeder {
		return func(context.Context, *harness.Assembly) ([]session.Entry, error) {
			return []session.Entry{session.Text(text)}, nil
		}
	}
	h := harness.New(with(base(&script{}),
		harness.Seed(note("first")),
		harness.Seed(note("second")),
	)...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)

	entries := a.Log().Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "first", entries[0].Message.Text())
	assert.Equal(t, "second", entries[1].Message.Text())
}

func TestNewPassesTheAgentDefaults(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(p), harness.AgentDefaults(agent.Name("base")))...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "hello")

	entries := a.Log().Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "base", entries[1].Author)
}

func TestTheEffortOfTheConfigWinsOverTheAgentDefaults(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(p), harness.AgentDefaults(agent.Thinking(ai.ThinkingLow)))...)
	c := config(t)
	c.Effort = ai.ThinkingHigh

	a, err := h.New(context.Background(), c)
	require.NoError(t, err)
	run(t, a, "hello")

	p.mu.Lock()
	defer p.mu.Unlock()
	level, ok := ai.GetOption(p.opts[0], ai.Thinking)
	assert.True(t, ok)
	assert.Equal(t, ai.ThinkingHigh, level)
}

func TestNewUsesThePromptBuilder(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	var seen *harness.Assembly
	prompt := func(_ context.Context, asm *harness.Assembly) ([]ai.Section, error) {
		seen = asm
		return []ai.Section{{Name: "custom", Text: "custom prompt"}}, nil
	}
	h := harness.New(with(base(p), harness.Prompt(prompt))...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "hello")

	assert.Equal(t, []ai.Section{{Name: "custom", Text: "custom prompt"}}, a.State().Sections())
	require.NotNil(t, seen)
	assert.Equal(t, "fake/one", seen.Spec)
}

func TestTheDefaultPromptListsOnlyTheBoundTools(t *testing.T) {
	tests := []struct {
		name      string
		tools     []harness.Option
		toolNames []string
		want      []string
	}{
		{
			name:  "the skill tool and the agent tool",
			tools: []harness.Option{skillTool, agentTool},
			want:  []string{"skills", "agents"},
		},
		{
			name: "no binder",
		},
		{
			name:      "the ToolNames of the config leave out the agent tool",
			tools:     []harness.Option{harness.Toolset(harness.Tools(harness.SkillTool(), harness.AgentTool()))},
			toolNames: []string{"skill"},
			want:      []string{"skills"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := with(base(&script{}), tt.tools...)
			c := config(t)
			c.Defs = def.Set{
				Skills: []def.Skill{deploy},
				Agents: []def.Agent{reviewer},
			}
			c.ToolNames = tt.toolNames

			sections, err := harness.DefaultPrompt(context.Background(), assemblyOf(t, opts, c))
			require.NoError(t, err)

			var names []string
			for _, s := range sections {
				names = append(names, s.Name)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

func TestNewReportsASeedError(t *testing.T) {
	errBuild := errors.New("build failed")
	h := harness.New(with(base(&script{}),
		harness.Seed(func(context.Context, *harness.Assembly) ([]session.Entry, error) {
			return nil, errBuild
		}),
	)...)

	_, err := h.New(context.Background(), config(t))
	assert.ErrorIs(t, err, errBuild)
}

func TestNewReportsTwoToolsWithOneName(t *testing.T) {
	h := harness.New(with(base(&script{}), harness.Toolset(harness.Tools(echo, echo)))...)

	_, err := h.New(context.Background(), config(t))
	assert.ErrorContains(t, err, `"echo"`)
}

func TestNewNeedsAModel(t *testing.T) {
	withCatalog := harness.Catalog(newCatalog(&script{}))
	tests := []struct {
		name string
		opts []harness.Option
		spec string
		want string
	}{
		{"no catalog", nil, "fake/one", "Catalog"},
		{"no spec", []harness.Option{withCatalog}, "", "Spec"},
		{"unknown provider", []harness.Option{withCatalog}, "nine/x", "nine/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := agent.Config{Spec: tt.spec}
			_, err := harness.New(tt.opts...).New(context.Background(), c)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRunReportsAPromptError(t *testing.T) {
	errBuild := errors.New("build failed")
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(p),
		harness.Prompt(func(context.Context, *harness.Assembly) ([]ai.Section, error) {
			return nil, errBuild
		}),
	)...)

	a, err := h.New(context.Background(), config(t))
	require.NoError(t, err, "the builder runs before each model call")
	_, err = a.Run(context.Background(), session.Text("hello")).Wait()
	assert.ErrorIs(t, err, errBuild)
	assert.Empty(t, p.called(), "the run fails before the model call")
}

func TestStartUpdatesTheSkillsSection(t *testing.T) {
	p := &script{responses: []ai.Message{reply("a"), reply("b"), reply("c")}}
	h := harness.New(with(base(p), skillTool)...)
	c := config(t)
	c.Store = session.NewMemoryStore()

	for _, r := range []struct{ description, input string }{
		{"Deploy the service.", "first"},
		{"Deploy the service.", "second"},
		{"Deploy with the release script.", "third"},
	} {
		withSkill := c
		withSkill.Defs = def.Set{Skills: []def.Skill{
			{Name: "deploy", Description: r.description, Body: "Run make deploy."},
		}}
		a := start(t, h, withSkill)
		run(t, a, r.input)
	}

	a := start(t, h, c)
	var changes []string
	for _, msg := range a.Log().Messages() {
		for _, c := range msg.Content {
			if s, ok := c.(ai.Section); ok {
				changes = append(changes, s.Text)
			}
		}
	}
	require.Len(t, changes, 2, "a resumed session with the same skills writes no change")
	assert.Contains(t, changes[0], "Deploy the service.")
	assert.Contains(t, changes[1], "Deploy with the release script.")
}

func TestStartKeepsTheSpecOfTheSession(t *testing.T) {
	p := &script{responses: []ai.Message{reply("a"), reply("b"), reply("c")}}
	h := harness.New(base(p)...)
	c := config(t)
	c.Store = session.NewMemoryStore()

	// The first call picks fake/two, and the session keeps it.
	two := c
	two.Spec = "fake/two"
	a := start(t, h, two)
	run(t, a, "first")

	// A resume with no spec uses the spec of the session.
	noSpec := c
	noSpec.Spec = ""
	a = start(t, h, noSpec)
	run(t, a, "second")

	// A spec changes the model of the session.
	a = start(t, h, c)
	run(t, a, "third")

	assert.Equal(t, []string{"two", "two", "one"}, p.called())
}

func TestStartSeedsOnlyANewSession(t *testing.T) {
	h := harness.New(with(base(&script{}), harness.Seed(harness.DefaultSeed))...)
	c := config(t)
	c.Store = session.NewMemoryStore()

	for range 2 {
		a := start(t, h, c)
		assert.Len(t, a.Log().Entries(), 1)
	}
}

func TestStartWithoutAStoreResumesTheSession(t *testing.T) {
	p := &script{responses: []ai.Message{reply("a")}}
	h := harness.New(base(p)...)
	c := config(t)

	a := start(t, h, c)
	run(t, a, "first")

	a = start(t, h, c)
	assert.Equal(t, "s1", a.Log().Session().ID)
	assert.Len(t, a.Log().Entries(), 2)
}

func TestStartRejectsAnEmptyID(t *testing.T) {
	h := harness.New(base(&script{})...)
	c := config(t)
	c.SessionID = ""

	_, err := h.Start(context.Background(), c)
	assert.Error(t, err)
}

func TestStartStoreWinsOverTheAgentStore(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	other := session.NewMemoryStore()
	h := harness.New(with(base(&script{}), harness.AgentDefaults(agent.Store(other)))...)
	c := config(t)
	c.Store = store

	_, err := h.Start(ctx, c)
	require.NoError(t, err)

	_, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	_, err = other.Load(ctx, "s1")
	assert.ErrorIs(t, err, session.ErrNotFound)
}

func TestStartLoadsTheSessionOnce(t *testing.T) {
	ctx := context.Background()
	store := &loadCounter{MemoryStore: session.NewMemoryStore()}
	h := harness.New(base(&script{})...)
	c := config(t)
	c.Store = store
	two := c
	two.Spec = "fake/two"

	_, err := h.Start(ctx, two)
	require.NoError(t, err)
	store.loads = 0

	c.Spec = ""
	a := start(t, h, c)
	assert.Equal(t, 1, store.loads)

	spec, _, err := harness.SessionModel.Get(a.Log())
	require.NoError(t, err)
	assert.Equal(t, "fake/two", spec)
}
