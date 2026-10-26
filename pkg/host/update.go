package host

import (
	"context"
	"fmt"
	"reflect"
	"slices"
)

// Update changes the config of the session id with change, keeps the new
// config in the session, and returns the session. Use it to change the
// model, the effort, the mode, the system text, the skills, the rules,
// or the MCP servers of a session.
//
// The agent of an active run keeps its config to the end of the run. The
// next run starts the agent again from the new config, and the driver
// resumes the log.
//
// Update returns [ErrInvalid] if change sets another ID, Dir, Dirs, or
// Agent, or a model of another kind of agent, because the new agent
// then cannot resume the conversation. It also returns [ErrInvalid] for
// an effort that is not a thinking level. Such a change changes nothing.
// If the host does not know the session, Update returns [ErrNotFound].
func (h *Host) Update(ctx context.Context, id string, change func(*Config)) (Session, error) {
	s, err := h.session(id)
	if err != nil {
		return Session{}, err
	}
	if err := s.writable(); err != nil {
		return Session{}, err
	}
	return s.update(ctx, change)
}

// update changes the config value of s. It holds startMu, so no agent
// starts from the old config during the change.
func (s *hosted) update(ctx context.Context, change func(*Config)) (Session, error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closing {
		return Session{}, fmt.Errorf("host: session %s: %w", s.id, ErrClosed)
	}

	w, err := s.log(ctx)
	if err != nil {
		return Session{}, err
	}
	// Two reads give two copies, so change cannot alter the old config
	// through a shared slice.
	old, ok, err := configValue.Get(w)
	if err == nil && !ok {
		err = fmt.Errorf("no config: %w", ErrInvalid)
	}
	if err != nil {
		return Session{}, fmt.Errorf("host: session %s: %w", s.id, err)
	}
	config, _, _ := configValue.Get(w)
	change(&config)

	if err := s.host.checkChange(old, config); err != nil {
		return Session{}, fmt.Errorf("host: session %s: %w", s.id, err)
	}
	if err := configValue.Set(ctx, w, config); err != nil {
		return Session{}, fmt.Errorf("host: session %s: write the config: %w", s.id, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sess.Model = config.Model
	s.sess.Effort = config.Effort
	s.sess.Mode = config.Mode
	s.stale = s.runner != nil
	s.updateLocked()
	return s.sess.clone(), nil
}

// checkChange returns an error that wraps [ErrInvalid] if the agent of
// config cannot resume the conversation of the agent of old.
func (h *Host) checkChange(old, config Config) error {
	fixed := old.ID == config.ID &&
		old.Dir == config.Dir &&
		old.Agent == config.Agent &&
		slices.Equal(old.Dirs, config.Dirs)
	if !fixed {
		return fmt.Errorf("the ID, the folders, and the agent file cannot change: %w", ErrInvalid)
	}
	if err := checkEffort(config); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	oldDriver, _, err := h.cat.Driver(old.Model)
	if err != nil {
		return err
	}
	driver, _, err := h.cat.Driver(config.Model)
	if err != nil {
		return err
	}
	// Each kind of agent keeps its own conversation, so the type of the
	// driver names the conversation that the agent resumes.
	if reflect.TypeOf(driver) != reflect.TypeOf(oldDriver) {
		return fmt.Errorf("the model %s runs another kind of agent: %w", config.Model, ErrInvalid)
	}
	return nil
}
