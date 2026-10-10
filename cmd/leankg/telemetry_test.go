package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// telemetryHome points HOME and LEANKG_HOME at scratch dirs so no test can
// read or write the real ~/.leankg.
func telemetryHome(t *testing.T) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	home := filepath.Join(t.TempDir(), "lk")
	t.Setenv("LEANKG_HOME", home)
	t.Setenv("LEANKG_TELEMETRY", "")
	return home
}

func TestTelemetryEnableNonTTYRefusesWithoutYes(t *testing.T) {
	home := telemetryHome(t)
	var out bytes.Buffer
	err := telemetryEnable(home, telemetryEnableOpts{}, strings.NewReader(""), false, &out)
	if err == nil {
		t.Fatal("enable without a TTY and without --yes must refuse")
	}
	if _, statErr := os.Stat(telemetry.ConfigPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("refused enable still wrote the config")
	}
}

func TestTelemetryEnableYesWritesConsentAndPrintsPaths(t *testing.T) {
	home := telemetryHome(t)
	var out bytes.Buffer
	if err := telemetryEnable(home, telemetryEnableOpts{yes: true}, strings.NewReader(""), false, &out); err != nil {
		t.Fatalf("enable --yes: %v", err)
	}
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capture != telemetry.Metadata || cfg.Consent.Version != telemetry.ConsentVersion ||
		cfg.Consent.GrantedBy != "cli" || cfg.Consent.GrantedAt.IsZero() {
		t.Fatalf("consent = %+v capture=%q", cfg.Consent, cfg.Capture)
	}
	if got := telemetry.EffectiveLevel(cfg, ""); got != telemetry.Metadata {
		t.Fatalf("effective level = %q", got)
	}
	for _, want := range []string{telemetry.ConfigPath(home), telemetry.DBPath(home)} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("enable output does not name %s:\n%s", want, out.String())
		}
	}
}

func TestTelemetryEnableBodiesAndSessionGrant(t *testing.T) {
	home := telemetryHome(t)
	var out bytes.Buffer
	opts := telemetryEnableOpts{bodies: true, sessions: "claude-code,pi", yes: true}
	if err := telemetryEnable(home, opts, strings.NewReader(""), false, &out); err != nil {
		t.Fatal(err)
	}
	cfg, _ := telemetry.LoadConfig(home)
	if cfg.Capture != telemetry.Bodies {
		t.Fatalf("capture = %q, want bodies", cfg.Capture)
	}
	if !cfg.Sessions.Enabled || strings.Join(cfg.Sessions.Clients, ",") != "claude-code,pi" {
		t.Fatalf("sessions = %+v", cfg.Sessions)
	}
	if !strings.Contains(out.String(), ".claude/projects") {
		t.Errorf("enable output lacks the claude-code transcript root:\n%s", out.String())
	}
}

func TestTelemetryEnableRejectsUnknownClient(t *testing.T) {
	home := telemetryHome(t)
	err := telemetryEnable(home, telemetryEnableOpts{sessions: "claude-code,bogus", yes: true}, strings.NewReader(""), false, &bytes.Buffer{})
	if err == nil {
		t.Fatal("unknown client accepted")
	}
	if _, statErr := os.Stat(telemetry.ConfigPath(home)); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("invalid enable wrote the config")
	}
}

func TestTelemetryEnableTTYPrompt(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"declined", "n\n", false},
		{"empty answer is no", "\n", false},
		{"accepted", "y\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := telemetryHome(t)
			var out bytes.Buffer
			_ = telemetryEnable(home, telemetryEnableOpts{}, strings.NewReader(c.input), true, &out)
			_, err := os.Stat(telemetry.ConfigPath(home))
			written := err == nil
			if written != c.want {
				t.Fatalf("config written = %v, want %v\n%s", written, c.want, out.String())
			}
			if !strings.Contains(out.String(), "[y/N]") {
				t.Fatalf("no y/N prompt shown:\n%s", out.String())
			}
		})
	}
}

func TestTelemetryDisableTurnsCaptureAndSessionsOff(t *testing.T) {
	home := telemetryHome(t)
	if err := telemetryEnable(home, telemetryEnableOpts{sessions: "pi", yes: true}, strings.NewReader(""), false, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if err := telemetryDisable(home, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := telemetry.LoadConfig(home)
	if cfg.Capture != telemetry.Off || cfg.Sessions.Enabled {
		t.Fatalf("after disable: capture=%q sessions=%+v", cfg.Capture, cfg.Sessions)
	}
}

func TestTelemetryStatusNeverCreatesLedger(t *testing.T) {
	home := telemetryHome(t)
	var out bytes.Buffer
	if err := telemetryStatus(home, false, &out); err != nil {
		t.Fatalf("status: %v", err)
	}
	if _, err := os.Stat(telemetry.DBPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created the ledger")
	}
	if !strings.Contains(out.String(), "off") {
		t.Fatalf("status does not show capture off:\n%s", out.String())
	}
}

func TestTelemetryPurgeWithoutLedgerCreatesNothing(t *testing.T) {
	home := telemetryHome(t)
	var out bytes.Buffer
	if err := telemetryPurge(home, time.Now().Add(time.Hour), &out); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := os.Stat(telemetry.DBPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("purge created the ledger")
	}
}

func TestParseBefore(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	got, err := parseBefore("30d", now)
	if err != nil || !got.Equal(now.AddDate(0, 0, -30)) {
		t.Fatalf("30d = %v err=%v", got, err)
	}
	got, err = parseBefore("2026-09-01", now)
	if err != nil || got.Year() != 2026 || got.Month() != 9 || got.Day() != 1 {
		t.Fatalf("date = %v err=%v", got, err)
	}
	if _, err := parseBefore("soon", now); err == nil {
		t.Fatal("invalid --before accepted")
	}
}

func TestParseSessionClients(t *testing.T) {
	got, err := parseSessionClients("all")
	if err != nil || len(got) != 1 || got[0] != "all" {
		t.Fatalf("all = %v err=%v", got, err)
	}
	if _, err := parseSessionClients(" , "); err == nil {
		t.Fatal("empty list accepted")
	}
}
