package embed

import (
	"strings"
	"testing"
)

// TestResolveWriteCap pins every branch of the write-side text cap, in rule
// order: a recorded cap wins, a non-local label takes the general cap, a local
// sidecar budget narrows it, an operator pin is the last resort, and nothing
// else is an error (never a silent 1000).
func TestResolveWriteCap(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		budget  int
		pin     int
		stored  int
		want    int
		wantErr bool
	}{
		{"stored cap wins over a zero budget", "local", 0, 0, 4000, 4000, false},
		{"stored cap wins over a live budget", "local", 3000, 0, 4000, 4000, false},
		{"stored cap wins over a pin", "local", 0, 2048, 4000, 4000, false},
		{"stored cap wins for a non-local label", "openai", 0, 0, 1234, 1234, false},
		{"non-local label takes the general cap", "openai", 0, 0, 0, 8000, false},
		{"non-local label ignores the budget", "openai", 500, 0, 0, 8000, false},
		{"non-local label ignores the pin", "openai", 0, 2048, 0, 8000, false},
		{"local budget below the general cap", "local", 3000, 0, 0, 3000, false},
		{"local budget above the general cap is clamped", "local", 12000, 0, 0, 8000, false},
		{"local budget wins over the pin", "local", 3000, 2048, 0, 3000, false},
		{"local pin used when no budget", "local", 0, 2048, 0, 2048, false},
		{"local with no budget and no pin is an error", "local", 0, 0, 0, 0, true},
		{"negative budget counts as no budget", "local", -1, 0, 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveWriteCap(tc.label, tc.budget, tc.pin, tc.stored)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("cap = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestResolveWriteCapErrorNamesBothFixes pins the operator guidance: the error
// must tell the operator to make /props reachable or to set the pin.
func TestResolveWriteCapErrorNamesBothFixes(t *testing.T) {
	_, err := resolveWriteCap("local", 0, 0, 0)
	if err == nil {
		t.Fatal("want an error when nothing determines the cap")
	}
	for _, want := range []string{"/props", "LEANKG_EMBED_TEXT_CAP"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q must name %q", err, want)
		}
	}
}
