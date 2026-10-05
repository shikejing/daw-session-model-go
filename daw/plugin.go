package daw

import "fmt"

// Plugin processes audio buffers in place (stereo: 2 channels).
type Plugin interface {
	// ID identifies the plugin type, e.g. "builtin.gain".
	ID() string
	// Process modifies buf in place. seed makes stochastic plugins
	// deterministic for a given render.
	Process(buf [][]float32, params map[string]float64, sampleRate float64, seed uint64) error
}

// PluginError wraps a plugin failure with the channel (track) and slot
// that caused it, so failures are locatable.
type PluginError struct {
	TrackID  string
	SlotID   string
	PluginID string
	Err      error
}

func (e *PluginError) Error() string {
	return fmt.Sprintf("daw: plugin %q failed on track %q slot %q: %v",
		e.PluginID, e.TrackID, e.SlotID, e.Err)
}

func (e *PluginError) Unwrap() error { return e.Err }

// FailurePolicy decides how the renderer reacts to a plugin failure.
type FailurePolicy int

const (
	// FailFast aborts the render, returning a *PluginError.
	FailFast FailurePolicy = iota
	// BypassOnFailure bypasses the failing plugin and records the error
	// in RenderResult.Failures; output is never silently dropped.
	BypassOnFailure
)

// Registry maps plugin IDs to factories.
type Registry struct {
	factories map[string]func() Plugin
}

// NewRegistry returns a registry with the built-in plugins.
func NewRegistry() *Registry {
	r := &Registry{factories: map[string]func() Plugin{}}
	r.Register("builtin.gain", func() Plugin { return gainPlugin{} })
	r.Register("builtin.noise", func() Plugin { return noisePlugin{} })
	return r
}

// Register adds a plugin factory.
func (r *Registry) Register(id string, f func() Plugin) {
	r.factories[id] = f
}

// Create instantiates a plugin by ID.
func (r *Registry) Create(id string) (Plugin, error) {
	f, ok := r.factories[id]
	if !ok {
		return nil, fmt.Errorf("daw: unknown plugin %q", id)
	}
	return f(), nil
}

// gainPlugin applies a constant gain, param "gain" (default 1).
type gainPlugin struct{}

func (gainPlugin) ID() string { return "builtin.gain" }

func (gainPlugin) Process(buf [][]float32, params map[string]float64, _ float64, _ uint64) error {
	g := float32(paramOr(params, "gain", 1))
	for ch := range buf {
		for i := range buf[ch] {
			buf[ch][i] *= g
		}
	}
	return nil
}

// noisePlugin adds deterministic white noise, param "amount" (default 0.01).
// The noise depends only on (seed, sample index), so renders with the same
// seed are bit-identical.
type noisePlugin struct{}

func (noisePlugin) ID() string { return "builtin.noise" }

func (noisePlugin) Process(buf [][]float32, params map[string]float64, _ float64, seed uint64) error {
	amount := float32(paramOr(params, "amount", 0.01))
	for ch := range buf {
		state := seed ^ (uint64(ch+1) * 0x9E3779B97F4A7C15)
		for i := range buf[ch] {
			state ^= state << 13
			state ^= state >> 7
			state ^= state << 17
			// map to [-1, 1)
			u := float32(state>>11) * (1.0 / (1 << 53))
			buf[ch][i] += amount * (2*u - 1)
		}
	}
	return nil
}

func paramOr(params map[string]float64, key string, def float64) float64 {
	if params != nil {
		if v, ok := params[key]; ok {
			return v
		}
	}
	return def
}
