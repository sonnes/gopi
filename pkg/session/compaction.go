package session

import (
	"errors"
	"fmt"
	"slices"

	"github.com/sonnes/gopi/pkg/ai"
)

// KindCompaction is the kind of a custom entry that holds a [Compaction].
const KindCompaction = "gopi.compaction"

// The values of [Compaction.Trigger].
const (
	TriggerManual = "manual"
	TriggerAuto   = "auto"
)

// Edit replaces the entries from First to Last with Messages. The edits
// of a compaction apply in order, and each edit reads the view that the
// edit before it leaves. To trim one tool result, set First and Last to
// its entry, and give one smaller result with the same ToolCallID.
//
// Each message of Messages gets the ID First.N, where N is its index.
// For example, an edit with First "e2" makes the entry "e2.0". A later
// edit can name that ID. The log sets the ID of a compaction only on
// append, so a replacement ID does not use it.
type Edit struct {
	First    string       `json:"first"`
	Last     string       `json:"last"`
	Messages []ai.Message `json:"messages,omitempty"`
}

// Compaction is the data of a custom entry of kind [KindCompaction]. The
// log keeps each original message, and [Fold] applies the edits for the
// request.
type Compaction struct {
	// Strategy names the strategy that made the edits.
	Strategy string `json:"strategy,omitempty"`
	// Trigger is "manual" or "auto" for a compaction of a CLI. It is empty
	// for a compaction of the harness.
	Trigger string `json:"trigger,omitempty"`
	// Edits apply in order.
	Edits []Edit `json:"edits"`
	// Before and After are the estimated tokens of the view.
	Before int `json:"before"`
	After  int `json:"after"`
	// Summary is the summary text of a CLI that gives it. A CLI keeps its
	// own session, so a compaction of a CLI has no edits.
	Summary string `json:"summary,omitempty"`
	// Model is the model ID of the calls of the strategy, or empty if
	// the strategy made no call.
	Model string `json:"model,omitempty"`
	// Usage is the usage of the model calls of the strategy.
	Usage ai.Usage `json:"usage,omitzero"`
}

// Fold returns view with edits applied in order. An edit that does not
// apply is skipped, because a request with the full history is valid.
// [CheckEdits] refuses such an edit before the log stores it.
func Fold(view []Entry, edits []Edit) []Entry {
	for _, e := range edits {
		next, err := applyEdit(view, e)
		if err == nil {
			view = next
		}
	}
	return view
}

// applyEdit returns view with one edit applied. A system message or an
// ephemeral entry in the range stays after the replacement: sections and
// tool changes are in the system messages, and an ephemeral entry is
// true for this run.
func applyEdit(view []Entry, e Edit) ([]Entry, error) {
	first := slices.IndexFunc(view, hasID(e.First))
	if first < 0 {
		return nil, fmt.Errorf("no entry %q", e.First)
	}
	last := slices.IndexFunc(view, hasID(e.Last))
	if last < 0 {
		return nil, fmt.Errorf("no entry %q", e.Last)
	}
	if first > last {
		return nil, fmt.Errorf("%q comes after %q", e.First, e.Last)
	}

	out := make([]Entry, 0, len(view)+len(e.Messages))
	out = append(out, view[:first]...)
	for i, m := range e.Messages {
		out = append(out, Entry{
			ID:      fmt.Sprintf("%s.%d", e.First, i),
			Message: m,
		})
	}
	for _, kept := range view[first : last+1] {
		if kept.Ephemeral || kept.Message.Role == ai.RoleSystem {
			out = append(out, kept)
		}
	}
	return append(out, view[last+1:]...), nil
}

func hasID(id string) func(Entry) bool {
	return func(e Entry) bool { return e.ID == id }
}

// CheckEdits returns an error if edits would break view. It applies the
// edits in order to a copy of the view. It returns an error in each of
// these conditions:
//
//   - An edit names an entry that is not in the view at that step. So an
//     edit cannot start or end inside a range that an earlier edit
//     replaced.
//   - First comes after Last.
//   - An edit names an ephemeral entry, because the entry is not in the
//     log after the run.
//   - The range has the base, because the base must stay first.
//   - A replacement is a system message, because [Fold] and the sections
//     read the system messages of the log.
//   - A replacement has an [ai.ThinkingBlock], because the signature of
//     the block is valid only for the original message.
//   - The result has a tool call with no result, or a result with no
//     call, because a provider rejects them.
func CheckEdits(view []Entry, edits []Edit) error {
	for i, e := range edits {
		if err := checkEdit(view, e); err != nil {
			return fmt.Errorf("session: edit %d: %w", i, err)
		}
		next, err := applyEdit(view, e)
		if err != nil {
			return fmt.Errorf("session: edit %d: %w", i, err)
		}
		view = next
	}
	return checkToolPairs(view)
}

// checkEdit checks one edit against the view that it applies to.
func checkEdit(view []Entry, e Edit) error {
	for _, id := range []string{e.First, e.Last} {
		i := slices.IndexFunc(view, hasID(id))
		if i < 0 {
			continue
		}
		if view[i].Ephemeral {
			return fmt.Errorf("entry %q is ephemeral", id)
		}
		if i == 0 && view[i].Message.Role == ai.RoleSystem {
			return errors.New("the range has the base")
		}
	}
	for _, m := range e.Messages {
		if m.Role == ai.RoleSystem {
			return errors.New("a replacement is a system message")
		}
		for _, c := range m.Content {
			if _, ok := c.(ai.ThinkingBlock); ok {
				return errors.New("a replacement has a thinking block")
			}
		}
	}
	return nil
}

// checkToolPairs checks that each tool call has its result before the
// next user or assistant message, and that each result has its call.
func checkToolPairs(view []Entry) error {
	var open []string
	for _, e := range view {
		switch e.Message.Role {
		case ai.RoleSystem:
			continue
		case ai.RoleToolResult:
			i := slices.Index(open, e.Message.ToolCallID)
			if i < 0 {
				return fmt.Errorf("session: tool result %q has no call", e.Message.ToolCallID)
			}
			open = slices.Delete(open, i, i+1)
			continue
		}
		if len(open) > 0 {
			return fmt.Errorf("session: tool call %q has no result", open[0])
		}
		for _, tc := range e.Message.ToolCalls() {
			open = append(open, tc.ID)
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("session: tool call %q has no result", open[0])
	}
	return nil
}
