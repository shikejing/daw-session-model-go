package daw

import (
	"sync"
	"testing"
)

func baseStore() *Store {
	s := NewSession(48000)
	s.Tracks = []*Track{
		{ID: "a", Name: "A", Gain: 1, Chain: &EffectChain{}},
		{ID: "b", Name: "B", Gain: 1, Chain: &EffectChain{}},
	}
	return NewStore(s)
}

func TestCommitAppliesAtomically(t *testing.T) {
	st := baseStore()
	tx := st.Begin()
	if err := tx.Do(&SetTrackParamCmd{TrackID: "a", Field: "gain", Value: 0.3}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Do(&AddClipCmd{TrackID: "a", Clip: &Clip{ID: "c", Duration: 1, Gain: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	s, rev := st.Snapshot()
	if rev != 1 {
		t.Fatalf("revision = %d, want 1", rev)
	}
	ta, _ := findTrack(s, "a")
	if ta.Gain != 0.3 || len(ta.Clips) != 1 {
		t.Fatal("both edits of the transaction must be visible")
	}
}

func TestConflictingCommitsSecondRejected(t *testing.T) {
	st := baseStore()
	tx1 := st.Begin()
	tx2 := st.Begin()
	_ = tx1.Do(&SetTrackParamCmd{TrackID: "a", Field: "gain", Value: 0.1})
	_ = tx2.Do(&SetTrackParamCmd{TrackID: "a", Field: "gain", Value: 0.9})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	err := tx2.Commit()
	if err == nil {
		t.Fatal("second commit must be rejected")
	}
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if ce.Reason != ConflictVersion {
		t.Fatalf("reason = %s, want %s", ce.Reason, ConflictVersion)
	}
	// The rejected transaction must not have leaked partial edits.
	s, _ := st.Snapshot()
	ta, _ := findTrack(s, "a")
	if ta.Gain != 0.1 {
		t.Fatalf("gain = %v: rejected transaction overwrote committed state", ta.Gain)
	}
}

func TestConflictTrackMissingDistinguished(t *testing.T) {
	st := baseStore()
	tx1 := st.Begin()
	tx2 := st.Begin()
	// tx1 deletes track b; tx2 edits track b.
	_ = tx1.Do(&RemoveTrackCmd{TrackID: "b"})
	_ = tx2.Do(&SetTrackParamCmd{TrackID: "b", Field: "gain", Value: 0.5})
	if err := tx1.Commit(); err != nil {
		t.Fatalf("delete commit: %v", err)
	}
	err := tx2.Commit()
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if ce.Reason != ConflictTrackMissing {
		t.Fatalf("reason = %s, want %s", ce.Reason, ConflictTrackMissing)
	}
	if ce.TrackID != "b" {
		t.Fatalf("TrackID = %q, want b", ce.TrackID)
	}
}

func TestCommitRejectsCycle(t *testing.T) {
	st := baseStore()
	tx := st.Begin()
	// Bypass AddSendCmd's own guard by mutating the session directly,
	// simulating an edit path that creates a loop.
	tx.Session().Tracks[0].Sends = append(tx.Session().Tracks[0].Sends, &Send{TargetTrackID: "b", Gain: 1})
	tx.Session().Tracks[1].Sends = append(tx.Session().Tracks[1].Sends, &Send{TargetTrackID: "a", Gain: 1})
	err := tx.Commit()
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("error type = %T, want *ConflictError", err)
	}
	if ce.Reason != ConflictCycle {
		t.Fatalf("reason = %s, want %s", ce.Reason, ConflictCycle)
	}
	if ce.TrackID == "" {
		t.Fatal("cycle conflict must name an involved track")
	}
	// Store must be unchanged.
	s, rev := st.Snapshot()
	if rev != 0 || len(s.Tracks[0].Sends) != 0 {
		t.Fatal("rejected transaction must leave the store untouched")
	}
}

func TestConcurrentCommitsExactlyOneWins(t *testing.T) {
	st := baseStore()
	const n = 16
	var wg sync.WaitGroup
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			tx := st.Begin()
			_ = tx.Do(&SetTrackParamCmd{TrackID: "a", Field: "gain", Value: v})
			results <- tx.Commit()
		}(float64(i) / 100)
	}
	wg.Wait()
	close(results)
	var okCount, conflictCount int
	for err := range results {
		if err == nil {
			okCount++
		} else if ce, ok := err.(*ConflictError); ok && ce.Reason == ConflictVersion {
			conflictCount++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if okCount+conflictCount != n {
		t.Fatalf("ok=%d conflict=%d, total must be %d", okCount, conflictCount, n)
	}
	if okCount == 0 {
		t.Fatal("at least one transaction must succeed")
	}
	// Every commit is serialized, so revision equals number of successes.
	if rev := st.Revision(); rev != uint64(okCount) {
		t.Fatalf("revision = %d, want %d", rev, okCount)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	st := baseStore()
	snap, _ := st.Snapshot()
	snap.Tracks[0].Gain = -99
	snap.Tracks[0].Chain.Slots = append(snap.Tracks[0].Chain.Slots, &PluginSlot{ID: "x"})
	snap.Tracks[0].Clips = append(snap.Tracks[0].Clips, &Clip{ID: "y"})
	s2, _ := st.Snapshot()
	if s2.Tracks[0].Gain == -99 || len(s2.Tracks[0].Chain.Slots) != 0 || len(s2.Tracks[0].Clips) != 0 {
		t.Fatal("mutating a snapshot must not affect the store")
	}
}
