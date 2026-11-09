// Package codex runs the OpenAI Codex CLI as a gopi agent. An [Agent]
// starts `codex app-server` and talks JSON-RPC to it over stdio. The
// CLI runs its own loop and its own tools. The Agent turns the output
// into [agent.Event] values with the same order as [agent.Agent], and
// it writes each message to a [session.Log].
//
// A caller must obey these rules:
//
//   - Runs on one Agent are sequential. A second Run fails on its
//     stream.
//   - A Run needs an entry with text or a file, because a Codex turn
//     needs input.
//   - Call [Agent.Close] when the agent is done. Close stops the
//     process.
//
// The Codex thread ID is a value on the log, so a [Durable] agent over
// a stored session resumes the same thread.
//
// The agent declines every approval request from the CLI, unless the
// Approve function of the config answers it.
//
// The agent sets the permission keys of the CLI with -c flags, so the
// config.toml of the user does not decide them. The exec policy rules
// in ~/.codex/rules still apply, because Codex has no key that turns
// them off. An allow rule there runs a command outside the sandbox.
//
// A subagent of the CLI, which a spawn_agent call starts, keeps its
// messages in a child session. The stream of the run carries its
// start, its events, and its end. A run waits for each subagent that
// still runs when the turn completes. If a direct subagent ends after
// the turn, the run starts a report turn that gives the model its
// result. A report turn can start more subagents, so the run sends a
// report turn after each round of late subagents. The wait has no time
// limit, and a cancel of the run ends it. A run that ends with a
// subagent that runs, such as a turn that failed, stops the subagent,
// and the next run starts a new process. An agent of [New] has unsaved
// children.
//
// # The Session Config
//
// [New] and [Durable] take an [agent.Config]. The agent reads its
// fields as follows:
//
//   - The ID of Model is the model of the thread. Without it, the CLI
//     uses its configured model. Dir is the working directory of the
//     process and of the thread.
//   - Effort is the reasoning effort of each turn. [ai.ThinkingOff]
//     keeps the default effort of the model, because not every model
//     accepts an effort of none. AutoCompact sets the size of the
//     context, in tokens, that starts a compaction. Without them, the
//     CLI uses its defaults.
//   - The agent calls Sections before each run. The sections are the
//     developer instructions of the thread, and the CLI keeps its own
//     base instructions. The first sections of a session are the
//     developer instructions, and the log keeps them as its base, so
//     each start and each resume of the thread gets the same
//     instructions. A later change goes as a developer message in the
//     history of the thread, before the next turn, because a Codex
//     model keeps a reminder in a user message only for its own turn.
//     If Sections returns an error, the run fails.
//   - MCP is the mcp_servers table of the config of the thread. The CLI
//     connects to each server. The servers of the config.toml of the
//     CLI stay, so a server of MCP replaces a server of the config with
//     the same name.
//   - Each folder of AddDirs is a writable root of the workspace-write
//     sandbox, as Dir is. The read-only sandbox stays read-only, and
//     the full access sandbox has no limits.
//   - The agent ignores Compaction, Idle, and Wake.
//
// The Mode sets the approval policy and the sandbox of the thread:
//
//   - The default mode is the read-only sandbox with the policy
//     "untrusted". A command that is not known to be safe asks, and so
//     does each file change, because the sandbox does not let it write.
//   - The accept edits mode is the workspace-write sandbox with the
//     policy "on-request". A file change and a command in the working
//     directory run with no question. A command that needs more asks.
//     So the mode asks less than the accept edits mode of Claude Code.
//   - The bypass mode is the danger-full-access sandbox with the policy
//     "never". With the limit BlockWritesOutside or Fence, it is the
//     workspace-write sandbox with the policy "never".
//   - Without a Mode, the CLI keeps its configured sandbox. With the
//     limit BlockWritesOutside or Fence, the sandbox is
//     workspace-write. The policy is "untrusted" with an Approve
//     function, so that the CLI asks before a command that is not known
//     to be safe. Without Approve, the policy is "never", because the
//     agent has nobody to ask.
//
// Codex has no control for reads outside the roots, so a config with
// the limit BlockReadsOutside fails [Durable] with
// [errors.ErrUnsupported].
//
// Approve answers each approval request of the CLI. If Approve returns
// an error, the agent declines the request. The call is the tool call
// of the item that the request names, as the log has it: a bash call
// for a command, and an apply_patch call with the changes for a file
// change. A file change request has no changes. So if it comes before
// its item, the agent declines it and does not call Approve. An Always
// decision accepts only this action. The agent and the CLI keep no
// rule for it. The caller keeps the rule of Always, as the host does.
// Codex cannot change the input of an action, so a decision with an
// Input declines it. Codex has no field for the Message of a decision,
// so the model does not read it.
//
// Codex has no rules for each tool, so the agent blocks the tools of
// the config itself, in a hook of the CLI at PreToolUse:
//
//   - A Deny rule with no content, such as "web_fetch", blocks each
//     call of its tool. The agent does not check a rule with content.
//     The hooks of the caller check it, as the host does.
//   - bash or apply_patch gets a Deny rule if Tools or ToolNames leaves
//     it out. So without Tools, the agent blocks both. The hooks of the
//     CLI do not see web_search and agent, so the agent keeps them.
//     ToolNames also blocks a call of each other tool, such as an MCP
//     tool, in that hook. The model still sees the tool.
//   - A hook that returns an [agent.AskError] asks the Approve function
//     in the hook, and the hook waits for the answer.
//
// The agent skips a well-known tool of Tools that Codex does not have,
// such as read_file, and a tool of the caller, because the CLI runs
// only its own tools. A tool of the caller that ToolNames names fails
// [Durable]. A Deny rule with no content, a left-out tool, ToolNames,
// or a hook of the config needs the hook command of [HookCommand].
//
// The agent loads Defs and writes the skills and the agents to a folder
// for each session at each start of the CLI, so an edit reaches the
// next process. It removes the folder at Close. A
// process that dies leaves its folder until the next start. The folder
// of each session is in the folder gopi-defs of [os.TempDir]. The agent
// gives the skills to the CLI with skills/extraRoots/set, and each
// agent as a role in the config of the thread. A CLI with no
// skills/extraRoots/set fails with [errors.ErrUnsupported]. A role has
// no key for the Tools, the Disallowed, or the MaxTurns of an agent, so
// a role runs with each tool of the session. The Mode of an agent file
// has no effect, because Codex 0.163 runs a role with the sandbox of
// the session. The CLI lists each skill and loads its body, so the
// agent adds no text for Skills. If the first text of a turn is a slash
// call of a skill, the agent writes "$" for "/", because Codex loads a
// skill from a mention. A name of Skills that no skill has fails
// [Durable]. A skill name or an agent name with a slash fails the
// start of the CLI, because the name is a file name. The agent does not
// read the instructions of Defs. The CLI also keeps the skills and the
// agents of its own folders.
//
// # Hooks
//
// The CLI calls the Hooks of the config at its own hook events. Codex
// has no hook callback over its protocol, so the agent adds a command
// hook to each thread through the config of thread/start, with the
// hook trust bypassed. The command, [HookCommand], relays each call to
// a socket of the agent:
//
//   - BeforeTool at PreToolUse. An error denies the call with its text.
//     A new input allows the call with it. A new name denies the call.
//     For Bash and apply_patch, a new input must be an object with a
//     string command. A new input of another shape denies the call.
//   - AfterTool at PostToolUse. A new text, or the text of an error,
//     replaces the output that the model reads.
//   - BeforeStop at Stop. The text of the entries continues the turn.
//     An error stops the turn and fails the run.
//
// If the command fails or times out, Codex runs the tool. The command
// at PreToolUse has a time limit of 24 hours, and the other commands
// keep the limit of the CLI. If the command cannot reach the agent, it
// denies a call at PreToolUse, gives the error as the output at
// PostToolUse, and ends the turn at Stop.
// The hooks do not see a hosted tool, such as web search. A hook gives
// only text entries. A hook that uses BeforeCall, AfterCall, or
// AfterTurn, or that implements no point, fails [Durable] and each run
// of an agent of [New]. The error for a point wraps
// [errors.ErrUnsupported].
//
// The reasons are in docs/concepts/agent/cli-agent.md,
// docs/concepts/agent/cli-request.md, and
// docs/concepts/agent/subagent.md.
package codex
