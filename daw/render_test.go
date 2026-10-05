package daw

import (
	"errors"
	"math"
	"testing"
)

// constSource returns a constant-valued source provider for predictable
// render arithmetic.
type constSource struct{ v float32 }

func (c constSource) Samples(_ string, _ float64, n int, _ float64) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = c.v
	}
	return out
}

func renderSession() *Session {
	s := NewSession(100) // 100 Hz keeps sample counts tiny
	s.Tracks = []*Track{
		{
			ID: "gtr", Gain: 0.5, Pan: 0,
			Clips: []*Clip{{ID: "c1", Start: 0, Duration: 1, Gain: 1, SourceID: "gtr.wav"}},
			Chain: &EffectChain{},
		},
		{
			ID: "vox", Gain: 1, Pan: 0, Mute: true,
			Clips: []*Clip{{ID: "c2", Start: 0, Duration: 1, Gain: 1, SourceID: "vox.wav"}},
			Chain: &EffectChain{},
		},
	}
	return s
}

func cfg() RenderConfig { return RenderConfig{Duration: 1, Policy: FailFast, Seed: 42} }

func almostEqual(a, b []float32, tol float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if math.Abs(float64(a[i]-b[i])) > tol {
			return false
		}
	}
	return true
}

func TestRenderDeterministic(t *testing.T) {
	s := renderSession()
	// Include a stochastic plugin: same seed must still be bit-identical.
	s.Tracks[0].Chain.Slots = append(s.Tracks[0].Chain.Slots,
		&PluginSlot{ID: "n", PluginID: "builtin.noise", Enabled: true, Params: map[string]float64{"amount": 0.2}})
	r := &Renderer{Registry: NewRegistry()}
	r1, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	if !almostEqual(r1.Left, r2.Left, 0) || !almostEqual(r1.Right, r2.Right, 0) {
		t.Fatal("renders with the same seed must be bit-identical")
	}
	r3, err := r.Render(s, RenderConfig{Duration: 1, Policy: FailFast, Seed: 7})
	if err != nil {
		t.Fatal(err)
	}
	if almostEqual(r1.Left, r3.Left, 0) {
		t.Fatal("different seeds should change stochastic plugin output")
	}
}

func TestRenderGainPanMute(t *testing.T) {
	s := renderSession()
	r := &Renderer{Registry: NewRegistry(), Sources: constSource{v: 1}}
	res, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	// gtr: source 1 * clip gain 1 * track gain 0.5, pan 0 -> cos/sin(pi/4).
	want := 0.5 * float32(math.Cos(math.Pi/4))
	if math.Abs(float64(res.Left[0])-float64(want)) > 1e-6 {
		t.Fatalf("left[0] = %v, want %v", res.Left[0], want)
	}
	// vox is muted: total energy must come from gtr alone (100 samples).
	if len(res.Left) != 100 {
		t.Fatalf("expected 100 samples, got %d", len(res.Left))
	}
	for i, v := range res.Left {
		if math.Abs(float64(v)-float64(want)) > 1e-6 {
			t.Fatalf("left[%d] = %v, muted track leaked into mix", i, v)
		}
	}
}

func TestRenderSoloIsolates(t *testing.T) {
	s := renderSession()
	s.Tracks[1].Mute = false
	s.Tracks[1].Solo = true // only vox audible now
	r := &Renderer{Registry: NewRegistry(), Sources: constSource{v: 1}}
	res, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	want := float32(math.Cos(math.Pi / 4)) // vox: gain 1, pan 0
	if math.Abs(float64(res.Left[0])-float64(want)) > 1e-6 {
		t.Fatalf("left[0] = %v, want %v (only soloed track)", res.Left[0], want)
	}
}

func TestRenderSendAndEffectChain(t *testing.T) {
	s := NewSession(100)
	s.Tracks = []*Track{
		{
			ID: "src", Gain: 1, Pan: 0,
			Clips: []*Clip{{ID: "c", Start: 0, Duration: 1, Gain: 1, SourceID: "x"}},
			Sends: []*Send{{TargetTrackID: "bus", Gain: 1, PreFader: false}},
			Chain: &EffectChain{},
		},
		{
			ID: "bus", Gain: 1, Pan: 0, // receives send, halves it
			Chain: &EffectChain{Slots: []*PluginSlot{
				{ID: "g", PluginID: "builtin.gain", Enabled: true, Params: map[string]float64{"gain": 0.5}},
			}},
		},
	}
	r := &Renderer{Registry: NewRegistry(), Sources: constSource{v: 1}}
	res, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	// master = src (1 * cos45) + bus (send 1*cos45 -> chain 0.5 -> bus fader cos45)
	c45 := float32(math.Cos(math.Pi / 4))
	want := c45 + c45*0.5*c45
	if math.Abs(float64(res.Left[0])-float64(want)) > 1e-6 {
		t.Fatalf("left[0] = %v, want %v", res.Left[0], want)
	}
}

