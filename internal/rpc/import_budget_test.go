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
	"github.com/FreePeak/LeanKG/internal/httpx"
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
	client := rpcClient(t, dir)
	big := strings.Repeat("func Filler() { /* a line of filler text for the read */ }\n", 2000)
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

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

// rpcClient serves a fresh store rooted at dir over ConnectRPC and returns a
// client for it. The store closes with the test.
func rpcClient(t *testing.T, dir string) leankgv1connect.LeanKGClient {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), store.OpenOptions{DBPath: filepath.Join(dir, ".leankg", "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return serveRPC(t, core.New(st, core.Options{ProjectDir: dir}))
}

// serveRPC serves engine over ConnectRPC and returns a client for it. The
// server closes with the test.
func serveRPC(t *testing.T, engine *core.Engine) leankgv1connect.LeanKGClient {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := rpc.Handler(engine, telemetry.Nop{})
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return leankgv1connect.NewLeanKGClient(srv.Client(), srv.URL)
}

// TestImportBodyOverOneMiBIsResourceExhausted pins the body cap on the
// ConnectRPC wire: a request over httpx.MaxBodyBytes is refused by the
// framework before the handler runs, with the resource-exhausted code.
func TestImportBodyOverOneMiBIsResourceExhausted(t *testing.T) {
	client := rpcClient(t, t.TempDir())
	big := strings.Repeat("a", httpx.MaxBodyBytes+1)
	_, err := client.Import(context.Background(), connect.NewRequest(&leankgv1.ImportRequest{Action: "read", Path: big}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("import over 1 MiB: code %v; want %v", connect.CodeOf(err), connect.CodeResourceExhausted)
	}
}
