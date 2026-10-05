package daw

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func sampleSession() *Session {
	return &Session{
		Name:       "demo",
		SampleRate: 48000,
		Tracks: []*Track{
			{
				ID: "t1", Name: "vocals", GroupID: "g1",
				Gain: 0.8, Pan: -0.25,
				Clips: []Clip{
					{ID: "c1", Source: "vox.wav", Start: 0, Duration: 48000, Offset: 100, Gain: 1},
					{ID: "c2", Source: "vox2.wav", Start: 48000, Duration: 24000, Gain: 0.5},
				},
				Automation: []AutomationCurve{
					{Param: "gain", Points: []AutomationPoint{{Time: 0, Value: 1}, {Time: 48000, Value: 0.5}}},
				},
				Sends: []Send{{TargetID: "bus1", Gain: 0.4}},
				Chain: []PluginSlot{
					{PluginID: "gain", Enabled: true, Params: map[string]float64{"gain": 1.2}},
					{PluginID: "delay", Enabled: false},
				},
			},
			{ID: "bus1", Name: "reverb bus", Gain: 1, Pan: 0, Clips: []Clip{}},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	orig := sampleSession()
	var buf bytes.Buffer
	if err := Save(&buf, orig); err != nil {
		t.Fatalf("Save: %v", err)
	}
	res, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(res.Migrations) != 0 {
		t.Fatalf("unexpected migrations: %+v", res.Migrations)
	}
	want := orig.Clone()
	want.Version = CurrentFormatVersion
	want.normalize()
	if !reflect.DeepEqual(want, res.Session) {
		t.Fatalf("round-trip mismatch:\nwant %+v\ngot  %+v", want, res.Session)
	}

	// Re-saving the loaded session must produce identical bytes.
	var buf2 bytes.Buffer
	if err := Save(&buf2, res.Session); err != nil {
		t.Fatalf("Save again: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), buf2.Bytes()) {
		t.Fatalf("re-save bytes differ:\n%s\n---\n%s", buf.Bytes(), buf2.Bytes())
	}
}

func TestLoadMigratesV1(t *testing.T) {
	v1 := `{
	  "version": 1,
	  "name": "old project",
	  "sampleRate": 44100,
	  "tracks": [
	    {"id": "t1", "name": "drums", "gain": 1, "pan": 0, "mute": false, "solo": false,
	     "clips": [{"id": "c1", "source": "kick.wav", "start": 0, "duration": 1000, "offset": 0, "gain": 1}]}
	  ]
	}`
	res, err := Load(strings.NewReader(v1))
	if err != nil {
		t.Fatalf("Load v1: %v", err)
	}
	if res.Session.Version != CurrentFormatVersion {
		t.Fatalf("version = %d, want %d", res.Session.Version, CurrentFormatVersion)
	}
	if len(res.Migrations) != 1 || res.Migrations[0].From != 1 || res.Migrations[0].To != 2 {
		t.Fatalf("unexpected migrations: %+v", res.Migrations)
	}
	if res.Migrations[0].Description == "" {
		t.Fatal("migration must carry an explanation")
	}
	tr := res.Session.Tracks[0]
	if tr.ID != "t1" || len(tr.Clips) != 1 || tr.Clips[0].Source != "kick.wav" {
		t.Fatalf("v1 data lost: %+v", tr)
	}

	// Migrated session must survive a save/load round-trip unchanged.
	var buf bytes.Buffer
	if err := Save(&buf, res.Session); err != nil {
		t.Fatalf("Save migrated: %v", err)
	}
	res2, err := Load(&buf)
	if err != nil {
		t.Fatalf("Reload migrated: %v", err)
	}
	if !reflect.DeepEqual(res.Session, res2.Session) {
		t.Fatal("migrated session not stable across round-trip")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	doc := `{"version": 2, "name": "x", "sampleRate": 48000, "tracks": [], "bogusField": 1}`
	if _, err := Load(strings.NewReader(doc)); err == nil {
		t.Fatal("expected unknown-field rejection, got nil error")
	}
}

func TestLoadRejectsNewerVersion(t *testing.T) {
	doc := `{"version": 99, "name": "x", "sampleRate": 48000, "tracks": []}`
	if _, err := Load(strings.NewReader(doc)); err == nil {
		t.Fatal("expected newer-version rejection, got nil error")
	}
}
