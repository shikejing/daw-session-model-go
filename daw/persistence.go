package daw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Migration describes one applied format upgrade step.
type Migration struct {
	From        int    `json:"from"`
	To          int    `json:"to"`
	Description string `json:"description"`
}

// LoadResult wraps a loaded session with the migrations that were applied.
type LoadResult struct {
	Session    *Session
	Migrations []Migration
}

// migrations[i] upgrades format version i+1 to i+2.
var migrations = []struct {
	to    int
	desc  string
	apply func(s *Session)
}{
	{
		to:   2,
		desc: "v1->v2: add track group, sends, automation and effect-chain fields (default empty)",
		apply: func(s *Session) {
			// The new fields decode as zero values; nothing to rewrite,
			// they only need to exist with defined defaults.
		},
	},
}

// Save serializes the session at CurrentFormatVersion. The caller's session
// is not mutated; the written document is normalized so that a subsequent
// Load yields a deeply equal session.
func Save(w io.Writer, s *Session) error {
	c := s.Clone()
	c.Version = CurrentFormatVersion
	c.normalize()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

// Load deserializes a session, migrating older format versions. Decoding is
// strict: unknown fields are rejected rather than silently dropped, so no
// data is lost or rewritten without an explainable migration.
func Load(r io.Reader) (*LoadResult, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("daw: read session: %w", err)
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("daw: parse session header: %w", err)
	}
	if probe.Version < 1 {
		return nil, fmt.Errorf("daw: session has invalid version %d", probe.Version)
	}
	if probe.Version > CurrentFormatVersion {
		return nil, fmt.Errorf("daw: session version %d is newer than supported %d", probe.Version, CurrentFormatVersion)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Session
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("daw: decode session: %w", err)
	}

	res := &LoadResult{Session: &s}
	for s.Version < CurrentFormatVersion {
		m := migrations[s.Version-1]
		m.apply(&s)
		s.Version = m.to
		res.Migrations = append(res.Migrations, Migration{
			From:        m.to - 1,
			To:          m.to,
			Description: m.desc,
		})
	}
	s.normalize()
	return res, nil
}
