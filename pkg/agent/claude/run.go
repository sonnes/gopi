package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
)

// interruptTimeout is how long a cancelled run waits for the result of
// the CLI after the interrupt request. Then the agent kills the CLI,
// and the next run starts a new one.
const interruptTimeout = 5 * time.Second

// emptyInput is the input of a tool call whose input the CLI did not
// stream.
const emptyInput = "{}"

// runner maps the lines of one run to events and log entries. The
// runner of a subagent maps the lines of the child to the log of the
// child, and has no process.
type runner struct {
	a    *Agent
	proc *process
	em   *emit.Emitter
	// reminder is the change of the sections, as a text block before
	// the input. The log keeps the change as a system message, and the
	// input without the reminder.
	reminder string
	// sessionID is the ID of the session of em, or "" if it is unsaved.
	sessionID string
	// subs tracks the subagents. The runner of the run and the runner of
	// each child share it.
	subs *subagents

	// cur is the API message in flight. The CLI sends one assistant
	// line per content block, so the runner collects the blocks until
	// the message ends.
	cur *apiMessage
	// blocks are the streamed content blocks of cur, by the index of
	// the API. A block that gopi does not keep has no entry.
	blocks map[int]*block
	// kept counts the kept blocks of cur. It is the local index of the
	// next kept block.
	kept int
	// interrupted is true after the runner sends the interrupt request.
	interrupted bool

	// pending are the steers that the CLI did not take yet. steered is
	// true after the first steer. segments is the usage of the segments
	// that a steer ended.
	pending  []pendingSteer
	steered  bool
	segments ai.Usage

	// input is the input of a run that waits for the end of the turn
	// that the CLI started by itself.
	input []session.Entry
	// moves are the requests that move a subagent to the background
	// before a steer, by request ID. held are the steers that wait for
	// them, until moveDeadline.
	moves        map[string]bool
	held         []session.Entry
	moveDeadline <-chan time.Time
}

// isChild reports whether r is the runner of a child. The runner of a
// child has no process.
func (r *runner) isChild() bool { return r.proc == nil }

// apiMessage is one assistant message of the Messages API, as the
// assistant lines of the CLI build it.
type apiMessage struct {
	id      string
	model   string
	content []ai.Content
	usage   ai.Usage
	stop    ai.StopReason
}

// block is one content block that the CLI streams and gopi keeps.
type block struct {
	kind string
	// index is the index of the block in the partial message. It counts
	// only the kept blocks, as the final content does.
	index int
	// text collects the text of a text or thinking block for its end
	// event. input collects the input of a tool call, because the
	// partial message holds a tool call only when it is complete.
	text  strings.Builder
	input strings.Builder
	id    string
	name  string
}

// run applies the lines that the reader between runs kept, then sends
// the input to the CLI, and reads its lines until the result.
//
// With no input, it is the run of Continue: it sends nothing, and the
// kept lines are the turn that the CLI started by itself. The result of
// that turn ends the run. If the kept lines have no turn, the run ends
// at once.
//
// With input, the kept turn ends only a segment. If that turn is still
// open, the input waits for its result, so that the CLI gets no input
// in the middle of a turn.
func (r *runner) run(
	ctx context.Context,
	entries []session.Entry,
	kept []*line,
) ([]ai.Message, error) {
	if len(entries) == 0 {
		if !slices.ContainsFunc(kept, rootLine) {
			return r.abort(ctx, ai.Usage{}, nil)
		}
		wake, err := session.Custom(agent.KindWake, nil)
		if err == nil {
			err = r.em.Input(ctx, []session.Entry{wake})
		}
		if err != nil {
			return r.abort(ctx, ai.Usage{}, err)
		}
		for i, l := range kept {
			msgs, end, err := r.handle(ctx, l)
			if end {
				// The lines after the result are for the next run.
				r.a.requeue(kept[i+1:])
				return msgs, err
			}
			if err != nil {
				return r.fail(ctx, err)
			}
		}
	} else {
		for _, l := range kept {
			if err := r.replay(ctx, l); err != nil {
				return r.fail(ctx, err)
			}
		}
		if turnOpen(kept) {
			r.input = entries
		} else if err := r.sendInput(ctx, entries); err != nil {
			return r.fail(ctx, err)
		}
	}

	var (
		done     = ctx.Done()
		deadline <-chan time.Time
	)
	for {
		select {
		case <-done:
			done = nil
			r.interrupted = true
			if _, err := r.proc.request(outRequestBody{Subtype: "interrupt"}); err != nil {
				return r.fail(ctx, ctx.Err())
			}
			timer := time.NewTimer(interruptTimeout)
			defer timer.Stop()
			deadline = timer.C

		case <-deadline:
			return r.fail(ctx, ctx.Err())

		case <-r.moveDeadline:
			if err := r.sendHeld(); err != nil {
				return r.fail(ctx, err)
			}

		case <-r.a.steerReady:
			if err := r.sendSteer(ctx); err != nil {
				return r.fail(ctx, err)
			}

		case data, ok := <-r.proc.lines():
			if !ok {
				err := r.proc.exited()
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				return r.fail(ctx, err)
			}
			l := parseLine(data)
			if l == nil {
				continue
			}
			msgs, end, err := r.handle(ctx, l)
			if end {
				return msgs, err
			}
			if err != nil {
				return r.fail(ctx, err)
			}
		}
	}
}

