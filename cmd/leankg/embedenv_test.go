package main

import (
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed/embedtest"
)

// TestSuiteIsGreenUnderAnExportedEmbedEnv is the regression guard for the
// hermeticity defect: the suite's result must not depend on the
// LEANKG_EMBED_* environment the operator's shell happens to export.
//
// It re-runs the three env-sensitive tests in a CHILD process whose embed
// environment is a plausible operator configuration — a local provider, a
// catalogued model, a width — and fails when that child fails. Before the
// fix the child failed: the tests inherited the ambient values instead of
// stating the ones they assert about, so the 4-wide fixture collided with the
// ambient 768-wide model, the coverage query matched a collection nobody
// wrote, and the server attached to the operator's own running sidecar
// instead of the broken one under test.
//
// The pattern must not match this test, or the child would run the guard
// again.
func TestSuiteIsGreenUnderAnExportedEmbedEnv(t *testing.T) {
	embedtest.RunInHostileEnv(t, "^(TestIndexHookAttachesToRunningLocalProvider|TestStandaloneEmbedRunIsSelfContained|TestServeSurvivesUnusableEmbedProvider)$")
}
