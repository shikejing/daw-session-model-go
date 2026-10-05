package daw

import (
	"errors"
	"testing"
)

func tracksWithSends(sends map[string][]Send) *Session {
	s := &Session{SampleRate: 48000}
	for id := range sends {
		s.Tracks = append(s.Tracks, &Track{ID: id, Gain: 1, Sends: sends[id]})
	}
	return s
}

func TestCycleDetected(t *testing.T) {
	s := tracksWithSends(map[string][]Send{
		"a": {{TargetID: "b", Gain: 1}},
		"b": {{TargetID: "c", Gain: 1}},
		"c": {{TargetID: "a", Gain: 1}},
		"d": nil, // uninvolved track
	})
	err := CheckAcyclic(s)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("want *CycleError, got %v", err)
	}
	involved := map[string]bool{}
	for _, id := range ce.TrackIDs {
		involved[id] = true
	}
	for _, id := range []string{"a", "b", "c"} {
		if !involved[id] {
			t.Fatalf("cycle must name track %q: %v", id, ce.TrackIDs)
		}
	}
	if involved["d"] {
		t.Fatalf("uninvolved track d falsely named: %v", ce.TrackIDs)
	}
}

func TestSelfLoopDetected(t *testing.T) {
	s := tracksWithSends(map[string][]Send{
		"a": {{TargetID: "a", Gain: 0.5}},
	})
	var ce *CycleError
	if !errors.As(CheckAcyclic(s), &ce) {
		t.Fatal("self-loop must be rejected")
	}
}

func TestAcyclicGraphAccepted(t *testing.T) {
	// Diamond routing: a -> b, a -> c, b -> d, c -> d. No cycle.
	s := tracksWithSends(map[string][]Send{
		"a": {{TargetID: "b", Gain: 1}, {TargetID: "c", Gain: 1}},
		"b": {{TargetID: "d", Gain: 1}},
		"c": {{TargetID: "d", Gain: 1}},
		"d": nil,
	})
	if err := CheckAcyclic(s); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	if err := Validate(s); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestDanglingSendRejected(t *testing.T) {
	s := tracksWithSends(map[string][]Send{
		"a": {{TargetID: "ghost", Gain: 1}},
	})
	if err := Validate(s); err == nil {
		t.Fatal("send to unknown track must be rejected")
	}
}

func TestDuplicateTrackIDRejected(t *testing.T) {
	s := &Session{Tracks: []*Track{{ID: "a"}, {ID: "a"}}}
	if err := Validate(s); err == nil {
		t.Fatal("duplicate track IDs must be rejected")
	}
}

func TestRenderRejectsCycle(t *testing.T) {
	s := tracksWithSends(map[string][]Send{
		"a": {{TargetID: "b", Gain: 1}},
		"b": {{TargetID: "a", Gain: 1}},
	})
	r := NewRenderer()
	var ce *CycleError
	if _, err := r.Render(t.Context(), s, RenderOptions{Frames: 64}); !errors.As(err, &ce) {
		t.Fatalf("render must reject cyclic session, got %v", err)
	}
}
