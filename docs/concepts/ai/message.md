---
title: Message
description: How one message of a conversation is typed, stored, and sent to a model.
package: pkg/ai
read_when:
  - You build a prompt or read a response
  - You store messages and read them back
  - You add a content block type
---

# Message

A message is what one role adds to a conversation. It has a role and a list of content blocks. A content block is text, thinking, a tool call, a file, a section, or a tool change. A model holds no state, so each call sends every message of the conversation.

## Design

- The `ai.SystemPrompt` option is the prompt in front of the first message. It holds `Text` and `Section` blocks, so a later system message can replace one of its sections by name.
- A system message adds instructions from its place in the conversation. A change in the middle of a conversation then appends a message, and the start of the request stays the same for the cache. A provider sends the message before the next assistant message, because a tool result must come directly after its tool call.
- A section and a tool change are blocks of a system message, so a change is content at its place, and the JSON of a message needs no extra field.
- A provider frames a section of a system message by its name, as an update or a removal, so the model can relate it to the system prompt. The frame needs no state of earlier sections, and `internal/transcript` writes it, so every provider sends the same text.
- A file is one block with a media type and raw bytes, and each provider maps the media type to its own block. A provider whose tool message takes only text sends the files of the results in a user message after them.
- A tool result is a message with its own role, so the caller keeps one result for each call. The provider groups consecutive results into the shape that its API wants. It adds an error result for each call with no result, because an API rejects a call without its result.
- The effort of a message is in the words of the provider API, and the provider sends it back as it is. A later change to the map from thinking levels to efforts then leaves the old messages and the cached prefix the same.
- The provider prices a response from the `Pricing` of the model when the response ends, so a saved message keeps what the call cost when it ran.
- A message round-trips through JSON with a type tag on each block, so a caller saves messages with no schema of its own. An unknown tag is an error, so a reader never drops a saved block in silence. The cost: an older build cannot read a block type from a newer build.
- A context window overflow maps to the length stop reason, because the caller handles both the same way. A refusal is its own stop reason, so the caller gets the partial message and does not run its tool calls.

## Map

| Task                             | File                                       |
| -------------------------------- | ------------------------------------------ |
| Add a content block type         | `pkg/ai/content.go`                        |
| Build a message                  | `pkg/ai/message.go`                        |
| Change the JSON of a message     | `pkg/ai/content.go`, `pkg/ai/message.go`   |
| Read the stop reason values      | `pkg/ai/event.go`                          |
| Change the frame of a section    | `internal/transcript/transcript.go`        |

## Related Concepts

- [Stream](stream.md)
- [Provider](provider.md)
