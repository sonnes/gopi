package harness_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/tool"
)

// bare returns the options of a harness for a test with no tool.
func bare() []harness.Option {
	return []harness.Option{
		harness.Catalog(newCatalog(&script{})),
		harness.Seed(harness.NoSeed),
	}
}

func infoNames(asm *harness.Assembly) []string {
	var names []string
	for _, info := range asm.Tools {
		names = append(names, info.Name)
	}
	return names
}

func TestAHarnessWithNoToolsetHasNoTool(t *testing.T) {
	c := config(t)
	c.Defs = def.Set{Skills: []def.Skill{review}}

	asm := assemblyOf(t, with(bare(), harness.Defs(def.Set{Agents: []def.Agent{reviewer}})), c)

	assert.Empty(t, asm.Tools)
}

func TestSkillToolAndAgentToolBindToTheSession(t *testing.T) {
	binders := harness.Toolset(harness.Tools(harness.SkillTool(), harness.AgentTool()))
	skills := []def.Skill{review}
	subagents := harness.Defs(def.Set{Agents: []def.Agent{reviewer}})
	tests := []struct {
		name   string
		skills []def.Skill
		opts   []harness.Option
		want   []string
	}{
		{name: "no skill and no subagent", want: nil},
		{name: "skills", skills: skills, want: []string{"skill"}},
		{name: "subagents", opts: []harness.Option{subagents}, want: []string{"agent"}},
		{
			name:   "both",
			skills: skills,
			opts:   []harness.Option{subagents},
			want:   []string{"skill", "agent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := with(bare(), binders)
			c := config(t)
			c.Defs = def.Set{Skills: tt.skills}
			asm := assemblyOf(t, with(opts, tt.opts...), c)

			assert.Equal(t, tt.want, infoNames(asm))
		})
	}
}

func TestUnboundToolsHaveTheirNames(t *testing.T) {
	assert.Equal(t, tool.Skill, harness.SkillTool().Info().Name)
	assert.Equal(t, tool.Agent, harness.AgentTool().Info().Name)
	assert.Equal(t, tool.ReadFile, harness.Sandboxed(read.New).Info().Name)
}

func TestSandboxedToolBindsToTheSandboxOfTheSession(t *testing.T) {
	dir := t.TempDir()
	var boxes []sandbox.Sandbox
	build := func(box sandbox.Sandbox) tool.Tool {
		boxes = append(boxes, box)
		return read.New(box)
	}
	opts := with(bare(), harness.Toolset(harness.Tools(harness.Sandboxed(build))))
	c := config(t)
	c.Dir = dir

	asm := assemblyOf(t, opts, c)

	assert.Equal(t, []string{tool.ReadFile}, infoNames(asm))
	require.NotEmpty(t, boxes)
	bound := boxes[len(boxes)-1]
	require.NotNil(t, bound)
	want, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	got, err := filepath.EvalSymlinks(bound.Dir())
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// boundSandbox returns the sandbox that the harness of opts binds to a
// tool of [harness.Sandboxed] for c.
func boundSandbox(t *testing.T, opts []harness.Option, c agent.Config) sandbox.Sandbox {
	t.Helper()
	var boxes []sandbox.Sandbox
	build := func(box sandbox.Sandbox) tool.Tool {
		boxes = append(boxes, box)
		return read.New(box)
	}
	assemblyOf(t, with(opts, harness.Toolset(harness.Tools(harness.Sandboxed(build)))), c)
	require.NotEmpty(t, boxes)
	bound := boxes[len(boxes)-1]
	require.NotNil(t, bound)
	return bound
}

// needFence skips t on an OS with no fence.
func needFence(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the fence runs only on macOS")
	}
}

// inDir returns the config of a test in dir.
func inDir(t *testing.T, dir string) agent.Config {
	t.Helper()
	c := config(t)
	c.Dir = dir
	return c
}

// gitHook makes a folder with a .git folder, and returns the folder and
// the path of a hook in it. The fence keeps .git/hooks read-only.
func gitHook(t *testing.T) (dir, hook string) {
	t.Helper()
	dir = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755))
	return dir, filepath.Join(dir, ".git", "hooks", "pre-commit")
}

func TestTheDefaultSandboxHasNoFence(t *testing.T) {
	dir, hook := gitHook(t)
	box := boundSandbox(t, bare(), inDir(t, dir))

	err := box.WriteFile(context.Background(), hook, []byte("x"))

	require.NoError(t, err)
	assert.FileExists(t, hook)
}

