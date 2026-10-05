// Package daw implements a DAW session model: tracks, clips, automation,
// effect chains, persistence, undo/redo, concurrent editing and offline
// rendering of the mix graph.
package daw

// Session is the root document of a project.
type Session struct {
	Version    int          `json:"version"`
	SampleRate float64      `json:"sample_rate"`
	Tracks     []*Track     `json:"tracks"`
	Master     *EffectChain `json:"master"`
}

// Track is a mixer channel with clips, sends and an effect chain.
type Track struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	GroupID string       `json:"group_id,omitempty"`
	Gain    float64      `json:"gain"`
	Pan     float64      `json:"pan"`
	Mute    bool         `json:"mute"`
	Solo    bool         `json:"solo"`
	Clips   []*Clip      `json:"clips"`
	Sends   []*Send      `json:"sends"`
	Chain   *EffectChain `json:"chain"`
}

// Clip is a piece of audio placed on the timeline.
type Clip struct {
	ID       string  `json:"id"`
	Start    float64 `json:"start"` // seconds on the timeline
	Duration float64 `json:"duration"`
	Offset   float64 `json:"offset"` // offset into the source
	Gain     float64 `json:"gain"`
	SourceID string  `json:"source_id"`
}

// Send routes a tap of a track's signal to another track (bus).
type Send struct {
	TargetTrackID string  `json:"target_track_id"`
	Gain          float64 `json:"gain"`
	PreFader      bool    `json:"pre_fader"`
}

// EffectChain is an ordered list of plugin slots.
type EffectChain struct {
	Slots []*PluginSlot `json:"slots"`
}

// PluginSlot hosts one plugin instance with enable/bypass state.
type PluginSlot struct {
	ID       string             `json:"id"`
	PluginID string             `json:"plugin_id"`
	Enabled  bool               `json:"enabled"`
	Bypassed bool               `json:"bypassed"`
	Params   map[string]float64 `json:"params"`
}

// NewSession creates an empty session with sane defaults.
func NewSession(sampleRate float64) *Session {
	return &Session{
		Version:    CurrentVersion,
		SampleRate: sampleRate,
		Tracks:     nil,
		Master:     &EffectChain{},
	}
}

// ChainSends returns the track's sends, tolerating a nil receiver list.
func (t *Track) ChainSends() []*Send {
	if t == nil {
		return nil
	}
	return t.Sends
}
