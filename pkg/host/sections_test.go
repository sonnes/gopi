package host_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/tool"
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
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "reviewer",
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

// sectionsOf returns the sections of the session that started last.
func sectionsOf(t *testing.T, f fixture) []ai.Section {
	t.Helper()
	c := f.lastConfig(t)
	require.NotNil(t, c.Sections)
	got, err := c.Sections(context.Background())
	require.NoError(t, err)
	return got
}

// skillTool gives the host of a fixture the skill tool.
var skillTool = host.Tools(tool.Named(tool.Skill))

// deployer is an agent file that keeps the skill tool.
const deployer = `---
name: deployer
description: Deploy the app.
model: fake/m
tools: [Read, Skill]
---
You deploy.
`

// deploy is a skill that the model can call.
var deploy = def.Skill{
	Name:        "deploy",
	Description: "Deploy the app.",
}

func TestSkillRulesFollowTheAgentSection(t *testing.T) {
	p := &prompts{}
	p.set("s1", "Write tests in Go.")
	f := newFixture(t, host.Sections(p.sections), skillTool)
	dir := t.TempDir()
	writeAgent(t, dir, "deployer", deployer)
	writeSkill(t, dir, "deploy", "Deploy the app.")

	_, err := f.h.Create(context.Background(), host.Config{
		ID:      "s1",
		DefDirs: []string{dir},
		Agent:   "deployer",
	})
	require.NoError(t, err)

	assert.Equal(t, []ai.Section{
		{Name: host.AgentSection, Text: "You deploy."},
		{Name: host.SkillRulesSection, Text: host.SkillRulesPrompt},
		{Name: "project", Text: "Write tests in Go."},
	}, sectionsOf(t, f))
}

func TestNoSkillRulesWithoutSkills(t *testing.T) {
	p := &prompts{}
	p.set("s1", "Write tests in Go.")
	f := newFixture(t, host.Sections(p.sections), skillTool)
	f.create(t, host.Config{ID: "s1"})

	assert.Equal(t, []ai.Section{
		{Name: "project", Text: "Write tests in Go."},
	}, sectionsOf(t, f))
}

func TestSkillRulesWithoutAnAgentFileOrAFunction(t *testing.T) {
	f := newFixture(t, skillTool)
	dir := t.TempDir()
	writeSkill(t, dir, "deploy", "Deploy the app.")
	f.create(t, host.Config{ID: "s1", DefDirs: []string{dir}})

	assert.Equal(t, []ai.Section{
		{Name: host.SkillRulesSection, Text: host.SkillRulesPrompt},
	}, sectionsOf(t, f))
}

func TestNoSkillRulesWhenTheModelCannotCallASkill(t *testing.T) {
	hidden := deploy
	hidden.NoModelCall = true
	tests := []struct {
		name   string
		opts   []host.Option
		config host.Config
	}{
		{
			name: "no skill tool in the host",
			opts: []host.Option{host.Defs(def.Set{Skills: []def.Skill{deploy}})},
		},
		{
			name:   "the tool names leave out the skill tool",
			opts:   []host.Option{skillTool, host.Defs(def.Set{Skills: []def.Skill{deploy}})},
			config: host.Config{ToolNames: []string{tool.Bash}},
		},
		{
			name:   "a deny rule removes the skill tool",
			opts:   []host.Option{skillTool, host.Defs(def.Set{Skills: []def.Skill{deploy}})},
			config: host.Config{Deny: []string{"Skill"}},
		},
		{
			name: "each skill is for a person only",
			opts: []host.Option{skillTool, host.Defs(def.Set{Skills: []def.Skill{hidden}})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.opts...)
			tt.config.ID = "s1"
			f.create(t, tt.config)

			assert.Nil(t, f.lastConfig(t).Sections, "the agent adds no prompt")
		})
	}
}

func TestSkillRulesReadTheSkillsOnceAtTheStart(t *testing.T) {
	f := newFixture(t, skillTool)
	dir := t.TempDir()
	writeSkill(t, dir, "deploy", "Deploy the app.")
	f.create(t, host.Config{ID: "s1", DefDirs: []string{dir}})
	require.NoError(t, os.Chmod(filepath.Join(dir, "skills"), 0))
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "skills"), 0o755) })

	assert.Equal(t, []ai.Section{
		{Name: host.SkillRulesSection, Text: host.SkillRulesPrompt},
	}, sectionsOf(t, f), "a model call reads no skill folder")
}
