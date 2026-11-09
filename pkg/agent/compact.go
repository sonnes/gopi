package agent

import (
	"context"
	"errors"
	"slices"

	"github.com/sonnes/gopi/pkg/agent/internal/sections"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/session"
	"github.com/sonnes/gopi/pkg/stream"
)

const (
	// compactRetryGrowthDivisor sets the growth before the next try after
	// a compaction that did not bring the view under the limit: one
	// tenth. Without it, a fault costs money on each call.
	compactRetryGrowthDivisor = 10
)

// Compactor is a compaction strategy. It decides when the history is too
// large, and which entries to replace. The package compact has the
// strategies of gopi. The reasons are in docs/concepts/agent/compaction.md.
type Compactor interface {
	// Limit returns the size of the view, in tokens, that starts a
	// compaction with the model m. A limit of 0 or less starts none.
	Limit(m ai.Model) int
	// Compact returns the edits for the view. No edits means no
	// compaction. If it returns an error, the run goes on with the old
	// view. Only a canceled context stops the run.
	Compact(ctx context.Context, in CompactInput) (CompactResult, error)
}

// CompactInput is what a strategy reads.
type CompactInput struct {
	// Entries are the current view. Each entry and replacement has an
	// ID. An edit cannot name an entry with Ephemeral set.
	Entries []session.Entry
	// Tokens is the size of the view.
	Tokens int
	// Limit is the result of [Compactor.Limit].
	Limit int
	// Instructions is the text of a manual compaction, or empty.
	Instructions string
	// Model is the model of the agent.
	Model ai.LanguageModel
	// Options are the options of the calls of the agent, with the base.
	// A call with these options and the messages of the view as its
	// start reads the prompt cache.
	Options ai.Options
}

// CompactResult is what a strategy returns.
type CompactResult struct {
	// Strategy names the strategy in the log and the events.
	Strategy string
	// Edits apply in order. See [session.Edit].
	Edits []session.Edit
	// Usage is the usage of the model calls of the strategy. A strategy
	// that fails also returns it.
	Usage ai.Usage
}

// Compaction sets the strategy that compacts the history. Without it, or
// with nil, the agent never compacts. The loop checks the size before
// each model call. If a provider refuses the size with
// [ai.ErrContextExceeded], the loop compacts and calls again one time.
func Compaction(c Compactor) Option {
	return func(a *Agent) { a.cfg.compactor = c }
}

// Compact runs the strategy now, with no limit, and returns the stream of
// the compaction. The instructions go to the strategy, such as "keep the
// schema decisions". The stream has agent_start, the events of the
// compaction, and agent_end. Its result has no messages. If a run is
// active, or the agent has no strategy, the stream fails with no event.
// If the strategy fails, the stream fails after agent_end.
func (a *Agent) Compact(ctx context.Context, instructions string) *Stream {
	return stream.New(func(push func(Event)) ([]ai.Message, error) {
		if a.cfg.compactor == nil {
			return nil, errors.New("agent: the agent has no compaction strategy")
		}
		if err := a.start(push); err != nil {
			return nil, err
		}
		em := &emitter{
			agent: a,
			push:  push,
		}
		return nil, a.compactNow(ctx, instructions, em)
	})
}

// compactNow is the body of [Agent.Compact]. A run that failed in its
// tool calls leaves calls with no result, so their results go first.
func (a *Agent) compactNow(ctx context.Context, instructions string, em *emitter) (err error) {
	defer func() {
		st := a.State()
		end := EventAgentEnd{Usage: st.Usage(), UsageByModel: st.UsageByModel()}
		if err != nil {
			end.Error = err.Error()
		}
		em.emit(end)
	}()

	if _, err := a.add(ctx, em, a.interrupted()...); err != nil {
		return err
	}
	outcome, err := a.compact(ctx, instructions, em)
	if err != nil {
		return err
	}
	return outcome.failure
}

// compactOutcome is what one compaction did. A failure does not stop a
// run.
type compactOutcome struct {
	compacted bool
	after     int
	failure   error
}

