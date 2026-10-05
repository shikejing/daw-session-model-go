package daw

import (
	"fmt"
	"hash/fnv"
	"math"
)

// RenderConfig controls an offline render.
type RenderConfig struct {
	Duration float64       // seconds of output
	Policy   FailurePolicy // plugin failure handling
	Seed     uint64        // deterministic randomness seed
}

// RenderResult holds the rendered stereo mix plus diagnostics.
type RenderResult struct {
	Left     []float32
	Right    []float32
	Failures []*PluginError // non-fatal plugin failures (BypassOnFailure)
}

// Renderer evaluates the mix graph offline.
type Renderer struct {
	Registry *Registry
	// Sources provides clip audio by source ID; nil means synthesized
	// deterministic tones derived from (SourceID, Seed).
	Sources SourceProvider
}

// SourceProvider returns mono samples for a clip source, starting at
// offset seconds, n samples long.
type SourceProvider interface {
	Samples(sourceID string, offset float64, n int, sampleRate float64) []float32
}

// trackRender holds the two tap points of a rendered track.
type trackRender struct {
	pre  [2][]float32 // post-chain, pre-fader
	post [2][]float32 // post gain/pan, pre-mute
}

// Render renders the whole session to a stereo mix. The result is
// deterministic for the same session, config and seed.
func (r *Renderer) Render(s *Session, cfg RenderConfig) (*RenderResult, error) {
	if s == nil {
		return nil, fmt.Errorf("daw: render: nil session")
	}
	if cfg.Duration < 0 {
		return nil, fmt.Errorf("daw: render: negative duration %g", cfg.Duration)
	}
	n := int(math.Round(cfg.Duration * s.SampleRate))
	order, err := topoOrder(s)
	if err != nil {
		return nil, err
	}
	anySolo := false
	for _, t := range s.Tracks {
		if t != nil && t.Solo {
			anySolo = true
			break
		}
	}

	byID := make(map[string]*Track, len(s.Tracks))
	for _, t := range s.Tracks {
		if t != nil {
			byID[t.ID] = t
		}
	}

	res := &RenderResult{
		Left:  make([]float32, n),
		Right: make([]float32, n),
	}
	master := [2][]float32{res.Left, res.Right}
	sendAcc := make(map[string][2][]float32) // pending send inputs per track

	for _, id := range order {
		t := byID[id]
		in := sendAcc[id] // may be zero-value nil slices
		tr, err := r.renderTrack(s, t, in, cfg, n, res)
		if err != nil {
			return nil, err
		}
		// Route sends to their targets.
		for _, send := range t.Sends {
			if send == nil {
				continue
			}
			tap := tr.post
			if send.PreFader {
				tap = tr.pre
			}
			acc := sendAcc[send.TargetTrackID]
			for ch := 0; ch < 2; ch++ {
				if acc[ch] == nil {
					acc[ch] = make([]float32, n)
				}
				g := float32(send.Gain)
				for i := 0; i < n; i++ {
					acc[ch][i] += tap[ch][i] * g
				}
			}
			sendAcc[send.TargetTrackID] = acc
		}
		// Audible tracks contribute to the master bus.
		if audible(t, anySolo) {
			for ch := 0; ch < 2; ch++ {
				for i := 0; i < n; i++ {
					master[ch][i] += tr.post[ch][i]
				}
			}
		}
	}

	// Master effect chain.
	if err := r.runChain(s.Master, "master", master, cfg, s.SampleRate, res); err != nil {
		return nil, err
	}
	return res, nil
}

// audible applies mute and solo semantics: when any track is soloed, only
// soloed tracks are audible; muted tracks are never audible.
func audible(t *Track, anySolo bool) bool {
	if t.Mute {
		return false
	}
	if anySolo && !t.Solo {
		return false
	}
	return true
}

