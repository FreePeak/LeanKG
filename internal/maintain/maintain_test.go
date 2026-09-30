package maintain

import (
	"testing"
	"time"
)

func TestIntervalFromEnv(t *testing.T) {
	env := func(v string) func(string) string {
		return func(string) string { return v }
	}
	for _, tc := range []struct {
		name string
		raw  string
		want time.Duration
	}{
		{"unset defaults to an hour", "", DefaultInterval},
		{"explicit hours", "2", 2 * time.Hour},
		{"fractional hours", "0.5", 30 * time.Minute},
		{"zero disables", "0", 0},
		{"off disables", "off", 0},
		{"false disables", "false", 0},
		{"a typo must not silently disable reclamation", "every-hour",
			DefaultInterval},
		{"a negative value falls back to the default", "-3", DefaultInterval},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IntervalFromEnv(env(tc.raw)); got != tc.want {
				t.Fatalf("IntervalFromEnv(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
