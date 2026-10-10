package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
)

// The rails of the tree. A node of the run is a turn, an error, or the
// usage. A node of a turn is a block of text, a block of thinking, or a
// tool call. A node is never the last child, except the usage, because
// the tree prints each event as it arrives and cannot know what comes
// next.
const (
	runNode   = "├─ "
	lastNode  = "└─ "
	turnNode  = "│  ├─ "
	turnLine  = "│  │  "
	turnBlank = "│  │"
)

// maxField is the most runes of the arguments or the result of a tool
// call on its line.
const maxField = 200

// The ANSI codes of the tree. The tree uses them only on a terminal.
const (
	reset  = "\033[0m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
)

// tree prints the events of runs as a tree. Each run is a root, with
// its turns as children. Text and thinking stream into their node as
// the deltas arrive.
//
// The tree skips the messages that a reader already knows: the input,
// the system messages, and the tool results. The tool calls show their
// results.
type tree struct {
	w     io.Writer
	model string
	color bool

	// block is the delta type of the open block: text, thinking, or
	// none. index is the content index of the open block.
	block ai.EventType
	index int
	// wrote is true when the open block has text on the screen.
	wrote bool
	// newlines counts the newlines of the open block that are not on
	// the screen. The tree writes them only before more text, so a
	// block does not end with an empty line.
	newlines int
	// streamed is true when the assistant message has a block on the
	// screen. If it is false at the end of the message, the tree prints
	// the blocks of the final message.
	streamed bool
	// calls numbers the tool calls of the turn, by call ID. A result
	// shows the number of its call, because calls run at the same time
	// and their results come in any order.
	calls map[string]int
	// shown records each call that the tree printed. It can be nil.
	shown *shownCalls
}

// newTree returns a tree that writes to w. The model is the label of
// each root. If color is true, the tree writes ANSI colors.
func newTree(w io.Writer, model string, color bool) *tree {
	return &tree{
		w:     w,
		model: model,
		color: color,
	}
}

// print writes the part of the tree that the event adds.
func (t *tree) print(e agent.Event) {
	switch e := e.(type) {
	case agent.EventAgentStart:
		t.line("", "agent  "+t.model)

	case agent.EventTurnStart:
		t.calls = make(map[string]int)
		t.line(runNode, fmt.Sprintf("turn %d", e.Turn))

	case agent.EventMessageUpdate:
		t.update(e.AssistantEvent)

	case agent.EventMessageEnd:
		t.end(e.Message)

	case agent.EventToolExecutionStart:
		if t.calls == nil {
			t.calls = make(map[string]int)
		}
		t.calls[e.ToolCallID] = len(t.calls) + 1
		n := strconv.Itoa(t.calls[e.ToolCallID])
		call := t.paint(yellow, "●") + " " + n + " " + e.ToolName + " " + compact(e.Args)
		t.line(turnNode, call)
		if t.shown != nil {
			t.shown.mark(e.ToolCallID)
		}

	case agent.EventToolExecutionEnd:
		mark := t.paint(green, "✓")
		if e.Result.IsError {
			mark = t.paint(red, "✗")
		}
		n := strconv.Itoa(t.calls[e.ToolCallID])
		result := oneLine(resultText(e.Result))
		t.line(turnNode, mark+" "+n+" "+e.ToolName+" "+result)

	case agent.EventAgentEnd:
		if e.Error != "" {
			// A provider error can hold a JSON body with newlines.
			msg := strings.Join(strings.Fields(clean(e.Error)), " ")
			t.line(runNode, t.paint(red, "✗ error: "+msg))
		}
		t.line(lastNode, "usage  "+usage(e.Usage))
	}
}

// update writes one model event. Only the deltas of text and thinking
// have text for the screen. The tool call deltas wait for the tool
// execution events.
func (t *tree) update(e ai.Event) {
	switch e.Type {
	case ai.EventTextDelta, ai.EventThinkDelta:
		t.streamed = true
		t.text(e.Type, e.ContentIndex, e.Delta)
	case ai.EventTextEnd, ai.EventThinkEnd:
		t.close()
	}
}

