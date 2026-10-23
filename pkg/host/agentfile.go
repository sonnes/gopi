package host

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
)

// inheritModel is the model of an agent file that uses the model of
// the spec.
const inheritModel = "inherit"

// SessionAgent is the session value that keeps the agent file that a
// session started with. [Host.Start] writes it once, when it starts a
// session with [Spec.Agent]. A resume reads the value and not the file,
// so an edit of the file changes only the sessions that start after it.
var SessionAgent = session.Value[PinnedAgent]{
	Namespace: "gopi.host",
	Key:       "agent",
}

// PinnedAgent is the part of an agent file that a session keeps. The
// dirs, the rules, and the approve function come from the [Spec] on
// each start.
type PinnedAgent struct {
	// Name is the name of the agent.
	Name string `json:"name"`
	// Path is the place of the file that won.
	Path string `json:"path,omitempty"`
	// Model is the model spec of the file, or "" for the model of the
	// spec.
	Model string `json:"model,omitempty"`
	// Effort is the thinking level of the file.
	Effort ai.ThinkingLevel `json:"effort,omitempty"`
	// Mode is the permission mode of the file.
	Mode string `json:"mode,omitempty"`
	// Prompt is the text of the file after the frontmatter.
	Prompt string `json:"prompt,omitempty"`
	// Disallowed lists the tools that the agent cannot use.
	Disallowed []string `json:"disallowed,omitempty"`
	// Skills names the skills that the agent loads at the start.
	Skills []string `json:"skills,omitempty"`
}

// checkEffort returns an error if the effort of spec is not a thinking
// level, so that a typo in an agent file fails the start and not the
// first model call.
func checkEffort(spec Spec) error {
	if spec.Effort == "" {
		return nil
	}
	_, err := ai.ParseThinking(string(spec.Effort))
	return err
}

// withAgent fills spec from its agent. A resumed session with a
// [SessionAgent] value runs that agent, and withAgent returns no new
// pin. Otherwise withAgent reads the file of spec.Agent from the dirs,
// and returns the pin that the session must keep. A spec with no agent
// stays as it is.
func withAgent(
	ctx context.Context,
	spec Spec,
	snap session.Snapshot,
	resumed bool,
) (Spec, *PinnedAgent, error) {
	if resumed {
		pinned, ok, err := SessionAgent.Read(snap)
		if err != nil {
			return spec, nil, err
		}
		if ok {
			isOther := spec.Agent != "" && spec.Agent != pinned.Name
			if isOther {
				return spec, nil, fmt.Errorf("the session started with the agent %q, not %q", pinned.Name, spec.Agent)
			}
			return pinned.apply(spec), nil, nil
		}
	}
	if spec.Agent == "" {
		return spec, nil, nil
	}

	readers := make([]def.AgentsReader, len(spec.Dirs))
	for i, d := range spec.Dirs {
		readers[i] = storefs.Dir(d)
	}
	a, err := def.FindAgent(ctx, spec.Agent, readers...)
	if err != nil {
		return spec, nil, err
	}
	pinned := pin(a)
	return pinned.apply(spec), &pinned, nil
}

// keepAgent writes p as the [SessionAgent] value of the session id. If
// the store does not have the session, keepAgent creates it.
func keepAgent(ctx context.Context, store session.Storer, id string, p PinnedAgent) error {
	log, err := session.Open(ctx, store, id)
	if err != nil {
		return err
	}
	return SessionAgent.Set(ctx, log, p)
}

// pin returns the part of a that a session keeps.
func pin(a def.Agent) PinnedAgent {
	model := a.Model
	if strings.EqualFold(model, inheritModel) {
		model = ""
	}
	return PinnedAgent{
		Name:       a.Name,
		Path:       a.Path,
		Model:      model,
		Effort:     a.Effort,
		Mode:       a.Mode,
		Prompt:     a.Prompt,
		Disallowed: a.Disallowed,
		Skills:     a.Skills,
	}
}

// apply returns spec with the fields of p. A field that the spec sets
// wins over the file.
func (p PinnedAgent) apply(spec Spec) Spec {
	spec.Model = cmp.Or(spec.Model, p.Model)
	spec.Mode = cmp.Or(spec.Mode, agent.Mode(p.Mode))
	spec.Effort = cmp.Or(spec.Effort, p.Effort)
	spec.System = joinText(p.Prompt, spec.System)
	spec.Deny = append(slices.Clip(spec.Deny), p.Disallowed...)
	spec.Skills = append(slices.Clip(spec.Skills), p.Skills...)
	return spec
}

// joinText returns the texts that are not empty, joined by a blank
// line.
func joinText(texts ...string) string {
	var parts []string
	for _, t := range texts {
		if t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, "\n\n")
}
