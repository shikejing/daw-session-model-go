package daw

import (
	"context"
	"fmt"
	"testing"
)

// bigSession builds a session with n independent clip-bearing tracks.
func bigSession(n int) *Session {
	s := &Session{SampleRate: 48000}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("t%03d", i)
		s.Tracks = append(s.Tracks, &Track{
			ID: id, Gain: 0.5, Pan: float64(i%7)/3 - 1,
			Clips: []Clip{
				{ID: id + "-c1", Source: id + "-a.wav", Start: 0, Duration: 4096, Gain: 1},
				{ID: id + "-c2", Source: id + "-b.wav", Start: 4096, Duration: 4096, Gain: 0.7},
			},
			Chain: []PluginSlot{{PluginID: "gain", Enabled: true, Params: map[string]float64{"gain": 1}}},
		})
	}
	return s
}

func TestIncrementalRenderIdentical(t *testing.T) {
	s := bigSession(20)
	opts := RenderOptions{Frames: 8192, Seed: 9, Incremental: true}
	r := NewRenderer()
	first, err := r.Render(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	// Change one clip on one track.
	s.FindTrack("t007").Clips[0].Gain = 0.3
	second, err := r.Render(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh full render of the changed session must match exactly.
	fresh := NewRenderer()
	ref, err := fresh.Render(context.Background(), s, RenderOptions{Frames: 8192, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	for i := range second.Samples {
		if second.Samples[i] != ref.Samples[i] {
			t.Fatalf("incremental render differs from full render at sample %d", i)
		}
	}
	_ = first
}

func TestIncrementalRenderScalesWithChange(t *testing.T) {
	const n = 40
	s := bigSession(n)
	r := NewRenderer()
	opts := RenderOptions{Frames: 8192, Seed: 2, Incremental: true}
	if _, err := r.Render(context.Background(), s, opts); err != nil {
		t.Fatal(err)
	}
	base := r.Stats()
	if base.TracksRendered != n {
		t.Fatalf("first render computed %d tracks, want %d", base.TracksRendered, n)
	}

	// Touch a single clip; re-render must recompute only that track.
	s.FindTrack("t013").Clips[1].Start += 128
	if _, err := r.Render(context.Background(), s, opts); err != nil {
		t.Fatal(err)
	}
	st := r.Stats()
	recomputed := st.TracksRendered - base.TracksRendered
	if recomputed != 1 {
		t.Fatalf("recomputed %d tracks after 1-track change, want 1 (hits=%d)", recomputed, st.CacheHits)
	}

	// No change at all: nothing recomputed.
	if _, err := r.Render(context.Background(), s, opts); err != nil {
		t.Fatal(err)
	}
	st2 := r.Stats()
	if got := st2.TracksRendered - st.TracksRendered; got != 0 {
		t.Fatalf("unchanged session recomputed %d tracks, want 0", got)
	}
}
