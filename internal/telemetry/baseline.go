package telemetry

import (
	"os"

	"github.com/FreePeak/LeanKG/internal/pathguard"
)

// Token estimator: bytes / 4, the same estimate the budget package uses
// (budget.TokenCharsPerToken). Labelled as an estimate everywhere.
const baselineCharsPerToken = 4

// Baseline estimates what the agent would have spent without LeanKG for a
// call that returned hitFiles: whole-file tokens of the distinct files
// (file_read), or none for status, import, memory, zero-hit and error
// outcomes. Files are resolved inside projectDir only; anything outside, or
// not on disk, is not counted.
//
// The sloc method (plan DS-07) is not produced: the signature carries file
// names only, and an SLOC count needs the element line ranges or the file
// contents, so a missing file falls back to none, not to a guess.
func Baseline(projectDir, tool, outcome string, hitFiles []string) (tokens int64, method string) {
	if len(hitFiles) == 0 || tool == "status" || tool == "import" || !savingOutcome(outcome) {
		return 0, BaselineNone
	}
	seen := make(map[string]bool, len(hitFiles))
	for _, f := range hitFiles {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		abs, _, err := pathguard.Resolve(projectDir, f)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		tokens += info.Size() / baselineCharsPerToken
		method = BaselineFileRead
	}
	if method == "" {
		return 0, BaselineNone
	}
	return tokens, method
}

// savingOutcome reports whether a call delivered content the agent could have
// used instead of reading files. Cold, zero-hit, refused, timeout and error
// calls are cost, not savings (DS-07).
func savingOutcome(outcome string) bool {
	switch outcome {
	case OutcomeOK, OutcomeLowConf, OutcomeDegraded, OutcomeStale:
		return true
	}
	return false
}

// Saved is max(0, baseline - delivered).
func Saved(baseline, delivered int64) int64 {
	if baseline > delivered {
		return baseline - delivered
	}
	return 0
}
