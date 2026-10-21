package refresh

import (
	"os"
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed/embedtest"
)

// TestMain starts the package from an UNCONFIGURED embed environment: the
// embed provider is configured entirely by the LEANKG_EMBED_* variables, so
// whatever the operator's shell exported would otherwise decide what these
// tests assert. A test that needs a value states it (see isolateEnv).
func TestMain(m *testing.M) {
	embedtest.UnsetProcess()
	os.Exit(m.Run())
}

// TestSuiteIsGreenUnderAnExportedEmbedEnv is the regression guard for the
// hermeticity defect: the suite's result must not depend on the
// LEANKG_EMBED_* environment the operator's shell happens to export.
//
// It re-runs the env-sensitive test in a CHILD process whose embed
// environment is a plausible operator configuration — a local provider, a
// catalogued model, a width — and fails when that child fails. Before the
// fix the child failed: the test's helper stated half of the embed baseline,
// and the ambient model and width won over the ones it did state, so the
// stamped collection was not the one the test asked about.
//
// The pattern must not match this test, or the child would run the guard
// again.
func TestSuiteIsGreenUnderAnExportedEmbedEnv(t *testing.T) {
	embedtest.RunInHostileEnv(t, "^TestRunIndexesCodeDocsAndEmbeds$")
}
