package rpc_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	connect "connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/rpc"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	"github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// TestImportAnswerIsCappedOnTheWire pins the import token budget on ConnectRPC,
// the same cut the MCP and REST import tools apply to an import{action:"read"}.
func TestImportAnswerIsCappedOnTheWire(t *testing.T) {
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
	mux := http.NewServeMux()
	path, handler := rpc.Handler(core.New(st, core.Options{ProjectDir: dir}), telemetry.Nop{})
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := leankgv1connect.NewLeanKGClient(srv.Client(), srv.URL)

	res, err := client.Import(context.Background(), connect.NewRequest(&leankgv1.ImportRequest{Action: "read", Path: "big.go"}))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Msg.Json), &out); err != nil {
		t.Fatal(err)
	}
	marker, _ := out[budget.BudgetMarkerKey].(map[string]any)
	if marker == nil || marker["max"] != float64(8000) {
		t.Fatalf("import read marker = %v; want truncated at the read cap 8000", marker)
	}
}
