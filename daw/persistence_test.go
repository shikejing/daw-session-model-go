package daw

import (
	"reflect"
	"strings"
	"testing"
)

func sampleSession() *Session {
	s := NewSession(48000)
	s.Tracks = []*Track{
		{
			ID: "drums", Name: "Drums", GroupID: "band",
			Gain: 0.8, Pan: -0.25,
			Clips: []*Clip{
				{ID: "c1", Start: 0, Duration: 1.5, Offset: 0.25, Gain: 0.9, SourceID: "kick.wav"},
				{ID: "c2", Start: 2, Duration: 1, Gain: 1, SourceID: "snare.wav"},
			},
			Sends: []*Send{{TargetTrackID: "verb", Gain: 0.3, PreFader: true}},
			Chain: &EffectChain{Slots: []*PluginSlot{
				{ID: "s1", PluginID: "builtin.gain", Enabled: true, Params: map[string]float64{"gain": 0.5}},
				{ID: "s2", PluginID: "builtin.noise", Enabled: true, Bypassed: true, Params: map[string]float64{"amount": 0.1}},
			}},
		},
		{
			ID: "verb", Name: "Reverb Bus", Gain: 1, Pan: 0.5, Mute: false, Solo: true,
			Chain: &EffectChain{Slots: []*PluginSlot{}},
		},
	}
	s.Master = &EffectChain{Slots: []*PluginSlot{
		{ID: "m1", PluginID: "builtin.gain", Enabled: true, Params: map[string]float64{"gain": 0.9}},
	}}
	return s
}

func TestSaveLoadRoundTrip(t *testing.T) {
	s := sampleSession()
	data, err := Save(s)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Migrations) != 0 {
		t.Fatalf("unexpected migrations: %+v", res.Migrations)
	}
	if !reflect.DeepEqual(s, res.Session) {
		t.Fatalf("round trip mismatch:\n got: %#v\nwant: %#v", res.Session, s)
	}
}

func TestSaveIsDeterministic(t *testing.T) {
	s := sampleSession()
	a, _ := Save(s)
	b, _ := Save(s)
	if string(a) != string(b) {
		t.Fatal("Save output not deterministic")
	}
}

func TestLoadV1Migrates(t *testing.T) {
	v1 := `{
	  "version": 1,
	  "sample_rate": 44100,
	  "tracks": [
	    {"id": "t1", "name": "T1", "gain": 1, "pan": 0, "mute": false, "solo": false,
	     "clips": [{"id": "c", "start": 0, "duration": 2, "offset": 0, "source_id": "a.wav"}],
	     "chain": {"slots": []}}
	  ]
	}`
	res, err := Load([]byte(v1))
	if err != nil {
		t.Fatalf("Load v1: %v", err)
	}
	if len(res.Migrations) != 1 || res.Migrations[0].From != 1 || res.Migrations[0].To != 2 {
		t.Fatalf("expected one 1->2 migration, got %+v", res.Migrations)
	}
	if res.Migrations[0].Summary == "" {
		t.Fatal("migration must carry an explainable summary")
	}
	s := res.Session
	if s.Version != CurrentVersion {
		t.Fatalf("version = %d, want %d", s.Version, CurrentVersion)
	}
	if s.Master == nil {
		t.Fatal("migration must add master chain")
	}
	got := s.Tracks[0].Clips[0].Gain
	if got != 1.0 {
		t.Fatalf("migrated clip gain = %v, want unity 1.0", got)
	}
	// Migrated session must round-trip cleanly.
	data, err := Save(s)
	if err != nil {
		t.Fatalf("Save migrated: %v", err)
	}
	res2, err := Load(data)
	if err != nil {
		t.Fatalf("re-Load migrated: %v", err)
	}
	if !reflect.DeepEqual(s, res2.Session) {
		t.Fatal("migrated session does not round-trip")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	s := sampleSession()
	data, _ := Save(s)
	bad := strings.Replace(string(data), `"gain"`, `"gain_typo"`, 1)
	if bad == string(data) {
		t.Fatal("test setup: replacement did not apply")
	}
	if _, err := Load([]byte(bad)); err == nil {
		t.Fatal("Load must reject unknown fields instead of silently dropping them")
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	data := `{"version": 99, "sample_rate": 48000, "tracks": [], "master": {"slots": []}}`
	if _, err := Load([]byte(data)); err == nil {
		t.Fatal("Load must reject versions newer than supported")
	}
}
