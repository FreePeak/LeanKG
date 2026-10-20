package lsp

import (
	"os"
	"testing"
	"time"
)

// TestEnvDurationKeepsDefaults pin the same invariant as astgrep's: a
// malformed or non-positive value keeps the caller default (the failure that
// would silently turn every subprocess into an unbounded spawn). The env is
// saved and cleared first, because CI sets it job-wide.
func TestEnvDurationKeepsDefaults(t *testing.T) {
	key := "LEANKG_TEST_LSP_TIMEOUT"
	restore := captureEnv(t, key)

	if got := envDuration(key, 7*time.Second); got != 7*time.Second {
		t.Fatalf("unset env changed the caller default: got %v", got)
	}
	if err := os.Setenv(key, "60s"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 60*time.Second {
		t.Fatalf("env ignored: got %v want 60s", got)
	}
	for _, bad := range []string{"absolutely-not", "-5s", ""} {
		if err := os.Setenv(key, bad); err != nil {
			t.Fatal(err)
		}
		if got := envDuration(key, 10*time.Second); got != 10*time.Second {
			t.Errorf("env %q dropped the bound: got %v", bad, got)
		}
	}
	restore()
}

func captureEnv(t *testing.T, key string) func() {
	t.Helper()
	orig, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	return func() {
		if had {
			_ = os.Setenv(key, orig)
			return
		}
		_ = os.Unsetenv(key)
	}
}
