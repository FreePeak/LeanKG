package telemetry

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// classifyFixtures is one response per label (plan v4.15 DS-06). Each
// fixture is a shape the core engine really emits; the comments name the
// emitting line so a drift in core.go shows up here first.
func classifyFixtures() []struct {
	name string
	out  any
	err  error
	want Classification
} {
	return []struct {
		name string
		out  any
		err  error
		want Classification
	}{
		{
			name: "ok: L1 exact hit with file paths",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L1", "reason": "exact identifier match", "confidence": "normal"},
				"freshness": "fresh",
				"hits": []any{
					map[string]any{"file_path": "internal/a.go", "name": "A"},
					map[string]any{"file_path": "internal/a.go", "name": "B"},
					map[string]any{"file_path": "internal/b.go", "name": "C"},
				},
			},
			want: Classification{Outcome: OutcomeOK, Reason: "exact identifier match", Rung: "L1", Confidence: "normal", Freshness: "fresh",
				Hits: 3, HitFiles: []string{"internal/a.go", "internal/b.go"}},
		},
		{
			name: "ok_low_confidence: semantic answer below the noise floor (core.go:976-982)",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L3", "reason": "vector similarity (cosine)", "confidence": "low"},
				"freshness": "fresh",
				"hits":      []any{map[string]any{"file_path": "x.go"}},
			},
			want: Classification{Outcome: OutcomeLowConf, Reason: "vector similarity (cosine)", Rung: "L3", Confidence: "low", Freshness: "fresh",
				Hits: 1, HitFiles: []string{"x.go"}},
		},
		{
			name: "degraded: L3 provider missing falls back to L2 (core.go:895)",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L2", "reason": "no embedding provider wired; degraded from L3"},
				"freshness": "fresh",
				"hits":      []any{map[string]any{"file_path": "y.go"}},
			},
			want: Classification{Outcome: OutcomeDegraded, Rung: "L2", Reason: "no embedding provider wired; degraded from L3",
				Freshness: "fresh", Hits: 1, HitFiles: []string{"y.go"}},
		},
		{
			name: "degraded: ast-grep missing degrades to L2 (core.go:1450)",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L2", "reason": "ast-grep CLI not installed; degraded to keyword search"},
				"freshness": "fresh",
				"hits":      []any{},
				"guidance":  "No keyword match.",
			},
			want: Classification{Outcome: OutcomeDegraded, Rung: "L2", Reason: "ast-grep CLI not installed; degraded to keyword search",
				Freshness: "fresh", Hits: 0},
		},
		{
			name: "zero_hit: empty hits with guidance (core.go:784-813)",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L2", "reason": "no keyword match"},
				"freshness": "fresh",
				"hits":      []any{},
				"guidance":  "No hits. Try a shorter identifier.",
			},
			want: Classification{Outcome: OutcomeZeroHit, Rung: "L2", Reason: "No hits. Try a shorter identifier.",
				Freshness: "fresh", Hits: 0},
		},
		{
			name: "cold: L0 with no elements indexed (core.go:740-742)",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L0", "reason": "no elements indexed"},
				"freshness": "cold",
				"hits":      []any{},
				"guidance":  "Index a repository first",
			},
			want: Classification{Outcome: OutcomeCold, Rung: "L0", Reason: "no elements indexed", Freshness: "cold"},
		},
		{
			name: "stale: freshness possibly_stale on an otherwise good answer",
			out: map[string]any{
				"retrieval": map[string]any{"rung": "L1", "reason": "exact identifier match"},
				"freshness": "possibly_stale",
				"hits":      []any{map[string]any{"file_path": "s.go"}},
			},
			want: Classification{Outcome: OutcomeStale, Reason: "exact identifier match", Rung: "L1", Freshness: "possibly_stale", Hits: 1, HitFiles: []string{"s.go"}},
		},
		{
			name: "status: no hits key and fresh is ok, not zero_hit",
			out:  map[string]any{"freshness": "fresh", "backend": "sqlite"},
			want: Classification{Outcome: OutcomeOK, Freshness: "fresh"},
		},
		{
			name: "error:LEANKG_ERROR_UNKNOWN_ACTION from an engine error",
			err:  fmt.Errorf("LEANKG_ERROR_UNKNOWN_ACTION: action %q is not supported. Fix: pick one", "bogus"),
			want: Classification{Outcome: "error:LEANKG_ERROR_UNKNOWN_ACTION", ErrorCode: "LEANKG_ERROR_UNKNOWN_ACTION",
				Reason: `LEANKG_ERROR_UNKNOWN_ACTION: action "bogus" is not supported. Fix: pick one`},
		},
		{
			name: "error:UNCLASSIFIED for an error without a catalog code (bad args)",
			err:  errors.New("mcp: decode args: json: cannot unmarshal string into Go value"),
			want: Classification{Outcome: OutcomeErrorPrefix + UnclassifiedCode,
				Reason: "mcp: decode args: json: cannot unmarshal string into Go value"},
		},
		{
			name: "refused: RBAC denial",
			err:  fmt.Errorf("LEANKG_ERROR_PERMISSION_DENIED: forbidden: tool \"import\" requires contributor"),
			want: Classification{Outcome: OutcomeRefused, ErrorCode: "LEANKG_ERROR_PERMISSION_DENIED",
				Reason: `LEANKG_ERROR_PERMISSION_DENIED: forbidden: tool "import" requires contributor`},
		},
		{
			name: "refused: path confinement",
			err:  fmt.Errorf("LEANKG_ERROR_PATH_OUTSIDE_PROJECT: path escapes the project"),
			want: Classification{Outcome: OutcomeRefused, ErrorCode: "LEANKG_ERROR_PATH_OUTSIDE_PROJECT",
				Reason: "LEANKG_ERROR_PATH_OUTSIDE_PROJECT: path escapes the project"},
		},
		{
			name: "timeout: context deadline",
			err:  fmt.Errorf("query: %w", context.DeadlineExceeded),
			want: Classification{Outcome: OutcomeTimeout, Reason: "query: context deadline exceeded"},
		},
	}
}

