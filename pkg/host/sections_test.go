package host_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/host"
)

// prompts gives the sections of each session, by ID, and records the
// IDs that the host asks for.
type prompts struct {
	mu    sync.Mutex
	texts map[string]string
	asked []string
}

func (p *prompts) set(id, text string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.texts == nil {
		p.texts = make(map[string]string)
	}
	p.texts[id] = text
}

func (p *prompts) sections(_ context.Context, id string) ([]ai.Section, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, id)
	return []ai.Section{{Name: "project", Text: p.texts[id]}}, nil
}

// sectionsIn returns the sections of the system messages of msgs.
func sectionsIn(msgs []ai.Message) []ai.Section {
	var out []ai.Section
	for _, m := range msgs {
		if m.Role != ai.RoleSystem {
			continue
		}
		for _, c := range m.Content {
			if s, ok := c.(ai.Section); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func TestSectionsReachEachRun(t *testing.T) {
	p := &prompts{}
	p.set("s1", "Write tests in Go.")
	f := newFixture(t, host.Sections(p.sections))
	f.create(t, host.Config{ID: "s1"})
	_, events := f.watch(t)

	f.send(t, "s1", "a")
	runEnd(t, events)
	p.set("s1", "Write tests in Rust.")
	f.send(t, "s1", "b")
	runEnd(t, events)

	f.model.mu.Lock()
	last := f.model.calls[len(f.model.calls)-1]
	f.model.mu.Unlock()
	assert.Equal(t, []ai.Section{
		{Name: "project", Text: "Write tests in Rust."},
	}, sectionsIn(last), "the change goes before the second message, and the base goes in the options")

	p.mu.Lock()
	defer p.mu.Unlock()
	assert.Contains(t, p.asked, "s1", "the host asks for the sections of the session")
}

func TestAgentFilePromptIsTheFirstSection(t *testing.T) {
	p := &prompts{}
	p.set("s1", "Write tests in Go.")
	f := newFixture(t, host.Sections(p.sections))
	dir := t.TempDir()
	writeAgent(t, dir, "reviewer", reviewer)

	_, err := f.h.Create(context.Background(), host.Config{
		ID:    "s1",
		Dirs:  []string{dir},
		Agent: "reviewer",
	})
	require.NoError(t, err)

	c := f.lastConfig(t)
	require.NotNil(t, c.Sections)
	got, err := c.Sections(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []ai.Section{
		{Name: host.AgentSection, Text: "You review."},
		{Name: "project", Text: "Write tests in Go."},
	}, got)

	writeAgent(t, dir, "reviewer", reviewerWith("You review twice."))
	got, err = c.Sections(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "You review twice.", got[0].Text, "the host reads the prompt of the agent file at each run")
}

func TestNoSectionsWithoutAnAgentFileOrAFunction(t *testing.T) {
	f := newFixture(t)
	f.create(t, host.Config{ID: "s1"})

	assert.Nil(t, f.lastConfig(t).Sections, "the agent adds no prompt")
}
