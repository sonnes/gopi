// Package def holds the definitions that an agent runs with: the
// agents, the skills, and the instruction texts. It also holds a reader
// interface for each.
//
// The package reads no file. A store reads the definitions, such as
// the store of pkg/store/fs. A program then needs only this package to
// give definitions from a folder, a database, or code. The package
// does not import the harness, the agent loop, or the providers.
//
// A caller must obey these rules:
//
//   - An [Agent] is an agents reader of itself, and a [Skill] is a
//     skills reader of itself. A definition in code then goes in the
//     list of readers, at the position where it must win or lose.
//   - The fields keep the values as the file writes them. The harness
//     maps them, for example [Agent.Model] to a model and [Agent.Mode]
//     to a permission mode.
//   - A reader keeps each key that no field takes in [Extra]. It does
//     not convert the values. A caller reads a key with [Extra.Read].
package def
