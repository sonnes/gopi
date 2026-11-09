package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"slices"

	"github.com/sonnes/gopi/pkg/agent"
	"github.com/sonnes/gopi/pkg/ai"
	"github.com/sonnes/gopi/pkg/catalog"
	"github.com/sonnes/gopi/pkg/host"
	"github.com/sonnes/gopi/pkg/permission"
)

// models is the catalog of gopi, with the detectors that filled it and
// the providers that they found.
type models struct {
	cat   *catalog.Catalog
	ds    []catalog.Detector
	found []catalog.Detection
}

// newModels returns a catalog of the providers that ds find. If ds find
// no provider, the error names each credential.
func newModels(ds []catalog.Detector) (models, error) {
	cat := catalog.New()
	found := cat.Detect(ds...)
	if len(found) == 0 {
		return models{}, fmt.Errorf("no provider: set %s", sources(ds))
	}
	m := models{
		cat:   cat,
		ds:    ds,
		found: found,
	}
	return m, nil
}

// retry runs fn, which starts or changes a session. If fn fails because
// the catalog does not know the model of the session, retry asks the
// provider of the model for its models, and runs fn again. A provider
// such as openrouter registers no models, so its models are known only
// after a list. A bare model ID asks every provider.
func (m models) retry(ctx context.Context, fn func() error) error {
	err := fn()
	var unknown *catalog.UnknownModelError
	if !errors.As(err, &unknown) || unknown.Spec == "" {
		return err
	}
	spec := unknown.Spec
	listErrs := fetchModels(ctx, m.cat, specProvider(spec, m.found, nil))
	err = fn()
	if err == nil {
		return nil
	}
	return m.modelErr(spec, err, listErrs)
}

// modelErr explains err, the error of a session of the model spec after
// gopi asked for the model lists. listErrs are the errors of the lists.
// An error that is not about the model stays as it is.
func (m models) modelErr(spec string, err error, listErrs []error) error {
	var unknown *catalog.UnknownModelError
	var ambiguous *catalog.AmbiguousModelError
	switch {
	case errors.As(err, &unknown):
		err = unknown
	case errors.As(err, &ambiguous):
		err = ambiguous
	default:
		return err
	}
	// A spec of a provider with no credential fails as an unknown model.
	// The credential is the fix, so the error names it. The check comes
	// after the lists, because a bare ID such as
	// anthropic/claude-sonnet-5.5 can resolve to another provider.
	if id := specProvider(spec, nil, m.ds); id != "" {
		if perr := providerErr(m.ds, m.found, id); perr != nil {
			return fmt.Errorf("model %q: %w", spec, perr)
		}
	}
	errs := append([]error{withHint(err)}, listErrs...)
	return fmt.Errorf("model %q: %w", spec, errors.Join(errs...))
}

// openSession returns the session of o in h. If the store of h has the
// session of --session-id, openSession changes its config with the
// flags that o sets, and the session keeps the rest of its config.
// Otherwise it creates the session from the flags and the agent file
// of --agent. If neither gives a model, the session gets the first
// model of the first provider with models.
func openSession(ctx context.Context, h *host.Host, o options, m models) (host.Session, error) {
	dir, err := filepath.Abs(o.root)
	if err != nil {
		return host.Session{}, err
	}
	var sess host.Session
	if _, ok := h.Get(o.sessionID); o.sessionID != "" && ok {
		err := m.retry(ctx, func() error {
			var err error
			sess, err = h.Update(ctx, o.sessionID, func(c *host.Config) {
				change(c, o, dir)
			})
			return err
		})
		return sess, err
	}

	config := host.Config{
		ID:     o.sessionID,
		Agent:  o.agent,
		Model:  o.model,
		Effort: ai.ThinkingLevel(o.thinking),
		Mode:   permission.Mode(o.mode),
		Dir:    dir,
		Allow:  o.allow,
		Deny:   o.deny,
	}
	err = m.retry(ctx, func() error {
		var err error
		sess, err = m.create(ctx, h, config)
		return err
	})
	return sess, err
}

