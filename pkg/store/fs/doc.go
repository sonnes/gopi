// Package fs is the store of gopi on the file system. It reads the
// definitions of [def] from Markdown files, and it keeps each session
// in one JSONL file.
//
// [Dir] gives the agents, the skills, and the AGENTS.md file of one
// folder on disk as a [def.Source]. [FS] reads the same layout from an
// [io/fs.FS], so an [embed.FS] of files that ship in the binary reads
// the same as a folder on disk. Its paths are relative to the fs.FS.
// [Sessions] returns the [SessionStore], a [session.Storer]. [WriteSkills]
// and [WriteAgents] write the formats that the sources read.
//
// The package name hides io/fs. A caller imports it as storefs:
//
//	import storefs "github.com/sonnes/gopi/pkg/store/fs"
//
// A layout on disk, such as .claude, is a folder of [Dir]. [def.Layers]
// merges the folders, lowest first:
//
//	defs := def.Layers(
//		storefs.Dir(filepath.Join(home, ".claude")),
//		storefs.Dir(filepath.Join(repo, ".claude")),
//	)
//
// A file starts with YAML frontmatter between two --- lines. The
// sources parse it as Claude Code and Codex do:
//
//   - If the YAML is not valid, the source quotes each simple
//     "key: value" line that holds a special YAML character, and
//     parses one more time.
//   - A boolean is true or false, or the text "true" or "false" in any
//     case.
//   - A list of tools or names is a YAML list, or text that the source
//     splits on commas and white space. The source does not split
//     inside parentheses, so "Bash(git push *), Read" is two tools.
//   - A key that no field takes goes to the Extra field with its parsed
//     value.
//
// A caller must obey these rules:
//
//   - Give a folder on disk with [Dir], or to [FS] as the FS of an
//     [os.Root], not as [os.DirFS]. os.DirFS follows a symbolic link out
//     of the folder. A skill from a cloned repository can then show the
//     model a file outside the repository, such as a credentials file.
//   - A folder that does not exist gives nothing. A home folder or a
//     project folder is often absent, and that is not an error.
//   - A source skips a malformed file, such as a SKILL.md with no
//     description, and reads the others. One typo must not stop every
//     session. A file that cannot be read is an error, because a fix
//     to the text cannot correct it.
package fs
