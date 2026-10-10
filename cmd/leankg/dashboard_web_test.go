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

func TestDashboardWantsTextOnlyWithFormatFlag(t *testing.T) {
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
		if got := dashboardWantsText(args); got != want {
			t.Errorf("dashboardWantsText(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestRunDashboardWebRefusesNonLoopbackWithoutAllowRemote(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	err := runDashboardWeb(context.Background(), []string{"--addr", "0.0.0.0:0", "--no-open"}, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "allow-remote") {
		t.Fatalf("err = %v, want an allow-remote refusal", err)
	}
}

func TestRunDashboardWebRemoteNeedsToken(t *testing.T) {
	t.Setenv("LEANKG_HOME", t.TempDir())
	err := runDashboardWeb(context.Background(), []string{"--addr", "0.0.0.0:0", "--allow-remote", "--no-open"}, io.Discard, nil)
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
	err := runDashboardWeb(ctx, []string{"--addr", "127.0.0.1:0", "--since", "7d"}, &out, opener)
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
	if err := runDashboardWeb(ctx, []string{"--addr", "127.0.0.1:0", "--no-open"}, io.Discard, opener); err != nil {
		t.Fatalf("runDashboardWeb: %v", err)
	}
	if called {
		t.Errorf("--no-open still opened the browser")
	}
}
