package daw

import (
	"fmt"
	"testing"
)

// bigSession builds nTracks independent tracks plus a send chain
// chain0 -> chain1 -> chain2 to test downstream invalidation.
func bigSession(nTracks int) *Session {
	s := NewSession(100)
	for i := 0; i < nTracks; i++ {
		s.Tracks = append(s.Tracks, &Track{
			ID: fmt.Sprintf("t%03d", i), Gain: 0.5, Pan: 0,
			Clips: []*Clip{{ID: fmt.Sprintf("c%03d", i), Start: 0, Duration: 1, Gain: 1, SourceID: fmt.Sprintf("s%03d", i)}},
			Chain: &EffectChain{Slots: []*PluginSlot{
				{ID: "g", PluginID: "builtin.gain", Enabled: true, Params: map[string]float64{"gain": 0.9}},
			}},
		})
	}
	for i := 0; i < 3; i++ {
		s.Tracks = append(s.Tracks, &Track{ID: fmt.Sprintf("chain%d", i), Gain: 1, Chain: &EffectChain{}})
		if i > 0 {
			prev := s.Tracks[len(s.Tracks)-2]
			prev.Sends = append(prev.Sends, &Send{TargetTrackID: fmt.Sprintf("chain%d", i), Gain: 1})
		}
	}
	// chain0 gets audio from t000.
	s.Tracks[0].Sends = append(s.Tracks[0].Sends, &Send{TargetTrackID: "chain0", Gain: 1})
	return s
}

func TestIncrementalMatchesFullRender(t *testing.T) {
	s := bigSession(50)
	cfg := RenderConfig{Duration: 1, Policy: FailFast, Seed: 9}
	r := &Renderer{Registry: NewRegistry()}
	full, err := r.Render(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ir := NewIncrementalRenderer(r)
	inc, err := ir.Render(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !almostEqual(full.Left, inc.Left, 0) || !almostEqual(full.Right, inc.Right, 0) {
		t.Fatal("incremental render must be bit-identical to full render")
	}
	// Second render with no changes: everything reused.
	if _, err := ir.Render(s, cfg); err != nil {
		t.Fatal(err)
	}
	st := ir.LastStats()
	if st.TracksRecomputed != 0 || st.TracksReused != st.TracksTotal {
		t.Fatalf("unchanged render must reuse all tracks: %+v", st)
	}
	// And still identical.
	inc2, _ := ir.Render(s, cfg)
	if !almostEqual(full.Left, inc2.Left, 0) {
		t.Fatal("cached render must remain bit-identical")
	}
}

func TestIncrementalRecomputesOnlyDownstreamCone(t *testing.T) {
	const nTracks = 200
	s := bigSession(nTracks)
	cfg := RenderConfig{Duration: 1, Policy: FailFast, Seed: 9}
	r := &Renderer{Registry: NewRegistry()}
	ir := NewIncrementalRenderer(r)
	if _, err := ir.Render(s, cfg); err != nil {
		t.Fatal(err)
	}
	st := ir.LastStats()
	if st.TracksRecomputed != nTracks+3 {
		t.Fatalf("first render must compute everything: %+v", st)
	}

	// Touch one clip on t000 (which feeds chain0 -> chain1 -> chain2).
	s.Tracks[0].Clips[0].Gain = 0.7
	if _, err := ir.Render(s, cfg); err != nil {
		t.Fatal(err)
	}
	st = ir.LastStats()
	// Expected recompute: t000 + chain0 + chain1 + chain2 = 4, not 203.
	if st.TracksRecomputed != 4 {
		t.Fatalf("recomputed = %d, want 4 (changed track + downstream cone); stats %+v",
			st.TracksRecomputed, st)
	}
	if st.TracksReused != nTracks+3-4 {
		t.Fatalf("reused = %d, want %d", st.TracksReused, nTracks+3-4)
	}

	// Result must equal a fresh full render of the edited session.
	full, err := r.Render(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	inc, _ := ir.Render(s, cfg)
	if !almostEqual(full.Left, inc.Left, 0) || !almostEqual(full.Right, inc.Right, 0) {
		t.Fatal("incremental render after edit must equal full render")
	}
}

func TestIncrementalIsolatedEditTouchesOneTrack(t *testing.T) {
	s := bigSession(100)
	cfg := RenderConfig{Duration: 1, Policy: FailFast, Seed: 3}
	r := &Renderer{Registry: NewRegistry()}
	ir := NewIncrementalRenderer(r)
	if _, err := ir.Render(s, cfg); err != nil {
		t.Fatal(err)
	}
	// Edit a track with no sends: only it is recomputed.
	s.Tracks[50].Clips[0].Duration = 0.5
	if _, err := ir.Render(s, cfg); err != nil {
		t.Fatal(err)
	}
	if st := ir.LastStats(); st.TracksRecomputed != 1 {
		t.Fatalf("isolated edit must recompute exactly 1 track, got %+v", st)
	}
}
