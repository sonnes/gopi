package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/harness/permission"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/session"
	storefs "github.com/sonnes/gopi/pkg/store/fs"
	"github.com/sonnes/gopi/pkg/tool"
)

// WeatherIn is the input of the weather tool. tool.New derives the JSON
// schema of the tool from it.
type WeatherIn struct {
	City string `json:"city" jsonschema:"the name of the city"`
}

// Tools is the preview of the tools tab: a Go function becomes a tool
// of an agent.
func Tools(model ai.LanguageModel, forecast func(string) string) *agent.Agent {
	// snippet:tools
	weather := tool.New("get_weather",
		func(ctx context.Context, in WeatherIn) (string, error) {
			return forecast(in.City), nil
		},
		tool.Description("Get the weather for a city"),
	)

	a := agent.New(model, agent.Toolset(weather))
	// end
	return a
}

// Hooks is the preview of the hooks tab: a BeforeStop hook keeps the
// run going until the agent posts its summary, and a BeforeTool hook
// blocks one tool.
func Hooks(ctx context.Context, model ai.LanguageModel, tools []tool.Tool) *agent.Agent {
	// snippet:hooks
	a := agent.New(model,
		agent.Toolset(tools...),
		agent.BeforeTool(func(
			ctx context.Context, s agent.State, c ai.ToolCall,
		) (ai.ToolCall, error) {
			if c.Name == "close_issue" {
				return c, agent.Deny("ask a maintainer to close issues")
			}
			return c, nil
		}),
		agent.BeforeStop(func(
			ctx context.Context, s agent.State,
		) ([]session.Entry, error) {
			if posted(s.Messages()) {
				return nil, nil
			}
			return []session.Entry{session.Text("Post the summary.")}, nil
		}),
	)
	// end
	return a
}

// posted reports whether the agent called post_summary.
func posted(msgs []ai.Message) bool {
	for _, m := range msgs {
		for _, call := range m.ToolCalls() {
			if call.Name == "post_summary" {
				return true
			}
		}
	}
	return false
}

// Harness is the preview of the harness band: one harness turns the
// folder of a repository into a coding agent, with its instructions,
// skills, subagents, tools, MCP servers, sandbox, and permission rules.
func Harness(
	ctx context.Context,
	cat *catalog.Catalog,
	store session.Storer,
	repo string,
	github agent.MCPServer,
) (*agent.Agent, error) {
	// snippet:harness
	h := harness.New(
		harness.Catalog(cat),
		harness.Model("anthropic/claude-opus-5-5"),
		harness.Store(store),
		// AGENTS.md, skills/, and agents/ of the repository.
		harness.Defs(storefs.Dir(repo)),
		harness.MCP(github),
		harness.Mode(permission.AcceptEdits),
		harness.Deny("bash(git push *)"),
	)

	// Each start reads the folder again, so an edit reaches the next agent.
	a, err := h.Durable(ctx, "fix-login", harness.Dir(repo))
	// end
	return a, err
}

// Events is the preview of the events tab: a web handler streams the
// agent events of one session to a page.
func Events(h *host.Host) http.HandlerFunc {
	// snippet:events
	return func(w http.ResponseWriter, r *http.Request) {
		_, events, err := h.Stream(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		for e, err := range events {
			if err != nil {
				return
			}
			data, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}
	// end
}
