---
title: ElevenLabs Provider
description: How the ElevenLabs provider speaks a text and transcribes an audio file with plain HTTP requests, with no text generation.
package: pkg/ai/elevenlabs
read_when:
  - You change a request that the ElevenLabs provider sends
  - A speech call uses the wrong voice
  - A transcription model of ElevenLabs is missing from the model list
---

# ElevenLabs Provider

The ElevenLabs provider serves the ElevenLabs API with plain HTTP requests. It has speech and transcription, and no text generation. The catalog registers it with `RegisterProvider`, which registers each capability that a provider implements.

```
ai.SpeechModel --------> POST /v1/text-to-speech/{voice}   JSON, MP3 out
ai.TranscriptionModel -> POST /v1/speech-to-text           multipart, JSON out
ListModels, CheckKey --> GET  /v1/models                   speech models only
                           header xi-api-key
```

## Design

- The provider uses `net/http` and no SDK. gopi calls three routes of the API, so a client of its own is small, and the module adds no dependency.
- Each request sends the key in the `xi-api-key` header. A header of `ai.Headers` replaces a header of the provider with the same name.
- A speech call asks for MP3 at 44.1 kHz and 128 kbps, because each plan of ElevenLabs has this format. The higher formats need a paid tier.
- `ai.Voice` is a voice ID, because the API takes only IDs. With no voice, the provider uses `DefaultVoice`, a default voice of each account.
- A transcription call names the file part with the extension of its media type. The API reads the format of the audio from the file.
- The model list has only the speech models, because the models API does not list the transcription models. The model file names the transcription models, such as `scribe_v3`.
- A key check reads the models API, which runs no model. The API gives no label and no credit for a key, so the info is zero.
- Each request goes through `internal/retry`, as in each other provider. A failed answer is an `*Error` with the status, the header, and the start of the body.

## Map

| Task                                | File                               |
| ----------------------------------- | ---------------------------------- |
| Change the key, the base URL, or the client | `pkg/ai/elevenlabs/elevenlabs.go` |
| Change the retries or read an error | `pkg/ai/elevenlabs/request.go`     |
| Speak a text                        | `pkg/ai/elevenlabs/speech.go`      |
| Transcribe an audio file            | `pkg/ai/elevenlabs/transcribe.go`  |
| List the models or check a key      | `pkg/ai/elevenlabs/list.go`        |
| Detect the key of a process         | `pkg/catalog/env/env.go`           |

## Related Concepts

- [Provider](provider.md)
- [Catalog](../catalog/catalog.md)
