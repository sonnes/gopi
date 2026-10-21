---
title: Host Provider
description: How a provider for a server that copies an OpenAI API sends its calls through the provider of that API, and lists the models of the server with their limits and prices.
package: pkg/ai/openrouter, pkg/ai/fireworks
read_when:
  - You add a server that copies an OpenAI API and has its own model list
  - A listed model of OpenRouter or Fireworks has the wrong limits or prices
  - A host provider sends a call to the wrong URL or with the wrong key
---

# Host Provider

A host provider is a provider for one server that copies an OpenAI API. It sends each model call through the provider of that API, and it adds what the server does differently: its key, its URL, and its model list. `openrouter` sends its calls through `openai`, and `fireworks` sends its calls through `openaichat`.

```
openrouter.Provider --StreamText, GenerateObject--> openai.Provider      Responses API
                    --ListModels------------------> GET /models of OpenRouter

fireworks.Provider  --StreamText, GenerateObject--> openaichat.Provider  Chat Completions API
                    --ListModels------------------> GET /v1/accounts/{account}/models
```

## Design

- The request, the stream, the retries, and the conversion of the messages stay in the provider of the API. A host provider adds no second copy of them, so a fix to the API provider fixes each host.
- A host provider has only the text call, the object call, and the model list. The image call and the token count call of `openai` go to APIs that OpenRouter does not serve, so `Detect` does not register a host provider for images.
- A host provider always gives its own key and base URL to the API provider. Without them, the OpenAI SDK reads `OPENAI_API_KEY` and `OPENAI_BASE_URL`, and sends the OpenAI key to another server.
- The model list of each host has more fields than the OpenAI list, which has only IDs, so a host provider reads the list of the host. The default models of each host are in [models.json](../catalog/model-data.md).
- OpenRouter gives a price for one token, and gopi keeps a price for one million tokens. The provider rounds the product to six decimal places, so a price has no float error. A negative price, such as the price of a router, is unknown and stays zero. An override with `min_prompt_tokens` becomes a price tier. A price that the override does not give, or gives as negative, is the base price.
- The model list of Fireworks is on its control plane, at the root of the API, and not under `/inference/v1`. So the base URL of `fireworks` is the root URL. The list has no output cap, so a model that `ListModels` returns has no `MaxTokens`. The models of Fireworks in models.json come from models.dev, with their caps.
- `fireworks.ListModels` keeps only a model that is ready, serverless, and has tools. Another model needs a deployment of its own, or is not for chat.
- `openrouter.ListModels` asks for the models with tools, because each call of a gopi agent sends tools. It skips a model of the Batch API, with the suffix `:batch`, because an agent streams each call. It skips a price for a time of day, because a tier of `ai.Pricing` depends only on the size of the prompt.
- OpenRouter can list an output cap that is larger than the context window. The list caps `MaxTokens` at the window, because an API rejects a larger cap. It drops a tier that starts after the window, because no request reaches it.
- For a Google model, the `input_cache_write` price of OpenRouter is only the storage of a write for 5 minutes. OpenRouter bills the write at the input price plus that storage, so the list adds the input price to the write price.
- `FetchModels` keeps the listed prices of a model that has no registered prices. A provider package registers the prices of its own API, and those stay.

## Map

| Task                                    | File                          |
| --------------------------------------- | ----------------------------- |
| Change how a model of OpenRouter lists  | `pkg/ai/openrouter/list.go`   |
| Change how a model of Fireworks lists   | `pkg/ai/fireworks/list.go`    |
| Change the key or the URL of a host     | `pkg/ai/<host>/<host>.go`     |
| Add a host to the built-in detectors    | `pkg/catalog/env/env.go`      |
| Change how a listed model merges        | `pkg/catalog/fetch.go`        |

## Related Concepts

- [Provider](provider.md)
- [OpenAI Providers](openai.md)
- [Catalog](../catalog/catalog.md)
