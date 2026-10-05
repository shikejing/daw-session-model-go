package daw

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestCommitAtomicAndOrdered(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})

	// A multi-edit transaction commits as one unit.
	tx := ed.Begin()
	tx.Edit("add a", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "a", Gain: 1})
		return nil
	})
	tx.Edit("add b", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "b", Gain: 1})
		return nil
	})
	if err := ed.Commit(tx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := len(ed.Session().Tracks); got != 2 {
		t.Fatalf("tracks = %d, want 2", got)
	}
	if ed.Revision() != 1 {
		t.Fatalf("revision = %d, want 1", ed.Revision())
	}
}

func TestStaleRevisionRejected(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})
	tx1 := ed.Begin()
	tx2 := ed.Begin() // same base revision

	tx1.Edit("add a", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "a", Gain: 1})
		return nil
	})
	tx2.Edit("add b", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "b", Gain: 1})
		return nil
	})
	if err := ed.Commit(tx1); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	err := ed.Commit(tx2)
	if err == nil {
		t.Fatal("stale transaction must be rejected")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error must wrap ErrConflict: %v", err)
	}
	var c *Conflict
	if !errors.As(err, &c) || c.Kind != ConflictRevision {
		t.Fatalf("want ConflictRevision, got %+v", c)
	}
	// The rejected transaction left no trace.
	if got := len(ed.Session().Tracks); got != 1 {
		t.Fatalf("tracks = %d, want 1 (no partial application)", got)
	}
}

func TestInvalidTransactionRejected(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})
	tx := ed.Begin()
	tx.Edit("add tracks", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "a", Gain: 1}, &Track{ID: "b", Gain: 1})
		return nil
	})
	tx.Edit("create send cycle", func(s *Session) error {
		s.FindTrack("a").Sends = []Send{{TargetID: "b", Gain: 1}}
		s.FindTrack("b").Sends = []Send{{TargetID: "a", Gain: 1}}
		return nil
	})
	err := ed.Commit(tx)
	var c *Conflict
	if !errors.As(err, &c) || c.Kind != ConflictValidation {
		t.Fatalf("want ConflictValidation, got %v", err)
	}
	if got := len(ed.Session().Tracks); got != 0 {
		t.Fatalf("rejected tx leaked %d tracks", got)
	}
	if ed.Revision() != 0 {
		t.Fatal("rejected tx must not bump revision")
	}
}

func TestFailingEditRollsBackWholeTx(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})
	tx := ed.Begin()
	tx.Edit("add a", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "a", Gain: 1})
		return nil
	})
	tx.Edit("boom", func(s *Session) error {
		return fmt.Errorf("simulated failure")
	})
	err := ed.Commit(tx)
	var c *Conflict
	if !errors.As(err, &c) || c.Kind != ConflictValidation {
		t.Fatalf("want ConflictValidation, got %v", err)
	}
	if got := len(ed.Session().Tracks); got != 0 {
		t.Fatalf("half-applied transaction: %d tracks", got)
	}
}

func TestConcurrentCommitters(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})
	const workers = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("t%d", w)
			for attempt := 0; attempt < 50; attempt++ {
				tx := ed.Begin()
				tx.Edit("add "+id, func(s *Session) error {
					s.Tracks = append(s.Tracks, &Track{ID: id, Gain: 1})
					return nil
				})
				err := ed.Commit(tx)
				if err == nil {
					mu.Lock()
					succeeded++
					mu.Unlock()
					return
				}
				var c *Conflict
				if !errors.As(err, &c) || c.Kind != ConflictRevision {
					t.Errorf("unexpected conflict kind: %v", err)
					return
				}
				// ConflictRevision: retry with a fresh base revision.
			}
			t.Errorf("worker %d never committed", w)
		}(w)
	}
	wg.Wait()

	s := ed.Session()
	if len(s.Tracks) != workers || succeeded != workers {
		t.Fatalf("tracks=%d succeeded=%d, want %d each", len(s.Tracks), succeeded, workers)
	}
	seen := map[string]bool{}
	for _, tr := range s.Tracks {
		if seen[tr.ID] {
			t.Fatalf("duplicate track %q: lost update", tr.ID)
		}
		seen[tr.ID] = true
	}
	if ed.Revision() != workers {
		t.Fatalf("revision = %d, want %d", ed.Revision(), workers)
	}
	if err := Validate(s); err != nil {
		t.Fatalf("final session invalid: %v", err)
	}
}

func TestEditorUndoRedo(t *testing.T) {
	ed := NewEditor(&Session{SampleRate: 48000})
	tx := ed.Begin()
	tx.Edit("add a", func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "a", Gain: 1})
		return nil
	})
	if err := ed.Commit(tx); err != nil {
		t.Fatal(err)
	}
	if _, ok := ed.Undo(); !ok {
		t.Fatal("undo failed")
	}
	if got := len(ed.Session().Tracks); got != 0 {
		t.Fatalf("after undo tracks = %d", got)
	}
	if _, ok := ed.Redo(); !ok {
		t.Fatal("redo failed")
	}
	if got := len(ed.Session().Tracks); got != 1 {
		t.Fatalf("after redo tracks = %d", got)
	}
}
