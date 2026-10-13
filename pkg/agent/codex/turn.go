package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sonnes/gopi/pkg/agent/internal/emit"
	"github.com/sonnes/gopi/pkg/ai"
)

// interruptTimeout is how long a cancelled run waits for the turn to
// stop after turn/interrupt. Then the agent kills the process, and the
// next Run starts a new one.
const interruptTimeout = 5 * time.Second

// The names of the tools that stand for the tool items of the CLI.
const (
	toolBash       = "bash"
	toolApplyPatch = "apply_patch"
	toolWebSearch  = "web_search"
)

// mcpSeparator joins the server and the tool of an MCP tool call into
// one tool name.
const mcpSeparator = "__"

// reasoningSeparator joins the parts of a reasoning item.
const reasoningSeparator = "\n\n"

// unfinishedText is the result of a tool call whose item did not
// complete before the end of the turn.
const unfinishedText = "the turn ended before the tool completed"

// The notifications that a turn uses.
const (
	methodTurnCompleted  = "turn/completed"
	methodTokenUsage     = "thread/tokenUsage/updated"
	methodItemStarted    = "item/started"
	methodItemCompleted  = "item/completed"
	methodTextDelta      = "item/agentMessage/delta"
	methodReasoningDelta = "item/reasoning/textDelta"
	methodSummaryDelta   = "item/reasoning/summaryTextDelta"
)

// usedMethods are the notifications that a turn decodes. It ignores
// every other notification.
var usedMethods = map[string]bool{
	methodTurnCompleted:  true,
	methodTokenUsage:     true,
	methodItemStarted:    true,
	methodItemCompleted:  true,
	methodTextDelta:      true,
	methodReasoningDelta: true,
	methodSummaryDelta:   true,
}

// turn is one Codex turn of a run. It maps the items of the turn to
// the events and messages of the emitter. The emitter holds the open
// assistant message.
//
// One item is active at a time: an agent message, a reasoning item, or
// a tool. The active item streams live. The notifications of every
// other item wait in a queue until the active item completes. Then the
// first queued item becomes active, and its notifications replay. So
// the items reach the emitter in sequence, and tools that the CLI runs
// in parallel look sequential to the caller.
//
// Consecutive text and reasoning items make one assistant message.
// When a tool item starts, its call joins the open message, the
// message is stored, and the tool starts at once. So the caller sees
// the tool while it runs, and a cancel keeps the text and the call.
type turn struct {
	conn     *conn
	emitter  *emit.Emitter
	threadID string
	model    string
	effort   string

	// turnID is the ID of the turn, from the response to turn/start.
	turnID string
	// early holds the notifications that arrive before the turn ID is
	// known.
	early []message

	// active is the ID of the active item, or "".
	active string
	// queue holds the notifications of the items that wait.
	queue []note
	// started and done hold the IDs of the items that started and
	// completed, so that a repeated notification does nothing.
	started map[string]bool
	done    map[string]bool
	// call is the stored call of the active tool item, or nil.
	call *ai.ToolCall

	// index maps an item ID to its block in the open message.
	index map[string]int
	// blocks is the number of blocks in the open message.
	blocks int
	// parts maps a reasoning item ID to the part of its last delta.
	parts map[string]part

	usage ai.Usage
	// total is the total usage of the thread in the last token update,
	// so that a repeated update adds nothing.
	total tokenBreakdown
	// dead is true if the process must not serve the next run, because
	// the server can still send notifications of this turn.
	dead bool
}

// note is one notification of the turn.
type note struct {
	method string
	params params
	// item is the decoded item of item/started and item/completed.
	item *item
}

// itemID returns the ID of the item of n, or "".
func (n note) itemID() string {
	if n.item != nil {
		return n.item.ID
	}
	return n.params.ItemID
}

// part names one part of a reasoning item: a summary part or a raw
// content part, and its index.
type part struct {
	summary bool
	index   int
}