func TestClassifyFixtures(t *testing.T) {
	for _, tc := range classifyFixtures() {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.out, tc.err)
			if got.Outcome != tc.want.Outcome {
				t.Fatalf("Outcome = %q, want %q", got.Outcome, tc.want.Outcome)
			}
			if got.Reason != tc.want.Reason {
				t.Errorf("Reason = %q, want %q", got.Reason, tc.want.Reason)
			}
			if got.ErrorCode != tc.want.ErrorCode {
				t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, tc.want.ErrorCode)
			}
			if got.Rung != tc.want.Rung || got.Confidence != tc.want.Confidence || got.Freshness != tc.want.Freshness {
				t.Errorf("signals rung/confidence/freshness = %q/%q/%q, want %q/%q/%q",
					got.Rung, got.Confidence, got.Freshness, tc.want.Rung, tc.want.Confidence, tc.want.Freshness)
			}
			if got.Hits != tc.want.Hits {
				t.Errorf("Hits = %d, want %d", got.Hits, tc.want.Hits)
			}
			if !reflect.DeepEqual(got.HitFiles, tc.want.HitFiles) && !(len(got.HitFiles) == 0 && len(tc.want.HitFiles) == 0) {
				t.Errorf("HitFiles = %v, want %v", got.HitFiles, tc.want.HitFiles)
			}
		})
	}
}

// Every fixture must land on a label from the plan list, never on unknown.
func TestClassifyNeverUnknown(t *testing.T) {
	known := map[string]bool{
		OutcomeOK: true, OutcomeLowConf: true, OutcomeDegraded: true, OutcomeZeroHit: true,
		OutcomeCold: true, OutcomeStale: true, OutcomeRefused: true, OutcomeTimeout: true,
	}
	for _, tc := range classifyFixtures() {
		got := Classify(tc.out, tc.err).Outcome
		if !known[got] && len(got) < len(OutcomeErrorPrefix) {
			t.Errorf("%s: outcome %q is not a label", tc.name, got)
		}
		if got == "unknown" {
			t.Errorf("%s: outcome must not be unknown", tc.name)
		}
	}
}
