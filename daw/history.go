package daw

// Edit is a single undoable mutation of a Session.
type Edit struct {
	Label string
	Apply func(s *Session) error
}

// historyEntry stores snapshots so undo/redo restore exact prior states.
// Because edits are deterministic functions of the session, restoring a
// snapshot is identical to replaying the command log up to that point.
type historyEntry struct {
	label  string
	before *Session
	after  *Session
}

// History tracks undo/redo stacks of edits applied to a session.
type History struct {
	s     *Session
	limit int // max undo entries kept; 0 = unlimited
	undo  []historyEntry
	redo  []historyEntry
}

// NewHistory creates an empty history bound to s. The session is mutated in
// place, so callers holding the pointer always see the current state.
func NewHistory(s *Session) *History { return &History{s: s} }

// SetLimit caps the undo stack depth (0 = unlimited).
func (h *History) SetLimit(n int) { h.limit = n }

// Do applies an edit and records it. Any redoable edits are discarded, since
// a new edit forks the timeline.
func (h *History) Do(e Edit) error {
	before := h.s.Clone()
	if err := e.Apply(h.s); err != nil {
		return err // failed edits are not recorded; session untouched
	}
	h.undo = append(h.undo, historyEntry{label: e.Label, before: before, after: h.s.Clone()})
	if h.limit > 0 && len(h.undo) > h.limit {
		h.undo = h.undo[len(h.undo)-h.limit:]
	}
	h.redo = h.redo[:0]
	return nil
}

// Undo reverts the most recent edit. ok is false when nothing can be undone.
func (h *History) Undo() (label string, ok bool) {
	if len(h.undo) == 0 {
		return "", false
	}
	e := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	*h.s = *e.before.Clone()
	h.redo = append(h.redo, e)
	return e.label, true
}

// Redo re-applies the most recently undone edit.
func (h *History) Redo() (label string, ok bool) {
	if len(h.redo) == 0 {
		return "", false
	}
	e := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	*h.s = *e.after.Clone()
	h.undo = append(h.undo, e)
	return e.label, true
}

// record pushes an externally-applied transition (used by Editor.Commit)
// and clears the redo stack.
func (h *History) record(label string, before, after *Session) {
	h.undo = append(h.undo, historyEntry{label: label, before: before, after: after})
	if h.limit > 0 && len(h.undo) > h.limit {
		h.undo = h.undo[len(h.undo)-h.limit:]
	}
	h.redo = h.redo[:0]
}

// CanUndo reports whether Undo would succeed.
func (h *History) CanUndo() bool { return len(h.undo) > 0 }

// CanRedo reports whether Redo would succeed.
func (h *History) CanRedo() bool { return len(h.redo) > 0 }
