// Package daw implements a DAW session & track model: sessions, tracks,
// clips, automation, effect chains, undo/redo history, concurrent editing,
// and offline rendering of the mix graph.
package daw

// CurrentFormatVersion is the session file format version written by Save.
const CurrentFormatVersion = 2

// AutomationPoint is one breakpoint of an automation curve.
type AutomationPoint struct {
	Time  int64   `json:"time"`  // sample offset from session start
	Value float64 `json:"value"` // parameter value at Time
}

// AutomationCurve is a named parameter automation lane.
type AutomationCurve struct {
	Param  string            `json:"param"`  // e.g. "gain", "pan"
	Points []AutomationPoint `json:"points"` // sorted by Time
}

// Clip is an audio region placed on a track timeline.
type Clip struct {
	ID       string  `json:"id"`
	Source   string  `json:"source"`   // source media identifier
	Start    int64   `json:"start"`    // position on timeline, in samples
	Duration int64   `json:"duration"` // length, in samples
	Offset   int64   `json:"offset"`   // offset into the source, in samples
	Gain     float64 `json:"gain"`     // linear clip gain
}

// Send routes a track's post-fader signal to another track (bus).
type Send struct {
	TargetID string  `json:"targetId"` // destination track ID
	Gain     float64 `json:"gain"`     // linear send gain
}

// PluginSlot is one slot of a track's effect chain.
type PluginSlot struct {
	PluginID string             `json:"pluginId"` // registered plugin identifier
	Enabled  bool               `json:"enabled"`  // false = bypassed
	Params   map[string]float64 `json:"params,omitempty"`
}

// Track is a mixer channel with clips, automation, sends and an effect chain.
type Track struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	GroupID    string            `json:"groupId,omitempty"` // track group membership
	Gain       float64           `json:"gain"`              // linear fader gain
	Pan        float64           `json:"pan"`               // -1 (left) .. +1 (right)
	Mute       bool              `json:"mute"`
	Solo       bool              `json:"solo"`
	Clips      []Clip            `json:"clips"`
	Automation []AutomationCurve `json:"automation,omitempty"`
	Sends      []Send            `json:"sends,omitempty"`
	Chain      []PluginSlot      `json:"chain,omitempty"`
}

// Session is the root document of a project.
type Session struct {
	Version    int      `json:"version"`
	Name       string   `json:"name"`
	SampleRate int      `json:"sampleRate"`
	Tracks     []*Track `json:"tracks"`
}

// normalize canonicalizes empty/nil slices so that a session and its
// save/load round-trip are deeply equal.
func (s *Session) normalize() {
	if s.Tracks == nil {
		s.Tracks = []*Track{}
	}
	for _, t := range s.Tracks {
		if t.Clips == nil {
			t.Clips = []Clip{}
		}
		if len(t.Automation) == 0 {
			t.Automation = nil
		}
		if len(t.Sends) == 0 {
			t.Sends = nil
		}
		if len(t.Chain) == 0 {
			t.Chain = nil
		}
		for i := range t.Chain {
			if len(t.Chain[i].Params) == 0 {
				t.Chain[i].Params = nil
			}
		}
		for i := range t.Automation {
			if t.Automation[i].Points == nil {
				t.Automation[i].Points = []AutomationPoint{}
			}
		}
	}
}

// Clone returns a deep copy of the session.
func (s *Session) Clone() *Session {
	c := *s
	if s.Tracks != nil {
		c.Tracks = make([]*Track, len(s.Tracks))
	}
	for i, t := range s.Tracks {
		nt := *t
		nt.Clips = append([]Clip(nil), t.Clips...)
		nt.Sends = append([]Send(nil), t.Sends...)
		if t.Automation != nil {
			nt.Automation = make([]AutomationCurve, len(t.Automation))
		}
		for j, a := range t.Automation {
			na := a
			na.Points = append([]AutomationPoint(nil), a.Points...)
			nt.Automation[j] = na
		}
		if t.Chain != nil {
			nt.Chain = make([]PluginSlot, len(t.Chain))
		}
		for j, p := range t.Chain {
			np := p
			if p.Params != nil {
				np.Params = make(map[string]float64, len(p.Params))
				for k, v := range p.Params {
					np.Params[k] = v
				}
			}
			nt.Chain[j] = np
		}
		c.Tracks[i] = &nt
	}
	return &c
}

// FindTrack returns the track with the given ID, or nil.
func (s *Session) FindTrack(id string) *Track {
	for _, t := range s.Tracks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
