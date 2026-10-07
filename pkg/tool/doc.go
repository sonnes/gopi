// Package tool defines the functions that a model can call. A [Tool]
// has an [ai.ToolInfo] that the model reads, and a Run function that
// the agent calls with an [ai.ToolCall]. [New] builds a tool with a
// typed input, and [Func] builds one with a raw run function. Both take
// the same options. A [Middleware] wraps a tool with [Wrap] and returns
// a tool.
//
// Package ai holds what goes to the model: the info, the call, and the
// result. This package holds what runs, so a provider does not depend
// on it.
//
// A caller must obey these rules:
//
//   - Return an error result for a failure that the model can correct.
//     Return a Go error for a failure that a middleware can act on, such
//     as a transport that is down. The agent gives the model the text of
//     the error.
//   - A middleware builds its tool with [Wrap], so the tool keeps its
//     schema and its Deferred flag.
//
// The reasons are in docs/concepts/tool/tool.md.
package tool