func TestRenderBypassedPluginSkipped(t *testing.T) {
	s := NewSession(100)
	s.Tracks = []*Track{{
		ID: "t", Gain: 1, Pan: 0,
		Clips: []*Clip{{ID: "c", Start: 0, Duration: 1, Gain: 1, SourceID: "x"}},
		Chain: &EffectChain{Slots: []*PluginSlot{
			{ID: "g", PluginID: "builtin.gain", Enabled: true, Bypassed: true, Params: map[string]float64{"gain": 0.01}},
			{ID: "d", PluginID: "builtin.gain", Enabled: false, Params: map[string]float64{"gain": 0.01}},
		}},
	}}
	r := &Renderer{Registry: NewRegistry(), Sources: constSource{v: 1}}
	res, err := r.Render(s, cfg())
	if err != nil {
		t.Fatal(err)
	}
	want := float32(math.Cos(math.Pi / 4))
	if math.Abs(float64(res.Left[0])-float64(want)) > 1e-6 {
		t.Fatalf("bypassed/disabled plugins must not process: left[0] = %v, want %v", res.Left[0], want)
	}
}

// failPlugin always returns an error.
type failPlugin struct{}

func (failPlugin) ID() string { return "test.fail" }
func (failPlugin) Process(_ [][]float32, _ map[string]float64, _ float64, _ uint64) error {
	return errors.New("dsp exploded")
}

func sessionWithFailingPlugin() *Session {
	s := NewSession(100)
	s.Tracks = []*Track{{
		ID: "lead", Gain: 1, Pan: 0,
		Clips: []*Clip{{ID: "c", Start: 0, Duration: 1, Gain: 1, SourceID: "x"}},
		Chain: &EffectChain{Slots: []*PluginSlot{
			{ID: "slot7", PluginID: "test.fail", Enabled: true},
		}},
	}}
	return s
}

func TestPluginFailureFailFast(t *testing.T) {
	reg := NewRegistry()
	reg.Register("test.fail", func() Plugin { return failPlugin{} })
	r := &Renderer{Registry: reg, Sources: constSource{v: 1}}
	_, err := r.Render(sessionWithFailingPlugin(), cfg())
	if err == nil {
		t.Fatal("FailFast must surface the plugin failure")
	}
	var pe *PluginError
	if !errors.As(err, &pe) {
		t.Fatalf("error type = %T, want *PluginError", err)
	}
	if pe.TrackID != "lead" || pe.SlotID != "slot7" {
		t.Fatalf("failure must locate the channel: got track %q slot %q", pe.TrackID, pe.SlotID)
	}
}

func TestPluginFailureBypassPolicy(t *testing.T) {
	reg := NewRegistry()
	reg.Register("test.fail", func() Plugin { return failPlugin{} })
	r := &Renderer{Registry: reg, Sources: constSource{v: 1}}
	res, err := r.Render(sessionWithFailingPlugin(), RenderConfig{Duration: 1, Policy: BypassOnFailure, Seed: 1})
	if err != nil {
		t.Fatalf("BypassOnFailure must not abort the render: %v", err)
	}
	if len(res.Failures) != 1 || res.Failures[0].TrackID != "lead" {
		t.Fatalf("failure must be recorded with its channel: %+v", res.Failures)
	}
	// Output must be the unprocessed signal, not silence or a truncated mix.
	want := float32(math.Cos(math.Pi / 4))
	for i, v := range res.Left {
		if math.Abs(float64(v)-float64(want)) > 1e-6 {
			t.Fatalf("left[%d] = %v: failed plugin must bypass, not mute", i, v)
		}
	}
}

func TestRenderRejectsCycle(t *testing.T) {
	s := NewSession(100)
	s.Tracks = []*Track{
		{ID: "a", Gain: 1, Chain: &EffectChain{}, Sends: []*Send{{TargetTrackID: "b", Gain: 1}}},
		{ID: "b", Gain: 1, Chain: &EffectChain{}, Sends: []*Send{{TargetTrackID: "a", Gain: 1}}},
	}
	r := &Renderer{Registry: NewRegistry()}
	_, err := r.Render(s, cfg())
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want *CycleError", err, err)
	}
	if len(ce.TrackIDs) != 2 {
		t.Fatalf("cycle must name both tracks, got %v", ce.TrackIDs)
	}
}

func TestRenderSelfLoopRejected(t *testing.T) {
	s := NewSession(100)
	s.Tracks = []*Track{
		{ID: "a", Gain: 1, Chain: &EffectChain{}, Sends: []*Send{{TargetTrackID: "a", Gain: 1}}},
	}
	if err := ValidateGraph(s); err == nil {
		t.Fatal("self-send must be rejected")
	}
}

func TestValidDAGNotMisjudged(t *testing.T) {
	// Diamond routing: a -> b, a -> c, b -> d, c -> d. Acyclic, must pass.
	s := NewSession(100)
	mk := func(id string, targets ...string) *Track {
		t := &Track{ID: id, Gain: 1, Chain: &EffectChain{}}
		for _, tg := range targets {
			t.Sends = append(t.Sends, &Send{TargetTrackID: tg, Gain: 1})
		}
		return t
	}
	s.Tracks = []*Track{mk("a", "b", "c"), mk("b", "d"), mk("c", "d"), mk("d")}
	if err := ValidateGraph(s); err != nil {
		t.Fatalf("valid DAG misjudged as cyclic: %v", err)
	}
	r := &Renderer{Registry: NewRegistry(), Sources: constSource{v: 0.1}}
	if _, err := r.Render(s, cfg()); err != nil {
		t.Fatalf("valid DAG must render: %v", err)
	}
}
