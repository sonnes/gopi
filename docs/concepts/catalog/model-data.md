---
title: Model Data
description: How make models writes the default models of each provider and driver to models.json, how a program reads the file, and how a fetch updates a catalog at runtime.
package: pkg/catalog, internal/modelsfile, cmd/modelsync
read_when:
  - You add a model, a price, or a provider to models.json
  - You run make models, or read its report
  - You need the meaning of a field or a price in models.json
  - A model from the file has a wrong limit or price
  - A spec resolves after a fetch but not before it
---

# Model Data

The model data of gopi is one file, `models.json`, at the root of the repository. It holds the default models of each provider and each driver, keyed by the ID of its detector, such as `anthropic` or `claude-code`. `make models` writes the file. A program reads it with `catalog.ReadModelFile`, from a path or a URL, and gives it to the detectors. `FetchModels` and `FetchDriverModels` add the models of today to a catalog at runtime.

```
  provider lists      CLI lists       models.dev
        \                 |               /
         +---- make models (modelsync) --+     a person reviews the diff
                          |
                    models.json
                          |
        catalog.ReadModelFile(path or URL)     the program chooses when
                          |
                   env detectors
                          |
                       catalog  <---- FetchModels, FetchDriverModels at runtime
```

## The File

The file is a JSON object. Each key is the ID of a detector, and each value is an entry.

| Field     | Meaning                                                                           |
| --------- | --------------------------------------------------------------------------------- |
| `kind`    | `provider` or `driver`.                                                           |
| `default` | The ID of the model that a caller gets when it names no model. It is in `models`. |
| `models`  | The models, sorted by ID.                                                         |

A model has the JSON fields of `ai.Model`, and a `note`. A zero or missing field is unknown.

| Field             | Meaning                                                                   |
| ----------------- | ------------------------------------------------------------------------- |
| `max_tokens`      | The output cap. A call without `ai.MaxTokens` sends this value.           |
| `context_window`  | The most tokens of one request: the prompt, the history, and the output.  |
| `input`           | The kinds of content that the model reads: `text`, `image`, `pdf`.        |
| `reasoning`       | The model reasons before it answers.                                      |
| `thinking_levels` | The `ai.Thinking` levels that the model takes.                            |
| `pricing`         | The prices, in US dollars for one million tokens.                         |
| `note`            | Text for a person, such as the reason for a value. gopi does not read it. |

The prices of `pricing`:

| Field              | Price of                                                                                                      |
| ------------------ | ------------------------------------------------------------------------------------------------------------- |
| `input`            | An input token that is not in the cache.                                                                      |
| `output`           | An output token.                                                                                              |
| `cache_read`       | An input token that the provider reads from its cache.                                                        |
| `cache_write`      | A token that the provider writes to its default cache. For Anthropic, this cache lasts 5 minutes.             |
| `cache_write_long` | A token that the provider writes to the long cache of `ai.CacheLong`. For Anthropic, this cache lasts 1 hour. |
| `tiers`            | The prices of a large request, in order of `input_above`.                                                     |

A tier applies when the input of a request is more than `input_above` tokens. The input is the sum of the input, cache read, and cache write tokens. The tier prices the whole request. OpenRouter gives the start of a tier as `min_prompt_tokens`, so the list of OpenRouter stores `min_prompt_tokens` minus 1.

An OpenAI model has no `cache_write_long`. The usage of OpenAI does not split the writes of its 24-hour cache, so gopi prices each write at `cache_write`.

The file holds no cache durations, because no model list gives them. The provider packages set the duration from `ai.CacheRetention`.

## Design

- gopi does not embed the file, and it reads the file only when a program asks. The program chooses the source, a path or a URL, and when to read it again. A new model then needs a new file, and no new release of the program.
- `ReadModelFile` keeps no copy and no cache. A program that wants a copy on disk writes it, and reads that path.
- A provider package names no models. Its tests read the file of the repository through `internal/modelstest`, so a recorded request sends the limits of the file. The cost: a model that the code names by ID has no limits or prices until a catalog registers it from the file.
- `ParseModelFile` accepts a field that it does not know, so a newer file still loads in an older program. The generator reads the file with `modelsfile.Parse`, which rejects an unknown field, so a typo in the file fails `make models`.
- With no file, the detectors register no models. A spec then resolves only after `FetchModels` or `FetchDriverModels`.
- The generator is the module `cmd/modelsync`, because it imports the detectors of `pkg/catalog/env` and the providers. The root module imports neither.
- `make models` merges a list with `catalog.MergeModel`, the rule of `FetchModels`, or with `catalog.MergeDriverModel` for a driver. A list then gives the same values to the file and to a catalog. Only the generator fills values from models.dev.
- A value in the file always wins over models.dev. models.dev fills only an empty value, because models.dev can be late or wrong, and it has no price for the 1-hour cache of Anthropic. The report names each price that differs, so a person checks it.
- models.dev fills the thinking of a model only if the model has no thinking levels. A false `reasoning` is a choice, and JSON cannot tell it from a missing value.
- A model that a list leaves out stays, unless `PRUNE=1` is set. A provider can drop a model from its list before it stops serving it. An empty list prunes nothing, because it is more likely an error than a provider with no models.
- An ID of `IDS` that the generator does not know is an error, so a typo does not rewrite the file.
- The OpenRouter entry holds only the models with tools, because each call of a gopi agent sends tools. It holds no model of the Batch API. The list of OpenRouter is public, so its update needs no key.
- The Fireworks entry comes from models.dev, because the list of Fireworks has no prices and no output caps.
- The list of OpenAI holds models that are not for chat, such as embedding models. A model of that list joins the file only if models.dev has it with tools.
- Each entry has a `default`. `ModelFile.Of` returns it first, and the CLI uses the first model of a detector when `--model` is unset. The generator gives a new provider entry its first model by ID, and a person sets the default. A driver entry takes the first model of the list of its CLI, because the CLI lists its default first.
- A reason for a value goes in `note`, because JSON has no comments.

## Map

| Task                                  | File                                 |
| ------------------------------------- | ------------------------------------ |
| Change a model, a price, or a default | `models.json`                        |
| Update the file from the lists        | `make models`, `make models IDS=...` |
| Change how the generator merges       | `cmd/modelsync/sync.go`              |
| Change how models.dev maps to a model | `cmd/modelsync/dev.go`               |
| Change the format of the file         | `internal/modelsfile/modelsfile.go`  |
| Change how a program reads the file   | `pkg/catalog/modelfile.go`           |
| Change the checks of the file         | `pkg/catalog/modelfile_test.go`      |

## Related Concepts

- [Catalog](catalog.md)
- [Host Provider](../ai/host-provider.md)
- [Driver](../agent/driver.md)
