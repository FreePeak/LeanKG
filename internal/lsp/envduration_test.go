package lsp

import (
	"os"
	"testing"
	"time"
)

// TestEnvDurationKeepsDefaults pins that the env override only takes effect
// at process start (it feeds a package-level var initialiser, deliberately:
// a request path must not read the environment), and that a malformed value
// keeps the shipped bound rather than disabling the timeout.
func TestEnvDurationKeepsDefaults(t *testing.T) {
	key := "LEANKG_TEST_LSP_TIMEOUT"
	defer func() { _ = os.Unsetenv(key) }()
	if got := envDuration(key, 7*time.Second); got != 7*time.Second {
		t.Fatalf("unset env changed the caller default: got %v", got)
	}
	if err := os.Setenv(key, "60s"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 60*time.Second {
		t.Fatalf("env ignored: got %v want 60s", got)
	}
	if err := os.Setenv(key, "absolutely-not"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 10*time.Second {
		t.Fatalf("malformed env dropped the bound: got %v", got)
	}
	if err := os.Setenv(key, "-5s"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 10*time.Second {
		t.Fatalf("non-positive env dropped the bound: got %v", got)
	}
}