// sendInput stores the input of the run, and sends it to the CLI.
func (r *runner) sendInput(ctx context.Context, entries []session.Entry) error {
	if err := r.em.Input(ctx, entries); err != nil {
		return err
	}
	input := emit.Input(entries)
	if r.reminder != "" {
		input = append([]ai.Content{ai.Text{Text: r.reminder}}, input...)
	}
	return r.proc.send(outUser{
		Type: "user",
		Message: outUserMessage{
			Role:    "user",
			Content: blocks(input),
		},
	})
}

// The media types of the files that the CLI reads. An image goes in an
// image block, and a PDF in a document block.
var (
	imageTypes = []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	pdfType    = "application/pdf"
)

// blocks returns the content blocks of a user line for the input. A
// file that the CLI does not read becomes a text note.
func blocks(input []ai.Content) []outBlock {
	out := make([]outBlock, 0, len(input))
	for _, c := range input {
		switch c := c.(type) {
		case ai.Text:
			out = append(out, textBlock(c))
		case ai.File:
			out = append(out, fileBlock(c))
		}
	}
	return out
}

// textBlock returns the text block of t.
func textBlock(t ai.Text) outBlock {
	return outBlock{
		Type: "text",
		Text: t.Text,
	}
}

// fileBlock returns the image or document block of f, or a text note if
// the CLI does not read its media type.
func fileBlock(f ai.File) outBlock {
	kind := ""
	switch {
	case slices.Contains(imageTypes, f.MediaType):
		kind = "image"
	case f.MediaType == pdfType:
		kind = "document"
	default:
		return textBlock(emit.Unread(f))
	}
	return outBlock{
		Type: kind,
		Source: &outSource{
			Type:      "base64",
			MediaType: f.MediaType,
			Data:      base64.StdEncoding.EncodeToString(f.Data),
		},
	}
}

// fail ends a run that cannot reach the result, and stops the CLI.
// The next run starts a new CLI, which resumes the session. The
// messages that the run stored are still its result, and their usage
// is the usage of the run.
func (r *runner) fail(ctx context.Context, err error) ([]ai.Message, error) {
	r.a.drop(r.proc)
	err = errors.Join(err, r.subs.release(ctx, false))
	return r.em.Finish(ctx, r.em.Usage(), err)
}

// abort ends a run with an error, and the CLI keeps running. The
// messages that the run stored are still its result.
func (r *runner) abort(ctx context.Context, u ai.Usage, err error) ([]ai.Message, error) {
	err = errors.Join(err, r.subs.release(ctx, true))
	return r.em.Finish(ctx, u, err)
}

// parseLine returns the line of data, or nil if data is not a line.
func parseLine(data []byte) *line {
	var l line
	if err := json.Unmarshal(data, &l); err != nil {
		return nil
	}
	return &l
}

// replay applies a line that the reader between runs kept, in a run
// with input. The kept lines are turns that the CLI started by itself,
// so the result of each one ends only a segment.
func (r *runner) replay(ctx context.Context, l *line) error {
	if l.Type == "result" {
		return r.endSegment(ctx, l)
	}
	_, _, err := r.handle(ctx, l)
	return err
}

// endSegment ends a segment of the run at a result line that does not
// end the run.
func (r *runner) endSegment(ctx context.Context, l *line) error {
	r.segments = r.segments.Add(usage(&l.Usage))
	return r.finish(ctx)
}

