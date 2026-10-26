package codex

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emittest"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

var (
	projectSection = ai.Section{Name: "project", Text: "# project\n\nWrite tests in Go."}
	emptyMemory    = ai.Section{Name: "memory", Text: "# memory\n\nThe memory is empty."}
	notedMemory    = ai.Section{Name: "memory", Text: "# memory\n\napi-port.md"}
)

// prompt is a sections function whose sections a test can change.
type prompt struct {
	mu       sync.Mutex
	sections []ai.Section
}

func (p *prompt) set(sections ...ai.Section) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sections = sections
}

func (p *prompt) get(context.Context) ([]ai.Section, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sections, nil
}

func TestSectionsChangeInTheNextTurn(t *testing.T) {
	useTraces(t, []string{"sections.trace"})
	p := &prompt{}
	p.set(projectSection, emptyMemory)
	a := newAgent(t, Sections(p.get))

	events, _, err := collect(a.Run(context.Background(), session.Text("One.")))
	require.NoError(t, err, "the thread gets the base as its developer instructions")
	emittest.CheckEvents(t, events)

	p.set(projectSection, notedMemory)
	events, _, err = collect(a.Run(context.Background(), session.Text("Two.")))
	require.NoError(t, err, "the change goes in the history as a developer message")
	emittest.CheckEvents(t, events)

	msgs := a.Log().Messages()
	require.Len(t, msgs, 6)
	assert.Equal(t, ai.SystemMessage(projectSection, emptyMemory), msgs[0], "the base is first")
	assert.Equal(t, ai.SystemMessage(notedMemory), msgs[3], "the change goes before the input")
	assert.Equal(t, "Two.", msgs[4].Text(), "the log keeps the input as it is")
	checkLog(t, a)
}

func TestSectionsResumeKeepsTheBase(t *testing.T) {
	useTraces(t, []string{"sections_resume_first.trace", "sections_resume_second.trace"})
	ctx := context.Background()
	store := session.NewMemoryStore()
	p := &prompt{}
	p.set(projectSection, emptyMemory)

	first, err := Durable(ctx, "s1", Store(store), Dir(workDir), Sections(p.get))
	require.NoError(t, err)
	_, _, err = collect(first.Run(ctx, session.Text("Remember 7.")))
	require.NoError(t, err)
	require.NoError(t, first.Close())

	p.set(projectSection, notedMemory)
	second, err := Durable(ctx, "s1", Store(store), Dir(workDir), Sections(p.get))
	require.NoError(t, err)
	t.Cleanup(func() { second.Close() })
	_, _, err = collect(second.Run(ctx, session.Text("What number?")))
	require.NoError(t, err, "the resumed thread gets the base of the log, and the change as a developer message")
}

func TestSystemIsOneSection(t *testing.T) {
	useTraces(t, []string{"text.trace"})
	a := newAgent(t,
		Model("gpt-5-codex"),
		System("Be brief."),
		Sandbox(SandboxReadOnly),
		Thinking(ai.ThinkingHigh),
		Name("codex"),
	)

	_, err := a.Run(context.Background(), session.Text("Say hello.")).Wait()
	require.NoError(t, err)

	msgs := a.Log().Messages()
	require.NotEmpty(t, msgs)
	assert.Equal(t, ai.SystemMessage(ai.Section{Name: agent.SystemSection, Text: "Be brief."}), msgs[0])
}
