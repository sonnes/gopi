package harness_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// listed returns the names that the skills section and the skill tool
// give to the skills of asm.
func listed(asm *harness.Assembly) []string {
	var out []string
	for i := range asm.Skills {
		out = append(out, asm.SkillName(i))
	}
	return out
}

func TestDefsGiveAgentsSkillsAndInstructions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skills/deploy/SKILL.md", skillFile("Deploy.", "body"))
	writeFile(t, dir, "agents/reviewer.md", "---\nname: reviewer\ndescription: Review a diff.\n---\nRead it.\n")
	writeFile(t, dir, "AGENTS.md", "Use tabs.")

	asm := assemblyOf(t, with(base(&script{}), harness.Defs(storefs.Dir(dir))), config(t))

	assert.Equal(t, []string{"deploy"}, listed(asm))
	require.Len(t, asm.Agents, 1)
	assert.Equal(t, "reviewer", asm.Agents[0].Name)
	require.Len(t, asm.Instructions, 1)
	assert.Equal(t, "Use tabs.", asm.Instructions[0].Text)
}

func TestWithoutDefsTheHarnessReadsNoFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".claude/skills/b/SKILL.md", skillFile("B.", "b"))
	writeFile(t, dir, "AGENTS.md", "Use tabs.")

	c := config(t)
	c.Dir = dir

	asm := assemblyOf(t, base(&script{}), c)

	assert.Empty(t, asm.Skills)
	assert.Empty(t, asm.Instructions)
}

func TestALaterDirWinsForASkillName(t *testing.T) {
	first := t.TempDir()
	later := t.TempDir()
	writeFile(t, first, "skills/deploy/SKILL.md", skillFile("First deploy.", "first"))
	writeFile(t, later, "skills/deploy/SKILL.md", skillFile("Later deploy.", "later"))

	asm := assemblyOf(
		t,
		with(base(&script{}), harness.Defs(def.Layers(storefs.Dir(first), storefs.Dir(later)))),
		config(t),
	)

	assert.Equal(t, []string{"deploy"}, listed(asm))
	assert.Equal(t, "Later deploy.", asm.Skills[0].Description)
}

func TestASkillInCodeAfterTheDefsWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "skills/deploy/SKILL.md", skillFile("File deploy.", "file"))

	c := config(t)
	c.Defs = def.Set{Skills: []def.Skill{deploy}}

	asm := assemblyOf(t, with(base(&script{}), harness.Defs(storefs.Dir(dir))), c)

	assert.Equal(t, []string{"deploy"}, listed(asm))
	assert.Equal(t, "Deploy the service.", asm.Skills[0].Description)
}

func TestTheSkillToolTakesTheQualifiedName(t *testing.T) {
	ops := def.Skill{
		Name:        "deploy",
		Qualifier:   "ops",
		Description: "Ops deploy.",
		Body:        "ops body",
	}
	p := &script{responses: []ai.Message{
		callTool("c1", "skill", `{"name":"ops:deploy"}`),
		reply("done"),
	}}
	c := config(t)
	c.Defs = def.Set{Skills: []def.Skill{ops, deploy}}

	a, err := harness.New(with(base(p), skillTool)...).New(context.Background(), c)
	require.NoError(t, err)
	run(t, a, "deploy it")

	assert.Equal(t, "ops body", toolResult(t, a).Text())
}

func TestASkillThatTheModelCannotCallIsNotListed(t *testing.T) {
	hidden := def.Skill{Name: "release", Description: "Cut a release.", Body: "x", NoModelCall: true}

	c := config(t)
	c.Defs = def.Set{Skills: []def.Skill{deploy, hidden}}

	asm := assemblyOf(t, with(base(&script{}), skillTool), c)

	require.Len(t, asm.Tools, 1)
	assert.Equal(t, []any{"deploy"}, asm.Tools[0].InputSchema.Properties["name"].Enum)
}

func TestInstructionsGoInTheOrderOfTheSources(t *testing.T) {
	first := t.TempDir()
	later := t.TempDir()
	writeFile(t, first, "AGENTS.md", "first rule")
	writeFile(t, later, "AGENTS.md", "later rule")
	p := &script{responses: []ai.Message{reply("ok")}}
	opts := with(base(p),
		harness.Defs(def.Layers(
			storefs.Dir(first),
			storefs.Dir(later),
			def.Set{Instructions: []def.Instruction{{Path: "code", Text: "code rule"}}},
		)),
	)

	a, err := harness.New(opts...).New(context.Background(), config(t))
	require.NoError(t, err)
	run(t, a, "hi")

	text := section(a, "instructions")
	last := -1
	for _, s := range []string{"first rule", "later rule", "code rule"} {
		at := strings.Index(text, s)
		require.GreaterOrEqual(t, at, 0, "the section holds %q", s)
		assert.Greater(t, at, last, "%q comes after the text before it", s)
		last = at
	}
}

func TestAnInstructionEditReachesTheNextCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "AGENTS.md", "old rule")
	p := &script{responses: []ai.Message{reply("a"), reply("b")}}
	h := harness.New(with(base(p), harness.Defs(storefs.Dir(dir)))...)

	c := config(t)

	run(t, start(t, h, c), "first")
	writeFile(t, dir, "AGENTS.md", "new rule")
	a := start(t, h, c)
	run(t, a, "second")

	assert.Contains(t, section(a, "instructions"), "new rule")
}
