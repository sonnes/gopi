package landing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

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

// ClaudeCode is the preview of the harness band: a harness with the
// parts of Claude Code. Each option is one part, so a caller can change
// one part and keep the others.
func ClaudeCode(
	ctx context.Context,
	cat *catalog.Catalog,
	home string,
	repo string,
	github agent.MCPServer,
	deploy tool.Tool,
	guard agent.BeforeToolFunc,
	ask agent.ApproveFunc,
) (*agent.Agent, error) {
	// snippet:harness
	store, err := storefs.Sessions(filepath.Join(home, ".gopi"))
	if err != nil {
		return nil, err
	}
	md := storefs.InstructionFiles("CLAUDE.md", "AGENTS.md")

	h := harness.New(
		harness.Catalog(cat),
		harness.Model("anthropic/claude-opus-5-5"),
		harness.Store(store),

		harness.Defs(
			storefs.Dir(filepath.Join(home, ".claude"), md),
			storefs.Dir(filepath.Join(repo, ".claude"), md),
			storefs.Dir(repo, md),
		),

		harness.MCP(github),
		harness.Toolset(harness.Tools(deploy)),

		harness.Mode(permission.AcceptEdits),
		harness.Allow("bash(go test *)"),
		harness.Deny("bash(git push *)"),
		harness.Approve(ask),

		harness.AgentDefaults(
			agent.Thinking(ai.ThinkingHigh),
			agent.BeforeTool(guard),
		),
	)

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
