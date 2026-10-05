package daw

import (
	"errors"
	"fmt"
	"sync"
)

// ConflictKind classifies why a transaction was rejected.
type ConflictKind int

const (
	ConflictNone ConflictKind = iota
	// ConflictRevision: the session changed since the transaction began
	// (optimistic-concurrency check failed).
	ConflictRevision
	// ConflictValidation: the transaction would produce an invalid session
	// (e.g. a send cycle, a dangling send target, or a failing edit).
	ConflictValidation
)

// Conflict describes a rejected transaction.
type Conflict struct {
	Kind    ConflictKind
	Message string
}

func (c *Conflict) Error() string { return c.Message }

// ErrConflict is a sentinel usable with errors.Is to detect rejections and
// errors.As to extract the *Conflict detail.
var ErrConflict = errors.New("daw: transaction conflict")

// Unwrap lets errors.Is(err, ErrConflict) match any *Conflict.
func (c *Conflict) Unwrap() error { return ErrConflict }

func conflictf(kind ConflictKind, format string, args ...any) error {
	return &Conflict{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Editor serializes concurrent transactions against one session. Commits are
// atomic: a transaction either takes effect completely or is rejected with a
// *Conflict and leaves the session untouched.
type Editor struct {
	mu   sync.Mutex
	s    *Session
	rev  uint64
	hist *History
}

// NewEditor creates an Editor over s. The editor owns the session; use
// Session() to read the current state.
func NewEditor(s *Session) *Editor {
	return &Editor{s: s, hist: NewHistory(s)}
}

// Tx is a unit of concurrent editing: a batch of edits applied atomically.
type Tx struct {
	BaseRevision uint64
	edits        []Edit
}

// Begin starts a transaction at the editor's current revision.
func (e *Editor) Begin() *Tx {
	e.mu.Lock()
	defer e.mu.Unlock()
	return &Tx{BaseRevision: e.rev}
}

// Edit appends an edit to the transaction.
func (t *Tx) Edit(label string, apply func(s *Session) error) {
	t.edits = append(t.edits, Edit{Label: label, Apply: apply})
}

// Commit applies the whole transaction atomically. It returns a *Conflict
// (wrapping ErrConflict) with a distinguishable Kind when rejected.
func (e *Editor) Commit(t *Tx) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t.BaseRevision != e.rev {
		return conflictf(ConflictRevision,
			"transaction based on revision %d, but session is at revision %d",
			t.BaseRevision, e.rev)
	}
	// Apply to a scratch copy so a half-failed batch never leaks.
	work := e.s.Clone()
	for i, ed := range t.edits {
		if err := ed.Apply(work); err != nil {
			return conflictf(ConflictValidation,
				"edit %d (%q) failed: %v", i, ed.Label, err)
		}
	}
	if err := Validate(work); err != nil {
		return conflictf(ConflictValidation, "invalid session: %v", err)
	}
	before := e.s.Clone()
	*e.s = *work // swap contents in place; e.s identity stays stable
	e.rev++
	e.hist.record("tx", before, e.s.Clone())
	return nil
}

// Undo reverts the last committed transaction.
func (e *Editor) Undo() (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hist.Undo()
}

// Redo re-applies the last undone transaction.
func (e *Editor) Redo() (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.hist.Redo()
}

// Revision returns the editor's current session revision.
func (e *Editor) Revision() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.rev
}

// Session returns a deep copy of the current session, safe to read without
// synchronization.
func (e *Editor) Session() *Session {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.s.Clone()
}
