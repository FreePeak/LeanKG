package ccmemory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps the suite hermetic: the exclude list points at a file that
// does not exist, so no test reads the developer's real
// ~/.config/leankg/memory-exclude. A test that needs an exclude list sets its
// own file with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ccmemory-test")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccmemory test dir: %v\n", err)
		os.Exit(1)
	}
	os.Setenv(excludeFileEnv, filepath.Join(dir, "absent"))
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