// run starts the turn with input and reads until the turn completes. It
// returns the usage of the turn. After a cancel, it stores with a
// context that is not cancelled, so that a finished tool keeps its
// result.
func (t *turn) run(ctx context.Context, input []userInput) (ai.Usage, error) {
	store := ctx
	t.started = map[string]bool{}
	t.done = map[string]bool{}
	startID, err := t.conn.request("turn/start", turnStartParams{
		ThreadID: t.threadID,
		Input:    input,
		Effort:   t.effort,
	})
	if err != nil {
		t.dead = true
		return t.usage, err
	}

	var (
		cancelled bool
		cancel    = ctx.Done()
		deadline  <-chan time.Time
	)
	// finish returns the result of a run that ends.
	finish := func(err error) (ai.Usage, error) {
		if err == nil && cancelled {
			err = ctx.Err()
		}
		return t.usage, err
	}
	for {
		var m message
		select {
		case msg, ok := <-t.conn.inbox:
			if !ok {
				t.dead = true
				return t.usage, t.conn.exitErr()
			}
			m = msg
		case <-cancel:
			cancelled = true
			cancel = nil
			store = context.WithoutCancel(ctx)
			deadline = time.After(interruptTimeout)
			if err := t.interrupt(); err != nil {
				t.dead = true
				return t.usage, ctx.Err()
			}
			continue
		case <-deadline:
			t.dead = true
			return t.usage, ctx.Err()
		}

		if m.isResponse(startID) {
			if m.Error != nil {
				return t.usage, fmt.Errorf("codex: turn/start: %s", m.Error.Message)
			}
			var res turnResult
			if err := json.Unmarshal(m.Result, &res); err != nil {
				t.dead = true
				return t.usage, err
			}
			t.turnID = res.Turn.ID
			if cancelled {
				if err := t.interrupt(); err != nil {
					t.dead = true
					return t.usage, ctx.Err()
				}
			}
			early := t.early
			t.early = nil
			for _, em := range early {
				done, err := t.dispatch(store, em, cancelled)
				if done {
					return finish(err)
				}
			}
			continue
		}
		if t.turnID == "" {
			t.early = append(t.early, m)
			continue
		}
		done, err := t.dispatch(store, m, cancelled)
		if done {
			return finish(err)
		}
	}
}

// dispatch applies one notification of the turn. It ignores the
// notifications that the turn does not use, and the ones of other
// threads and turns. It reports done when the run ends: at
// turn/completed, or on an error.
//
// After a cancel, dispatch applies each notification as before, so that
// the log keeps each item that the CLI completes before the turn stops.
func (t *turn) dispatch(ctx context.Context, m message, cancelled bool) (bool, error) {
	if !usedMethods[m.Method] {
		return false, nil
	}
	var p params
	if err := json.Unmarshal(m.Params, &p); err != nil {
		if m.Method != methodTurnCompleted {
			return false, nil
		}
		// Without turn/completed, the run has no end.
		t.dead = true
		return true, fmt.Errorf("codex: %s: %w", m.Method, err)
	}
	if !t.ours(p) {
		return false, nil
	}
	if m.Method == methodTurnCompleted {
		return true, t.complete(ctx, p, cancelled)
	}
	n := note{
		method: m.Method,
		params: p,
	}
	if len(p.Item) > 0 {
		n.item = &item{}
		if err := json.Unmarshal(p.Item, n.item); err != nil {
			t.dead = true
			return true, fmt.Errorf("codex: %s: %w", m.Method, err)
		}
	}

	if err := t.handle(ctx, n); err != nil {
		// The server turn goes on, so its later notifications must not
		// reach the next run.
		t.dead = true
		return true, err
	}
	return false, nil
}

// ours reports whether a notification belongs to the thread and the
// turn of the run.
func (t *turn) ours(p params) bool {
	if p.ThreadID != "" && p.ThreadID != t.threadID {
		return false
	}
	turnID := p.turnOf()
	return turnID == "" || turnID == t.turnID
}

// interrupt asks the server to stop the turn. Before the response to
// turn/start, the turn ID is unknown, so interrupt waits for it.
func (t *turn) interrupt() error {
	if t.turnID == "" {
		return nil
	}
	_, err := t.conn.request("turn/interrupt", turnInterruptParams{
		ThreadID: t.threadID,
		TurnID:   t.turnID,
	})
	return err
}

