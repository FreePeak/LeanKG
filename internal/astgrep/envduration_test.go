package astgrep

import (
	"os"
	"testing"
	"time"
)

func TestEnvDuration(t *testing.T) {
	key := "LEANKG_TEST_ASTGREP_TIMEOUT"
	defer func(k string) { _ = os.Unsetenv(k) }(key)
	if got := envDuration(key, 5*time.Second); got != 5*time.Second {
		t.Fatalf("unset env changed the caller default: got %v", got)
	}
	if err := os.Setenv(key, "45s"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 45*time.Second {
		t.Fatalf("env ignored: got %v want 45s", got)
	}
	if err := os.Setenv(key, "not-a-duration"); err != nil {
		t.Fatal(err)
	}
	if got := envDuration(key, 10*time.Second); got != 10*time.Second {
		t.Fatalf("malformed env changed the default: got %v", got)
	}
}
