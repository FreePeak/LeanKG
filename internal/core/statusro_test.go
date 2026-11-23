package core

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestStatusReadOnlyNeverEmbeddedIsHealthy (issue #592 item 6): a read-only
// store that was never embedded is not unhealthy. Its embedding state is
// reported as an empty list with no embeddings_error, and healthy stays true.
func TestStatusReadOnlyNeverEmbeddedIsHealthy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), ".leankg", "leankg.db")

	rw, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: path, Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rw.UpsertElements(ctx, []store.Element{{QualifiedName: "m.a", ElementType: "function", Name: "a", FilePath: "m.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	if err := rw.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenBackend(ctx, store.OpenOptions{DBPath: path, Mode: store.RO})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })

	out, err := New(ro, Options{}).Status(ctx)
	if err != nil {
		t.Fatalf("status must answer for a read-only, never-embedded store: %v", err)
	}
	if healthy, _ := out["healthy"].(bool); !healthy {
		t.Fatalf("a never-embedded read-only store must stay healthy: healthy=%v embeddings_error=%v", out["healthy"], out["embeddings_error"])
	}
	if _, ok := out["embeddings_error"]; ok {
		t.Fatalf("a never-embedded store must not carry embeddings_error: %v", out["embeddings_error"])
	}
	emb, ok := out["embeddings"].([]map[string]any)
	if !ok || emb == nil || len(emb) != 0 {
		t.Fatalf("a never-embedded store must publish an empty embeddings list: %#v", out["embeddings"])
	}
}
