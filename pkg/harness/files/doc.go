// Package files reads skills and subagents from Markdown files in an
// [io/fs.FS].
//
// [Skills] returns a [SkillDir], a skills reader. [Agents] returns an
// [AgentDir], an agents reader. Both take an fs.FS, so an [embed.FS] of
// files that ship in the binary reads the same as a folder on disk. The
// paths in the results are relative to the fs.FS. The package does not
// import the harness, because the harness imports it.
//
// A layout on disk, such as .agents or .claude, is a list of calls:
//
//	root, err := os.OpenRoot(repo)
//	if err != nil {
//		return err
//	}
//	repoFS := root.FS()
//	skills := files.Skills(repoFS, ".claude/skills")
//	agents := files.Agents(repoFS, ".claude/agents")
//
// A file starts with YAML frontmatter between two --- lines. The
// readers parse it as Claude Code and Codex do:
//
//   - If the YAML is not valid, the reader quotes each simple
//     "key: value" line that holds a special YAML character, and
//     parses one more time.
//   - A boolean is true or false, or the text "true" or "false" in any
//     case.
//   - A list of tools or names is a YAML list, or text that the reader
//     splits on commas and white space. The reader does not split
//     inside parentheses, so "Bash(git push *), Read" is two tools.
//   - A key that no field takes goes to the Meta field with its parsed
//     value.
//
// A caller must obey these rules:
//
//   - Give a folder on disk as the FS of an [os.Root], not as
//     [os.DirFS]. os.DirFS follows a symbolic link out of the folder. A
//     skill from a cloned repository can then show the model a file
//     outside the repository, such as a credentials file.
//   - A folder that does not exist gives nothing. A home folder or a
//     project folder is often absent, and that is not an error.
//   - The reader skips a malformed file, such as a SKILL.md with no
//     description, and reads the others. One typo must not stop every
//     session. A file that cannot be read is an error, because a fix
//     to the text cannot correct it.
package files
