package langs

import "os/exec"

// execLookPath is a thin indirection so tests can stub PATH lookups.
func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
