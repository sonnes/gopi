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
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/skill"
	"github.com/sonnes/gopi/pkg/session"
)

func TestNewAssemblesTheAgent(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(t, p),
		harness.System("You are a test agent."),
		harness.Skillset(deploy),
		harness.Toolset(harness.Tools(echo)),
	)...)

	a, err := h.New(context.Background())
	require.NoError(t, err)
	run(t, a, "hello")

	sections := a.State().Sections()
	require.Len(t, sections, 2)
	assert.Equal(t, ai.Section{Name: harness.SystemSection, Text: "You are a test agent."}, sections[0])
	assert.Equal(t, "skills", sections[1].Name)
	assert.Contains(t, sections[1].Text, "- deploy: Deploy the service.")
	assert.Equal(t, []string{"echo", "skill"}, toolNames(a))
	assert.Equal(t, []string{"one"}, p.called())
}

func TestNewSeedsTheLog(t *testing.T) {
	dir := t.TempDir()
	h := harness.New(with(base(t, &script{}),
		harness.Seed(harness.DefaultSeed),
		harness.Dir(dir),
	)...)

	a, err := h.New(context.Background())
	require.NoError(t, err)

	entries := a.Log().Entries()
	require.Len(t, entries, 1)
	seed := entries[0]
	assert.Equal(t, harness.SeedAuthor, seed.Author)
	assert.Contains(t, seed.Message.Text(), "Working directory: "+dir)
	assert.Contains(t, seed.Message.Text(), runtime.GOOS)
}

func TestNewRunsTheSeedersInOrder(t *testing.T) {
	note := func(text string) harness.Seeder {
		return func(context.Context, *harness.Assembly) ([]session.Entry, error) {
			return []session.Entry{session.Text(text)}, nil
		}
	}
	h := harness.New(with(base(t, &script{}), harness.Seed(note("first")))...)

	a, err := h.New(context.Background(), harness.Seed(note("second")))
	require.NoError(t, err)

	entries := a.Log().Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "first", entries[0].Message.Text())
	assert.Equal(t, "second", entries[1].Message.Text())
}

func TestNewPassesTheAgentDefaults(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(t, p), harness.AgentDefaults(agent.Name("base")))...)

	a, err := h.New(context.Background(), harness.AgentDefaults(agent.Name("call")))
	require.NoError(t, err)
	run(t, a, "hello")

	entries := a.Log().Entries()
	require.Len(t, entries, 2)
	assert.Equal(t, "call", entries[1].Author)
}

func TestNewUsesThePromptBuilder(t *testing.T) {
	p := &script{responses: []ai.Message{reply("hi")}}
	var seen *harness.Assembly
	prompt := func(_ context.Context, asm *harness.Assembly) ([]ai.Section, error) {
		seen = asm
		return []ai.Section{{Name: "custom", Text: "custom prompt"}}, nil
	}
	h := harness.New(with(base(t, p), harness.Prompt(prompt))...)

	a, err := h.New(context.Background())
	require.NoError(t, err)
	run(t, a, "hello")

	assert.Equal(t, []ai.Section{{Name: "custom", Text: "custom prompt"}}, a.State().Sections())
	require.NotNil(t, seen)
	assert.Equal(t, "fake/one", seen.Spec)
}

func TestNewReportsASeedError(t *testing.T) {
	errBuild := errors.New("build failed")
	h := harness.New(base(t, &script{})...)

	_, err := h.New(context.Background(), harness.Seed(func(context.Context, *harness.Assembly) ([]session.Entry, error) {
		return nil, errBuild
	}))
	assert.ErrorIs(t, err, errBuild)
}

func TestNewReportsTwoToolsWithOneName(t *testing.T) {
	h := harness.New(with(base(t, &script{}), harness.Toolset(harness.Tools(echo, echo)))...)

	_, err := h.New(context.Background())
	assert.ErrorContains(t, err, `"echo"`)
}