// handle applies one line. It reports end at the result of the run,
// with the result of the stream. An error without end fails the run.
//
// After an interrupt, the runner applies each line as before, so that
// the log keeps each block that the CLI completed. The emitter stores
// without the cancel of ctx.
func (r *runner) handle(
	ctx context.Context,
	l *line,
) (msgs []ai.Message, end bool, err error) {
	// A line with a parent tool call is from a subagent. Its lines go
	// to the log of the child, and never to the log of the run.
	if l.ParentToolUseID != nil {
		return nil, false, r.subs.line(ctx, *l.ParentToolUseID, l)
	}

	switch l.Type {
	case "control_request":
		return nil, false, r.proc.answer(ctx, l)

	case "control_response":
		return nil, false, r.moved(l)

	case "system":
		switch l.Subtype {
		case "init":
			return nil, false, r.saveSession(ctx, l.SessionID)
		case "task_started":
			return nil, false, r.subs.taskStarted(ctx, l)
		case "task_notification":
			return nil, false, r.subs.taskEnded(ctx, l)
		}

	case "stream_event":
		if l.Event == nil {
			return nil, false, nil
		}
		return nil, false, r.stream(ctx, l.Event)

	case "assistant":
		if l.Message == nil {
			return nil, false, nil
		}
		return nil, false, r.assistant(ctx, l.Message)

	case "user":
		if l.IsReplay {
			return nil, false, r.replayed(ctx, l)
		}
		if l.Message == nil {
			return nil, false, nil
		}
		return nil, false, r.user(ctx, l.Message)

	case "result":
		// The turn that the CLI started by itself ends before the
		// input of the run goes.
		if r.input != nil || r.segmentEnded(l) {
			if err := r.endSegment(ctx, l); err != nil {
				return nil, false, err
			}
			if r.input == nil {
				return nil, false, nil
			}
			entries := r.input
			r.input = nil
			return nil, false, r.sendInput(ctx, entries)
		}
		msgs, err := r.result(ctx, l)
		return msgs, true, err
	}
	return nil, false, nil
}

// stream applies one event of the Messages API stream.
func (r *runner) stream(ctx context.Context, ev *streamEvent) error {
	switch ev.Type {
	case "message_start":
		if err := r.finish(ctx); err != nil {
			return err
		}
		r.cur = &apiMessage{}
		if ev.Message != nil {
			r.cur.id = ev.Message.ID
			r.cur.model = ev.Message.Model
			r.cur.usage = usage(ev.Message.Usage)
		}
		r.blocks = map[int]*block{}
		r.kept = 0
		r.em.Begin()

	case "content_block_start":
		r.blockStart(ev)

	case "content_block_delta":
		r.blockDelta(ev)

	case "content_block_stop":
		r.blockStop(ev.Index)

	case "message_delta":
		if r.cur == nil {
			return nil
		}
		r.cur.stop = stopReason(ev.Delta.StopReason)
		if ev.Usage != nil {
			r.cur.usage = usage(ev.Usage)
		}

	case "message_stop":
		return r.finish(ctx)
	}
	return nil
}

// blockStart opens a streamed content block.
func (r *runner) blockStart(ev *streamEvent) {
	if ev.ContentBlock == nil {
		return
	}
	var t ai.EventType
	switch ev.ContentBlock.Type {
	case "text":
		t = ai.EventTextStart
	case "thinking":
		t = ai.EventThinkStart
	case "tool_use":
		t = ai.EventToolStart
	default:
		return
	}
	if r.blocks == nil {
		r.blocks = map[int]*block{}
	}
	b := &block{
		kind:  ev.ContentBlock.Type,
		index: r.kept,
		id:    ev.ContentBlock.ID,
		name:  ev.ContentBlock.Name,
	}
	r.kept++
	r.blocks[ev.Index] = b
	r.em.Update(ai.Event{
		Type:         t,
		ContentIndex: b.index,
	})
}

