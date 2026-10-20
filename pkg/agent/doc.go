// Package agent is the loop that runs a model with tools until the work
// is done. An [Agent] calls the model, runs the tools that the model
// calls, and stores every entry in its [session.Log] as the entry
// completes.
//
// [New] returns an agent over a log in memory, and does no I/O.
// [Durable] returns an agent over a session in a [session.Storer]: it
// creates the session, or resumes it after its last entry. A crash
// loses at most the message in flight.
//
// Each run changes the [State] of the agent one [Event] at a time:
// the loop applies the event to the state, then sends it on the stream
// of the run. So a viewer that applies the same events to a snapshot of
// the state gets the same state. The hooks read the same state.
//
// A [Driver] starts an agent of one kind: the loop of the harness, or
// an agent CLI. Each driver calls the hook points that its agent has,
// and rejects a hook at another point:
//
//	             harness   claude-code          codex
//	BeforeCall   yes       no                   no
//	AfterCall    yes       no                   no
//	BeforeTool   yes       PreToolUse           PreToolUse (1)
//	AfterTool    yes       PostToolUse (2)(3)   PostToolUse (1)(2)
//	AfterTurn    yes       PostToolBatch        no
//	BeforeStop   yes       Stop                 Stop
//
// A CLI has no hook at a model call. Claude Code calls AfterTurn only
// after a turn with tool calls. Codex runs a hook through the gopi
// binary, and runs the tool if the hook fails. If the binary is
// missing, the run fails. A hook of a CLI agent
// gives text entries only, and cannot rename a tool call.
//
//  1. Codex calls no hook for a hosted tool, such as web search.
//  2. On a CLI agent, the model reads only the text of the result that
//     AfterTool returns.
//  3. Claude Code also calls AfterTool after a failed call, at
//     PostToolUseFailure. The model reads the original error, and the
//     text of the hook comes after it as context.
//
// A caller must obey these rules:
//
//   - Runs on one agent are sequential. A second Run fails on its
//     stream, with no event.
//   - Every run that starts ends with agent_end, also a run that fails.
//   - Each message of a run arrives as one message_end, after the log
//     stores it. The input of a run arrives after the first turn_start.
//     A custom entry gets no event. If the store fails, the run fails.
//   - An ephemeral entry reaches the model in its run. The store does
//     not keep it, and agent_end removes it from the state.
//   - The results of the tool calls of one message are in the order in
//     which the calls end. The log stores each result as its call ends.
//   - If a run stops before a tool call ends, the call has no result.
//     The next run first adds a [session.Interrupted] result for it.
//   - A Go error from a [tool.Tool] goes to the model as an error
//     result, and the run goes on. To stop a run, cancel its context.
//   - If a failure cuts an assistant message short, the log keeps the
//     part that arrived, with the stop reason [ai.StopReasonError] or
//     [ai.StopReasonAborted].
//   - A deferred tool waits until the search of the agent loads it.
//     The next model call declares the change, and the log keeps it.
//   - With [System], a change to a section is a system message in the
//     log.
//   - [Agent.Steer] and [Agent.FollowUp] queue entries for an active
//     run, and they do not start one.
//   - A [Hook] runs at the points of the loop that it implements, in
//     the order of registration. Each point is its own interface,
//     such as [BeforeToolHook]. A hook must embed [BaseHook].
//     [BeforeCall], [AfterCall], [BeforeTool], [AfterTool],
//     [AfterTurn], and [BeforeStop] add a hook at one point. An
//     AfterTool method must be safe for concurrent use. A hook reads
//     the session and sets values through [State.Log]. It adds entries
//     from AfterTurn and BeforeStop, or queues them with [State.Steer]
//     and [State.FollowUp]. On a state that a viewer decoded, Steer and
//     FollowUp do nothing.
//   - A caller must not change a message of a [State].
//
// The reasons are in docs/concepts/agent/loop.md,
// docs/concepts/agent/durability.md,
// docs/concepts/agent/run-stream.md, docs/concepts/agent/state.md,
// docs/concepts/agent/tool-change.md, and
// docs/concepts/agent/section.md.
package agent
