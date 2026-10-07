// Package ai holds the types that name a model call, the stream that
// reports the call, and what a model learns about a tool. Every layer
// above this package uses these types. Package tool holds the tools
// that run.
//
// A [Model] is metadata. A provider holds the credentials and implements
// [TextProvider], [ObjectProvider], [ImageProvider], [SpeechProvider], or
// [TokenCounter]. A binding such as [LanguageModel] pairs a model with a
// provider.
//
// A caller must obey these rules:
//
//   - The messages are the only argument of a call. The system prompt,
//     the first tools, and every setting travel as options. A system
//     message in the messages adds instructions from its place.
//   - A provider reads every option from one [Options] value. A caller
//     builds the value with the setters in this package, or with a
//     setter from [NewOption] in another package. A later value wins.
//   - A [Message] round-trips through JSON. Each content block carries a
//     type tag, so a store can keep the message without a schema.
//
// The reasons are in docs/concepts/ai/message.md,
// docs/concepts/ai/stream.md, and docs/concepts/ai/provider.md.
package ai
