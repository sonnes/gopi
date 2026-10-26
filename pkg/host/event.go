package host

// Event is one change of a session: its new [Session]. The host sends one
// at each change of the status, of the waiting calls, and at the start
// of a session. Each event carries the whole session, so a reader needs no
// history.
//
// The events of the agent of a session are not host events.
// [Host.Stream] has them.
type Event struct {
	Session Session `json:"session"`
}

// State is what a reader of the host knows about each session. A
// reader changes it only through [State.Apply], one event at a time.
//
// Apply changes the map in place, because the host applies each event,
// and a reader joins less often. A copy of a State with = shares the
// map. To keep a state as it is, call [State.Clone].
type State struct {
	// Sessions has each session of the host, by ID.
	Sessions map[string]Session `json:"sessions"`
}

// Apply sets e.Session in the state.
func (s *State) Apply(e Event) {
	if s.Sessions == nil {
		s.Sessions = make(map[string]Session)
	}
	s.Sessions[e.Session.ID] = e.Session.clone()
}

// Clone returns a copy of s that shares no map and no slice with s.
func (s State) Clone() State {
	if s.Sessions == nil {
		return State{}
	}
	sessions := make(map[string]Session, len(s.Sessions))
	for id, sess := range s.Sessions {
		sessions[id] = sess.clone()
	}
	return State{Sessions: sessions}
}