// create creates the session of config in h. If neither config nor its
// agent file gives a model, create gives the session the first model
// of the first provider with models.
func (m models) create(ctx context.Context, h *host.Host, config host.Config) (host.Session, error) {
	sess, err := h.Create(ctx, config)
	var unknown *catalog.UnknownModelError
	noModel := config.Model == "" && errors.As(err, &unknown) && unknown.Spec == ""
	if !noModel {
		return sess, err
	}
	config.Model = defaultSpec(m.ds, m.found)
	if config.Model == "" {
		return host.Session{}, fmt.Errorf(
			"no default model: give a spec with --model, such as %s/<model-id>",
			m.found[0].Provider,
		)
	}
	return h.Create(ctx, config)
}

// change sets in c each flag that o sets, for a session in the folder
// dir. The rules of o add to the rules of c. The host refuses another
// folder or another agent file, because the agent then cannot resume
// the conversation.
func change(c *host.Config, o options, dir string) {
	c.Dir = dir
	if o.agent != "" {
		c.Agent = o.agent
	}
	if o.model != "" {
		c.Model = o.model
	}
	if o.thinking != "" {
		c.Effort = ai.ThinkingLevel(o.thinking)
	}
	if o.mode != "" {
		c.Mode = permission.Mode(o.mode)
	}
	c.Allow = addRules(c.Allow, o.allow)
	c.Deny = addRules(c.Deny, o.deny)
}

// addRules returns rules with each rule of more that rules does not
// have.
func addRules(rules, more []string) []string {
	for _, r := range more {
		if !slices.Contains(rules, r) {
			rules = append(rules, r)
		}
	}
	return rules
}

// runPrompt sends prompt to the session id of h, and prints the events
// of the run with t. approve answers each tool call of the run that
// waits for a person. An interrupt during the run interrupts the run.
// Outside a run, an interrupt stops gopi. The error of a run that
// failed wraps [errShown], because t shows it.
func runPrompt(
	ctx context.Context,
	h *host.Host,
	id string,
	t printer,
	approve agent.ApproveFunc,
	m models,
	prompt string,
) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	// After the first interrupt, a second one stops gopi, in case the
	// interrupt of the run waits.
	context.AfterFunc(ctx, stop)

	// The streams of the run outlive an interrupt, so they show the end
	// of the run.
	run, end := context.WithCancel(context.WithoutCancel(ctx))
	defer end()
	interrupt := context.AfterFunc(ctx, func() {
		_ = h.Interrupt(run, id)
	})
	defer interrupt()

	_, events, err := h.Stream(run, id)
	if err != nil {
		return err
	}
	if approve != nil {
		state, changes := h.Events(run)
		go confirmCalls(run, h, id, approve, state, changes)
	}
	send := func() error {
		return h.Send(ctx, id, host.Text(prompt))
	}
	if err := m.retry(ctx, send); err != nil {
		return err
	}

	for e, err := range events {
		if err != nil {
			return err
		}
		t.print(e)
		end, ok := e.(agent.EventAgentEnd)
		if !ok {
			continue
		}
		if end.Error != "" {
			return fmt.Errorf("%w: %s", errShown, end.Error)
		}
		return nil
	}
	return nil
}

// confirmCalls asks approve about each tool call of the session id that
// waits for a person, and gives the decision to [host.Host.Confirm]. It
// reads the calls from state, then from changes, until ctx ends. It asks
// about one call at a time, because each question reads the terminal.
// A question with no answer, such as at the end of stdin, denies the
// call, so the run does not wait for ever.
func confirmCalls(
	ctx context.Context,
	h *host.Host,
	id string,
	approve agent.ApproveFunc,
	state host.State,
	changes iter.Seq2[host.Event, error],
) {
	asked := make(map[string]bool)
	confirm := func(sess host.Session) {
		for _, req := range sess.Waiting {
			if asked[req.Call.ID] {
				continue
			}
			asked[req.Call.ID] = true
			d, err := approve(ctx, req)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				d = agent.Decision{Message: err.Error()}
			}
			// The call stops its wait at the end of its run, so a call
			// that no longer waits is no error.
			_ = h.Confirm(ctx, id, req.Call.ID, d)
		}
	}

	confirm(state.Sessions[id])
	for e, err := range changes {
		if err != nil {
			return
		}
		if e.Session.ID == id {
			confirm(e.Session)
		}
	}
}
