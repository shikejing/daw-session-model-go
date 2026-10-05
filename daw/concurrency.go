package daw

import (
	"errors"
	"fmt"
	"sync"
)

// ConflictReason distinguishes why a transaction was rejected.
type ConflictReason string

const (
	// ConflictVersion means the base revision no longer matches: another
	// transaction committed first.
	ConflictVersion ConflictReason = "version_mismatch"
	// ConflictTrackMissing means a track the transaction edited was
	// deleted by a concurrent commit.
	ConflictTrackMissing ConflictReason = "track_missing"
	// ConflictCycle means the transaction would introduce a routing cycle.
	ConflictCycle ConflictReason = "routing_cycle"
)

// ConflictError describes a rejected transaction.
type ConflictError struct {
	Reason  ConflictReason
	TrackID string
	Detail  string
}

func (e *ConflictError) Error() string {
	if e.TrackID != "" {
		return fmt.Sprintf("daw: commit rejected (%s, track %q): %s", e.Reason, e.TrackID, e.Detail)
	}
	return fmt.Sprintf("daw: commit rejected (%s): %s", e.Reason, e.Detail)
}

// ErrTxClosed is returned when using a transaction after Commit.
var ErrTxClosed = errors.New("daw: transaction already committed")

// Store is a concurrency-safe session store with optimistic transactions.
// Each transaction edits a private copy; Commit either applies all of its
// edits atomically or rejects them all with a *ConflictError.
type Store struct {
	mu       sync.Mutex
	session  *Session
	revision uint64
}

// NewStore creates a store holding the given session.
func NewStore(s *Session) *Store {
	return &Store{session: deepCopySession(s)}
}

// Revision returns the current committed revision number.
func (st *Store) Revision() uint64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.revision
}

// Snapshot returns a deep copy of the committed session and its revision.
func (st *Store) Snapshot() (*Session, uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return deepCopySession(st.session), st.revision
}

// Tx is an in-progress transaction: a private session copy plus edits.
type Tx struct {
	store        *Store
	base         uint64
	work         *Session
	baseTrackIDs map[string]bool
	closed       bool
}

// Begin starts a transaction at the current revision.
func (st *Store) Begin() *Tx {
	st.mu.Lock()
	defer st.mu.Unlock()
	ids := make(map[string]bool, len(st.session.Tracks))
	for _, t := range st.session.Tracks {
		if t != nil {
			ids[t.ID] = true
		}
	}
	return &Tx{
		store:        st,
		base:         st.revision,
		work:         deepCopySession(st.session),
		baseTrackIDs: ids,
	}
}

// Session returns the transaction's editable session copy.
func (tx *Tx) Session() *Session { return tx.work }

// Do applies a command inside the transaction.
func (tx *Tx) Do(cmd Command) error {
	if tx.closed {
		return ErrTxClosed
	}
	return cmd.Apply(tx.work)
}

// Commit attempts to atomically apply the transaction's edits. On any
// rejection the committed session is left untouched.
func (tx *Tx) Commit() error {
	if tx.closed {
		return ErrTxClosed
	}
	st := tx.store
	st.mu.Lock()
	defer st.mu.Unlock()

	if tx.base != st.revision {
		// Diagnose: did a concurrent commit delete a track we started from?
		current := make(map[string]bool, len(st.session.Tracks))
		for _, t := range st.session.Tracks {
			if t != nil {
				current[t.ID] = true
			}
		}
		for id := range tx.baseTrackIDs {
			if !current[id] {
				return &ConflictError{
					Reason:  ConflictTrackMissing,
					TrackID: id,
					Detail:  "track was deleted by a concurrent transaction",
				}
			}
		}
		return &ConflictError{
			Reason: ConflictVersion,
			Detail: fmt.Sprintf("base revision %d is stale (current %d)", tx.base, st.revision),
		}
	}

	if err := ValidateGraph(tx.work); err != nil {
		trackID := ""
		if ce, ok := err.(*CycleError); ok && len(ce.TrackIDs) > 0 {
			trackID = ce.TrackIDs[0]
		}
		return &ConflictError{
			Reason:  ConflictCycle,
			TrackID: trackID,
			Detail:  err.Error(),
		}
	}

	st.session = tx.work
	st.revision++
	tx.closed = true
	return nil
}

// deepCopySession returns an independent copy of s.
func deepCopySession(s *Session) *Session {
	if s == nil {
		return nil
	}
	out := &Session{
		Version:    s.Version,
		SampleRate: s.SampleRate,
		Master:     copyChain(s.Master),
	}
	for _, t := range s.Tracks {
		if t == nil {
			out.Tracks = append(out.Tracks, nil)
			continue
		}
		nt := *t
		nt.Chain = copyChain(t.Chain)
		nt.Clips = make([]*Clip, len(t.Clips))
		for i, c := range t.Clips {
			if c != nil {
				nc := *c
				nt.Clips[i] = &nc
			}
		}
		nt.Sends = make([]*Send, len(t.Sends))
		for i, sd := range t.Sends {
			if sd != nil {
				ns := *sd
				nt.Sends[i] = &ns
			}
		}
		out.Tracks = append(out.Tracks, &nt)
	}
	return out
}

func copyChain(c *EffectChain) *EffectChain {
	if c == nil {
		return nil
	}
	out := &EffectChain{}
	for _, slot := range c.Slots {
		if slot == nil {
			out.Slots = append(out.Slots, nil)
			continue
		}
		ns := *slot
		if slot.Params != nil {
			ns.Params = make(map[string]float64, len(slot.Params))
			for k, v := range slot.Params {
				ns.Params[k] = v
			}
		}
		out.Slots = append(out.Slots, &ns)
	}
	return out
}
