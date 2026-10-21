// Package env holds the built-in detectors. Each provider detector
// reads the credential of one provider from an environment variable.
// Each driver detector looks for one agent CLI on the PATH.
//
// A caller must obey these rules:
//
//   - The chain reads the environment when [catalog.Catalog.Detect]
//     runs it, not when [Detectors] returns it.
//   - A provider SDK also reads its base URL from the environment, for
//     example ANTHROPIC_BASE_URL or OPENAI_BASE_URL. The openrouter and
//     fireworks providers set their own base URLs.
//   - To give another source priority, such as a stored login, put its
//     detector before the chain.
//   - The package is its own module, because it imports each provider
//     module. To register other providers only, import [catalog] and
//     not this package.
//
// The reasons are in docs/concepts/catalog/catalog.md.
package env

import (
	"os"
	"os/exec"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/claude"
	"github.com/sonnes/gopi/pkg/agent/codex"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/fireworks"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/ai/openrouter"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/catalog/models"
)

// Detectors returns the built-in chain, in precedence order:
//
//   - anthropic, from ANTHROPIC_API_KEY, with [anthropic.Models].
//   - openai, from OPENAI_API_KEY, with [openai.Models].
//   - openrouter, from OPENROUTER_API_KEY, with [openrouter.New] and
//     the models with tools of models.json. Call
//     [catalog.Catalog.FetchModels] to list the models of today.
//   - fireworks, from FIREWORKS_API_KEY, with [fireworks.New] and the
//     models of models.json, which come from models.dev.
//   - openaichat, from OPENAI_API_KEY. It registers no models, because
//     it serves any server at OPENAI_BASE_URL. Call
//     [catalog.Catalog.FetchModels] to list the models of the server.
func Detectors() []catalog.Detector {
	return []catalog.Detector{
		{
			Provider: "anthropic",
			Source:   "ANTHROPIC_API_KEY",
			Models:   anthropic.Models(),
			Detect: func() (ai.TextProvider, bool) {
				if os.Getenv("ANTHROPIC_API_KEY") == "" {
					return nil, false
				}
				return anthropic.New(), true
			},
		},
		{
			Provider: "openai",
			Source:   "OPENAI_API_KEY",
			Models:   openai.Models(),
			Detect: func() (ai.TextProvider, bool) {
				if os.Getenv("OPENAI_API_KEY") == "" {
					return nil, false
				}
				return openai.New(), true
			},
		},
		{
			Provider: "openrouter",
			Source:   "OPENROUTER_API_KEY",
			Models:   models.Of("openrouter"),
			Detect: func() (ai.TextProvider, bool) {
				key := os.Getenv("OPENROUTER_API_KEY")
				if key == "" {
					return nil, false
				}
				return openrouter.New(openrouter.APIKey(key)), true
			},
		},
		{
			Provider: "fireworks",
			Source:   "FIREWORKS_API_KEY",
			Models:   models.Of("fireworks"),
			Detect: func() (ai.TextProvider, bool) {
				key := os.Getenv("FIREWORKS_API_KEY")
				if key == "" {
					return nil, false
				}
				return fireworks.New(fireworks.APIKey(key)), true
			},
		},
		{
			Provider: "openaichat",
			Source:   "OPENAI_API_KEY",
			Detect: func() (ai.TextProvider, bool) {
				if os.Getenv("OPENAI_API_KEY") == "" {
					return nil, false
				}
				return openaichat.New(), true
			},
		},
	}
}

// DriverDetectors returns the built-in chain of drivers, in precedence
// order:
//
//   - claude-code, if claude is on the PATH, with [claude.Models].
//   - codex, if codex is on the PATH, with [codex.Models].
//
// A detector does not start the CLI, so it does not know if the CLI is
// logged in. Call [catalog.Catalog.FetchDriverModels] to list the
// models of the installed CLI.
func DriverDetectors() []catalog.DriverDetector {
	return []catalog.DriverDetector{
		{
			Provider: "claude-code",
			Source:   "claude on PATH",
			Models:   claude.Models(),
			Detect: func() (agent.Driver, bool) {
				if _, err := exec.LookPath("claude"); err != nil {
					return nil, false
				}
				return claude.Driver(), true
			},
		},
		{
			Provider: "codex",
			Source:   "codex on PATH",
			Models:   codex.Models(),
			Detect: func() (agent.Driver, bool) {
				if _, err := exec.LookPath("codex"); err != nil {
					return nil, false
				}
				return codex.Driver(), true
			},
		},
	}
}
