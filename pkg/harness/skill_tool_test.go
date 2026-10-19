package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/skill"
)

var review = skill.Skill{
	Name:        "review",
	Description: "Review a diff.",
	Body:        "Read the diff.",
}

// callSkill runs an agent whose model calls the skill tool once with
// input, and returns the tool result message.
func callSkill(t *testing.T, input string) ai.Message {
	t.Helper()
	p := &script{responses: []ai.Message{
		callTool("c1", "skill", input),
		reply("done"),
	}}
	h := harness.New(with(base(t, p), harness.Skillset(deploy, review))...)
	a, err := h.New(context.Background())
	require.NoError(t, err)
	run(t, a, "go")
	return toolResult(t, a)
}

func TestSkillToolSchemaListsTheSkills(t *testing.T) {
	asm := assemblyOf(t, with(base(t, &script{}), harness.Skillset(deploy, review)))
	require.Len(t, asm.Tools, 1)

	info := asm.Tools[0]
	assert.Equal(t, "skill", info.Name)
	assert.Equal(t, []any{"deploy", "review"}, info.InputSchema.Properties["name"].Enum)
	assert.Equal(t, []string{"name"}, info.InputSchema.Required)
}

func TestAnAssemblyWithNoSkillsHasNoSkillTool(t *testing.T) {
	asm := assemblyOf(t, base(t, &script{}))

	assert.Empty(t, asm.Tools)
}

func TestSkillToolReturnsTheBody(t *testing.T) {
	m := callSkill(t, `{"name":"review"}`)

	assert.False(t, m.IsError)
	assert.Equal(t, "Read the diff.", m.Text())
}

func TestSkillToolListsTheSupportFiles(t *testing.T) {
	m := callSkill(t, `{"name":"deploy"}`)

	assert.Contains(t, m.Text(), "Run scripts/run.sh.")
	assert.Contains(t, m.Text(), "- scripts/run.sh")
	assert.NotContains(t, m.Text(), "SKILL.md")
}

func TestSkillToolReadsASupportFile(t *testing.T) {
	m := callSkill(t, `{"name":"deploy","file":"scripts/run.sh"}`)

	assert.False(t, m.IsError)
	assert.Equal(t, "echo deploy\n", m.Text())
}

func TestSkillToolErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"unknown skill", `{"name":"nope"}`, "deploy, review"},
		{"unknown file", `{"name":"deploy","file":"missing.sh"}`, "missing.sh"},
		{"file outside the skill", `{"name":"deploy","file":"../x"}`, "../x"},
		{"file of a skill with no files", `{"name":"review","file":"a.txt"}`, "a.txt"},
		{"bad input", `{"name":1}`, "input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := callSkill(t, tt.input)

			assert.True(t, m.IsError)
			assert.Contains(t, m.Text(), tt.want)
		})
	}
}
