package embed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAttachHookProviderPolicy pins the SessionStart hook's provider policy:
// the in-process deterministic provider, or a local provider attached to a
// loopback endpoint. Every other combination is refused, and no row may spawn
// a sidecar: LEANKG_EMBED_SIDECAR_CMD points at a script that touches a marker,
// and the marker must stay absent.
func TestAttachHookProviderPolicy(t *testing.T) {
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
			t.Setenv("LEANKG_EMBED_PROVIDER", tc.provider)
			t.Setenv("LEANKG_EMBED_BASE_URL", tc.baseURL)
			t.Setenv("LEANKG_EMBED_MODEL", tc.model)
			marker := filepath.Join(t.TempDir(), "spawned")
			script := filepath.Join(t.TempDir(), "sidecar.sh")
			if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LEANKG_EMBED_SIDECAR_CMD", script)

			p, release, err := AttachHookProvider(context.Background())
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
