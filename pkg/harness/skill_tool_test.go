package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
)

var review = def.Skill{
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

// lastUser returns the texts of the last user message that p received.
func lastUser(t *testing.T, p *script) []string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	require.NotEmpty(t, p.calls)
	msgs := p.calls[len(p.calls)-1]
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != ai.RoleUser {
			continue
		}
		var out []string
		for _, c := range msgs[i].Content {
			if text, ok := c.(ai.Text); ok {
				out = append(out, text.Text)
			}
		}
		return out
	}
	t.Fatal("no user message")
	return nil
}

func TestASlashCallLoadsTheSkillBeforeTheText(t *testing.T) {
	userOnly := def.Skill{
		Name:        "lw-tasks",
		Description: "File a task.",
		Body:        "File one task.",
		NoModelCall: true,
	}
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "a skill",
			input: "/review the diff",
			want:  []string{"## Skill: review\n\nRead the diff.", "/review the diff"},
		},
		{
			name:  "the body of the skill tool",
			input: "/deploy",
			want:  []string{"## Skill: deploy\n\nRun scripts/run.sh.\n\nSupport files of this skill. To read one, call this tool with the file input:\n\n- scripts/run.sh", "/deploy"},
		},
		{
			name:  "a skill that the model cannot call",
			input: "/lw-tasks File a task for each decision",
			want:  []string{"## Skill: lw-tasks\n\nFile one task.", "/lw-tasks File a task for each decision"},
		},
		{
			name:  "no skill",
			input: "/missing now",
			want:  []string{"/missing now"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &script{responses: []ai.Message{reply("done")}}
			h := harness.New(with(base(t, p), harness.Skillset(deploy, review, userOnly))...)
			a, err := h.New(context.Background())
			require.NoError(t, err)

			run(t, a, tt.input)

			assert.Equal(t, tt.want, lastUser(t, p))
			var logged []string
			for _, m := range a.Log().Messages() {
				if m.Role == ai.RoleUser {
					logged = append(logged, m.Text())
				}
			}
			assert.Equal(t, []string{tt.input}, logged, "the log keeps the text of the user")
		})
	}
}