// blockDelta applies a delta to a streamed content block. A delta with
// no text sends no event.
func (r *runner) blockDelta(ev *streamEvent) {
	b := r.blocks[ev.Index]
	if b == nil {
		return
	}
	var (
		t     ai.EventType
		delta string
	)
	switch ev.Delta.Type {
	case "text_delta":
		t = ai.EventTextDelta
		delta = ev.Delta.Text
		b.text.WriteString(delta)
	case "thinking_delta":
		t = ai.EventThinkDelta
		delta = ev.Delta.Thinking
		b.text.WriteString(delta)
	case "input_json_delta":
		t = ai.EventToolDelta
		delta = ev.Delta.PartialJSON
		b.input.WriteString(delta)
	default:
		return
	}
	if delta == "" {
		return
	}
	r.em.Update(ai.Event{
		Type:         t,
		ContentIndex: b.index,
		Delta:        delta,
	})
}

// blockStop closes a streamed content block. The end event of a text
// or thinking block carries the full text of the block.
func (r *runner) blockStop(index int) {
	b := r.blocks[index]
	if b == nil {
		return
	}
	evt := ai.Event{ContentIndex: b.index}
	switch b.kind {
	case "text":
		evt.Type = ai.EventTextEnd
		evt.Content = b.text.String()
	case "thinking":
		evt.Type = ai.EventThinkEnd
		evt.Content = b.text.String()
	case "tool_use":
		input := b.input.String()
		if input == "" {
			input = emptyInput
		}
		evt.Type = ai.EventToolEnd
		evt.ToolCall = &ai.ToolCall{
			ID:    b.id,
			Name:  b.name,
			Input: json.RawMessage(input),
		}
	default:
		return
	}
	r.em.Update(evt)
}

// assistant adds the blocks of an assistant line to the message in
// flight. A line with a new message ID ends the message before it.
func (r *runner) assistant(ctx context.Context, m *wireMessage) error {
	newMessage := r.cur != nil && m.ID != "" && r.cur.id != "" && r.cur.id != m.ID
	if newMessage {
		if err := r.finish(ctx); err != nil {
			return err
		}
	}
	if r.cur == nil {
		r.cur = &apiMessage{id: m.ID}
	}
	if m.Model != "" {
		r.cur.model = m.Model
	}
	if r.cur.usage == (ai.Usage{}) {
		r.cur.usage = usage(m.Usage)
	}
	for _, b := range m.blocks() {
		c := content(b)
		if c != nil {
			r.cur.content = append(r.cur.content, c)
		}
	}
	return nil
}

// content returns the gopi block of an assistant block, or nil for a
// block that gopi does not keep.
func content(b wireBlock) ai.Content {
	switch b.Type {
	case "text":
		return ai.Text{Text: b.Text}
	case "thinking":
		return ai.ThinkingBlock{
			Thinking:  b.Thinking,
			Signature: b.Signature,
		}
	case "tool_use":
		input := b.Input
		if len(input) == 0 {
			input = json.RawMessage(emptyInput)
		}
		return ai.ToolCall{
			ID:    b.ID,
			Name:  b.Name,
			Input: input,
		}
	}
	return nil
}

// finish stores the message in flight and starts its tool calls. A
// message with no content that gopi keeps is closed and not stored, so
// the next message gets its own message_start. After an interrupt, the
// message in flight has only the blocks that the CLI completed, and it
// gets the stop reason [ai.StopReasonAborted].
func (r *runner) finish(ctx context.Context) error {
	m := r.cur
	r.cur = nil
	r.blocks = nil
	r.kept = 0
	if m == nil {
		return nil
	}
	if len(m.content) == 0 {
		r.em.Drop()
		return nil
	}

	msg := ai.Message{
		Role:       ai.RoleAssistant,
		Content:    m.content,
		Model:      m.model,
		Usage:      m.usage,
		StopReason: m.stop,
	}
	calls := msg.ToolCalls()
	cutShort := msg.StopReason == "" && r.interrupted
	switch {
	case len(calls) > 0:
		msg.StopReason = ai.StopReasonToolUse
	case cutShort:
		msg.StopReason = ai.StopReasonAborted
	case msg.StopReason == "":
		msg.StopReason = ai.StopReasonStop
	}
	if err := r.em.End(ctx, msg); err != nil {
		return err
	}
	for _, tc := range calls {
		r.em.ToolStart(tc)
		if err := r.subs.callStarted(ctx, r, tc); err != nil {
			return err
		}
	}
	return nil
}

// user applies a user line: its tool results, then its text. The
// emitter drops a result for a call that it did not start.
func (r *runner) user(ctx context.Context, m *wireMessage) error {
	if err := r.finish(ctx); err != nil {
		return err
	}
	if err := r.toolResults(ctx, m); err != nil {
		return err
	}
	return r.userText(ctx, m)
}