// complete handles turn/completed. An active tool with no result gets
// an error result. A completed turn then replays the queue and stores
// its open message. A turn that fails or stops drops the queue, stores
// the blocks of the open message whose items completed, and returns an
// error.
func (t *turn) complete(ctx context.Context, p params, cancelled bool) error {
	if err := t.endUnfinished(ctx); err != nil {
		return err
	}
	var info turnInfo
	if p.Turn != nil {
		info = *p.Turn
	}
	if cancelled || info.Status != turnCompleted {
		t.queue = nil
	}
	if cancelled {
		return t.endCompleted(ctx, ai.StopReasonAborted)
	}
	if info.Status != turnCompleted {
		reason := ai.StopReasonError
		if info.Status == turnInterrupted {
			reason = ai.StopReasonAborted
		}
		if err := t.endCompleted(ctx, reason); err != nil {
			return err
		}
	}
	switch info.Status {
	case turnCompleted:
		for len(t.queue) > 0 {
			t.active = ""
			if err := t.next(ctx); err != nil {
				return err
			}
			if err := t.endUnfinished(ctx); err != nil {
				return err
			}
		}
		return t.end(ctx, ai.StopReasonStop)
	case turnFailed:
		msg := "the turn failed"
		if info.Error != nil {
			msg = info.Error.Message
		}
		unauthorized := info.Error != nil && string(info.Error.Info) == errorUnauthorized
		if unauthorized {
			return fmt.Errorf("%w: %s", ErrAuth, msg)
		}
		return errors.New("codex: " + msg)
	case turnInterrupted:
		return errors.New("codex: the turn was interrupted")
	default:
		return fmt.Errorf("codex: the turn completed with status %q", info.Status)
	}
}

// handle applies one notification of an item. If another item is
// active, the notification waits in the queue. When the active item
// completes, the next queued item becomes active.
func (t *turn) handle(ctx context.Context, n note) error {
	if n.method == methodTokenUsage {
		// The server can send the same update again. A repeated update
		// has the same total, so it adds nothing.
		tu := n.params.TokenUsage
		if tu != nil && tu.Total != t.total {
			t.total = tu.Total
			t.addUsage(tu.Last)
		}
		return nil
	}
	id := n.itemID()
	if id == "" || t.done[id] {
		return nil
	}
	if n.item != nil && !mapped(*n.item) {
		return nil
	}
	if t.active != "" && t.active != id {
		t.queue = append(t.queue, n)
		return nil
	}
	t.active = id
	if err := t.apply(ctx, n); err != nil {
		return err
	}
	if n.method != methodItemCompleted {
		return nil
	}
	t.done[id] = true
	t.active = ""
	return t.next(ctx)
}

// mapped reports whether the turn maps items of the type of it. An
// item of another type never becomes active, so it cannot hold back
// the items that follow it.
func mapped(it item) bool {
	if it.Type == itemAgentMessage || it.Type == itemReasoning {
		return true
	}
	_, ok := toolCall(it)
	return ok
}

