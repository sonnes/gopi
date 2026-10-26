package claude_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
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

// appendedPrompt returns the value of --append-system-prompt in args,
// or "".
func appendedPrompt(args []string) string {
	for i, a := range args {
		if a == "--append-system-prompt" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

const basePrompt = "# project\n\nWrite tests in Go.\n\n# memory\n\nThe memory is empty."

func TestSectionsChangeInTheNextUserMessage(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/sections.trace")
	p := &prompt{}
	p.set(projectSection, emptyMemory)
	a := claude.New(claude.Sections(p.get))
	defer closeAgent(t, a)

	events, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)
	emittest.CheckEvents(t, events)

	p.set(projectSection, notedMemory)
	events, _, err = run(context.Background(), a, "Again")
	require.NoError(t, err, "the trace matches the reminder before the input")
	emittest.CheckEvents(t, events)

	cmds := starts.Cmds()
	require.Len(t, cmds, 1)
	assert.Equal(t, basePrompt, appendedPrompt(cmds[0].Args))

	msgs := a.Log().Messages()
	require.Len(t, msgs, 6)
	assert.Equal(t, ai.SystemMessage(projectSection, emptyMemory), msgs[0], "the base is first")
	assert.Equal(t, ai.SystemMessage(notedMemory), msgs[3], "the change goes before the input")
	assert.Equal(t, "Again", msgs[4].Text(), "the log keeps the input without the reminder")
	checkLog(t, a)
}

func TestSectionsResumeKeepsTheBase(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore()
	starts := claude.Replay(t, nil, "testdata/text.trace", "testdata/sections_resume.trace")
	p := &prompt{}
	p.set(projectSection, emptyMemory)

	first, err := claude.Durable(ctx, "s1", claude.Store(store), claude.Sections(p.get))
	require.NoError(t, err)
	_, _, err = run(ctx, first, "Say hello")
	require.NoError(t, err)
	require.NoError(t, first.Close())

	p.set(projectSection, notedMemory)
	second, err := claude.Durable(ctx, "s1", claude.Store(store), claude.Sections(p.get))
	require.NoError(t, err)
	defer closeAgent(t, second)
	_, _, err = run(ctx, second, "Again")
	require.NoError(t, err)

	cmds := starts.Cmds()
	require.Len(t, cmds, 2)
	assert.Equal(t, basePrompt, appendedPrompt(cmds[1].Args),
		"a resumed CLI gets the base of the log, so its prompt stays")
}

func TestSystemIsOneSection(t *testing.T) {
	starts := claude.Replay(t, nil, "testdata/text.trace")
	a := claude.New(claude.System("Be brief."))
	defer closeAgent(t, a)

	_, _, err := run(context.Background(), a, "Say hello")
	require.NoError(t, err)

	assert.Equal(t, "Be brief.", appendedPrompt(starts.Cmds()[0].Args))
	msgs := a.Log().Messages()
	require.NotEmpty(t, msgs)
	assert.Equal(t, ai.SystemMessage(ai.Section{Name: agent.SystemSection, Text: "Be brief."}), msgs[0])
}
