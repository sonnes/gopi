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

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/sandbox"
	"github.com/sonnes/gopi/pkg/sandbox/fence"
	"github.com/sonnes/gopi/pkg/sandbox/read"
	"github.com/sonnes/gopi/pkg/tool"
)

// bare returns the options of a harness for a test with no tool.
func bare(t *testing.T) []harness.Option {
	t.Helper()
	return []harness.Option{
		harness.Catalog(newCatalog(&script{})),
		harness.Model("fake/one"),
		harness.Seed(harness.NoSeed),
		harness.Dir(t.TempDir()),
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
	asm := assemblyOf(t, with(bare(t),
		harness.Skillset(review),
		harness.Agents(reviewer),
	))

	assert.Empty(t, asm.Tools)
}

func TestSkillToolAndAgentToolBindToTheSession(t *testing.T) {
	binders := harness.Toolset(harness.Tools(harness.SkillTool(), harness.AgentTool()))
	tests := []struct {
		name string
		opts []harness.Option
		want []string
	}{
		{name: "no skill and no subagent", want: nil},
		{name: "skills", opts: []harness.Option{harness.Skillset(review)}, want: []string{"skill"}},
		{name: "subagents", opts: []harness.Option{harness.Agents(reviewer)}, want: []string{"agent"}},
		{
			name: "both",
			opts: []harness.Option{harness.Skillset(review), harness.Agents(reviewer)},
			want: []string{"skill", "agent"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := with(bare(t), binders)
			asm := assemblyOf(t, with(opts, tt.opts...))

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
	opts := with(bare(t),
		harness.Dir(dir),
		harness.Toolset(harness.Tools(harness.Sandboxed(build))),
	)

	asm := assemblyOf(t, opts)

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
// tool of [harness.Sandboxed].
func boundSandbox(t *testing.T, opts []harness.Option) sandbox.Sandbox {
	t.Helper()
	var boxes []sandbox.Sandbox
	build := func(box sandbox.Sandbox) tool.Tool {
		boxes = append(boxes, box)
		return read.New(box)
	}
	assemblyOf(t, with(opts, harness.Toolset(harness.Tools(harness.Sandboxed(build)))))
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

// secretInHome makes a home folder with a key in ~/.ssh, and returns
// the path of the key.
func secretInHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	key := filepath.Join(home, ".ssh", "id_ed25519")
	require.NoError(t, os.MkdirAll(filepath.Dir(key), 0o700))
	require.NoError(t, os.WriteFile(key, []byte("s3cret"), 0o600))
	return key
}

func TestTheDefaultSandboxHasNoFence(t *testing.T) {
	key := secretInHome(t)
	box := boundSandbox(t, bare(t))

	got, err := box.ReadFile(context.Background(), key)

	require.NoError(t, err)
	assert.Equal(t, "s3cret", string(got))
}

func TestFenceHidesTheCredentials(t *testing.T) {
	needFence(t)
	key := secretInHome(t)
	box := boundSandbox(t, with(bare(t), harness.Fence()))

	_, err := box.ReadFile(context.Background(), key)

	require.Error(t, err)
}

func TestFenceWritesInEachAddDir(t *testing.T) {
	needFence(t)
	extra := t.TempDir()
	box := boundSandbox(t, with(bare(t), harness.AddDirs(extra), harness.Fence()))

	err := box.WriteFile(context.Background(), filepath.Join(extra, "a.txt"), []byte("x"))

	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(extra, "a.txt"))
}

func TestFenceAddsItsOptionsToTheWorkspacePolicy(t *testing.T) {
	needFence(t)
	hidden := filepath.Join(t.TempDir(), "notes.txt")
	require.NoError(t, os.WriteFile(hidden, []byte("x"), 0o600))
	box := boundSandbox(t, with(bare(t), harness.Fence(fence.Hide(hidden))))

	_, err := box.ReadFile(context.Background(), hidden)

	require.Error(t, err)
}

func TestFenceFailsOnAnOSWithNoFence(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("macOS has a fence")
	}
	opts := with(bare(t),
		harness.Fence(),
		harness.Toolset(harness.Tools(harness.Sandboxed(read.New))),
	)

	_, err := harness.New(opts...).New(context.Background())

	require.ErrorIs(t, err, errors.ErrUnsupported)
	assert.Contains(t, err.Error(), "harness.Sandbox")
}

func TestTheHarnessSkipsANamedTool(t *testing.T) {
	opts := with(bare(t), harness.Toolset(harness.Tools(
		tool.Named(tool.ApplyPatch),
		tool.Func("lookup", func(context.Context, ai.ToolCall) (ai.ToolResult, error) {
			return ai.ToolResult{}, nil
		}),
	)))

	asm := assemblyOf(t, opts)

	assert.Equal(t, []string{"lookup"}, infoNames(asm))
}

func TestOnlyFailsOnANameOutsideTheTools(t *testing.T) {
	opts := with(bare(t),
		harness.Toolset(harness.Tools(tool.Func("lookup", nil))),
		harness.Only("lookup2"),
		harness.Seed(harness.NoSeed),
	)
	h := harness.New(opts...)

	_, err := h.New(context.Background())

	assert.ErrorIs(t, err, tool.ErrUnknownTool)
}

func TestOnlyReadsTheNamesOfTheListBeforeBinding(t *testing.T) {
	opts := with(bare(t),
		harness.Toolset(harness.Tools(
			harness.SkillTool(),
			harness.AgentTool(),
			tool.Named(tool.WebFetch),
			echo,
		)),
		harness.Only("skill", "agent", "WebFetch", "echo"),
	)

	asm := assemblyOf(t, opts)

	assert.Equal(t, []string{"echo"}, infoNames(asm), "no skill, no subagent, and a named tool bind to no tool")
}

func TestOnlyBindsNoToolThatItLeavesOut(t *testing.T) {
	var built int
	build := func(box sandbox.Sandbox) tool.Tool {
		if box != nil {
			built++
		}
		return read.New(box)
	}
	opts := with(bare(t),
		harness.Toolset(harness.Tools(harness.Sandboxed(build), echo)),
		harness.Only("echo"),
	)

	asm := assemblyOf(t, opts)

	assert.Equal(t, []string{"echo"}, infoNames(asm))
	assert.Zero(t, built, "the harness makes no sandbox for a tool that Only leaves out")
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
			opts := with(bare(t),
				harness.Toolset(harness.Tools(bashTool, otherTool)),
				harness.Deny(tt.rule),
			)

			asm := assemblyOf(t, opts)

			assert.Equal(t, tt.want, infoNames(asm))
		})
	}
}