// stopFeedbackPrefix starts the text of the user line that the CLI
// writes when a Stop hook continues the run.
const stopFeedbackPrefix = "Stop hook feedback:"

// userText stores the feedback of a Stop hook in a user line as a user
// message. The CLI also writes other text in user lines, such as the
// body of a skill or the summary of a compaction. The log does not keep
// that text. After an interrupt, userText stores nothing.
func (r *runner) userText(ctx context.Context, m *wireMessage) error {
	if r.interrupted {
		return nil
	}
	var texts []ai.Content
	for _, b := range m.blocks() {
		feedback := b.Type == "text" && strings.HasPrefix(b.Text, stopFeedbackPrefix)
		if feedback {
			texts = append(texts, ai.Text{Text: b.Text})
		}
	}
	if len(texts) == 0 {
		return nil
	}
	msg := ai.Message{
		Role:    ai.RoleUser,
		Content: texts,
	}
	return r.em.Input(ctx, []session.Entry{session.NewEntry(msg)})
}

// toolResults stores the tool_result blocks of a user line. The emitter
// drops a result for a call that is not open.
func (r *runner) toolResults(ctx context.Context, m *wireMessage) error {
	for _, b := range m.blocks() {
		if b.Type != "tool_result" {
			continue
		}
		result := ai.ToolResult{
			Content: toolContent(b.Content),
			IsError: b.IsError,
		}
		r.subs.callEnded(b.ToolUseID, result)
		if _, err := r.em.ToolEnd(ctx, b.ToolUseID, result); err != nil {
			return err
		}
	}
	return nil
}

// toolContent returns the text of a tool result. The content is a
// string or a list of blocks. Blocks that are not text are skipped.
func toolContent(raw json.RawMessage) []ai.Content {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []ai.Content{ai.Text{Text: s}}
	}
	var blocks []wireBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil
	}
	var out []ai.Content
	for _, b := range blocks {
		if b.Type == "text" {
			out = append(out, ai.Text{Text: b.Text})
		}
	}
	return out
}

// result ends the run at the result line of the CLI.
func (r *runner) result(ctx context.Context, l *line) ([]ai.Message, error) {
	u := usage(&l.Usage).Add(r.segments)
	if err := r.saveSession(ctx, l.SessionID); err != nil {
		return r.abort(ctx, u, err)
	}
	if err := r.finish(ctx); err != nil {
		return r.abort(ctx, u, err)
	}
	if err := r.subs.release(ctx, true); err != nil {
		return r.abort(ctx, u, err)
	}
	if r.interrupted {
		return r.abort(ctx, u, ctx.Err())
	}
	if err := r.proc.takeHookErr(); err != nil {
		return r.abort(ctx, u, err)
	}

	failed := l.IsError || l.Subtype != "success"
	if failed {
		return r.abort(ctx, u, resultError(l))
	}
	return r.em.Finish(ctx, u, nil)
}

// loginText is in the result of a CLI that is not logged in.
const loginText = "Please run /login"

// resultError returns the error of a result line that reports a
// failure. A result that asks for a login is [ErrAuth].
func resultError(l *line) error {
	text := strings.Join(l.Errors, "; ")
	if l.Subtype == "success" {
		text = l.Result
	}
	if strings.Contains(text, loginText) {
		return fmt.Errorf("%w: %s", ErrAuth, text)
	}
	if text == "" {
		return fmt.Errorf("claude: %s", l.Subtype)
	}
	return fmt.Errorf("claude: %s: %s", l.Subtype, text)
}

// saveSession stores the session ID of the CLI, so that a later CLI
// resumes it.
func (r *runner) saveSession(ctx context.Context, id string) error {
	return conversation.Save(ctx, r.a.log, id)
}

// usage converts the usage of the CLI. A nil usage is zero.
func usage(u *wireUsage) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	return ai.Usage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
	}
}

// stopReason converts a stop reason of the Messages API. An unknown or
// empty reason is empty, and the message gets a default.
func stopReason(s string) ai.StopReason {
	switch s {
	case "end_turn", "stop_sequence", "pause_turn":
		return ai.StopReasonStop
	case "tool_use":
		return ai.StopReasonToolUse
	case "max_tokens", "model_context_window_exceeded":
		return ai.StopReasonLength
	case "refusal":
		return ai.StopReasonRefusal
	}
	return ""
}
