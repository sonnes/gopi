// Package permission decides if a tool call of an agent runs. A
// [Policy] holds a [Mode] and three lists of rules: Deny, Ask, and
// Allow. [Policy.Hook] turns the policy into one [agent.Hook] that
// checks each call before it runs.
//
// For one call, the hook uses the first step that applies:
//
//  1. If a Deny rule matches the call, the hook blocks the call.
//  2. If an Ask rule matches the call, the hook asks [Policy.Approve].
//  3. If an Allow rule matches the call, the call runs.
//  4. The mode and the [Kind] of the tool decide. The result is to run
//     the call, to ask, or to block the call.
//
// A Deny rule and an Ask rule apply in every mode, also in [Bypass].
//
// A rule has the format of the permission rules in the settings of
// Claude Code: "Tool" or "Tool(content)". The rule format, the tool
// aliases, and the match rules for commands and paths come from Claude
// Code (utils/permissions/permissionRuleParser.ts,
// utils/permissions/shellRuleMatching.ts,
// tools/BashTool/bashPermissions.ts, and
// utils/permissions/filesystem.ts). A tool name is one of these:
//
//   - The name of a tool, such as "bash" or "mcp__github__create_issue".
//   - A Claude Code name: "Bash" is bash. "Read" is read_file, grep, and
//     find. "Edit" is edit_file and write_file. "Write" is write_file.
//   - A prefix that ends in "*", such as "mcp__github__*".
//   - "mcp__server", which is each tool of the MCP server "server".
//
// Only the bash tool and the file tools accept content. For bash, the
// content is a pattern for the command. For read_file, write_file,
// edit_file, grep, and find, the content is a glob for the "path"
// input. [Policy.Validate] gives an error for content on another tool.
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
