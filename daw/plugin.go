package daw

import (
	"context"
	"math"
	"sync"
)

// Plugin processes audio. Implementations must be deterministic for a given
// input, parameter set and seed.
type Plugin interface {
	// Process transforms interleaved stereo samples (frames*2 values).
	// The returned slice must have the same length as the input.
	Process(ctx context.Context, in []float64, sampleRate int, params map[string]float64, seed uint64) ([]float64, error)
}

// PluginFunc adapts a function to the Plugin interface.
type PluginFunc func(ctx context.Context, in []float64, sampleRate int, params map[string]float64, seed uint64) ([]float64, error)

// Process implements Plugin.
func (f PluginFunc) Process(ctx context.Context, in []float64, sampleRate int, params map[string]float64, seed uint64) ([]float64, error) {
	return f(ctx, in, sampleRate, params, seed)
}

// Registry maps plugin IDs to plugin instances. It is safe for concurrent
// use during rendering.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
}

// NewRegistry returns a registry pre-populated with the builtin plugins
// ("gain", "pan", "delay").
func NewRegistry() *Registry {
	r := &Registry{plugins: map[string]Plugin{}}
	r.Register("gain", PluginFunc(gainPlugin))
	r.Register("pan", PluginFunc(panPlugin))
	r.Register("delay", PluginFunc(delayPlugin))
	return r
}

// Register adds or replaces a plugin under id.
func (r *Registry) Register(id string, p Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[id] = p
}

// Lookup returns the plugin for id.
func (r *Registry) Lookup(id string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[id]
	return p, ok
}

func paramOr(params map[string]float64, key string, def float64) float64 {
	if v, ok := params[key]; ok {
		return v
	}
	return def
}

// gainPlugin applies a linear gain ("gain", default 1).
func gainPlugin(_ context.Context, in []float64, _ int, params map[string]float64, _ uint64) ([]float64, error) {
	g := paramOr(params, "gain", 1)
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = v * g
	}
	return out, nil
}

// panPlugin applies constant-power stereo panning ("pan", -1..1, default 0).
func panPlugin(_ context.Context, in []float64, _ int, params map[string]float64, _ uint64) ([]float64, error) {
	pan := math.Max(-1, math.Min(1, paramOr(params, "pan", 0)))
	angle := (pan + 1) * math.Pi / 4 // 0..pi/2
	lg, rg := math.Cos(angle), math.Sin(angle)
	out := make([]float64, len(in))
	for i := 0; i+1 < len(in); i += 2 {
		m := (in[i] + in[i+1]) * 0.5
		out[i], out[i+1] = m*lg, m*rg
	}
	return out, nil
}

// delayPlugin is a simple stereo echo ("time" seconds, default 0.25;
// "mix" 0..1, default 0.3; "feedback" 0..0.95, default 0).
func delayPlugin(_ context.Context, in []float64, sampleRate int, params map[string]float64, _ uint64) ([]float64, error) {
	dt := paramOr(params, "time", 0.25)
	mix := math.Max(0, math.Min(1, paramOr(params, "mix", 0.3)))
	fb := math.Max(0, math.Min(0.95, paramOr(params, "feedback", 0)))
	delayFrames := int(dt*float64(sampleRate) + 0.5)
	out := make([]float64, len(in))
	copy(out, in)
	if delayFrames <= 0 {
		return out, nil
	}
	d := delayFrames * 2 // interleaved
	for i := d; i < len(out); i++ {
		out[i] += (in[i-d] + fb*out[i-d]) * mix
	}
	return out, nil
}