// end writes the blocks of an assistant message that did not stream,
// then the reason of a stop that does not fail the run. The run end
// shows an error or a cancel.
func (t *tree) end(m ai.Message) {
	t.close()
	if m.Role != ai.RoleAssistant {
		return
	}

	if !t.streamed {
		for i, c := range m.Content {
			switch b := c.(type) {
			case ai.Text:
				t.text(ai.EventTextDelta, i, b.Text)
			case ai.ThinkingBlock:
				t.text(ai.EventThinkDelta, i, b.Thinking)
			}
		}
		t.close()
	}
	t.streamed = false

	stopped := m.StopReason == ai.StopReasonLength || m.StopReason == ai.StopReasonRefusal
	if !stopped {
		return
	}
	reason := "✗ stopped: " + string(m.StopReason)
	if m.StopDetail != "" {
		reason += "  " + m.StopDetail
	}
	t.line(turnNode, t.paint(red, reason))
}

// text writes a delta into the block of its type and index. A new
// block closes the open block.
func (t *tree) text(block ai.EventType, index int, delta string) {
	if block != t.block || index != t.index {
		t.close()
		t.block = block
		t.index = index
	}

	for i, part := range strings.Split(clean(delta), "\n") {
		if i > 0 && t.wrote {
			t.newlines++
		}
		if part == "" {
			continue
		}
		t.prefix()
		if t.block == ai.EventThinkDelta {
			part = t.paint(dim, part)
		}
		t.write(part)
	}
}

// prefix writes the rails before the next text of the open block. The
// first text opens the node. A later text first writes the newlines
// that wait.
func (t *tree) prefix() {
	if !t.wrote {
		t.write(turnNode)
		if t.block == ai.EventThinkDelta {
			t.write(t.paint(dim, "thinking") + "  ")
		}
		t.wrote = true
		return
	}
	if t.newlines == 0 {
		return
	}

	t.write("\n")
	for range t.newlines - 1 {
		t.write(turnBlank + "\n")
	}
	t.write(turnLine)
	t.newlines = 0
}

// close ends the open block. A block with no text on the screen writes
// nothing.
func (t *tree) close() {
	if t.wrote {
		t.write("\n")
	}
	t.block = ""
	t.index = 0
	t.wrote = false
	t.newlines = 0
}

// line closes the open block and writes one node.
func (t *tree) line(rail, s string) {
	t.close()
	t.write(rail + s + "\n")
}

func (t *tree) write(s string) {
	io.WriteString(t.w, s)
}

func (t *tree) paint(code, s string) string {
	if !t.color {
		return s
	}
	return code + s + reset
}

// compact returns the arguments of a tool call as JSON on one line. If
// the arguments are not valid JSON, it returns them as they are.
func compact(args json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, args); err != nil {
		return oneLine(string(args))
	}
	return truncate(b.String())
}

// resultText returns the text blocks of a tool result.
func resultText(r ai.ToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if text, ok := c.(ai.Text); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, " ")
}

// oneLine joins the words of s with single spaces, so that s fits on
// the line of its node.
func oneLine(s string) string {
	return truncate(strings.Join(strings.Fields(clean(s)), " "))
}

// clean removes the control codes of s, except newline and tab. Model
// text and tool output can hold an escape sequence, which would change
// the terminal of the user.
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		control := r < 0x20 || (r >= 0x7f && r <= 0x9f)
		if control && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, s)
}

func truncate(s string) string {
	runes := []rune(s)
	if len(runes) <= maxField {
		return s
	}
	return string(runes[:maxField]) + "…"
}

// usage returns the tokens of a run, and the cost if the model has
// prices.
func usage(u ai.Usage) string {
	fields := []string{
		fmt.Sprintf("in=%d", u.Input),
		fmt.Sprintf("out=%d", u.Output),
	}
	if u.CacheRead > 0 {
		fields = append(fields, fmt.Sprintf("cache_read=%d", u.CacheRead))
	}
	if u.CacheWrite > 0 {
		fields = append(fields, fmt.Sprintf("cache_write=%d", u.CacheWrite))
	}
	if cost := u.Cost.Total(); cost > 0 {
		fields = append(fields, fmt.Sprintf("$%.4f", cost))
	}
	return strings.Join(fields, " ")
}
