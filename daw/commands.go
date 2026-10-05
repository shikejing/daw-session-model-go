package daw

import "fmt"

// findTrack locates a track by ID, returning the track and its index.
func findTrack(s *Session, id string) (*Track, int) {
	for i, t := range s.Tracks {
		if t != nil && t.ID == id {
			return t, i
		}
	}
	return nil, -1
}

// AddTrackCmd appends a track.
type AddTrackCmd struct{ Track *Track }

func (c *AddTrackCmd) Apply(s *Session) error {
	if c.Track == nil {
		return fmt.Errorf("daw: AddTrack: nil track")
	}
	if t, _ := findTrack(s, c.Track.ID); t != nil {
		return fmt.Errorf("daw: AddTrack: track %q already exists", c.Track.ID)
	}
	s.Tracks = append(s.Tracks, c.Track)
	return nil
}

func (c *AddTrackCmd) Revert(s *Session) error {
	_, i := findTrack(s, c.Track.ID)
	if i < 0 {
		return fmt.Errorf("daw: AddTrack revert: track %q missing", c.Track.ID)
	}
	s.Tracks = append(s.Tracks[:i], s.Tracks[i+1:]...)
	return nil
}

func (c *AddTrackCmd) String() string { return "AddTrack(" + c.Track.ID + ")" }

// RemoveTrackCmd removes a track by ID, remembering its index for revert.
type RemoveTrackCmd struct {
	TrackID string
	removed *Track
	index   int
}

func (c *RemoveTrackCmd) Apply(s *Session) error {
	t, i := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: RemoveTrack: track %q not found", c.TrackID)
	}
	c.removed, c.index = t, i
	s.Tracks = append(s.Tracks[:i], s.Tracks[i+1:]...)
	return nil
}

func (c *RemoveTrackCmd) Revert(s *Session) error {
	if c.removed == nil {
		return fmt.Errorf("daw: RemoveTrack revert: nothing removed")
	}
	s.Tracks = append(s.Tracks, nil)
	copy(s.Tracks[c.index+1:], s.Tracks[c.index:])
	s.Tracks[c.index] = c.removed
	return nil
}

func (c *RemoveTrackCmd) String() string { return "RemoveTrack(" + c.TrackID + ")" }

// SetTrackParamCmd changes a scalar track field (gain or pan).
type SetTrackParamCmd struct {
	TrackID string
	Field   string // "gain" or "pan"
	Value   float64
	old     float64
}

func (c *SetTrackParamCmd) Apply(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: SetTrackParam: track %q not found", c.TrackID)
	}
	switch c.Field {
	case "gain":
		c.old, t.Gain = t.Gain, c.Value
	case "pan":
		c.old, t.Pan = t.Pan, c.Value
	default:
		return fmt.Errorf("daw: SetTrackParam: unknown field %q", c.Field)
	}
	return nil
}

func (c *SetTrackParamCmd) Revert(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: SetTrackParam revert: track %q not found", c.TrackID)
	}
	switch c.Field {
	case "gain":
		t.Gain = c.old
	case "pan":
		t.Pan = c.old
	default:
		return fmt.Errorf("daw: SetTrackParam revert: unknown field %q", c.Field)
	}
	return nil
}

func (c *SetTrackParamCmd) String() string {
	return fmt.Sprintf("SetTrackParam(%s.%s=%g)", c.TrackID, c.Field, c.Value)
}

// AddClipCmd inserts a clip into a track.
type AddClipCmd struct {
	TrackID string
	Clip    *Clip
}

func (c *AddClipCmd) Apply(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: AddClip: track %q not found", c.TrackID)
	}
	if c.Clip == nil {
		return fmt.Errorf("daw: AddClip: nil clip")
	}
	for _, ex := range t.Clips {
		if ex != nil && ex.ID == c.Clip.ID {
			return fmt.Errorf("daw: AddClip: clip %q already exists on track %q", c.Clip.ID, c.TrackID)
		}
	}
	t.Clips = append(t.Clips, c.Clip)
	return nil
}

func (c *AddClipCmd) Revert(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: AddClip revert: track %q not found", c.TrackID)
	}
	for i, ex := range t.Clips {
		if ex == c.Clip {
			t.Clips = append(t.Clips[:i], t.Clips[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("daw: AddClip revert: clip %q missing", c.Clip.ID)
}

func (c *AddClipCmd) String() string { return "AddClip(" + c.Clip.ID + "->" + c.TrackID + ")" }

// AddSendCmd inserts a send on a track. It refuses to create a routing
// cycle so the session always stays renderable.
type AddSendCmd struct {
	TrackID string
	Send    *Send
}

func (c *AddSendCmd) Apply(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: AddSend: track %q not found", c.TrackID)
	}
	if c.Send == nil {
		return fmt.Errorf("daw: AddSend: nil send")
	}
	t.Sends = append(t.Sends, c.Send)
	if err := ValidateGraph(s); err != nil {
		t.Sends = t.Sends[:len(t.Sends)-1]
		return err
	}
	return nil
}

func (c *AddSendCmd) Revert(s *Session) error {
	t, _ := findTrack(s, c.TrackID)
	if t == nil {
		return fmt.Errorf("daw: AddSend revert: track %q not found", c.TrackID)
	}
	for i, ex := range t.Sends {
		if ex == c.Send {
			t.Sends = append(t.Sends[:i], t.Sends[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("daw: AddSend revert: send to %q missing", c.Send.TargetTrackID)
}

func (c *AddSendCmd) String() string {
	return "AddSend(" + c.TrackID + "->" + c.Send.TargetTrackID + ")"
}
