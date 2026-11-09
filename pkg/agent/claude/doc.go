// Package claude runs the Claude Code CLI as a gopi agent. The CLI runs
// its own loop and its own tools. An [Agent] sends the input of each
// run to the CLI and turns the output into the same [agent.Event]
// values as [agent.Agent]. It writes every message to a
// [session.Log].
//
// Each event of a run changes the state of the agent before the
// stream gets it, as in [agent.Agent]. [Agent.State] returns a copy.
//
// The agent speaks the stream-json protocol of the CLI on stdin and
// stdout, with the control channel for the initialize, interrupt, and
// permission requests.
//
// The CLI reads each of its settings files, also the user settings in
// ~/.claude. An Allow rule there runs a call before the caller sees it.
//
// A caller must obey these rules:
//
//   - The CLI must be installed and logged in. [Binary] sets its path.
//     A run of a CLI that is not logged in fails with
//     [agent.ErrNotLoggedIn].
//   - Runs on one agent are sequential. A second Run fails on its
//     stream.
//   - The first run starts the CLI, and it runs until [Agent.Close].
//     Call Close when the agent is no longer needed.
//   - Without an Approve function, the agent denies every permission
//     prompt of the CLI. Set Approve or a Mode in the config to let the
//     CLI run the tools.
//   - The text, the images, and the PDFs of the input entries go to the
//     CLI. Each other file becomes a text note.
//   - The log holds the messages of the main agent. A subagent of the
//     CLI, which an Agent call starts, keeps its messages in a child
//     session. The stream of the run carries its start, its events,
//     and its end. An agent of [New] has unsaved children.
//   - A subagent can run in the background, after the result of the
//     run that started it. Between runs, the agent reads the CLI, and
//     the Idle function of the config gets the events of the child.
//     When the CLI starts a turn on its own, the agent calls Wake. Call
//     [Agent.Continue] to run that turn.
//   - A tool name of an event, an approval request, or a hook is a gopi
//     name, such as bash for Bash. A field of the config takes a gopi
//     name or a Claude Code name. The agent maps each name in both
//     directions.
//
// # The Session Config
//
// [New] and [Durable] take an [agent.Config]. The agent reads its
// fields as follows:
//
//   - The ID of Model goes to --model. Dir is the working directory of
//     the CLI. Each folder of AddDirs goes to --add-dir, so the tools of
//     the CLI read and write there, as they do in Dir.
//   - Effort goes to --effort. [ai.ThinkingOff] turns thinking off.
//     AutoCompact goes to --autocompact, and the CLI takes 100k to 1M.
//     Without them, the CLI uses its defaults.
//   - Mode goes to --permission-mode. Without a Mode, the settings of
//     the CLI decide.
//   - Limits go in the settings of --settings:
//     permissions.blockReadsOutsideWorkingDirectories,
//     permissions.blockWritesOutsideWorkingDirectories, and
//     sandbox.enabled. The CLI adds them to its own settings.
//   - The agent calls Sections before each run, and appends the
//     sections to the system prompt of the CLI. The CLI keeps its own
//     prompt, because its tools depend on it. The first sections of a
//     session go in the system prompt, and the log keeps them as its
//     base, so each start of the CLI gets the same prompt. A later
//     change goes as a reminder before the input of the next run, so
//     the cache of the conversation stays. If Sections returns an
//     error, the run fails.
//   - The names of the Tools that ToolNames keeps go to --tools, with
//     ToolSearch, because the CLI loads the deferred MCP tools with it.
//     Without Tools, the CLI has no built-in tool. The agent skips a
//     well-known tool that Claude Code does not have, such as
//     apply_patch, and a tool of the caller, because the CLI runs only
//     its own tools. A tool of the caller that ToolNames names fails
//     the start. With ToolNames, a hook blocks the MCP tools that
//     ToolNames does not name.
//   - Each Deny rule with no content, such as "web_fetch", goes to
//     --disallowedTools. The CLI gets no rule with content. The CLI
//     reads such a rule in its own way, so the hooks of the caller
//     check it, as the host does.
//   - MCP goes to --mcp-config. The CLI connects to each server and
//     names each tool "mcp__<server>__<tool>". The servers of the
//     settings of the CLI stay, so a server of MCP replaces a server
//     of the settings with the same name.
//   - Approve answers each permission prompt of the CLI. The call of
//     the request has the tool_use_id, the tool name, and the input of
//     the prompt. The request has the reason of the CLI. It has Safety
//     for an edit of a protected path. If Approve returns an error, the
//     agent sends the error to the CLI, which denies the call. Approve
//     also answers a hook that returns an [agent.AskError]. The agent
//     keeps no rule for an Always decision, and the CLI keeps none. The
//     caller keeps the rule of Always, as the host does.
//   - Idle gets the events between runs: the events of a subagent in
//     the background, wrapped as in a run. Without Idle, the agent drops
//     them, and the log of each child still gets its messages. The
//     agent calls Wake when the CLI starts a turn between runs.
//   - The agent loads Defs and writes the skills and the agents to a
//     folder for each session in the folder gopi-defs of [os.TempDir]. It gives the folder to the
//     CLI with --add-dir at each start of the CLI, so an edit reaches
//     the next process, and it removes the folder at Close. A process
//     that dies leaves its folder until the next start. The CLI lists
//     each skill and loads its body, so the agent adds no text for
//     Skills. A name of Skills that no skill has fails [Durable]. A
//     skill name or an agent name with a slash fails the start of the
//     CLI, because the name is a file name. The agent does not read the
//     instructions of Defs. The CLI also keeps the skills and the agents
//     of its own folders. A subagent gets the model ID of its spec only if the
//     spec has the account of Spec. Each other model of a file gives
//     the subagent the model of the parent.
//   - If the first text of a user turn is a slash call of a skill, the
//     agent moves that text after the files of the turn, because the
//     CLI loads a skill only from the last text block.
//   - The agent ignores Compaction, because the CLI compacts its own
//     session.
//
// # Hooks
//
// The CLI calls the Hooks of the config at its own hook events, over
// the control protocol:
//
//   - BeforeTool at PreToolUse. An error denies the call with its text.
//     Approve answers an [agent.AskError] before the hook answers the
//     CLI, as [agent.RunBeforeTool] says. The CLI runs the call when a
//     hook times out. So the callback at PreToolUse has a time limit of
//     24 hours, and the other callbacks keep the limit of the CLI. A
//     new input replaces the input. The CLI cannot rename a call, so a
//     new name denies it.
//   - AfterTool at PostToolUse. The result has the text of the
//     tool_response. A built-in tool such as Bash gives a JSON object,
//     so its result is the JSON of that object. A new text replaces the
//     output that the model reads. If the tool_response is an object
//     and the new text is valid JSON, the CLI gets the JSON. Otherwise
//     the CLI gets a string. The CLI ignores an output whose shape is
//     not the shape of the tool_response. So a hook must keep the JSON
//     shape of a built-in tool. An error adds its text to the result.
//   - AfterTool also at PostToolUseFailure, after a call that fails.
//     The result is an error with the error text of the CLI. The CLI
//     cannot replace the output of a failed call. So the model reads
//     the error, and a new text or the text of an error comes after it
//     as context.
//   - AfterTurn at PostToolBatch, after a turn with tool calls only.
//     The text of the entries reaches the model before its next call.
//   - BeforeStop at Stop. The text of the entries continues the run.
//
// Each hook gets the state of [Agent.State], with the log of the agent.
// A hook gives only text entries. An error of AfterTurn or BeforeStop
// stops the CLI and fails the run. A hook that uses BeforeCall or
// AfterCall, or that implements no point, fails [Durable] and each run
// of an agent of [New]. The error for a point wraps
// [errors.ErrUnsupported].
//
// The reasons are in docs/concepts/agent/cli-agent.md,
// docs/concepts/agent/cli-steer.md, docs/concepts/agent/cli-request.md,
// docs/concepts/agent/subagent.md, and docs/concepts/tool/name.md.
package claude
