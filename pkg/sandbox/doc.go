// Package sandbox is the place where the tools read files, write files,
// and run commands. [Sandbox] is the interface. [Local] is a directory
// on this host. A remote sandbox, such as a cloud VM, implements the
// same interface in its own package.
//
// Each tool is a package under this one: read, write, edit, bash, grep,
// and find. Call the New function of each tool that the agent needs. To
// rename a tool or change its description, wrap it with an
// [github.com/sonnes/gopi/pkg/tool.Middleware].
//
// A caller must obey these rules:
//
//   - A tool gives each path to the sandbox without change. The sandbox
//     resolves a relative path against [Sandbox.Dir] and decides which
//     paths it allows.
//   - A [Local] sandbox has no limits by default. A command runs as the
//     user of this process, with the whole environment and its secrets,
//     such as ANTHROPIC_API_KEY. The tools reach every path that the
//     user can reach.
//   - A [Local] sandbox with a [Policy] runs each command inside a fence
//     with the policy. The file tools and the search tools obey the file
//     rules of the policy. [Local] returns an error if the fence cannot
//     run.
//   - The grep and find tools check the search path against the policy
//     of a [Local] sandbox only when they get the sandbox itself. A type
//     that wraps a local sandbox skips this check. rg still runs through
//     [Sandbox.Exec], so with a [Policy], the fence keeps rg out of each
//     hidden path. Without rg, a wrapped sandbox returns an error,
//     because the tools walk the directory in Go only for [Local].
//   - The grep and find tools run rg in the sandbox. A remote sandbox
//     must have rg installed. A local sandbox without rg walks the
//     directory in Go.
//   - [Sandbox.Exec] returns an error when [Command.Dir] is not a
//     directory. The grep and find tools start rg in the directory that
//     they search, and they need the error to tell a file from a
//     directory.
//
// The reasons are in docs/concepts/sandbox/sandbox.md.
package sandbox
