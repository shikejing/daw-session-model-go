package daw

import (
	"reflect"
	"testing"
)

func addTrackEdit(id string) Edit {
	return Edit{Label: "add track " + id, Apply: func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: id, Name: id, Gain: 1})
		return nil
	}}
}

func setGainEdit(id string, gain float64) Edit {
	return Edit{Label: "set gain", Apply: func(s *Session) error {
		t := s.FindTrack(id)
		t.Gain = gain
		return nil
	}}
}

// replay applies edits from scratch, used as the ground-truth reference.
func replay(edits ...Edit) *Session {
	s := &Session{SampleRate: 48000}
	for _, e := range edits {
		if err := e.Apply(s); err != nil {
			panic(err)
		}
	}
	return s
}

func TestUndoRedoMatchesReplay(t *testing.T) {
	edits := []Edit{
		addTrackEdit("a"),
		addTrackEdit("b"),
		setGainEdit("a", 0.5),
		setGainEdit("b", 0.25),
	}
	s := &Session{SampleRate: 48000}
	h := NewHistory(s)
	for _, e := range edits {
		if err := h.Do(e); err != nil {
			t.Fatalf("Do(%s): %v", e.Label, err)
		}
	}
	if !reflect.DeepEqual(s, replay(edits...)) {
		t.Fatal("state after Do differs from replay")
	}

	// Undo all the way down, comparing against replay of the remaining prefix.
	for n := len(edits) - 1; n >= 0; n-- {
		if _, ok := h.Undo(); !ok {
			t.Fatalf("Undo %d failed", n)
		}
		if want := replay(edits[:n]...); !reflect.DeepEqual(s, want) {
			t.Fatalf("after undo to %d edits: state differs from replay\n got %+v\nwant %+v", n, s, want)
		}
	}
	if h.CanUndo() {
		t.Fatal("CanUndo should be false at the bottom")
	}

	// Redo all the way up.
	for n := 1; n <= len(edits); n++ {
		if _, ok := h.Redo(); !ok {
			t.Fatalf("Redo %d failed", n)
		}
		if want := replay(edits[:n]...); !reflect.DeepEqual(s, want) {
			t.Fatalf("after redo to %d edits: state differs from replay", n)
		}
	}
	if h.CanRedo() {
		t.Fatal("CanRedo should be false at the top")
	}
}

func TestNewEditClearsRedoStack(t *testing.T) {
	s := &Session{SampleRate: 48000}
	h := NewHistory(s)
	h.Do(addTrackEdit("a"))
	h.Do(addTrackEdit("b"))
	h.Undo()
	if !h.CanRedo() {
		t.Fatal("expected redo to be available")
	}
	h.Do(addTrackEdit("c")) // forks the timeline
	if h.CanRedo() {
		t.Fatal("new edit must discard the redo stack")
	}
	want := replay(addTrackEdit("a"), addTrackEdit("c"))
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("forked state differs from replay\n got %+v\nwant %+v", s, want)
	}
}

func TestFailedEditNotRecorded(t *testing.T) {
	s := &Session{SampleRate: 48000}
	h := NewHistory(s)
	boom := Edit{Label: "boom", Apply: func(s *Session) error {
		s.Tracks = append(s.Tracks, &Track{ID: "x"})
		return errTest
	}}
	if err := h.Do(boom); err == nil {
		t.Fatal("expected error")
	}
	if h.CanUndo() {
		t.Fatal("failed edit must not be recorded")
	}
}

var errTest = errorString("test error")

type errorString string

func (e errorString) Error() string { return string(e) }
