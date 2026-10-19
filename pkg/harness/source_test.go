package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/skill"
)

// layout is a project in a git repository and a home folder, in
// temporary folders.
type layout struct {
	root string
	home string
}

func newLayout(t *testing.T) layout {
	t.Helper()
	l := layout{root: t.TempDir(), home: t.TempDir()}
	require.NoError(t, os.Mkdir(filepath.Join(l.root, ".git"), 0o755))
	return l
}

// opts returns the options of a harness that works in the folder dir of
// the project.
func (l layout) opts(t *testing.T, dir string) []harness.Option {
	t.Helper()
	return with(base(t, &script{}),
		harness.Dir(filepath.Join(l.root, dir)),
		harness.Home(l.home),
		harness.Sources(harness.Project, harness.User),
	)
}

// listed returns the names that the skills section and the skill tool
// give to the skills of asm.
func listed(asm *harness.Assembly) []string {
	var out []string
	for i := range asm.Skills {
		out = append(out, asm.SkillName(i))
	}
	return out
}

func TestSourcesReadEveryLayout(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.home, ".codex/skills/a/SKILL.md", skillFile("A.", "a"))
	writeFile(t, l.home, ".claude/skills/b/SKILL.md", skillFile("B.", "b"))
	writeFile(t, l.home, ".agents/skills/c/SKILL.md", skillFile("C.", "c"))
	writeFile(t, l.home, ".gopi/skills/d/SKILL.md", skillFile("D.", "d"))
	writeFile(t, l.root, ".codex/skills/e/SKILL.md", skillFile("E.", "e"))
	writeFile(t, l.root, ".claude/skills/f/SKILL.md", skillFile("F.", "f"))
	writeFile(t, l.root, ".agents/skills/g/SKILL.md", skillFile("G.", "g"))

	asm := assemblyOf(t, l.opts(t, ""))

	assert.Equal(t, []string{"a", "b", "c", "d", "e", "f", "g"}, listed(asm))
}

func TestSourcesDefaultToProjectAndUser(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.home, ".claude/skills/a/SKILL.md", skillFile("A.", "a"))
	writeFile(t, l.root, ".claude/skills/b/SKILL.md", skillFile("B.", "b"))
	opts := with(base(t, &script{}), harness.Dir(l.root), harness.Home(l.home))

	asm := assemblyOf(t, opts)

	assert.Equal(t, []string{"a", "b"}, listed(asm))
}

func TestSourcesWithNoSourceReadNoFile(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.root, ".claude/skills/b/SKILL.md", skillFile("B.", "b"))
	writeFile(t, l.root, "AGENTS.md", "Use tabs.")

	asm := assemblyOf(t, with(l.opts(t, ""), harness.Sources()))

	assert.Empty(t, asm.Skills)
	assert.Empty(t, asm.Instructions)
}

func TestAProjectSkillWinsTheBareName(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.home, ".claude/skills/deploy/SKILL.md", skillFile("Home deploy.", "home"))
	writeFile(t, l.root, ".claude/skills/deploy/SKILL.md", skillFile("Project deploy.", "project"))
	writeFile(t, l.root, ".agents/skills/deploy/SKILL.md", skillFile("Agents deploy.", "agents"))

	asm := assemblyOf(t, l.opts(t, ""))

	require.Len(t, asm.Skills, 2, ".agents replaces .claude in one folder")
	assert.Equal(t, []string{"~:deploy", "deploy"}, listed(asm))
	assert.Equal(t, "Agents deploy.", asm.Skills[1].Description)
}

func TestASkillInASubfolderHasItsPathAsAQualifier(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.root, ".claude/skills/deploy/SKILL.md", skillFile("Root deploy.", "root"))
	writeFile(t, l.root, "apps/web/.claude/skills/deploy/SKILL.md", skillFile("Web deploy.", "web"))
	writeFile(t, l.root, "apps/web/.claude/skills/lint/SKILL.md", skillFile("Web lint.", "lint"))
	writeFile(t, l.root, "node_modules/x/.claude/skills/bad/SKILL.md", skillFile("Bad.", "bad"))

	asm := assemblyOf(t, l.opts(t, ""))

	assert.Equal(t, []string{"apps/web:deploy", "lint", "deploy"}, listed(asm))
	assert.Equal(t, "apps/web", asm.Skills[0].Qualifier)
}

