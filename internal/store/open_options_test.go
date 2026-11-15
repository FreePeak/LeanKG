package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestOpenBackendReadOnlyNeverCreatesTheStore pins the no-create open the
// exposure gate needs (CX-18): a read-only OpenOptions on a project without a
// store fails and leaves no database file behind.
func TestOpenBackendReadOnlyNeverCreatesTheStore(t *testing.T) {
	dir := t.TempDir()
	_, err := OpenBackend(context.Background(), OpenOptions{ProjectDir: dir, Engine: EngineSQLite, Mode: RO})
	if err == nil {
		t.Fatal("read-only open of a missing store returned nil, want an error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".leankg", "leankg.db")); !os.IsNotExist(statErr) {
		t.Fatalf("read-only open created the store file (stat err %v)", statErr)
	}
}