// next replays the queue. The first queued item becomes active, and the
// notifications of the other items wait again.
func (t *turn) next(ctx context.Context) error {
	queue := t.queue
	t.queue = nil
	for _, n := range queue {
		if err := t.handle(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// apply applies one notification of the active item.
func (t *turn) apply(ctx context.Context, n note) error {
	p := n.params
	switch n.method {
	case methodItemStarted:
		if n.item == nil || t.started[n.item.ID] {
			return nil
		}
		t.started[n.item.ID] = true
		return t.itemStarted(ctx, *n.item)
	case methodItemCompleted:
		if n.item == nil {
			return nil
		}
		return t.itemCompleted(ctx, *n.item)
	case methodTextDelta:
		t.delta(p.ItemID, p.Delta, ai.EventTextStart, ai.EventTextDelta)
	case methodReasoningDelta:
		t.thinkDelta(p, part{index: p.ContentIndex})
	case methodSummaryDelta:
		t.thinkDelta(p, part{summary: true, index: p.SummaryIndex})
	}
	return nil
}

// itemStarted opens a text block for an agent message, or starts the
// tool of a tool item. A reasoning item gets its block with its first
// text, because it often has none.
func (t *turn) itemStarted(ctx context.Context, it item) error {
	if it.Type == itemAgentMessage {
		t.block(it.ID, ai.EventTextStart)
		return nil
	}
	tc, ok := toolCall(it)
	if !ok {
		return nil
	}
	return t.startTool(ctx, tc)
}

// itemCompleted sets the final text of a block, or ends a tool call.
// The completed item has the full text, so it corrects the text that
// the deltas built.
func (t *turn) itemCompleted(ctx context.Context, it item) error {
	switch it.Type {
	case itemAgentMessage:
		i := t.block(it.ID, ai.EventTextStart)
		t.emitter.Update(ai.Event{
			Type:         ai.EventTextEnd,
			ContentIndex: i,
			Content:      it.Text,
		})
		return nil
	case itemReasoning:
		text := reasoningText(it)
		i, open := t.index[it.ID]
		if text == "" {
			if !open {
				return nil
			}
			// The item has no text of its own, so the streamed text stays.
			text = t.thinking(i)
		}
		i = t.block(it.ID, ai.EventThinkStart)
		t.emitter.Update(ai.Event{
			Type:         ai.EventThinkEnd,
			ContentIndex: i,
			Content:      text,
		})
		return nil
	}

	tc, ok := toolCall(it)
	if !ok {
		return nil
	}
	if t.call == nil {
		if err := t.startTool(ctx, tc); err != nil {
			return err
		}
	}
	return t.endTool(ctx, toolResult(it))
}

// thinking returns the streamed text of the thinking block i of the
// open message.
func (t *turn) thinking(i int) string {
	p := t.emitter.Partial()
	if p == nil || i >= len(p.Content) {
		return ""
	}
	b, _ := p.Content[i].(ai.ThinkingBlock)
	return b.Thinking
}

// delta appends text to the block of an item.
func (t *turn) delta(id, text string, startType, deltaType ai.EventType) {
	i := t.block(id, startType)
	t.emitter.Update(ai.Event{
		Type:         deltaType,
		ContentIndex: i,
		Delta:        text,
	})
}

// thinkDelta appends a delta of a reasoning item. The delta of a new
// part starts with the separator. The final text joins the parts in
// the same way, so the streamed text matches it when the summary
// streams before the content.
func (t *turn) thinkDelta(p params, pt part) {
	if t.parts == nil {
		t.parts = map[string]part{}
	}
	prev, seen := t.parts[p.ItemID]
	text := p.Delta
	if seen && prev != pt {
		text = reasoningSeparator + text
	}
	t.parts[p.ItemID] = pt
	t.delta(p.ItemID, text, ai.EventThinkStart, ai.EventThinkDelta)
}

// block returns the index of the block of item id in the open message.
// If the item has no block, block adds one and sends its start event.
func (t *turn) block(id string, startType ai.EventType) int {
	if i, ok := t.index[id]; ok {
		return i
	}
	if t.index == nil {
		t.index = map[string]int{}
	}
	i := t.blocks
	t.blocks++
	t.index[id] = i
	t.emitter.Update(ai.Event{
		Type:         startType,
		ContentIndex: i,
	})
	return i
}

// startTool adds tc to the open message, stores the message, and sends
// the start of the tool call.
func (t *turn) startTool(ctx context.Context, tc ai.ToolCall) error {
	t.emitter.Update(ai.Event{
		Type:         ai.EventToolEnd,
		ContentIndex: t.blocks,
		ToolCall:     &tc,
	})
	t.blocks++
	if err := t.end(ctx, ai.StopReasonToolUse); err != nil {
		return err
	}
	t.emitter.ToolStart(tc)
	t.call = &tc
	return nil
}

// endTool stores the result of the active tool call.
func (t *turn) endTool(ctx context.Context, result ai.ToolResult) error {
	id := t.call.ID
	t.call = nil
	_, err := t.emitter.ToolEnd(ctx, id, result)
	return err
}

// endUnfinished stores an error result for an active tool call whose
// item did not complete, and marks the item as complete.
func (t *turn) endUnfinished(ctx context.Context) error {
	if t.call == nil {
		return nil
	}
	t.done[t.call.ID] = true
	t.active = ""
	return t.endTool(ctx, ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: unfinishedText}},
		IsError: true,
	})
}

// end stores the open assistant message, if there is one. A message
// with no content is dropped.
func (t *turn) end(ctx context.Context, reason ai.StopReason) error {
	return t.endWith(ctx, reason, nil)
}

// endCompleted stores the blocks of the open message whose items
// completed. The block of an item in flight is cut short, so it is not
// stored.
func (t *turn) endCompleted(ctx context.Context, reason ai.StopReason) error {
	completed := map[int]bool{}
	for id, i := range t.index {
		completed[i] = t.done[id]
	}
	return t.endWith(ctx, reason, completed)
}

