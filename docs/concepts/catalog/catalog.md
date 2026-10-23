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

A catalog maps a spec to a model bound to its provider, or to the [driver](../agent/driver.md) that starts its agent. A spec is `<provider>/<model-id>`, or a bare model ID. The catalog holds one set of providers for each capability, and the providers share one model index. The drivers have a separate model index. A detector reads one source and gives the catalog a provider or a driver: an API key for a provider, and a CLI on the PATH for a driver.

```
  env.Detectors(file)        one detector per credential source
  env.DriverDetectors(file)  one detector per CLI on the PATH
     |
     | Catalog.Detect, Catalog.DetectDrivers:
     | in order, the first hit for each provider ID
     v
  Catalog                  text, image, and speech providers,
     |                     the drivers, and the default driver
     |                     the models of the providers: "<provider>/<model-id>"
     |                     the models of the drivers:   "<driver>/<model-id>"
     | LanguageModel(spec), ImageModel(spec), SpeechModel(spec)
     v
  ai.LanguageModel{Model, Provider}  ->  StreamText, agent.New

  Catalog.Driver(spec)
     |-- the provider has a driver ....... that driver, a model of the driver
     +-- the provider is a text provider . the default driver, a model of the provider
```

## Design

- Each capability has its own register method, so a provider of the wrong type fails at compile time and not at the call. `Detect` registers a provider for each capability that it implements.
- A spec names a registered model, for a provider and for a driver. A typo in a spec then fails when the catalog resolves it, and not at the API after a call with no prices. A model that the SDK does not list needs `RegisterModel` or `FetchModels` first. A driver gets its models from `RegisterDriver`.
- A bare model ID resolves only when one provider registered it. If two providers registered it, the error lists the full specs, because a silent choice can send a call to the wrong account.
- A failed resolution returns an `UnknownModelError` or an `AmbiguousModelError`, so a caller can name the fix: the specs to choose from, the registered providers, or the command that lists the models.
- The models of the drivers are separate from the models of the providers, because a driver cannot serve a call to a model. `Model`, `LanguageModel`, `Specs`, and `FetchModels` read only the models of the providers. A CLI and its model API often share a model ID, such as `claude-code/claude-opus-5-5` and `anthropic/claude-opus-5-5`, and each keeps its own metadata.
- `Driver` resolves a bare model ID in the models of the providers first, so a bare ID names the same model in each method. A bare ID resolves to the models of the drivers only when no provider registered it. To start the agent of a driver, a caller gives the full spec, such as `claude-code/claude-opus-5-5`.
- The catalog resolves a spec and makes no calls. The binding has the calls, so the catalog adds no second surface. A caller gives the binding to `agent.New`, or the config of a session to the driver.
- A driver is one more capability, so one spec names both the kind of agent and its model, such as `claude-code/claude-opus-5-5`. A text provider with no driver runs on the default driver, the harness, so a spec of a model API needs no driver of its own.
- Detection is in the detectors, not in the provider packages. A detector owns the provider ID, so one credential serves two IDs, such as `openai` and `openaichat` from `OPENAI_API_KEY`.
- A driver has the same parts as a provider: the models of its entry in the [model file](model-data.md), a detector, a built-in chain, and a fetch of the models. A caller then wires a CLI as it wires a model API.
- The order of the detectors is the precedence. For each provider ID, the first detector that finds a credential wins, and `Detect` skips the rest. A list of detectors then reads from top to bottom as priority.
- `pkg/catalog` imports `pkg/ai` and `pkg/agent`, for the type of a driver. `pkg/agent` does not import the catalog. `pkg/catalog/env` is its own module because it imports each provider module, so a program that registers its own providers does not download the SDK of each provider.
- `FetchModels` merges each listed model into the registered model, field by field. A field that the list leaves zero keeps the registered value, because the OpenAI API lists only IDs. The prices and the thinking levels of a registered model stay if it has them, because a provider package registers the prices of its own API, and the Anthropic API cannot say if a model can turn thinking off. A model with no registered prices keeps the listed prices, such as the prices of [OpenRouter](../ai/host-provider.md).
- `FetchDriverModels` lists the models of the drivers, and `FetchModels` does not call it. The list of a driver starts its CLI, and the CLI runs the hooks of its settings. It merges with `MergeDriverModel`, which keeps the thinking levels of the CLI.
- An ID of `FetchModels` or `FetchDriverModels` with no list gets a result with `ErrNoModelList`, so a typo does not pass in silence.
- The catalog fetches the lists only when a caller calls `FetchModels`. Detection makes no network call. The CLI calls `FetchModels` only when its spec names no registered model, and only for the provider of the spec, so a typo does not call each API.
- A caller creates each catalog with `New`, and the package holds no catalog of its own. A call then shows which credentials it uses, and two tests do not share a catalog.

## Map

| Task                              | File                             |
| --------------------------------- | -------------------------------- |
| Change how a spec resolves        | `pkg/catalog/catalog.go`         |
| Change the order or the skip rule | `pkg/catalog/detect.go`          |
| Change how a model list merges    | `pkg/catalog/fetch.go`           |
| Change how a CLI lists its models | `pkg/agent/<cli>/models.go`      |
| Add a built-in credential source  | `pkg/catalog/env/env.go`         |
| Add a built-in CLI                | `pkg/catalog/env/env.go`         |
| Change the default models         | `models.json`                    |
| Change how the CLI picks a model  | `cmd/gopi/main.go`               |
| Read the model file               | `pkg/catalog/modelfile.go`       |

## Related Concepts

- [Model Data](model-data.md)
- [Provider](../ai/provider.md)
- [Driver](../agent/driver.md)
- [OpenAI Providers](../ai/openai.md)
