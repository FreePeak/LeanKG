package embed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachHookPolicy pins the SessionStart hook's provider policy: the
// in-process deterministic provider, or a local provider attached to a loopback
// endpoint. Every other combination is refused, and no row may spawn a sidecar:
// the sidecar command is a script that touches a marker, and the marker must
// stay absent.
func TestAttachHookPolicy(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		baseURL  string
		model    string
		refusal  string // reason fragment the refusal must name; empty when allowed
	}{
		{"deterministic", "deterministic", "", "", ""},
		{"unset provider, loopback 127.0.0.1", "", "http://127.0.0.1:9101", "", ""},
		{"local, loopback localhost", "local", "http://localhost:9101", "", ""},
		{"local, loopback ipv6", "local", "http://[::1]:9101", "", ""},
		{"local, remote host", "local", "https://embed.example.com", "", "is not a loopback address"},
		{"local, no base url", "local", "", "", "is unset, so a sidecar would have to be spawned"},
		{"openai, loopback base url", "openai", "http://127.0.0.1:9101", "text-embedding-3-small", "is not local or deterministic"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "spawned")
			script := filepath.Join(t.TempDir(), "sidecar.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := Config{Provider: tc.provider, BaseURL: tc.baseURL, Model: tc.model,
				Sidecar: SidecarConfig{Command: script}}

			p, release, err := AttachHook(cfg)
			release()
			if _, statErr := os.Stat(marker); statErr == nil {
				t.Fatal("hook spawned the sidecar")
			}
			if tc.refusal != "" {
				if err == nil || !strings.Contains(err.Error(), "hook mode only attaches to a running local provider") ||
					!strings.Contains(err.Error(), tc.refusal) {
					t.Fatalf("err = %v, want the hook refusal naming %q", err, tc.refusal)
				}
				return
			}
			if err != nil || p == nil {
				t.Fatalf("provider = %v, err = %v; want attached", p, err)
			}
		})
	}
}

// TestStartRejectsWidthBeforeSpawn: an unsupported width for a catalogued model
// is a configuration error, so it must fail before the sidecar process exists.
// The sidecar command touches a marker; the marker must stay absent.
func TestStartRejectsWidthBeforeSpawn(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	script := filepath.Join(t.TempDir(), "sidecar.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Provider: "local", Model: "embeddinggemma-2", Dims: 300, Sidecar: SidecarConfig{Command: script}}

	_, release, err := Start(context.Background(), cfg)
	release()
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("Start spawned the sidecar before rejecting the width")
	}
	if err == nil || !strings.Contains(err.Error(), "dims 300") {
		t.Fatalf("err = %v, want the width error naming dims 300", err)
	}
}
