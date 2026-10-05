package daw

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
)

// FailurePolicy decides how plugin failures are surfaced during rendering.
type FailurePolicy int

const (
	// FailFast aborts the render with a *PluginError identifying the track.
	FailFast FailurePolicy = iota
	// BypassOnFailure treats a failing plugin as bypassed and records a
	// warning, so the render still completes with full-length output.
	BypassOnFailure
)

// PluginError identifies a plugin failure at a specific track and slot.
type PluginError struct {
	TrackID  string
	Slot     int
	PluginID string
	Err      error
}

func (e *PluginError) Error() string {
	return fmt.Sprintf("daw: plugin %q (track %q slot %d) failed: %v", e.PluginID, e.TrackID, e.Slot, e.Err)
}

// Unwrap returns the underlying plugin error.
func (e *PluginError) Unwrap() error { return e.Err }

// RenderWarning records a non-fatal problem (e.g. bypassed failing plugin).
type RenderWarning struct {
	TrackID  string
	Slot     int
	PluginID string
	Message  string
}

// RenderOptions configures an offline render.
type RenderOptions struct {
	Frames      int           // output length in frames (per channel)
	Seed        uint64        // random seed; same seed => identical output
	Policy      FailurePolicy // plugin failure handling
	Registry    *Registry     // plugin registry; nil uses NewRegistry()
	Incremental bool          // reuse cached per-track renders when possible
}

// RenderResult is the rendered mix plus diagnostics.
type RenderResult struct {
	// Samples is interleaved stereo: Frames*2 values (L,R per frame).
	Samples  []float64
	Warnings []RenderWarning
}

// Renderer renders sessions to audio, optionally caching per-track results
// for incremental re-renders.
type Renderer struct {
	mu             sync.Mutex
	cache          map[uint64][]float64 // track-output cache for incremental renders
	tracksRendered int                  // tracks actually computed (diagnostics)
	cacheHits      int                  // tracks served from cache
}

// NewRenderer creates a Renderer.
func NewRenderer() *Renderer {
	return &Renderer{cache: map[uint64][]float64{}}
}

// RenderStats reports how many tracks were computed vs served from cache
// across all renders performed by this Renderer.
type RenderStats struct {
	TracksRendered int
	CacheHits      int
}

// Stats returns cumulative rendering statistics.
func (r *Renderer) Stats() RenderStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RenderStats{TracksRendered: r.tracksRendered, CacheHits: r.cacheHits}
}

// ResetCache drops all cached per-track renders.
func (r *Renderer) ResetCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cache = map[uint64][]float64{}
}

// Render evaluates the mix graph offline. The result is deterministic for
// identical session content, options and seed.
func (r *Renderer) Render(ctx context.Context, s *Session, opts RenderOptions) (*RenderResult, error) {
	if opts.Frames < 0 {
		return nil, fmt.Errorf("daw: negative frame count %d", opts.Frames)
	}
	if err := CheckAcyclic(s); err != nil {
		return nil, err
	}
	reg := opts.Registry
	if reg == nil {
		reg = NewRegistry()
	}
	sr := s.SampleRate
	if sr <= 0 {
		sr = 48000
	}

	// Solo logic: with any solo active, only solo tracks and tracks
	// downstream of them (via sends) are audible.
	audible := audibleTracks(s)

	// Render tracks in topological order so senders are computed before
	// the tracks receiving their sends.
	order := topoOrder(s)
	n := opts.Frames * 2
	outputs := make(map[string][]float64, len(s.Tracks))
	res := &RenderResult{}

	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t := s.FindTrack(id)
		var buf []float64
		if audible[id] && !t.Mute {
			var err error
			buf, err = r.renderTrack(ctx, s, t, outputs, sr, opts, reg, res)
			if err != nil {
				return nil, err
			}
		} else {
			buf = make([]float64, n)
		}
		outputs[id] = buf
	}

	mix := make([]float64, n)
	for _, t := range s.Tracks {
		out := outputs[t.ID]
		for i := range mix {
			mix[i] += out[i]
		}
	}
	res.Samples = mix
	return res, nil
}

