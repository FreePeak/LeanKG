package embed

import (
	"testing"

	"github.com/FreePeak/LeanKG/internal/embed/embedtest"
)

// TestSuiteIsGreenUnderAnExportedEmbedEnv is the regression guard for the
// hermeticity defect: the suite must not decide its own result by the
// LEANKG_EMBED_* environment the operator's shell happens to export.
//
// It re-runs the env-reading tests in a CHILD process whose embed environment
// is a plausible operator configuration (a local provider, a catalogued
// model, a width). Before the fix the child failed: the env-reading tests
// inherited the ambient values instead of stating the ones they assert about.
func TestSuiteIsGreenUnderAnExportedEmbedEnv(t *testing.T) {
	embedtest.RunInHostileEnv(t, "^TestFromEnv$")
}
