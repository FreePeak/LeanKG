package rest

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestImportAnswerIsCappedLikeMCP pins the import token budget on REST: an
// import{action:"read"} answer over its cap (8000 tokens) is cut and carries
// the budget marker, exactly as the MCP import tool cuts it.
func TestImportAnswerIsCappedLikeMCP(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("func Filler() { /* a line of filler text for the read */ }\n", 2000)
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(gatedHandler(core.New(st, core.Options{ProjectDir: dir}), nil))
	defer srv.Close()

	res, err := httpPost(srv.URL+"/api/v1/import", map[string]any{"action": "read", "path": "big.go"})
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 200 {
		t.Fatalf("import read: %d %s", res.status, res.body)
	}
	var out map[string]any
	if err := json.Unmarshal(res.body, &out); err != nil {
		t.Fatal(err)
	}
	marker, _ := out[budget.BudgetMarkerKey].(map[string]any)
	if marker == nil || marker["max"] != float64(8000) || marker["truncated"] != true {
		t.Fatalf("import read marker = %v; want truncated at the read cap 8000", marker)
	}
}
