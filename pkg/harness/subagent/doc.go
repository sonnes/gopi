// Package subagent holds the definition of a subagent: an agent that
// the main agent starts to do one task in its own context.
//
// The package imports only the standard library. An agents reader, such
// as a folder of files or a database, then needs only this package to
// give subagents. It does not import the harness, the agent loop, or
// the providers.
//
// A caller must obey these rules:
//
//   - An [Agent] is an agents reader of itself. An agent in code then
//     goes in the list of readers, at the position where it must win
//     or lose.
//   - The fields keep the values as the file writes them. The harness
//     maps them, for example [Agent.Model] to a model and [Agent.Mode]
//     to a permission mode.
package subagent
