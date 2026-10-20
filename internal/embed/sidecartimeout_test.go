package embed

import (
	"os"
	"testing"
	"time"
)

// TestSidecarReadyTimeout pins the override that keeps this suite honest on a
// contended runner: the shipped 15s is unchanged when the env is unset or
// malformed, and a valid value is honoured.
func TestSidecarReadyTimeout(t *testing.T) {
	key := "LEANKG_TEST_SIDECAR_TIMEOUT"
	orig, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, orig)
			return
		}
		_ = os.Unsetenv(key)
	})

	if got := sidecarReadyTimeout(); got != 15*time.Second {
		t.Fatalf("unset env changed the shipped bound: %v", got)
	}
	if err := os.Setenv(key, "90s"); err != nil {
		t.Fatal(err)
	}
	if got := sidecarReadyTimeout(); got != 90*time.Second {
		t.Fatalf("env ignored: %v", got)
	}
	for _, bad := range []string{"not-a-duration", "-5s", ""} {
		if err := os.Setenv(key, bad); err != nil {
			t.Fatal(err)
		}
		if got := sidecarReadyTimeout(); got != 15*time.Second {
			t.Errorf("env %q changed the bound: %v", bad, got)
		}
	}
}
