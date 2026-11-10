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
	"github.com/sonnes/gopi/pkg/def"
	"github.com/sonnes/gopi/pkg/harness"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/permission"
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
		agent.Hooks(
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
		),
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

// CodingAgent is the preview of the harness band: a coding agent from
// the AGENTS.md files and the definitions of its folders, and the
// config of one session. Each option is one part, so a caller can
// change one part and keep the others.
func CodingAgent(
	ctx context.Context,
	cat *catalog.Catalog,
	home string,
	repo string,
	github agent.MCPServer,
	deploy tool.Tool,
	guard agent.BeforeToolFunc,
	ask agent.ApproveFunc,
) (agent.Runner, error) {
	// snippet:harness
	store, err := storefs.Sessions(filepath.Join(home, ".gopi"))
	if err != nil {
		return nil, err
	}

	h := harness.New(
		harness.Catalog(cat),
		harness.Defs(def.Layers(
			storefs.Dir(filepath.Join(home, ".agents")),
			storefs.Dir(filepath.Join(repo, ".agents")),
			storefs.Dir(repo),
		)),
		harness.Toolset(harness.Tools(deploy)),
		harness.Allow("bash(go test *)"),
	)

	a, err := h.Start(ctx, agent.Config{
		SessionID: "fix-login",
		Store:     store,
		Spec:      "openrouter/z-ai/glm-5.3",
		Dir:       repo,
		Effort:    ai.ThinkingHigh,
		Mode:      permission.AcceptEdits,
		Deny:      []string{"bash(git push *)"},
		Approve:   ask,
		Hooks:     []agent.Hook{agent.BeforeTool(guard)},
		MCP:       []agent.MCPServer{github},
	})
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
