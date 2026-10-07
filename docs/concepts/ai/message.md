---
title: Message
description: How one turn of a conversation is typed, stored, and sent to a model.
package: pkg/ai
read_when:
  - You build a prompt or read a response
  - You store messages and read them back
  - You add a content block type
---

# Message

A message is one turn of a conversation. It has a role and a list of content blocks. A content block is text, thinking, a tool call, a file, or a tool change. A model holds no state, so each call sends every message of the conversation.

```
StreamText(ctx, messages, opts...)
  messages: user -> assistant -> tool_result -> system -> assistant -> ...
  opts:     SystemPrompt, Tools, MaxTokens, Thinking, ...
```

## Decisions

- The `ai.SystemPrompt` option of a call is the prompt in front of the first message. A system message is a change from its place in the conversation. The two have different names, so a reader does not take the prompt for a change to it. The option holds `Text` and `Section` blocks, so a later system message can replace a section of the first prompt by its name. A provider sends it 1:1 as the system prompt of its API, with the cache marker. A string option lost, because a section of the first prompt then had no name. A prompt made from the system messages at the start lost, because each provider then split the messages before it built a request.
- A system message adds instructions from its place in the conversation. A change mid-session then appends a message, and the start of the request stays the same. Each provider sends a system message in its place, with the system role of its API. No provider moves it into the prompt, because that change makes the next call miss the cache. A model that rejects a system message inside the conversation returns the API error. On Anthropic, Opus 4.8 and later, Sonnet 5.5, and Fable 5 take one. Many servers that copy the OpenAI Chat API do not.
- A content block is one of eight sealed types. There is no reminder block, because a reminder belongs to the harness.
- A tool change and a section are blocks of a system message, not fields of the message. A change from a place in the conversation is then content at that place, and the log keeps it without a schema of its own.
- A provider sends a section as its text and sends nothing for a removed section. It keeps no state of earlier sections. The layer that changes a section writes the framing, such as an "updated" note, as a text block next to it. A provider that compared each section with the earlier messages lost, because every provider then repeated the same diff, and the framing was a decision of the agent, not of the API.
- A file is one block with a media type and raw bytes, not one type for each format. A tool that reads an image or a PDF returns it in its result, and each provider maps the media type to its own block. A provider whose tool message takes only text puts a note in place of the file, so the model knows that the file exists.
- A tool call holds its input as raw JSON, not a map. The provider streams the input as text and the tool decodes it into its typed input, so a map in between decodes twice.
- A tool result is a message with its own role. The provider groups consecutive results into the shape that its API wants, so the caller keeps one result per call.
- A message keeps the model, the usage, and the stop reason of the response. It does not keep a timestamp, the API name, the provider name, an error string, or the tool name. Each of those has one reader, and that reader is not in this layer.
- Usage has four disjoint counts and no total. Providers do not agree on what belongs in a total, so the caller sums the fields that it needs.
- Usage carries the cost of the response, priced by the provider from the `Pricing` of the model when the response ends. The log then keeps what each call cost when it ran. A cost that a caller computes later from a price list lost, because a later price change would rewrite the cost of past calls.
- A message and a tool result round-trip through JSON with a type tag on each block. A store keeps them without a schema of its own. An unknown tag is an error, so a store cannot lose a block without a report.
- A context window overflow maps to the length stop reason, because the caller handles both the same way. Refusal is its own stop reason, because a caller reads it before it runs a tool call.

## Map

| Task                             | File                                       |
| -------------------------------- | ------------------------------------------ |
| Add a content block type         | `pkg/ai/content.go`                        |
| Build a message                  | `pkg/ai/message.go`                        |
| Change the JSON of a message     | `pkg/ai/content.go`, `pkg/ai/message.go`   |
| Read the stop reason values      | `pkg/ai/event.go`                          |
| Sum usage across turns           | `pkg/ai/model.go`                          |

## Related Concepts

- [Stream](stream.md)
- [Provider](provider.md)
