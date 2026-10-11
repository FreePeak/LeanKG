package astgrep

import (
	"os"
	"testing"
	"time"
)

// TestEnvDuration covers the override mechanism. The env key is saved,
// cleared and restored, because CI sets it job-wide (120s) and a test that
// assumed the shipped default would read "unset" as "the default changed".
func TestEnvDuration(t *testing.T) {
	key := "LEANKG_TEST_ASTGREP_TIMEOUT"
	restore := captureEnv(t, key)

	if got := envDuration(key, 5*time.Second); got != 5*time.Second {
		t.Fatalf("unset env changed the caller default: got %v", got)
	}
	for _, tc := range []struct {
		set  string
		want time.Duration
	}{
		{"45s", 45 * time.Second},
		{"not-a-duration", 10 * time.Second}, // malformed keeps the caller default
		{"-5s", 10 * time.Second},            // non-positive is never a bound
		{"", 10 * time.Second},               // empty keeps the caller default
	} {
		if err := os.Setenv(key, tc.set); err != nil {
			t.Fatal(err)
		}
		if got := envDuration(key, 10*time.Second); got != tc.want {
			t.Errorf("env %q: got %v want %v", tc.set, got, tc.want)
		}
	}
	restore()
}

// captureEnv clears key and returns a func restoring its original state.
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
