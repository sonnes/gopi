// Package permission decides if a tool call of an agent runs. A
// [Policy] holds a [Mode] and three lists of rules: Deny, Ask, and
// Allow. [Policy.Hook] turns the policy into one [agent.Hook] that
// checks each call before it runs.
//
// For one call, the hook uses the first step that applies:
//
//  1. If a path of a file tool has symlinks that do not resolve, the
//     hook blocks the call.
//  2. If a Deny rule matches the call, the hook blocks the call.
//  3. If a hard limit applies and a path is outside the roots, the hook
//     blocks the call. The limits are [Policy.BlockReadsOutside] and
//     [Policy.BlockWritesOutside].
//  4. If an Ask rule matches the call, the hook asks [Policy.Approve].
//  5. If an Allow rule matches the call, the call runs.
//  6. The mode, the [tool.Kind] of the tool, and its paths decide. The
//     result is to run the call, to ask, or to block the call.
//
// A Deny rule, an Ask rule, and a hard limit apply in every mode, also
// in [Bypass].
//
// In the mode step, the roots are [Policy.Roots], or [Policy.Dir] if
// Roots is empty. A read tool runs if each of its paths is in a root.
// An edit tool on a protected path asks. A protected path has a folder
// such as .git, .claude, or .vscode in it, or ends in a file name such
// as .bashrc or .envrc. The lists come from Claude Code 2.1.316. In
// [AcceptEdits], an edit tool runs if each of its paths is in a root. A
// tool that is not a file tool has no paths, so each root check passes.
//
// A path has more than one spelling when it goes through a symlink: the
// path of the input, the target of each symlink, and the real path. A
// root check and an Allow rule need each spelling. A Deny rule and an
// Ask rule need one spelling, and ignore the letter case. On macOS,
// /private/tmp and /private/var are the same paths as /tmp and /var.
//
// A rule has the format of the permission rules in the settings of
// Claude Code: "Tool" or "Tool(content)". The rule format and the match
// rules for commands and paths come from Claude Code
// (utils/permissions/permissionRuleParser.ts,
// utils/permissions/shellRuleMatching.ts,
// tools/BashTool/bashPermissions.ts, and
// utils/permissions/filesystem.ts). A tool name is one of these:
//
//   - The name of a tool, such as "bash", "lookup_order", or
//     "mcp__github__create_issue". A Claude Code name, such as "Read",
//     names the tools that [tool.RuleNames] gives: read_file, grep,
//     find, and a tool named "Read".
//   - A prefix that ends in "*", such as "mcp__github__*".
//   - "mcp__server", which is each tool of the MCP server "server".
//
// Only the bash tool and the file tools accept content. For bash, the
// content is a pattern for the command. For read_file, write_file,
// edit_file, grep, and find, the content is a glob for the "path"
// input. For apply_patch, the content is a glob for each path that the
// patch changes. [Policy.Validate] gives an error for content on
// another tool.
//
// The command rules match text, not the parsed shell program. So a
// command rule is not a security boundary:
//
//   - The hook splits a command at &&, ||, ;, |, &, and a newline
//     outside quotes. A Deny rule or an Ask rule matches if it matches
//     the whole command or one part. An Allow rule with a prefix or a *
//     matches only if an Allow rule matches each part.
//   - A command with $(...), backticks, <(...), or >(...) matches an
//     Allow rule only if the rule is the exact command.
//   - A Deny rule does not see a command inside a substitution, a
//     subshell, "sh -c", or "eval", unless its * matches the text.
//   - The hook does not remove environment variables, wrappers such as
//     "timeout", or redirections before it matches.
package permission