// renderTrack renders one track: clip audio plus incoming sends, through
// the effect chain, then gain and pan.
func (r *Renderer) renderTrack(s *Session, t *Track, sendIn [2][]float32, cfg RenderConfig, n int, res *RenderResult) (*trackRender, error) {
	buf := [2][]float32{make([]float32, n), make([]float32, n)}
	// Sum clip audio (mono sources feed both channels).
	for _, c := range t.Clips {
		if c == nil || c.Duration <= 0 {
			continue
		}
		r.mixClip(s, c, buf, cfg, n)
	}
	// Sum incoming sends.
	for ch := 0; ch < 2; ch++ {
		if sendIn[ch] != nil {
			for i := 0; i < n; i++ {
				buf[ch][i] += sendIn[ch][i]
			}
		}
	}
	// Effect chain.
	if err := r.runChain(t.Chain, t.ID, buf, cfg, s.SampleRate, res); err != nil {
		return nil, err
	}
	tr := &trackRender{pre: buf}
	// Gain and pan (constant-power pan law).
	g := float32(t.Gain)
	angle := (clamp(t.Pan, -1, 1) + 1) * math.Pi / 4 // 0..pi/2
	lg, rg := float32(math.Cos(angle))*g, float32(math.Sin(angle))*g
	post := [2][]float32{make([]float32, n), make([]float32, n)}
	for i := 0; i < n; i++ {
		post[0][i] = buf[0][i] * lg
		post[1][i] = buf[1][i] * rg
	}
	tr.post = post
	return tr, nil
}

// mixClip adds one clip's audio into buf.
func (r *Renderer) mixClip(s *Session, c *Clip, buf [2][]float32, cfg RenderConfig, n int) {
	sr := s.SampleRate
	start := int(math.Round(c.Start * sr))
	clipLen := int(math.Round(c.Duration * sr))
	if start >= n || start+clipLen <= 0 {
		return
	}
	src := r.sourceSamples(c.SourceID, c.Offset, clipLen, sr, cfg.Seed)
	g := float32(c.Gain)
	lo := max(start, 0)
	hi := min(start+clipLen, n)
	for i := lo; i < hi; i++ {
		v := src[i-start] * g
		buf[0][i] += v
		buf[1][i] += v
	}
}

// sourceSamples returns clip audio, from the provider if set, otherwise a
// deterministic synthesized signal keyed by (sourceID, seed).
func (r *Renderer) sourceSamples(sourceID string, offset float64, n int, sampleRate float64, seed uint64) []float32 {
	if r.Sources != nil {
		return r.Sources.Samples(sourceID, offset, n, sampleRate)
	}
	out := make([]float32, n)
	state := hashString(sourceID) ^ seed
	pos := int(math.Round(offset * sampleRate))
	// Skip to the offset deterministically.
	for i := 0; i < pos; i++ {
		state = xorshift(state)
	}
	for i := range out {
		state = xorshift(state)
		u := float32(state>>11) * (1.0 / (1 << 53))
		out[i] = 0.5 * (2*u - 1)
	}
	return out
}

// runChain applies an effect chain to buf, honoring enable/bypass and the
// failure policy. Failures are always surfaced: either returned (FailFast)
// or recorded in res.Failures (BypassOnFailure) — never silently dropped.
func (r *Renderer) runChain(chain *EffectChain, trackID string, buf [2][]float32, cfg RenderConfig, sampleRate float64, res *RenderResult) error {
	if chain == nil {
		return nil
	}
	for _, slot := range chain.Slots {
		if slot == nil || !slot.Enabled || slot.Bypassed {
			continue
		}
		p, err := r.Registry.Create(slot.PluginID)
		if err != nil {
			err = &PluginError{TrackID: trackID, SlotID: slot.ID, PluginID: slot.PluginID, Err: err}
			if cfg.Policy == FailFast {
				return err
			}
			res.Failures = append(res.Failures, err.(*PluginError))
			continue
		}
		seed := cfg.Seed ^ hashString(trackID) ^ hashString(slot.ID)
		if perr := p.Process(buf[:], slot.Params, sampleRate, seed); perr != nil {
			pe := &PluginError{TrackID: trackID, SlotID: slot.ID, PluginID: slot.PluginID, Err: perr}
			if cfg.Policy == FailFast {
				return pe
			}
			res.Failures = append(res.Failures, pe)
			continue // bypass: audio passes through unchanged
		}
	}
	return nil
}

func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func xorshift(x uint64) uint64 {
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	return x
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