func TestFenceKeepsTheGitHooks(t *testing.T) {
	needFence(t)
	dir, hook := gitHook(t)
	box := boundSandbox(t, with(bare(), harness.Fence()), inDir(t, dir))

	err := box.WriteFile(context.Background(), hook, []byte("x"))

	require.Error(t, err)
	assert.NoFileExists(t, hook)
}

func TestFenceWritesInEachAddDir(t *testing.T) {
	needFence(t)
	extra := t.TempDir()
	c := config(t)
	c.AddDirs = []string{extra}
	box := boundSandbox(t, with(bare(), harness.Fence()), c)

	err := box.WriteFile(context.Background(), filepath.Join(extra, "a.txt"), []byte("x"))

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(extra, "a.txt"))
}

func TestFenceAddsItsOptionsToTheWorkspacePolicy(t *testing.T) {
	needFence(t)
	hidden := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(hidden, []byte("x"), 0o600))
	box := boundSandbox(t, with(bare(), harness.Fence(fence.Hide(hidden))), config(t))

	_, err := box.ReadFile(context.Background(), hidden)

	require.Error(t, err)
}

func TestFenceFailsOnAnOSWithNoFence(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has a fence")
	}
	opts := with(bare(),
		harness.Fence(),
		harness.Toolset(harness.Tools(harness.Sandboxed(read.New))),
	)

	_, err := harness.New(opts...).New(context.Background(), config(t))

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Contains(t, err.Error(), "harness.Sandbox")
}

func TestTheHarnessSkipsANamedTool(t *testing.T) {
	opts := with(bare(), harness.Toolset(harness.Tools(
		tool.Named(tool.ApplyPatch),
		tool.Func("lookup", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
			return ai.ToolResult{}, nil
		}),
	)))

	asm := assemblyOf(t, opts, config(t))

	assert.Equal(t, []string{"lookup"}, infoNames(asm))
}

// onlyNames returns the config of a test with the tool names names.
func onlyNames(t *testing.T, names ...string) agent.Config {
	t.Helper()
	c := config(t)
	c.ToolNames = names
	return c
}

func TestToolNamesFailOnANameOutsideTheTools(t *testing.T) {
	h := harness.New(with(bare(), harness.Toolset(harness.Tools(tool.Func("lookup", nil))))...)

	_, err := h.New(context.Background(), onlyNames(t, "lookup2"))

	assert.ErrorIs(t, err, tool.ErrUnknownTool)
}

func TestToolNamesAreReadBeforeBinding(t *testing.T) {
	opts := with(bare(), harness.Toolset(harness.Tools(
		harness.SkillTool(),
		harness.AgentTool(),
		tool.Named(tool.WebFetch),
		echo,
	)))

	asm := assemblyOf(t, opts, onlyNames(t, "skill", "agent", "WebFetch", "echo"))

	assert.Equal(t, []string{"echo"}, infoNames(asm), "no skill, no subagent, and a named tool bind to no tool")
}

func TestToolNamesBindNoToolThatTheyLeaveOut(t *testing.T) {
	var built int
	build := func(box sandbox.Sandbox) tool.Tool {
		if box != nil {
			built++
		}
		return read.New(box)
	}
	opts := with(bare(), harness.Toolset(harness.Tools(harness.Sandboxed(build), echo)))

	asm := assemblyOf(t, opts, onlyNames(t, "echo"))

	assert.Equal(t, []string{"echo"}, infoNames(asm))
	assert.Zero(t, built, "the harness makes no sandbox for a tool that ToolNames leaves out")
}

func TestCodingGivesTheCodingTools(t *testing.T) {
	var names []string
	for _, tl := range harness.Coding() {
		names = append(names, tl.Info().Name)
	}

	assert.Equal(t, []string{"read_file", "write_file", "edit_file", "bash", "grep", "find", "todo_write"}, names)
}

func TestADenyRuleWithNoContentRemovesTheTool(t *testing.T) {
	tests := []struct {
		name string
		rule string
		want []string
	}{
		{name: "no content", rule: "bash", want: []string{"other"}},
		{name: "Claude Code name", rule: "Bash", want: []string{"other"}},
		{name: "content", rule: "bash(git push *)", want: []string{"bash", "other"}},
		{name: "prefix", rule: "mcp__*", want: []string{"bash", "other"}},
	}
	bashTool := tool.Func(tool.Bash, func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
		return ai.ToolResult{}, nil
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := with(bare(), harness.Toolset(harness.Tools(bashTool, otherTool)))
			c := config(t)
			c.Deny = []string{tt.rule}

			asm := assemblyOf(t, opts, c)

			assert.Equal(t, tt.want, infoNames(asm))
		})
	}
}
