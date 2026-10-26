package harness

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/session"
)

// PromptBuilder builds the sections of the system prompt from an
// [Assembly]. The harness gives it to the agent through
// [agent.System], so the agent calls it before each model call. If a
// section changes, the log gets a section change, and the start of the
// request stays the same. The harness puts [Assembly.System] first, in
// the section named [SystemSection]. A builder does not repeat it.
//
// An error from the builder fails the run before the model call. It
// does not fail [Harness.New] or [Harness.Durable].
type PromptBuilder func(ctx context.Context, asm *Assembly) ([]ai.Section, error)

// Seeder builds the first entries of a new session from an [Assembly].
// The harness calls it only when the log has no entries, and
// appends the entries before it returns the agent. A seeder sets the
// author of its entries, so that a reader can find them and hide them.
type Seeder func(ctx context.Context, asm *Assembly) ([]session.Entry, error)

// SeedAuthor is the author of the entry of [DefaultSeed]. A caller
// that writes a seeder can use it too.
const SeedAuthor = "harness"

// SystemSection is the name of the section that holds the text of
// [System]. It is the first section of the prompt.
const SystemSection = agent.SystemSection

// The names of the sections of [DefaultPrompt].
const (
	instructionsSection = "instructions"
	skillsSection       = "skills"
	agentsSection       = "agents"
)

// skillsIntro tells the model how to use the skill list.
const skillsIntro = "A skill is a set of instructions that you load with the `skill` tool. " +
	"Before you start a task that matches a skill, load the skill."

// agentsIntro tells the model how to use the subagent list.
const agentsIntro = "A subagent does a task in its own context with the `agent` tool, " +
	"and returns its last message. Give a task to a subagent when its description matches the task."

// DefaultPrompt is the prompt builder that the harness uses when none
// is set. It returns up to three sections, in this order:
//
//   - "instructions": the text of each instruction, in the order of the readers.
//   - "skills": the name and the description of each skill that the
//     model can call, not the body.
//   - "agents": the name and the description of each subagent.
//
// It leaves out a section with nothing to list. DefaultPrompt writes no
// fact that changes between sessions, such as the date, so a provider
// can cache the prompt. Those facts go in the seed.
func DefaultPrompt(_ context.Context, asm *Assembly) ([]ai.Section, error) {
	var out []ai.Section
	if len(asm.Instructions) > 0 {
		out = append(out, ai.Section{Name: instructionsSection, Text: instructionsText(asm.Instructions)})
	}

	var skills []string
	for i, s := range asm.Skills {
		if s.NoModelCall {
			continue
		}
		skills = append(skills, "- "+asm.SkillName(i)+": "+s.Description)
	}
	if len(skills) > 0 {
		text := strings.Join([]string{"## Skills", skillsIntro, strings.Join(skills, "\n")}, "\n\n")
		out = append(out, ai.Section{Name: skillsSection, Text: text})
	}

	if asm.agentTool() {
		var agents []string
		for _, a := range asm.Agents {
			agents = append(agents, "- "+a.Name+": "+a.Description)
		}
		text := strings.Join([]string{"## Subagents", agentsIntro, strings.Join(agents, "\n")}, "\n\n")
		out = append(out, ai.Section{Name: agentsSection, Text: text})
	}
	return out, nil
}

// instructionsText returns the instruction files as one text, each
// with its path.
func instructionsText(files []def.Instruction) string {
	parts := make([]string, len(files))
	for i, f := range files {
		parts[i] = fmt.Sprintf("Contents of %s:\n\n%s", f.Path, strings.TrimSpace(f.Text))
	}
	return strings.Join(parts, "\n\n")
}

// DefaultSeed is the seeder that the harness uses when none is set. It
// makes one entry with the working directory, the platform, and the
// date. These facts are true when the session starts, so they go
// into the log once, and not into the system prompt.
func DefaultSeed(_ context.Context, asm *Assembly) ([]session.Entry, error) {
	text := fmt.Sprintf(
		"<environment>\nWorking directory: %s\nPlatform: %s/%s\nDate: %s\n</environment>",
		asm.Dir,
		runtime.GOOS,
		runtime.GOARCH,
		time.Now().Format(time.DateOnly),
	)
	return []session.Entry{session.Text(text, session.Author(SeedAuthor))}, nil
}

// NoSeed is the seeder that makes no entries. Give it to [Seed] to
// start a session with an empty log.
func NoSeed(context.Context, *Assembly) ([]session.Entry, error) {
	return nil, nil
}
