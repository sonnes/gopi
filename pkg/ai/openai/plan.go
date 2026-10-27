package openai

import (
	"errors"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// planNamespace is the namespace of the function tools on the route of
// the ChatGPT plan. The route refuses a function tool outside a
// namespace.
const planNamespace = "gopi"

// planNamespaceDescription is the description of [planNamespace]. The
// API requires one.
const planNamespaceDescription = "The tools of the agent."

// ErrNotOnPlan is the error of a call that the route of the ChatGPT plan
// does not serve, such as [Provider.CountTokens].
var ErrNotOnPlan = errors.New("openai: the ChatGPT plan route does not serve this call")

// ChatGPTPlan shapes each request for the Responses route of a ChatGPT
// plan, as Sign in with ChatGPT gives it. The caller also sets an
// [HTTPClient] that sends the OAuth token of the plan. With this option:
//
//   - A request has no max_output_tokens, temperature, or
//     prompt_cache_retention, because the route refuses them.
//   - The function tools go into one namespace, "gopi", and each
//     function call of the history carries that namespace.
//   - [Provider.GenerateObject] reads its response from a stream,
//     because the route serves only a stream.
//   - [Provider.CountTokens] returns [ErrNotOnPlan].
func ChatGPTPlan() Option {
	return func(c *config) { c.plan = true }
}

// toPlan removes the fields that the route of the plan refuses, and puts
// the function tools in [planNamespace].
func toPlan(params *responses.ResponseNewParams) {
	params.MaxOutputTokens = param.Opt[int64]{}
	params.Temperature = param.Opt[float64]{}
	params.TopP = param.Opt[float64]{}
	params.PromptCacheRetention = ""
	params.Tools = namespaceTools(params.Tools)

	for _, item := range params.Input.OfInputItemList {
		if call := item.OfFunctionCall; call != nil {
			call.Namespace = param.NewOpt(planNamespace)
		}
	}
}

// namespaceTools moves each function tool of tools into one namespace
// tool. The other tools stay as they are.
func namespaceTools(tools []responses.ToolUnionParam) []responses.ToolUnionParam {
	ns := responses.NamespaceToolParam{
		Name:        planNamespace,
		Description: planNamespaceDescription,
	}
	var out []responses.ToolUnionParam
	for _, t := range tools {
		fn := t.OfFunction
		if fn == nil {
			out = append(out, t)
			continue
		}
		ns.Tools = append(ns.Tools, responses.NamespaceToolToolUnionParam{
			OfFunction: &responses.NamespaceToolToolFunctionParam{
				Name:        fn.Name,
				Description: fn.Description,
				Strict:      fn.Strict,
				Parameters:  fn.Parameters,
			},
		})
	}
	if len(ns.Tools) == 0 {
		return out
	}
	return append(out, responses.ToolUnionParam{OfNamespace: &ns})
}
