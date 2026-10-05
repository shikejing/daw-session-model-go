package daw

// Command is a reversible edit to a session.
type Command interface {
	// Apply mutates the session; it must be deterministic.
	Apply(s *Session) error
	// Revert undoes Apply exactly.
	Revert(s *Session) error
	// String describes the command for history display.
	String() string
}

// History tracks undo/redo stacks of applied commands. Applying a new
// command after undoing clears the redo stack, so the history is always
// a single linear sequence equivalent to replaying commands in order.
type History struct {
	done   []Command
	undone []Command
}

// NewHistory creates an empty history.
func NewHistory() *History { return &History{} }

// Do applies cmd and pushes it on the undo stack, clearing the redo stack.
// If Apply fails the command is not recorded and the redo stack is kept.
func (h *History) Do(s *Session, cmd Command) error {
	if err := cmd.Apply(s); err != nil {
		return err
	}
	h.done = append(h.done, cmd)
	h.undone = h.undone[:0]
	return nil
}

// Undo reverts the most recent command. Returns false if nothing to undo.
func (h *History) Undo(s *Session) bool {
	n := len(h.done)
	if n == 0 {
		return false
	}
	cmd := h.done[n-1]
	h.done = h.done[:n-1]
	if err := cmd.Revert(s); err != nil {
		// Revert must never fail for a well-formed command; if it does,
		// keep history consistent by not recording the undo.
		h.done = append(h.done, cmd)
		return false
	}
	h.undone = append(h.undone, cmd)
	return true
}

// Redo re-applies the most recently undone command. Returns false if none.
func (h *History) Redo(s *Session) bool {
	n := len(h.undone)
	if n == 0 {
		return false
	}
	cmd := h.undone[n-1]
	h.undone = h.undone[:n-1]
	if err := cmd.Apply(s); err != nil {
		h.undone = append(h.undone, cmd)
		return false
	}
	h.done = append(h.done, cmd)
	return true
}

// CanUndo reports whether there is a command to undo.
func (h *History) CanUndo() bool { return len(h.done) > 0 }

// CanRedo reports whether there is a command to redo.
func (h *History) CanRedo() bool { return len(h.undone) > 0 }

// Len returns the number of applied (undoable) commands.
func (h *History) Len() int { return len(h.done) }
