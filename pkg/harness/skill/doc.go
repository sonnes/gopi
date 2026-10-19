// Package skill holds the skill: a named body of instructions that the
// model loads on demand.
//
// The package imports only the standard library. A skills reader, such
// as a folder of files or a database, then needs only this package to
// give skills. It does not import the harness, the agent loop, or the
// providers.
//
// A caller must obey these rules:
//
//   - A [Skill] is a skills reader of itself. A skill in code then goes
//     in the list of readers, at the position where it must win or
//     lose.
//   - A reader leaves [Skill.Qualifier] empty. The harness sets it,
//     because only the harness knows where each reader is.
package skill
