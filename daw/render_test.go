package daw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func renderSession() *Session {
	return &Session{
		SampleRate: 48000,
		Tracks: []*Track{
			{
				ID: "lead", Gain: 0.9, Pan: 0.2,
				Clips: []Clip{{ID: "c1", Source: "lead.wav", Start: 0, Duration: 4096, Gain: 1}},
				Sends: []Send{{TargetID: "bus", Gain: 0.5}},
				Chain: []PluginSlot{{PluginID: "gain", Enabled: true, Params: map[string]float64{"gain": 0.8}}},
			},
			{
				ID: "pad", Gain: 0.7, Pan: -0.3, Mute: false,
				Clips: []Clip{{ID: "c2", Source: "pad.wav", Start: 1024, Duration: 2048, Gain: 0.6}},
			},
			{ID: "bus", Gain: 1, Chain: []PluginSlot{{PluginID: "delay", Enabled: true}}},
		},
	}
}

func TestRenderDeterministic(t *testing.T) {
	s := renderSession()
	r := NewRenderer()
	opts := RenderOptions{Frames: 8192, Seed: 42}
	a, err := r.Render(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Render(context.Background(), s, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Samples) != 8192*2 {
		t.Fatalf("got %d samples, want %d", len(a.Samples), 8192*2)
	}
	for i := range a.Samples {
		if a.Samples[i] != b.Samples[i] {
			t.Fatalf("sample %d differs between identical renders: %v vs %v", i, a.Samples[i], b.Samples[i])
		}
	}
	// A different seed may differ, but must itself be reproducible.
	c1, _ := r.Render(context.Background(), s, RenderOptions{Frames: 8192, Seed: 7})
	c2, _ := r.Render(context.Background(), s, RenderOptions{Frames: 8192, Seed: 7})
	for i := range c1.Samples {
		if c1.Samples[i] != c2.Samples[i] {
			t.Fatalf("seed 7 renders differ at sample %d", i)
		}
	}
}

func energy(x []float64) float64 {
	var e float64
	for _, v := range x {
		e += v * v
	}
	return e
}

func TestMuteAndSolo(t *testing.T) {
	base := renderSession()
	r := NewRenderer()
	opts := RenderOptions{Frames: 4096, Seed: 1}
	full, err := r.Render(context.Background(), base, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Muting the only clip-bearing non-bus track must reduce energy.
	muted := renderSession()
	muted.FindTrack("lead").Mute = true
	muted.FindTrack("pad").Mute = true
	m, err := r.Render(context.Background(), muted, opts)
	if err != nil {
		t.Fatal(err)
	}
	if energy(m.Samples) >= energy(full.Samples) {
		t.Fatal("muting all source tracks must reduce mix energy")
	}

	// Solo on pad: lead (and its send into bus) must be inaudible.
	solo := renderSession()
	solo.FindTrack("pad").Solo = true
	solo.FindTrack("lead").Clips = nil // isolate: even its clips removed below
	s2 := renderSession()
	s2.FindTrack("pad").Solo = true
	s2.FindTrack("lead").Mute = true
	s2.FindTrack("bus").Chain = nil
	a, err := r.Render(context.Background(), solo, opts)
	if err != nil {
		t.Fatal(err)
	}
	// Compare against a session where lead is deleted entirely.
	s3 := renderSession()
	s3.FindTrack("pad").Solo = true
	s3.Tracks = []*Track{s3.FindTrack("pad"), s3.FindTrack("bus")}
	b, err := r.Render(context.Background(), s3, opts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Samples {
		if a.Samples[i] != b.Samples[i] {
			t.Fatalf("solo render differs from lead-deleted render at sample %d", i)
		}
	}
}

func TestSendRouting(t *testing.T) {
	// Track with a send to a bus must be audible on the bus output.
	withSend := &Session{
		SampleRate: 48000,
		Tracks: []*Track{
			{ID: "src", Gain: 1, Clips: []Clip{{ID: "c", Source: "x.wav", Start: 0, Duration: 2048, Gain: 1}},
				Sends: []Send{{TargetID: "bus", Gain: 1}}},
			{ID: "bus", Gain: 1},
		},
	}
	noSend := &Session{
		SampleRate: 48000,
		Tracks: []*Track{
			{ID: "src", Gain: 1, Clips: []Clip{{ID: "c", Source: "x.wav", Start: 0, Duration: 2048, Gain: 1}}},
			{ID: "bus", Gain: 1},
		},
	}
	r := NewRenderer()
	opts := RenderOptions{Frames: 2048, Seed: 3}
	a, err := r.Render(context.Background(), withSend, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Render(context.Background(), noSend, opts)
	if err != nil {
		t.Fatal(err)
	}
	if energy(a.Samples) <= energy(b.Samples) {
		t.Fatal("send must add the source signal to the bus")
	}
}

// failPlugin always errors, to exercise failure policies.
type failPlugin struct{ id string }

func (f failPlugin) Process(context.Context, []float64, int, map[string]float64, uint64) ([]float64, error) {
	return nil, errors.New("dsp crashed")
}

func sessionWithFailingPlugin() *Session {
	return &Session{
		SampleRate: 48000,
		Tracks: []*Track{
			{
				ID: "vox", Gain: 1,
				Clips: []Clip{{ID: "c", Source: "vox.wav", Start: 0, Duration: 2048, Gain: 1}},
				Chain: []PluginSlot{
					{PluginID: "gain", Enabled: true, Params: map[string]float64{"gain": 1}},
					{PluginID: "broken", Enabled: true},
				},
			},
		},
	}
}

func TestPluginFailureFailFast(t *testing.T) {
	reg := NewRegistry()
	reg.Register("broken", failPlugin{id: "broken"})
	r := NewRenderer()
	_, err := r.Render(context.Background(), sessionWithFailingPlugin(),
		RenderOptions{Frames: 2048, Seed: 1, Policy: FailFast, Registry: reg})
	var pe *PluginError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PluginError, got %v", err)
	}
	if pe.TrackID != "vox" || pe.Slot != 1 || pe.PluginID != "broken" {
		t.Fatalf("error must locate the failing channel: %+v", pe)
	}
}

func TestPluginFailureBypassKeepsOutput(t *testing.T) {
	reg := NewRegistry()
	reg.Register("broken", failPlugin{id: "broken"})
	r := NewRenderer()
	res, err := r.Render(context.Background(), sessionWithFailingPlugin(),
		RenderOptions{Frames: 2048, Seed: 1, Policy: BypassOnFailure, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Samples) != 2048*2 {
		t.Fatalf("output truncated: %d samples", len(res.Samples))
	}
	if len(res.Warnings) != 1 || res.Warnings[0].TrackID != "vox" || res.Warnings[0].Slot != 1 {
		t.Fatalf("warning must locate the failing slot: %+v", res.Warnings)
	}
	// Output must equal the render with the broken slot simply disabled:
	// the failure caused neither silence nor partial audio.
	ref := sessionWithFailingPlugin()
	ref.FindTrack("vox").Chain[1].Enabled = false
	want, err := r.Render(context.Background(), ref, RenderOptions{Frames: 2048, Seed: 1, Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Samples {
		if res.Samples[i] != want.Samples[i] {
			t.Fatalf("bypass-on-failure output differs at sample %d", i)
		}
	}
	if energy(res.Samples) == 0 {
		t.Fatal("plugin failure must not silence the output")
	}
}

func TestUnregisteredPluginFails(t *testing.T) {
	s := sessionWithFailingPlugin() // "broken" not in default registry
	r := NewRenderer()
	_, err := r.Render(context.Background(), s, RenderOptions{Frames: 256, Seed: 1, Policy: FailFast})
	var pe *PluginError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PluginError for unregistered plugin, got %v", err)
	}
	if !strings.Contains(pe.Err.Error(), "not registered") {
		t.Fatalf("unexpected cause: %v", pe.Err)
	}
}

func TestBypassedPluginNotInvoked(t *testing.T) {
	reg := NewRegistry()
	reg.Register("broken", failPlugin{id: "broken"})
	s := sessionWithFailingPlugin()
	s.FindTrack("vox").Chain[1].Enabled = false // bypassed
	r := NewRenderer()
	if _, err := r.Render(context.Background(), s,
		RenderOptions{Frames: 256, Seed: 1, Policy: FailFast, Registry: reg}); err != nil {
		t.Fatalf("bypassed plugin must not run: %v", err)
	}
}

func TestAutomationAffectsRender(t *testing.T) {
	mk := func(auto []AutomationCurve) *Session {
		return &Session{
			SampleRate: 48000,
			Tracks: []*Track{{
				ID: "t", Gain: 1,
				Clips:      []Clip{{ID: "c", Source: "a.wav", Start: 0, Duration: 1000, Gain: 1}},
				Automation: auto,
			}},
		}
	}
	r := NewRenderer()
	opts := RenderOptions{Frames: 1000, Seed: 5}
	plain, err := r.Render(context.Background(), mk(nil), opts)
	if err != nil {
		t.Fatal(err)
	}
	faded, err := r.Render(context.Background(), mk([]AutomationCurve{
		{Param: "gain", Points: []AutomationPoint{{Time: 0, Value: 1}, {Time: 999, Value: 0}}},
	}), opts)
	if err != nil {
		t.Fatal(err)
	}
	if energy(faded.Samples) >= energy(plain.Samples) {
		t.Fatal("gain fade-out automation must reduce energy")
	}
	if fmt.Sprintf("%v", faded.Samples) == fmt.Sprintf("%v", plain.Samples) {
		t.Fatal("automation had no effect")
	}
}
