package daw

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// CurrentVersion is the schema version written by Save.
const CurrentVersion = 2

// Migration describes one applied schema upgrade.
type Migration struct {
	From    int    `json:"from"`
	To      int    `json:"to"`
	Summary string `json:"summary"`
}

// LoadResult carries the loaded session plus the migrations applied.
type LoadResult struct {
	Session    *Session
	Migrations []Migration
}

// Save serializes the session to JSON bytes. The output is deterministic:
// map keys are sorted by encoding/json and indentation is fixed.
func Save(s *Session) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("daw: cannot save nil session")
	}
	cp := *s
	cp.Version = CurrentVersion
	out, err := json.MarshalIndent(&cp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("daw: save: %w", err)
	}
	return out, nil
}

// Load deserializes a session, migrating older schema versions. Unknown
// fields are rejected so nothing is silently dropped; migrations are
// reported in the result so upgrades are explainable.
func Load(data []byte) (*LoadResult, error) {
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("daw: load: invalid JSON: %w", err)
	}
	if head.Version <= 0 {
		return nil, fmt.Errorf("daw: load: missing or invalid version %d", head.Version)
	}
	if head.Version > CurrentVersion {
		return nil, fmt.Errorf("daw: load: version %d is newer than supported %d", head.Version, CurrentVersion)
	}

	// Work on the raw JSON tree so migrations see exactly what was saved.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("daw: load: %w", err)
	}

	res := &LoadResult{}
	v := head.Version
	for v < CurrentVersion {
		mig, err := migrate(raw, v)
		if err != nil {
			return nil, err
		}
		res.Migrations = append(res.Migrations, mig)
		v = mig.To
	}

	upgraded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("daw: load: re-encode after migration: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(upgraded))
	dec.DisallowUnknownFields()
	var s Session
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("daw: load: schema mismatch (unknown or bad field): %w", err)
	}
	if s.Master == nil {
		s.Master = &EffectChain{}
	}
	res.Session = &s
	return res, nil
}

// migrate upgrades the raw document one version step.
func migrate(raw map[string]interface{}, from int) (Migration, error) {
	switch from {
	case 1:
		return migrateV1toV2(raw), nil
	default:
		return Migration{}, fmt.Errorf("daw: load: no migration path from version %d", from)
	}
}

// migrateV1toV2 documents the v1 -> v2 upgrade:
//   - sessions gained a master effect chain (default: empty);
//   - clips gained a per-clip gain (default: 1.0, i.e. unity);
//   - tracks gained a sends list (default: empty).
func migrateV1toV2(raw map[string]interface{}) Migration {
	raw["version"] = 2
	if _, ok := raw["master"]; !ok {
		raw["master"] = map[string]interface{}{"slots": []interface{}{}}
	}
	if tracks, ok := raw["tracks"].([]interface{}); ok {
		for _, t := range tracks {
			tm, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			if _, ok := tm["sends"]; !ok {
				tm["sends"] = []interface{}{}
			}
			if clips, ok := tm["clips"].([]interface{}); ok {
				for _, c := range clips {
					if cm, ok := c.(map[string]interface{}); ok {
						if _, ok := cm["gain"]; !ok {
							cm["gain"] = 1.0
						}
					}
				}
			}
		}
	}
	return Migration{
		From:    1,
		To:      2,
		Summary: "add master effect chain, per-clip gain (default 1.0) and track sends list",
	}
}
