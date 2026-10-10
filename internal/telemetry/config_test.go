package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var grantTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func TestLoadConfigMissingFileYieldsDefaultsAndCreatesNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "lk")
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Capture != Off || cfg.RetentionDays != DefaultRetentionDays || cfg.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Sessions.Enabled || len(cfg.Sessions.Clients) != 0 {
		t.Fatalf("sessions must default to disabled, got %+v", cfg.Sessions)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("LoadConfig created %s (err=%v)", home, err)
	}
}

func TestSaveConfigRoundTripAndModes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "lk")
	in := Config{
		Capture:       Metadata,
		Sessions:      SessionsConfig{Enabled: true, Clients: []string{"claude-code", "pi"}},
		RetentionDays: 7,
		MaxBodyBytes:  100,
		Consent:       Consent{GrantedAt: grantTime, GrantedBy: "cli", Version: ConsentVersion},
	}
	if err := SaveConfig(home, in); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	fi, err := os.Stat(ConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("config mode = %o, want 600", got)
	}
	hi, err := os.Stat(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := hi.Mode().Perm(); got != 0o700 {
		t.Errorf("home mode = %o, want 700", got)
	}
	out, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if out.Capture != Metadata || out.RetentionDays != 7 || out.MaxBodyBytes != 100 {
		t.Errorf("round trip = %+v", out)
	}
	if !out.Sessions.Enabled || len(out.Sessions.Clients) != 2 || out.Sessions.Clients[1] != "pi" {
		t.Errorf("sessions round trip = %+v", out.Sessions)
	}
	if !out.Consent.GrantedAt.Equal(grantTime) || out.Consent.GrantedBy != "cli" || out.Consent.Version != ConsentVersion {
		t.Errorf("consent round trip = %+v", out.Consent)
	}
}

func TestEffectiveLevel(t *testing.T) {
	granted := Consent{GrantedAt: grantTime, GrantedBy: "cli", Version: ConsentVersion}
	cases := []struct {
		name string
		cfg  Config
		env  string
		want Level
	}{
		{"no consent is off", Config{Capture: Bodies}, "", Off},
		{"old consent version is off", Config{Capture: Bodies, Consent: Consent{GrantedAt: grantTime, Version: ConsentVersion - 1}}, "", Off},
		{"consent without timestamp is off", Config{Capture: Metadata, Consent: Consent{Version: ConsentVersion}}, "", Off},
		{"metadata granted", Config{Capture: Metadata, Consent: granted}, "", Metadata},
		{"bodies granted", Config{Capture: Bodies, Consent: granted}, "", Bodies},
		{"env lowers bodies to metadata", Config{Capture: Bodies, Consent: granted}, "metadata", Metadata},
		{"env off lowers metadata", Config{Capture: Metadata, Consent: granted}, "off", Off},
		{"env cannot raise metadata to bodies", Config{Capture: Metadata, Consent: granted}, "bodies", Metadata},
		{"env unknown value is ignored", Config{Capture: Bodies, Consent: granted}, "banana", Bodies},
		{"env cannot grant consent", Config{Capture: Bodies}, "bodies", Off},
		{"unknown configured level is off", Config{Capture: Level("loud"), Consent: granted}, "", Off},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EffectiveLevel(c.cfg, c.env); got != c.want {
				t.Fatalf("EffectiveLevel = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSessionsGranted(t *testing.T) {
	cases := []struct {
		name    string
		cfg     SessionsConfig
		client  string
		granted bool
	}{
		{"disabled even when listed", SessionsConfig{Enabled: false, Clients: []string{"pi"}}, "pi", false},
		{"enabled and listed", SessionsConfig{Enabled: true, Clients: []string{"pi"}}, "pi", true},
		{"enabled but other client", SessionsConfig{Enabled: true, Clients: []string{"pi"}}, "omp", false},
		{"enabled with all", SessionsConfig{Enabled: true, Clients: []string{"all"}}, "codex", true},
		{"enabled with no clients", SessionsConfig{Enabled: true}, "pi", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SessionsGranted(Config{Sessions: c.cfg}, c.client); got != c.granted {
				t.Fatalf("SessionsGranted = %v, want %v", got, c.granted)
			}
		})
	}
}
