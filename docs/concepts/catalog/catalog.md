---
title: Catalog
description: How a spec resolves to a model bound to its provider, and how detectors fill a catalog from the credentials of a process.
package: pkg/catalog, pkg/catalog/env
read_when:
  - You resolve a spec to a model
  - You add a credential source or a built-in detector
  - A spec resolves to the wrong provider, or to no provider
---

# Catalog

A catalog maps a spec to a model bound to its provider. A spec is `<provider>/<model-id>`, or a bare model ID. The catalog holds one set of providers for each capability, and all of the sets share one model index. A detector reads one credential source and gives the catalog a provider.

```
  env.Detectors()          one detector per credential source
     |
     | Catalog.Detect: in order, the first hit for each provider ID
     v
  Catalog                  text, image, and speech providers
     |                     one model index: "<provider>/<model-id>"
     | LanguageModel(spec), ImageModel(spec), SpeechModel(spec)
     v
  ai.LanguageModel{Model, Provider}  ->  StreamText, agent.New
```

## Design

- Each capability has its own register method, so a provider of the wrong type fails at compile time and not at the call. `Detect` registers a provider for each capability that it implements.
- If a provider is registered, an ID that it did not register passes through as a model with that ID and no metadata. A provider serves a new model before the SDK lists it. The model has no output cap and no prices, so the provider uses its default and the cost is zero.
- A bare model ID resolves only when one provider registered it. If two providers registered it, the error lists the full specs, because a silent choice can send a call to the wrong account.
- The catalog resolves a spec and makes no calls. The binding has the calls, so the catalog adds no second surface. The catalog does not import `pkg/agent`, and a caller gives the binding to `agent.New`.
- Detection is in the detectors, not in the provider packages. A detector owns the provider ID, so one provider type serves two IDs, such as `openai` and `openrouter`.
- The order of the detectors is the precedence. For each provider ID, the first detector that finds a credential wins, and `Detect` skips the rest. A list of detectors then reads from top to bottom as priority.
- `pkg/catalog` imports only `pkg/ai`, and `pkg/catalog/env` is its own module because it imports each provider module. A program that registers its own providers does not download the SDK of each provider.
- `FetchModels` merges each listed model into the registered model, field by field. A field that the list leaves zero keeps the registered value, because the OpenAI API lists only IDs. The prices and the thinking levels of a registered model always stay, because no API lists prices, and the Anthropic API cannot say if a model can turn thinking off.
- The catalog fetches the lists only when a caller calls `FetchModels`. Detection makes no network call, and an unknown ID passes through without a list.
- A caller creates each catalog with `New`, and the package holds no catalog of its own. A call then shows which credentials it uses, and two tests do not share a catalog.

## Map

| Task                                | File                              |
| ----------------------------------- | --------------------------------- |
| Change how a spec resolves          | `pkg/catalog/catalog.go`          |
| Change the order or the skip rule   | `pkg/catalog/detect.go`           |
| Change how a model list merges      | `pkg/catalog/fetch.go`            |
| Add a built-in credential source    | `pkg/catalog/env/env.go`          |
| Change the models of a provider     | `pkg/ai/<provider>/<provider>.go` |

## Related Concepts

- [Provider](../ai/provider.md)
- [OpenAI Providers](../ai/openai.md)