func TestTheFolderOfTheAgentWinsOverItsParents(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.root, ".claude/skills/deploy/SKILL.md", skillFile("Root deploy.", "root"))
	writeFile(t, l.root, "apps/web/.claude/skills/deploy/SKILL.md", skillFile("Web deploy.", "web"))

	asm := assemblyOf(t, l.opts(t, "apps/web"))

	assert.Equal(t, []string{".:deploy", "deploy"}, listed(asm))
	assert.Equal(t, "Web deploy.", asm.Skills[1].Description)
}

func TestASkillInCodeWinsOverTheFiles(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.root, ".claude/skills/deploy/SKILL.md", skillFile("File deploy.", "file"))

	asm := assemblyOf(t, with(l.opts(t, ""), harness.Skillset(deploy)))

	assert.Equal(t, []string{".:deploy", "deploy"}, listed(asm))
	assert.Equal(t, "Deploy the service.", asm.Skills[1].Description)
}

func TestTheSkillToolTakesTheQualifiedName(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.home, ".claude/skills/deploy/SKILL.md", skillFile("Home deploy.", "home body"))
	writeFile(t, l.root, ".claude/skills/deploy/SKILL.md", skillFile("Project deploy.", "project body"))
	p := &script{responses: []ai.Message{
		callTool("c1", "skill", `{"name":"~:deploy"}`),
		reply("done"),
	}}
	opts := with(l.opts(t, ""), harness.Catalog(newCatalog(p)))

	a, err := harness.New(opts...).New(context.Background())
	require.NoError(t, err)
	run(t, a, "deploy it")

	assert.Equal(t, "home body", toolResult(t, a).Text())
}

func TestASkillThatTheModelCannotCallIsNotListed(t *testing.T) {
	hidden := skill.Skill{Name: "release", Description: "Cut a release.", Body: "x", NoModelCall: true}

	asm := assemblyOf(t, with(base(t, &script{}), harness.Skillset(deploy, hidden)))

	require.Len(t, asm.Tools, 1)
	assert.Equal(t, []any{"deploy"}, asm.Tools[0].InputSchema.Properties["name"].Enum)
}

func TestInstructionsGoRootFirst(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.home, ".gopi/AGENTS.md", "home rule")
	writeFile(t, l.root, "CLAUDE.md", "root claude rule")
	writeFile(t, l.root, "AGENTS.md", "root agents rule")
	writeFile(t, l.root, "apps/web/AGENTS.md", "web rule")
	writeFile(t, l.root, "apps/api/AGENTS.md", "api rule")
	p := &script{responses: []ai.Message{reply("ok")}}
	opts := with(l.opts(t, "apps/web"), harness.Catalog(newCatalog(p)))

	a, err := harness.New(opts...).New(context.Background())
	require.NoError(t, err)
	run(t, a, "hi")

	text := section(a, "instructions")
	assert.NotContains(t, text, "api rule", "a sibling folder does not apply")
	order := []string{"home rule", "root claude rule", "root agents rule", "web rule"}
	last := -1
	for _, s := range order {
		at := strings.Index(text, s)
		require.GreaterOrEqual(t, at, 0, "the section holds %q", s)
		assert.Greater(t, at, last, "%q comes after the text before it", s)
		last = at
	}
}

func TestAnInstructionEditReachesTheNextCall(t *testing.T) {
	l := newLayout(t)
	writeFile(t, l.root, "AGENTS.md", "old rule")
	p := &script{responses: []ai.Message{reply("a"), reply("b")}}
	h := harness.New(with(l.opts(t, ""), harness.Catalog(newCatalog(p)))...)

	a, err := h.Durable(context.Background(), "s1")
	require.NoError(t, err)
	run(t, a, "first")
	writeFile(t, l.root, "AGENTS.md", "new rule")
	a, err = h.Durable(context.Background(), "s1")
	require.NoError(t, err)
	run(t, a, "second")

	assert.Contains(t, section(a, "instructions"), "new rule")
}
