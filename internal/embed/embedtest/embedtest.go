// Package embedtest makes the embed environment of a test hermetic.
//
// The embed provider is configured entirely by the environment: the provider
// name, the model, the endpoint, the vector width. A test that inherits the
// values an operator exported asserts about that machine rather than about
// the code under test, so whether the suite is green depends on who runs it.
//
// Every embed reader treats an empty value as "not configured" (they all read
// the variable with os.Getenv and compare it against ""), so an empty value
// IS the neutral baseline. Clear applies it to one test and restores the
// previous values when the test ends; UnsetProcess applies it to a whole test
// binary.
//
// It imports testing, so only _test files may import it.
package embedtest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Vars are the environment variables the embed provider reads.
var Vars = []string{
	"LEANKG_EMBED_PROVIDER",
	"LEANKG_EMBED_MODEL",
	"LEANKG_EMBED_BASE_URL",
	"LEANKG_EMBED_API_KEY",
	"LEANKG_EMBED_DIMS",
	"LEANKG_EMBED_REVISION",
	"LEANKG_EMBED_TEXT_CAP",
	"LEANKG_EMBED_SIDECAR_CMD",
	"LEANKG_EMBED_SIDECAR_ARGS",
	"LEANKG_EMBED_SIDECAR_PORT",
	"LEANKG_EMBED_SIDECAR_READY_SECS",
}

// HostileEnv is the embed configuration an operator's shell plausibly
// exports: a configured local provider, a catalogued model, a width. The
// suite must be green under it.
var HostileEnv = []string{
	"LEANKG_EMBED_PROVIDER=local",
	"LEANKG_EMBED_MODEL=embeddinggemma-2",
	"LEANKG_EMBED_BASE_URL=http://127.0.0.1:9103/v1",
	"LEANKG_EMBED_DIMS=512",
}

// hostileMarker names the child process RunInHostileEnv starts. A package
// TestMain calls UnsetProcess to wipe the ambient embed configuration; in the
// guard's child that ambient configuration IS the subject under test, so
// UnsetProcess leaves it alone.
const hostileMarker = "LEANKG_EMBEDTEST_HOSTILE"

// Clear empties every embed variable for the duration of the test and restores
// the previous values when the test ends. Use it in a test whose subject is
// what a configured (or an unconfigured) provider does.
func Clear(t *testing.T) {
	t.Helper()
	for _, k := range Vars {
		t.Setenv(k, "")
	}
}

// UnsetProcess removes every embed variable from the process environment. Use
// it in a package's TestMain: the test binary then starts from the same
// baseline everywhere, whatever the operator's shell exported, and every CLI
// child it spawns inherits that baseline.
//
// The guard's own child is exempt (see RunInHostileEnv): there the hostile
// environment is what is under test.
func UnsetProcess() {
	if os.Getenv(hostileMarker) != "" {
		return
	}
	for _, k := range Vars {
		_ = os.Unsetenv(k)
	}
}

// RunInHostileEnv re-runs the tests matching runPattern in a CHILD process
// whose embed environment is HostileEnv, and fails the test when that child
// does not pass.
//
// It is the guard against the defect this package exists for: a suite whose
// result depends on the machine it runs on. The child inherits the parent's
// environment and overrides the embed knobs with the hostile ones (the last
// entry for a key wins in os/exec), so it sees what an operator's shell would
// hand the suite.
//
// The pattern must not match the calling test itself, or the child would run
// the guard again.
func RunInHostileEnv(t *testing.T, runPattern string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	cmd := exec.Command(bin, "-test.run="+runPattern, "-test.count=1")
	// The marker tells the package's TestMain to leave the embed environment
	// alone: in this child the hostile environment is the subject, so
	// UnsetProcess must not wipe it before the test runs.
	cmd.Env = append(os.Environ(), append(HostileEnv, hostileMarker+"=1")...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the suite must be green under %s, but the child failed: %v\n%s",
			strings.Join(HostileEnv, " "), err, out)
	}
}
