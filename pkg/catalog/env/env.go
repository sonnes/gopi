// Package env holds the built-in detectors. Each one reads the credential
// of one provider from an environment variable.
//
// A caller must obey these rules:
//
//   - The chain reads the environment when [catalog.Catalog.Detect]
//     runs it, not when [Detectors] returns it.
//   - A provider SDK also reads its base URL from the environment, for
//     example ANTHROPIC_BASE_URL or OPENAI_BASE_URL. The openrouter
//     provider sets its own base URL.
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

	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/ai/anthropic"
	"github.com/sonnes/gopi/pkg/ai/openai"
	"github.com/sonnes/gopi/pkg/ai/openaichat"
	"github.com/sonnes/gopi/pkg/catalog"
)

// openRouterURL is the base URL of the Responses API of OpenRouter.
const openRouterURL = "https://openrouter.ai/api/v1"

// Detectors returns the built-in chain, in precedence order:
//
//   - anthropic, from ANTHROPIC_API_KEY, with [anthropic.Models].
//   - openai, from OPENAI_API_KEY, with [openai.Models].
//   - openrouter, from OPENROUTER_API_KEY, through the openai provider.
//     It registers no models, because OpenRouter serves too many to
//     list. A spec such as "openrouter/qwen/qwen3-coder" passes through.
//   - openaichat, from OPENAI_API_KEY. It registers no models, because
//     it serves any server at OPENAI_BASE_URL.
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
			Detect: func() (ai.TextProvider, bool) {
				key := os.Getenv("OPENROUTER_API_KEY")
				if key == "" {
					return nil, false
				}
				p := openai.New(
					openai.APIKey(key),
					openai.BaseURL(openRouterURL),
				)
				return p, true
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