func TestNewNeedsAModel(t *testing.T) {
	tests := []struct {
		name string
		opts []harness.Option
		want string
	}{
		{"no catalog", []harness.Option{harness.Model("fake/one")}, "Catalog"},
		{"no spec", []harness.Option{harness.Catalog(newCatalog(&script{}))}, "Model"},
		{"unknown provider", []harness.Option{harness.Catalog(newCatalog(&script{})), harness.Model("nine/x")}, "nine/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := with(tt.opts, harness.Builtin(harness.NoBuiltins), harness.Sources())
			_, err := harness.New(opts...).New(context.Background())
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestRunReportsAPromptError(t *testing.T) {
	errBuild := errors.New("build failed")
	p := &script{responses: []ai.Message{reply("hi")}}
	h := harness.New(with(base(t, p),
		harness.Prompt(func(context.Context, *harness.Assembly) ([]ai.Section, error) {
			return nil, errBuild
		}),
	)...)

	a, err := h.New(context.Background())
	require.NoError(t, err, "the builder runs before each model call")
	_, err = a.Run(context.Background(), session.Text("hello")).Wait()
	assert.ErrorIs(t, err, errBuild)
	assert.Empty(t, p.called(), "the run fails before the model call")
}

func TestDurableUpdatesTheSkillsSection(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{reply("a"), reply("b"), reply("c")}}
	h := harness.New(with(base(t, p), harness.Store(session.NewMemoryStore()))...)
	withSkill := func(description string) harness.Option {
		return harness.Skillset(skill.Skill{Name: "deploy", Description: description, Body: "Run make deploy."})
	}

	for _, r := range []struct{ description, input string }{
		{"Deploy the service.", "first"},
		{"Deploy the service.", "second"},
		{"Deploy with the release script.", "third"},
	} {
		a, err := h.Durable(ctx, "s1", withSkill(r.description))
		require.NoError(t, err)
		run(t, a, r.input)
	}

	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
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

func TestDurableKeepsTheSpecOfTheSession(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{reply("a"), reply("b"), reply("c")}}
	h := harness.New(with(base(t, p), harness.Store(session.NewMemoryStore()))...)

	// The first call picks fake/two, and the session keeps it.
	a, err := h.Durable(ctx, "s1", harness.Model("fake/two"))
	require.NoError(t, err)
	run(t, a, "first")

	// A resume with no spec uses the spec of the session, not the default.
	a, err = h.Durable(ctx, "s1")
	require.NoError(t, err)
	run(t, a, "second")

	// A new session uses the default.
	a, err = h.Durable(ctx, "s2")
	require.NoError(t, err)
	run(t, a, "third")

	assert.Equal(t, []string{"two", "two", "one"}, p.called())
}

func TestDurableSeedsOnlyANewSession(t *testing.T) {
	ctx := context.Background()
	h := harness.New(with(base(t, &script{}),
		harness.Store(session.NewMemoryStore()),
		harness.Seed(harness.DefaultSeed),
	)...)

	for range 2 {
		a, err := h.Durable(ctx, "s1")
		require.NoError(t, err)
		assert.Len(t, a.Log().Entries(), 1)
	}
}

func TestDurableWithoutAStoreResumesTheSession(t *testing.T) {
	ctx := context.Background()
	p := &script{responses: []ai.Message{reply("a")}}
	h := harness.New(base(t, p)...)

	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
	run(t, a, "first")

	a, err = h.Durable(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, "s1", a.Log().Session().ID)
	assert.Len(t, a.Log().Entries(), 2)
}

func TestDurableRejectsAnEmptyID(t *testing.T) {
	h := harness.New(base(t, &script{})...)

	_, err := h.Durable(context.Background(), "")
	assert.Error(t, err)
}

func TestDurableStoreWinsOverTheAgentStore(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	other := session.NewMemoryStore()
	h := harness.New(with(base(t, &script{}),
		harness.Store(store),
		harness.AgentDefaults(agent.Store(other)),
	)...)

	_, err := h.Durable(ctx, "s1")
	require.NoError(t, err)

	_, err = store.Load(ctx, "s1")
	require.NoError(t, err)
	_, err = other.Load(ctx, "s1")
	assert.ErrorIs(t, err, session.ErrNotFound)
}

func TestDurableLoadsTheSessionOnce(t *testing.T) {
	ctx := context.Background()
	store := &loadCounter{MemoryStore: session.NewMemoryStore()}
	h := harness.New(with(base(t, &script{}), harness.Store(store))...)

	_, err := h.Durable(ctx, "s1", harness.Model("fake/two"))
	require.NoError(t, err)
	store.loads = 0

	a, err := h.Durable(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, 1, store.loads)

	spec, _, err := harness.SessionModel.Get(a.Log())
	require.NoError(t, err)
	assert.Equal(t, "fake/two", spec)
}

func TestTheDefaultBuiltinsAreTheCodingTools(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the fence of the default sandbox runs only on macOS")
	}
	tests := []struct {
		name string
		opt  harness.Option
		want []string
	}{
		{"default", func(*harness.Harness) {}, []string{"read_file", "write_file", "edit_file", "bash", "grep", "find", "todo_write"}},
		{"read only", harness.Builtin(harness.ReadOnly), []string{"read_file", "grep", "find", "todo_write"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := []harness.Option{
				harness.Catalog(newCatalog(&script{})),
				harness.Model("fake/one"),
				harness.Seed(harness.NoSeed),
				harness.Sources(),
				harness.Dir(t.TempDir()),
				tt.opt,
			}

			asm := assemblyOf(t, opts)

			var names []string
			for _, info := range asm.Tools {
				names = append(names, info.Name)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}
