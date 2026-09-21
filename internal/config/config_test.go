package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportEnvAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	env := "TOMORROW_API_KEY=abc\nLOCATION=33.4,-86.8\nTIMEZONE=America/Chicago\nGOOGLE_CLIENT_ID=\"cid\"\nDROPBOX_REFRESH_TOKEN=rt\n# comment\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewStore(dir)
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Connectors.Weather.TomorrowAPIKey != "abc" || cfg.Connectors.Weather.Provider != "tomorrowio" {
		t.Fatalf("tomorrow key not imported: %+v", cfg.Connectors.Weather)
	}
	if cfg.General.Timezone != "America/Chicago" || cfg.Connectors.Google.ClientID != "cid" {
		t.Fatalf("env import wrong: %+v", cfg.General)
	}
	if cfg.Tasks.Source != "dropbox" {
		t.Fatalf("dropbox token should switch task source, got %s", cfg.Tasks.Source)
	}
	lat, lng, ok := cfg.LatLng()
	if !ok || lat != 33.4 || lng != -86.8 {
		t.Fatalf("latlng parse failed: %v %v %v", lat, lng, ok)
	}
	cfg.Sections = []Section{{ID: "header", Enabled: true}}
	if err := st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	st2 := NewStore(dir)
	cfg2, err := st2.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Connectors.Weather.TomorrowAPIKey != "abc" || len(cfg2.Sections) != 1 {
		t.Fatalf("round trip lost data: %+v", cfg2)
	}
}

func TestRedactAndMerge(t *testing.T) {
	prev := Default()
	prev.Connectors.HomeAssistant.Token = "secret"
	red := prev.Redacted()
	if red.Connectors.HomeAssistant.Token != Masked {
		t.Fatal("token not masked")
	}
	MergeSecrets(red, prev)
	if red.Connectors.HomeAssistant.Token != "secret" {
		t.Fatal("secret not restored")
	}
}

func TestValidate(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
	cfg.Schedules = append(cfg.Schedules, Schedule{Time: "25:00"})
	if err := cfg.Validate(); err == nil {
		t.Fatal("bad schedule time should fail")
	}
	cfg = Default()
	cfg.Sections = []Section{{ID: "a"}, {ID: "a"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("duplicate sections should fail")
	}
}
