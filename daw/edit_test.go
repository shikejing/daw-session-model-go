package daw

import (
	"reflect"
	"testing"
)

// script builds a deterministic sequence of commands used by several tests.
func script() []Command {
	return []Command{
		&AddTrackCmd{Track: &Track{ID: "a", Name: "A", Gain: 1, Chain: &EffectChain{}}},
		&AddTrackCmd{Track: &Track{ID: "b", Name: "B", Gain: 1, Chain: &EffectChain{}}},
		&SetTrackParamCmd{TrackID: "a", Field: "gain", Value: 0.5},
		&AddClipCmd{TrackID: "a", Clip: &Clip{ID: "c1", Start: 1, Duration: 2, Gain: 1, SourceID: "x"}},
		&AddSendCmd{TrackID: "a", Send: &Send{TargetTrackID: "b", Gain: 0.4}},
		&SetTrackParamCmd{TrackID: "b", Field: "pan", Value: -0.75},
	}
}

// replay applies cmds to a fresh session, simulating ordered replay.
func replay(cmds []Command) *Session {
	s := NewSession(48000)
	for _, c := range cmds {
		if err := c.Apply(s); err != nil {
			panic(err)
		}
	}
	return s
}

func TestUndoRestoresInitial(t *testing.T) {
	s := NewSession(48000)
	initial := deepCopySession(s)
	h := NewHistory()
	for _, c := range script() {
		if err := h.Do(s, c); err != nil {
			t.Fatalf("Do %s: %v", c, err)
		}
	}
	for h.CanUndo() {
		if !h.Undo(s) {
			t.Fatal("Undo returned false while CanUndo is true")
		}
	}
	if !reflect.DeepEqual(deepCopySession(initial), deepCopySession(s)) {
		t.Fatalf("session not restored after undoing everything:\n got %#v\nwant %#v", s, initial)
	}
	if h.Undo(s) {
		t.Fatal("Undo on empty history must return false")
	}
}

func TestRedoMatchesOrderedReplay(t *testing.T) {
	cmds := script()
	s := NewSession(48000)
	h := NewHistory()
	for _, c := range cmds {
		if err := h.Do(s, c); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	// Undo all, redo all: must equal replaying the script in order.
	for h.CanUndo() {
		h.Undo(s)
	}
	for h.CanRedo() {
		if !h.Redo(s) {
			t.Fatal("Redo returned false while CanRedo is true")
		}
	}
	want := replay(script())
	if !reflect.DeepEqual(want, s) {
		t.Fatalf("redo result differs from ordered replay:\n got %#v\nwant %#v", s, want)
	}
}

func TestPartialUndoRedoMatchesPrefixReplay(t *testing.T) {
	cmds := script()
	s := NewSession(48000)
	h := NewHistory()
	for _, c := range cmds {
		if err := h.Do(s, c); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	h.Undo(s)
	h.Undo(s)
	h.Redo(s)
	// State must equal replaying all but the last command.
	want := replay(script()[:len(cmds)-1])
	if !reflect.DeepEqual(want, s) {
		t.Fatalf("undo+redo state differs from prefix replay:\n got %#v\nwant %#v", s, want)
	}
}

func TestNewEditClearsRedoStack(t *testing.T) {
	s := NewSession(48000)
	h := NewHistory()
	cmds := script()
	for _, c := range cmds[:3] {
		if err := h.Do(s, c); err != nil {
			t.Fatalf("Do: %v", err)
		}
	}
	h.Undo(s)
	if !h.CanRedo() {
		t.Fatal("expected redo to be available after undo")
	}
	// A new edit must discard the redo stack.
	if err := h.Do(s, &SetTrackParamCmd{TrackID: "b", Field: "gain", Value: 0.1}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if h.CanRedo() {
		t.Fatal("redo stack must be cleared by a new edit")
	}
	if h.Redo(s) {
		t.Fatal("Redo must fail after redo stack was cleared")
	}
}

func TestFailedCommandNotRecorded(t *testing.T) {
	s := NewSession(48000)
	h := NewHistory()
	bad := &SetTrackParamCmd{TrackID: "ghost", Field: "gain", Value: 1}
	if err := h.Do(s, bad); err == nil {
		t.Fatal("expected error for unknown track")
	}
	if h.CanUndo() {
		t.Fatal("failed command must not be recorded in history")
	}
}

func TestAddSendRejectsCycle(t *testing.T) {
	s := NewSession(48000)
	h := NewHistory()
	_ = h.Do(s, &AddTrackCmd{Track: &Track{ID: "a", Gain: 1, Chain: &EffectChain{}}})
	_ = h.Do(s, &AddTrackCmd{Track: &Track{ID: "b", Gain: 1, Chain: &EffectChain{}}})
	if err := h.Do(s, &AddSendCmd{TrackID: "a", Send: &Send{TargetTrackID: "b", Gain: 1}}); err != nil {
		t.Fatalf("a->b should be fine: %v", err)
	}
	err := h.Do(s, &AddSendCmd{TrackID: "b", Send: &Send{TargetTrackID: "a", Gain: 1}})
	if err == nil {
		t.Fatal("b->a must be rejected as a cycle")
	}
	ce, ok := err.(*CycleError)
	if !ok {
		t.Fatalf("error type = %T, want *CycleError", err)
	}
	if len(ce.TrackIDs) == 0 {
		t.Fatal("CycleError must name the tracks involved")
	}
	// The rejected send must not be left behind on the track.
	ta, _ := findTrack(s, "b")
	if len(ta.Sends) != 0 {
		t.Fatal("rejected send must be rolled back")
	}
}
