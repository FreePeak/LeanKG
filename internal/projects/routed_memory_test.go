package projects

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// indexedProject builds a project directory with a store and no memory tree,
// so the router serves it and a memory open has to build the index.
func indexedProject(t *testing.T) string {
	t.Helper()
	dir := canonical(t.TempDir())
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return dir
}

// TestRoutedMemoryOpenFailureRefusesTheProject pins that a routed project whose
// memory layer cannot open is refused and not cached, so the next request
// tries again instead of being served a project without its memory layer.
func TestRoutedMemoryOpenFailureRefusesTheProject(t *testing.T) {
	t.Setenv("LEANKG_DB_PATH", "")
	dir := indexedProject(t)
	// A file where the memory root must be a directory makes memory.Open fail.
	if err := os.WriteFile(filepath.Join(dir, ".leankg", "memory"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("block the memory root: %v", err)
	}
	r := NewRouter(canonical(t.TempDir()), Config{
		ExtraDirs:  []string{dir},
		Mode:       store.RW,
		EngineName: store.EngineSQLite,
		Memory:     true,
	})
	if _, _, err := r.Open(context.Background(), dir); err == nil || !strings.Contains(err.Error(), "memory for") {
		t.Fatalf("Open with an unusable memory root: err = %v, want a memory error", err)
	}
	if n := len(r.entries); n != 0 {
		t.Fatalf("a project whose memory failed was cached (%d entries)", n)
	}
}