// renderTrack produces one track's post-fader, post-chain stereo output:
// clips + incoming sends, automation, gain/pan, then the effect chain.
func (r *Renderer) renderTrack(ctx context.Context, s *Session, t *Track, outputs map[string][]float64, sr int, opts RenderOptions, reg *Registry, res *RenderResult) ([]float64, error) {
	n := opts.Frames * 2

	// Incoming sends (senders are already rendered due to topo order).
	var sendIn []float64
	hasSendIn := false
	for _, other := range s.Tracks {
		for _, send := range other.Sends {
			if send.TargetID == t.ID {
				hasSendIn = true
				if sendIn == nil {
					sendIn = make([]float64, n)
				}
				src := outputs[other.ID]
				for i := range sendIn {
					sendIn[i] += src[i] * send.Gain
				}
			}
		}
	}

	// Full-output cache: only for tracks without incoming sends, since
	// their output depends solely on their own content.
	var key uint64
	cacheable := opts.Incremental && !hasSendIn
	if cacheable {
		key = r.trackKey(s, t, opts)
		r.mu.Lock()
		if cached, ok := r.cache[key]; ok {
			r.cacheHits++
			r.mu.Unlock()
			return append([]float64(nil), cached...), nil
		}
		r.mu.Unlock()
	}

	buf := r.renderClips(t, sr, opts)
	if hasSendIn {
		for i := range buf {
			buf[i] += sendIn[i]
		}
	}
	applyAutomation(buf, t, sr)
	applyGainPan(buf, t)

	for slot, ps := range t.Chain {
		if !ps.Enabled {
			continue // bypassed
		}
		p, ok := reg.Lookup(ps.PluginID)
		if !ok {
			err := &PluginError{TrackID: t.ID, Slot: slot, PluginID: ps.PluginID,
				Err: fmt.Errorf("plugin not registered")}
			if opts.Policy == FailFast {
				return nil, err
			}
			res.Warnings = append(res.Warnings, RenderWarning{
				TrackID: t.ID, Slot: slot, PluginID: ps.PluginID,
				Message: "plugin not registered; slot bypassed"})
			continue
		}
		out, err := p.Process(ctx, buf, sr, ps.Params, opts.Seed)
		if err != nil || len(out) != len(buf) {
			if err == nil {
				err = fmt.Errorf("plugin returned %d samples, expected %d", len(out), len(buf))
			}
			pe := &PluginError{TrackID: t.ID, Slot: slot, PluginID: ps.PluginID, Err: err}
			if opts.Policy == FailFast {
				return nil, pe
			}
			res.Warnings = append(res.Warnings, RenderWarning{
				TrackID: t.ID, Slot: slot, PluginID: ps.PluginID,
				Message: fmt.Sprintf("plugin failed (%v); slot bypassed", err)})
			continue // keep buf: failure must not silence or truncate output
		}
		buf = out
	}

	if cacheable {
		r.mu.Lock()
		r.cache[key] = append([]float64(nil), buf...)
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.tracksRendered++
	r.mu.Unlock()
	return buf, nil
}

// renderClips sums the track's clips into a stereo buffer. Clip audio is
// synthesized deterministically from the source ID and render seed.
func (r *Renderer) renderClips(t *Track, sr int, opts RenderOptions) []float64 {
	n := opts.Frames * 2
	buf := make([]float64, n)
	for _, c := range t.Clips {
		for f := int64(0); f < c.Duration; f++ {
			pos := c.Start + f
			if pos < 0 || pos >= int64(opts.Frames) {
				continue
			}
			v := sourceSample(c.Source, opts.Seed, c.Offset+f, sr) * c.Gain
			buf[pos*2] += v
			buf[pos*2+1] += v
		}
	}
	return buf
}

// sourceSample deterministically synthesizes a source's sample at pos.
func sourceSample(source string, seed uint64, pos int64, sr int) float64 {
	h := fnv.New64a()
	h.Write([]byte(source))
	var b [8]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(seed >> (8 * i))
	}
	h.Write(b[:])
	freq := 110.0 + float64(h.Sum64()%990) // 110..1099 Hz, stable per source+seed
	return 0.5 * math.Sin(2*math.Pi*freq*float64(pos)/float64(sr))
}

