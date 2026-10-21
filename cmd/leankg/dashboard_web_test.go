package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// TestParseDashboardFlagsDecidesTheModeFromTheParsedValues: the mode is
// decided by what the ONE flag set parsed, not by a scan of argv.
func TestParseDashboardFlagsDecidesTheModeFromTheParsedValues(t *testing.T) {
	cases := map[string]bool{
		"":                                false,
		"--project /tmp/x":                false,
		"--addr 127.0.0.1:9701 --no-open": false,
		"--format json":                   true,
		"--format=text":                   true,
		"-format text":                    true,
		"--since 7d --format json":        true,
	}
	for in, want := range cases {
		var args []string
		if in != "" {
			args = strings.Fields(in)
		}
		if got := parseDashboardFlags(args).formatSet; got != want {
			t.Errorf("parseDashboardFlags(%q).formatSet = %v, want %v", in, got, want)
		}
	}
}

// TestParseDashboardFlagsReadsTheWebFlagsTheTablesModeAlsoDeclares: the
// documented `leankg dashboard --addr 127.0.0.1:9701 --no-open --format text`
// must parse in ONE pass — the web flags and the table flags are one set.
func TestParseDashboardFlagsReadsTheWebFlagsTheTablesModeAlsoDeclares(t *testing.T) {
	f := parseDashboardFlags([]string{"--addr", "127.0.0.1:9701", "--no-open", "--format", "text"})
	if !f.formatSet {
		t.Error("--format must select the tables mode")
	}
	if f.addr != "127.0.0.1:9701" {
		t.Errorf("addr = %q, want 127.0.0.1:9701", f.addr)
	}
	if !f.noOpen {
		t.Error("--no-open must be read from the same flag set")
	}
	if f.format != "text" {
		t.Errorf("format = %q, want text", f.format)
	}
}

func TestRunDashboardWebRefusesNonLoopbackWithoutAllowRemote(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	err := runDashboardWeb(context.Background(), parseDashboardFlags([]string{"--addr", "0.0.0.0:0", "--no-open"}), io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "allow-remote") {
		t.Fatalf("err = %v, want an allow-remote refusal", err)
	}
}

func TestRunDashboardWebRemoteNeedsToken(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	err := runDashboardWeb(context.Background(), parseDashboardFlags([]string{"--addr", "0.0.0.0:0", "--allow-remote", "--no-open"}), io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("err = %v, want a token refusal", err)
	}
}

func TestRunDashboardWebServesAndOpensStubbedBrowser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("LEANKG_HOME", home)
	t.Setenv("HOME", home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var opened string
	var status int
	opener := func(url string) error {
		opened = url
		resp, err := http.Get(url + "api/dashboard/v1/overview")
		if err != nil {
			t.Errorf("GET overview: %v", err)
		} else {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		cancel()
		return nil
	}
	var out strings.Builder
	err := runDashboardWeb(ctx, parseDashboardFlags([]string{"--addr", "127.0.0.1:0", "--since", "7d"}), &out, opener)
	if err != nil {
		t.Fatalf("runDashboardWeb: %v", err)
	}
	if !strings.HasPrefix(opened, "http://127.0.0.1:") {
		t.Errorf("opener got %q", opened)
	}
	if status != http.StatusOK {
		t.Errorf("overview status %d", status)
	}
	if _, err := os.Stat(telemetry.DBPath(home)); !os.IsNotExist(err) {
		t.Errorf("dashboard created the ledger (err=%v)", err)
	}
}

func TestRunDashboardWebNoOpenSkipsBrowser(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	called := false
	opener := func(string) error { called = true; return nil }
	if err := runDashboardWeb(ctx, parseDashboardFlags([]string{"--addr", "127.0.0.1:0", "--no-open"}), io.Discard, opener); err != nil {
		t.Fatalf("runDashboardWeb: %v", err)
	}
	if called {
		t.Errorf("--no-open still opened the browser")
	}
}
