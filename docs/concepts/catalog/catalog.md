---
title: Catalog
description: How a spec resolves to a model bound to its provider or to the driver of its agent, and how detectors fill a catalog from the credentials of a process.
package: pkg/catalog, pkg/catalog/env
read_when:
  - You resolve a spec to a model
  - You add a credential source or a built-in detector
  - A spec resolves to the wrong provider, or to no provider
  - A spec starts the wrong kind of agent
---

# Catalog

A catalog maps a spec to a model bound to its provider, or to the [driver](../agent/driver.md) that starts its agent. A spec is `<provider>/<model-id>`, or a bare model ID. The catalog holds one set of providers for each capability, and all of the sets share one model index. A detector reads one credential source and gives the catalog a provider.

```
  env.Detectors()          one detector per credential source
     |
     | Catalog.Detect: in order, the first hit for each provider ID
     v
  Catalog                  text, image, and speech providers,
     |                     the drivers, and the default driver
     |                     one model index: "<provider>/<model-id>"
     | LanguageModel(spec), ImageModel(spec), SpeechModel(spec)
     v
  ai.LanguageModel{Model, Provider}  ->  StreamText, agent.New

  Catalog.Driver(spec)
     |-- the provider has a driver ....... that driver
     +-- the provider is a text provider . the default driver
```

## Design

- Each capability has its own register method, so a provider of the wrong type fails at compile time and not at the call. `Detect` registers a provider for each capability that it implements.
- If a provider is registered, an ID that it did not register passes through as a model with that ID and no metadata. A provider serves a new model before the SDK lists it. The model has no output cap and no prices, so the provider uses its default and the cost is zero.
- A bare model ID resolves only when one provider registered it. If two providers registered it, the error lists the full specs, because a silent choice can send a call to the wrong account.
- A bare model ID does not count a provider that has only a driver, because a driver cannot serve a call to a model. `Driver` uses the same rule first, so a bare ID names the same model in each method. A driver serves a bare ID only when no other provider registered it, so its agent needs the full spec, such as `claude-code/claude-opus-5-5`.
- The catalog resolves a spec and makes no calls. The binding has the calls, so the catalog adds no second surface. A caller gives the binding to `agent.New`, or the config of a session to the driver.
- A driver is one more capability, so one spec names both the kind of agent and its model, such as `claude-code/claude-opus-5-5`. A text provider with no driver runs on the default driver, the harness, so a spec of a model API needs no driver of its own.
- Detection is in the detectors, not in the provider packages. A detector owns the provider ID, so one provider type serves two IDs, such as `openai` and `openrouter`.
- The order of the detectors is the precedence. For each provider ID, the first detector that finds a credential wins, and `Detect` skips the rest. A list of detectors then reads from top to bottom as priority.
- `pkg/catalog` imports `pkg/ai` and `pkg/agent`, for the type of a driver. `pkg/agent` does not import the catalog. `pkg/catalog/env` is its own module because it imports each provider module, so a program that registers its own providers does not download the SDK of each provider.
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
| Change the models of a CLI driver   | `pkg/agent/<cli>/driver.go`       |

## Related Concepts

- [Provider](../ai/provider.md)
- [Driver](../agent/driver.md)
- [OpenAI Providers](../ai/openai.md)