// applyAutomation evaluates "gain" and "pan" curves per frame.
func applyAutomation(buf []float64, t *Track, sr int) {
	for _, curve := range t.Automation {
		if len(curve.Points) == 0 {
			continue
		}
		frames := len(buf) / 2
		switch curve.Param {
		case "gain":
			for f := 0; f < frames; f++ {
				g := evalCurve(curve.Points, int64(f))
				buf[f*2] *= g
				buf[f*2+1] *= g
			}
		case "pan":
			for f := 0; f < frames; f++ {
				p := evalCurve(curve.Points, int64(f))
				applyPan(buf[f*2:], p)
			}
		}
	}
}

// evalCurve linearly interpolates the curve at time t.
func evalCurve(pts []AutomationPoint, t int64) float64 {
	if t <= pts[0].Time {
		return pts[0].Value
	}
	last := pts[len(pts)-1]
	if t >= last.Time {
		return last.Value
	}
	lo, hi := 0, len(pts)-1
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if pts[mid].Time <= t {
			lo = mid
		} else {
			hi = mid
		}
	}
	a, b := pts[lo], pts[hi]
	frac := float64(t-a.Time) / float64(b.Time-a.Time)
	return a.Value + frac*(b.Value-a.Value)
}

// applyGainPan applies the track fader and constant-power pan balance.
func applyGainPan(buf []float64, t *Track) {
	for f := 0; f+1 < len(buf); f += 2 {
		buf[f] *= t.Gain
		buf[f+1] *= t.Gain
	}
	applyPan(buf, t.Pan)
}

// applyPan applies a constant-power balance to interleaved stereo in place.
func applyPan(buf []float64, pan float64) {
	p := math.Max(-1, math.Min(1, pan))
	angle := (p + 1) * math.Pi / 4
	lg, rg := math.Sqrt2*math.Cos(angle), math.Sqrt2*math.Sin(angle)
	for f := 0; f+1 < len(buf); f += 2 {
		buf[f] *= lg
		buf[f+1] *= rg
	}
}

// audibleTracks computes the set of tracks contributing to the mix.
func audibleTracks(s *Session) map[string]bool {
	audible := map[string]bool{}
	anySolo := false
	for _, t := range s.Tracks {
		if t.Solo {
			anySolo = true
		}
	}
	if !anySolo {
		for _, t := range s.Tracks {
			audible[t.ID] = true
		}
		return audible
	}
	// Solo tracks plus everything downstream of them via sends.
	var mark func(id string)
	mark = func(id string) {
		if audible[id] {
			return
		}
		audible[id] = true
		t := s.FindTrack(id)
		for _, send := range t.Sends {
			mark(send.TargetID)
		}
	}
	for _, t := range s.Tracks {
		if t.Solo {
			mark(t.ID)
		}
	}
	return audible
}

// topoOrder returns track IDs with senders before their send targets.
func topoOrder(s *Session) []string {
	indeg := map[string]int{}
	for _, t := range s.Tracks {
		indeg[t.ID] = 0
	}
	for _, t := range s.Tracks {
		for _, send := range t.Sends {
			indeg[send.TargetID]++
		}
	}
	var queue []string
	for _, t := range s.Tracks {
		if indeg[t.ID] == 0 {
			queue = append(queue, t.ID)
		}
	}
	sort.Strings(queue)
	var order []string
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, id)
		t := s.FindTrack(id)
		var ready []string
		for _, send := range t.Sends {
			indeg[send.TargetID]--
			if indeg[send.TargetID] == 0 {
				ready = append(ready, send.TargetID)
			}
		}
		sort.Strings(ready)
		queue = append(queue, ready...)
	}
	return order
}

// trackKey hashes everything that affects a send-free track's output.
func (r *Renderer) trackKey(s *Session, t *Track, opts RenderOptions) uint64 {
	payload := struct {
		Track      *Track `json:"track"`
		Frames     int    `json:"frames"`
		SampleRate int    `json:"sampleRate"`
		Seed       uint64 `json:"seed"`
		AnySolo    bool   `json:"anySolo"`
	}{t, opts.Frames, s.SampleRate, opts.Seed, anySolo(s)}
	data, _ := json.Marshal(payload)
	h := fnv.New64a()
	h.Write(data)
	return h.Sum64()
}

func anySolo(s *Session) bool {
	for _, t := range s.Tracks {
		if t.Solo {
			return true
		}
	}
	return false
}
