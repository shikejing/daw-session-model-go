package daw

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

// IncrementalRenderer caches per-track renders so that re-rendering after
// a small edit only recomputes the tracks whose inputs changed. A track's
// cache key covers its own content and, transitively, the keys of every
// track it receives sends from, so edits invalidate exactly the downstream
// cone of the change.
type IncrementalRenderer struct {
	r     *Renderer
	cache map[string]*trackCacheEntry
	stats RenderStats
}

type trackCacheEntry struct {
	key      uint64
	tr       *trackRender
	failures []*PluginError
}

// NewIncrementalRenderer creates an incremental renderer wrapping r.
func NewIncrementalRenderer(r *Renderer) *IncrementalRenderer {
	return &IncrementalRenderer{r: r, cache: map[string]*trackCacheEntry{}}
}

// Stats reports cache behaviour of the last Render call.
type RenderStats struct {
	TracksTotal      int
	TracksReused     int
	TracksRecomputed int
}

// LastStats returns stats of the most recent Render.
func (ir *IncrementalRenderer) LastStats() RenderStats { return ir.stats }

// Render renders the session, reusing cached track buffers when the
// track's content and its send dependencies are unchanged.
func (ir *IncrementalRenderer) Render(s *Session, cfg RenderConfig) (*RenderResult, error) {
	if s == nil {
		return nil, fmt.Errorf("daw: render: nil session")
	}
	n := int(math.Round(cfg.Duration * s.SampleRate))
	order, err := topoOrder(s)
	if err != nil {
		return nil, err
	}
	anySolo := false
	byID := make(map[string]*Track, len(s.Tracks))
	for _, t := range s.Tracks {
		if t != nil {
			byID[t.ID] = t
			if t.Solo {
				anySolo = true
			}
		}
	}

	res := &RenderResult{
		Left:  make([]float32, n),
		Right: make([]float32, n),
	}
	master := [2][]float32{res.Left, res.Right}
	sendAcc := make(map[string][2][]float32)
	keys := make(map[string]uint64, len(order))
	stats := RenderStats{TracksTotal: len(order)}
	alive := make(map[string]bool, len(order))

	for _, id := range order {
		t := byID[id]
		alive[id] = true
		key := ir.trackKey(s, t, keys, cfg, n)
		keys[id] = key

		var tr *trackRender
		if ent, ok := ir.cache[id]; ok && ent.key == key {
			tr = ent.tr
			res.Failures = append(res.Failures, ent.failures...)
			stats.TracksReused++
		} else {
			before := len(res.Failures)
			var err error
			tr, err = ir.r.renderTrack(s, t, sendAcc[id], cfg, n, res)
			if err != nil {
				return nil, err
			}
			ent := &trackCacheEntry{key: key, tr: tr}
			ent.failures = append(ent.failures, res.Failures[before:]...)
			ir.cache[id] = ent
			stats.TracksRecomputed++
		}

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
		if audible(t, anySolo) {
			for ch := 0; ch < 2; ch++ {
				for i := 0; i < n; i++ {
					master[ch][i] += tr.post[ch][i]
				}
			}
		}
	}

	// Drop cache entries for deleted tracks.
	for id := range ir.cache {
		if !alive[id] {
			delete(ir.cache, id)
		}
	}

	if err := ir.r.runChain(s.Master, "master", master, cfg, s.SampleRate, res); err != nil {
		return nil, err
	}
	ir.stats = stats
	return res, nil
}

// trackKey hashes the track's own content together with the keys of the
// tracks it receives sends from, plus render-shape parameters.
func (ir *IncrementalRenderer) trackKey(s *Session, t *Track, keys map[string]uint64, cfg RenderConfig, n int) uint64 {
	h := hashString(t.ID)
	// Own content: everything on the track that can affect its output.
	raw, _ := json.Marshal(t)
	h ^= hashBytes(raw)
	// Dependencies: sorted send-source keys for determinism.
	var srcKeys []uint64
	for _, other := range s.Tracks {
		if other == nil {
			continue
		}
		for _, send := range other.Sends {
			if send != nil && send.TargetTrackID == t.ID {
				srcKeys = append(srcKeys, keys[other.ID])
			}
		}
	}
	sort.Slice(srcKeys, func(i, j int) bool { return srcKeys[i] < srcKeys[j] })
	for _, k := range srcKeys {
		h ^= k*0x9E3779B97F4A7C15 + 1
	}
	// Render shape.
	h ^= uint64(n)<<32 ^ uint64(s.SampleRate) ^ cfg.Seed
	return h
}

func hashBytes(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}