// compactIfNeeded compacts if the view is at the limit. After a
// compaction that leaves the view at the limit, it waits for the view to
// grow by one tenth.
func (a *Agent) compactIfNeeded(ctx context.Context, em *emitter) error {
	c := a.cfg.compactor
	if c == nil {
		return nil
	}
	limit := c.Limit(a.lm.Model)
	tokens := contextTokens(a.State())
	if limit <= 0 || tokens < limit || tokens < a.compactRetryAt {
		return nil
	}

	outcome, err := a.compact(ctx, "", em)
	if err != nil {
		return err
	}
	size := tokens
	if outcome.compacted {
		size = outcome.after
	}
	a.compactRetryAt = 0
	if size >= limit {
		a.compactRetryAt = size + size/compactRetryGrowthDivisor
	}
	return nil
}

// callAfterOverflow handles a call that the provider refused because of
// the size. It compacts, and it calls the model one more time. If the
// agent has no strategy, or the compaction made no edits, it returns
// cause.
func (a *Agent) callAfterOverflow(
	ctx context.Context,
	cause error,
	em *emitter,
) (*ai.Message, error) {
	if a.cfg.compactor == nil {
		return nil, cause
	}
	outcome, err := a.compact(ctx, "", em)
	if err != nil {
		return nil, errors.Join(cause, err)
	}
	if !outcome.compacted {
		return nil, cause
	}
	return a.call(ctx, a.State().View(), em)
}

// compact runs the strategy, checks its edits, and stores them. It
// returns an error only if the context ends or the store fails.
func (a *Agent) compact(
	ctx context.Context,
	instructions string,
	em *emitter,
) (compactOutcome, error) {
	em.emit(EventCompactionStart{})

	s := a.State()
	view := s.viewEntries()
	base, _ := sections.Base(s.View())
	before := contextTokens(s)

	res, err := a.cfg.compactor.Compact(ctx, CompactInput{
		Entries:      view,
		Tokens:       before,
		Limit:        a.cfg.compactor.Limit(a.lm.Model),
		Instructions: instructions,
		Model:        a.lm,
		Options:      ai.JoinOptions(a.cfg.callOpts, baseOptions(base)),
	})
	data := session.Compaction{
		Strategy: res.Strategy,
		Model:    a.lm.Model.ID,
		Usage:    res.Usage,
	}
	if ctx.Err() != nil {
		em.emit(EventCompaction{
			Compaction: data,
			Error:      ctx.Err().Error(),
		})
		return compactOutcome{}, ctx.Err()
	}
	if err == nil {
		err = session.CheckEdits(view, res.Edits)
	}
	if err != nil {
		em.emit(EventCompaction{
			Compaction: data,
			Error:      err.Error(),
		})
		return compactOutcome{failure: err}, nil
	}
	if len(res.Edits) == 0 {
		em.emit(EventCompaction{Compaction: data})
		return compactOutcome{}, nil
	}

	data.Edits = res.Edits
	data.Before = before
	data.After = estimateEntries(session.Fold(view, res.Edits))
	entry, err := session.Custom(session.KindCompaction, data)
	if err != nil {
		return compactOutcome{}, err
	}
	err = em.commit(ctx, entry, EventCompaction{Compaction: data})
	if err != nil {
		return compactOutcome{}, err
	}
	return compactOutcome{
		compacted: true,
		after:     data.After,
	}, nil
}

// contextTokens returns the size of the next request. The usage of the
// last call after the last compaction is exact, and a message after that
// call is an estimate. A call before the last compaction reports the old
// size, so with no later call the whole view is an estimate.
func contextTokens(s State) int {
	recent := s.messages[s.compactedAt:]
	for i, m := range slices.Backward(recent) {
		u := m.Usage
		total := u.Input + u.CacheRead + u.CacheWrite
		if m.Role == ai.RoleAssistant && total > 0 {
			return total + ai.EstimateTokens(recent[i+1:]...)
		}
	}
	return ai.EstimateTokens(s.View()...)
}

func estimateEntries(entries []session.Entry) int {
	total := 0
	for _, e := range entries {
		total += ai.EstimateTokens(e.Message)
	}
	return total
}
