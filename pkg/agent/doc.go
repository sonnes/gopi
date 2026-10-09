// Package agent is the loop that runs a model with tools until the work
// is done. An [Agent] calls the model, runs the tools that the model
// calls, and adds every message to its history in memory as the message
// completes.
//
// [New] returns an agent and does no I/O. The [History] option gives
// the agent the messages of an earlier conversation. The agent keeps
// its history for its life. To save a conversation, the caller saves
// the message of each message_end event.
//
// Each run changes the [State] of the agent one [Event] at a time:
// the loop applies the event to the state, then sends it on the stream
// of the run. So a viewer that applies the same events to a snapshot of
// the state gets the same state. The hooks read the same state.
//
// A caller must obey these rules:
//
//   - Runs on one agent are sequential. A second Run fails on its
//     stream, with no event.
//   - Every run that starts ends with agent_end, also a run that fails.
//   - Each message of the history arrives as one message_end. The
//     input of a run arrives after the first turn_start.
//   - A Go error from a [tool.Tool] goes to the model as an error
//     result, and the run goes on. To stop a run, cancel its context.
//   - If a failure cuts an assistant message short, the history keeps
//     the part that arrived, with the stop reason [ai.StopReasonError]
//     or [ai.StopReasonAborted].
//   - A deferred tool waits until the search of the agent loads it.
//     The next model call declares the change, and the history keeps
//     it.
//   - With [System], a change to a section is a system message in the
//     history.
//   - [Agent.Steer] and [Agent.FollowUp] queue messages for an active
//     run, and they do not start one.
//   - A [Hook] runs at six points of the loop, in the order of
//     registration. A hook must embed [BaseHook]. [BeforeCall],
//     [AfterCall], [BeforeTool], [AfterTool], [AfterTurn], and
//     [BeforeStop] add a hook at one point. An AfterTool method must be
//     safe for concurrent use.
//   - A caller must not change a message of a [State].
//
// The reasons are in docs/concepts/agent/loop.md,
// docs/concepts/agent/run-stream.md, docs/concepts/agent/state.md,
// docs/concepts/agent/tool-change.md, and
// docs/concepts/agent/section.md.
package agent