// endWith stores the open assistant message. If keep is not nil, the
// message keeps only the blocks whose index keep holds. A message with
// no content is dropped.
func (t *turn) endWith(ctx context.Context, reason ai.StopReason, keep map[int]bool) error {
	t.index = nil
	t.blocks = 0
	t.parts = nil
	p := t.emitter.Partial()
	if p == nil {
		return nil
	}
	content := p.Content
	if keep != nil {
		content = nil
		for i, c := range p.Content {
			if keep[i] {
				content = append(content, c)
			}
		}
	}
	if len(content) == 0 {
		t.emitter.Drop()
		return nil
	}
	return t.emitter.End(ctx, ai.Message{
		Role:       ai.RoleAssistant,
		Content:    content,
		Model:      t.model,
		StopReason: reason,
	})
}

// addUsage adds the usage of one model request. Input excludes the
// cached tokens, as in [ai.Usage].
func (t *turn) addUsage(b tokenBreakdown) {
	t.usage = t.usage.Add(ai.Usage{
		Input:      b.InputTokens - b.CachedInputTokens,
		Output:     b.OutputTokens,
		CacheRead:  b.CachedInputTokens,
		CacheWrite: b.CacheWriteInputTokens,
	})
}

// reasoningText returns the text of a reasoning item: the summary
// parts, then the raw content parts, joined with the separator. It is
// the order in which the CLI streams them.
func reasoningText(it item) string {
	var content, summary []string
	_ = json.Unmarshal(it.Content, &content)
	_ = json.Unmarshal(it.Summary, &summary)
	return strings.Join(append(summary, content...), reasoningSeparator)
}

// toolCall returns the tool call of a tool item. It reports false for
// an item that is not a tool.
func toolCall(it item) (ai.ToolCall, bool) {
	var (
		name  string
		input any
	)
	switch it.Type {
	case itemCommandExecution:
		name = toolBash
		input = map[string]string{"command": it.Command}
	case itemFileChange:
		name = toolApplyPatch
		input = map[string]json.RawMessage{"changes": orNull(it.Changes)}
	case itemMcpToolCall:
		name = it.Server + mcpSeparator + it.Tool
		input = orEmptyObject(it.Arguments)
	case itemWebSearch:
		name = toolWebSearch
		input = map[string]string{"query": it.Query}
	default:
		return ai.ToolCall{}, false
	}
	data, err := json.Marshal(input)
	if err != nil {
		data = []byte("{}")
	}
	return ai.ToolCall{
		ID:    it.ID,
		Name:  name,
		Input: data,
	}, true
}

// toolResult returns the result of a completed tool item.
func toolResult(it item) ai.ToolResult {
	failed := it.Status == statusFailed || it.Status == statusDeclined
	var text string
	switch it.Type {
	case itemCommandExecution:
		if it.AggregatedOutput != nil {
			text = *it.AggregatedOutput
		}
		nonzero := it.ExitCode != nil && *it.ExitCode != 0
		failed = failed || nonzero
	case itemFileChange:
		text = fileChanges(it.Changes)
	case itemMcpToolCall:
		text = mcpText(it)
		failed = failed || it.Error != nil
	case itemWebSearch:
		text = it.Query
	}
	if text == "" {
		text = it.Status
	}
	return ai.ToolResult{
		Content: []ai.Content{ai.Text{Text: text}},
		IsError: failed,
	}
}

// fileChanges returns one line per change: the kind, then the path.
func fileChanges(raw json.RawMessage) string {
	var changes []struct {
		Path string `json:"path"`
		Kind struct {
			Type string `json:"type"`
		} `json:"kind"`
	}
	_ = json.Unmarshal(raw, &changes)
	lines := make([]string, 0, len(changes))
	for _, c := range changes {
		lines = append(lines, c.Kind.Type+" "+c.Path)
	}
	return strings.Join(lines, "\n")
}

// mcpText returns the error of an MCP tool call, or the text of its
// result. A content block that is not text stays as JSON.
func mcpText(it item) string {
	if it.Error != nil {
		return it.Error.Message
	}
	if it.Result == nil {
		return ""
	}
	parts := make([]string, 0, len(it.Result.Content))
	for _, raw := range it.Result.Content {
		var block struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &block)
		if block.Type == "text" {
			parts = append(parts, block.Text)
			continue
		}
		parts = append(parts, string(raw))
	}
	return strings.Join(parts, "\n")
}

// orNull returns raw, or JSON null if raw is empty.
func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// orEmptyObject returns raw, or an empty JSON object if raw is empty or
// null.
func orEmptyObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
