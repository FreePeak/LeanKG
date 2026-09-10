package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

func newEngine(t *testing.T) (*core.Engine, *memory.Memory) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	mem, err := memory.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return core.New(st, mem, nil), mem
}

func TestHealth(t *testing.T) {
	e, _ := newEngine(t)
	srv := httptest.NewServer(Handler(e, nil))
	defer srv.Close()
	res, err := httpGet(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 200 || !bytes.Contains(res.body, []byte("ok")) {
		t.Fatalf("health: %d %s", res.status, res.body)
	}
}

func TestStatusEndpoint(t *testing.T) {
	e, _ := newEngine(t)
	srv := httptest.NewServer(Handler(e, nil))
	defer srv.Close()
	res, err := httpGet(srv.URL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 200 {
		t.Fatalf("status: %d %s", res.status, res.body)
	}
	var out map[string]any
	if err := json.Unmarshal(res.body, &out); err != nil {
		t.Fatal(err)
	}
	if out["backend"] != "sqlite" || out["freshness"] != "cold" {
		t.Fatalf("status payload: %+v", out)
	}
	tools, _ := out["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("tools = %v, want exactly 3", tools)
	}
}

func TestQueryLadderEndpoints(t *testing.T) {
	e, _ := newEngine(t)
	srv := httptest.NewServer(Handler(e, nil))
	defer srv.Close()

	// L0 cold: guidance, never error.
	res, _ := httpPost(srv.URL+"/api/v1/query", map[string]any{"query": "anything"})
	if res.status != 200 {
		t.Fatalf("cold query: %d %s", res.status, res.body)
	}
	var out map[string]any
	_ = json.Unmarshal(res.body, &out)
	if out["freshness"] != "cold" {
		t.Fatalf("cold freshness: %+v", out)
	}

	// Seed one element, then L1.
	_ = e.Store().UpsertElements([]store.Element{{
		QualifiedName: "main.parseConfig", ElementType: "function", Name: "parseConfig",
		FilePath: "main.go", Language: "go", LineStart: 1, LineEnd: 2,
	}})
	res, _ = httpPost(srv.URL+"/api/v1/query", map[string]any{"query": "parseConfig"})
	_ = json.Unmarshal(res.body, &out)
	retr, _ := out["retrieval"].(map[string]any)
	if retr["rung"] != "L1" {
		t.Fatalf("L1: %s", res.body)
	}

	// L2 fuzzy for non-exact words.
	res, _ = httpPost(srv.URL+"/api/v1/query", map[string]any{"query": "parse yaml config"})
	_ = json.Unmarshal(res.body, &out)
	retr, _ = out["retrieval"].(map[string]any)
	if retr["rung"] != "L2" {
		t.Fatalf("L2: %s", res.body)
	}
}

func TestImportEndpoint(t *testing.T) {
	e, _ := newEngine(t)
	srv := httptest.NewServer(Handler(e, nil))
	defer srv.Close()

	src := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.go"), []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := httpPost(srv.URL+"/api/v1/import", map[string]any{"action": "repo", "path": src})
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 200 {
		t.Fatalf("import: %d %s", res.status, res.body)
	}
	var out map[string]any
	_ = json.Unmarshal(res.body, &out)
	if _, ok := out["indexed"]; !ok {
		t.Fatalf("import payload: %s", res.body)
	}
	st := e.Store()
	if n, _ := st.ElementCount(); n == 0 {
		t.Fatal("no elements after import")
	}
}

func TestMemoryHTTPAPI(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem))
	defer srv.Close()

	bank := memory.BankName(t.TempDir())
	res, err := httpPost(srv.URL+"/api/v1/memory/banks/"+bank+"/memories", map[string]any{
		"through_user_turn": 1,
		"entries":           []map[string]any{{"content": "user prefers tabs over spaces"}},
	})
	if err != nil || res.status != 200 {
		t.Fatalf("retain: %d %v %s", res.status, err, res.body)
	}
	res, err = httpPost(srv.URL+"/api/v1/memory/banks/"+bank+"/recall", map[string]any{"query": "tabs spaces", "limit": 5})
	if err != nil || res.status != 200 {
		t.Fatalf("recall: %d %v %s", res.status, err, res.body)
	}
	var out map[string]any
	_ = json.Unmarshal(res.body, &out)
	entries, _ := out["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("recall entries: %s", res.body)
	}
}

// --- tiny helpers (test-local; no external deps) ---

type httpResp struct {
	status int
	body   []byte
}

func httpGet(url string) (httpResp, error) {
	return doReq("GET", url, nil)
}

func httpPost(url string, body any) (httpResp, error) {
	return doReq("POST", url, body)
}

func doReq(method, url string, body any) (httpResp, error) {
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return httpResp{}, err
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return httpResp{}, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return httpResp{}, err
	}
	defer res.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(res.Body); err != nil {
		return httpResp{}, err
	}
	return httpResp{status: res.StatusCode, body: buf.Bytes()}, nil
}

// TestRetainRejectsEmptyContent pins the trust-boundary guard: an entry with
// empty content would be permanently unrecallable (zero-match filtering) —
// the API must reject it instead of silently storing dead data.
func TestRetainRejectsEmptyContent(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem))
	defer srv.Close()

	bank := memory.BankName(t.TempDir())
	res, err := httpPost(srv.URL+"/api/v1/memory/banks/"+bank+"/memories", map[string]any{
		"through_user_turn": 1,
		"entries": []map[string]any{
			{"content": "valid entry about databases"},
			{"content": "   "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.status != 400 {
		t.Fatalf("empty content must 400, got %d %s", res.status, res.body)
	}
	if !bytes.Contains(res.body, []byte("unrecallable")) {
		t.Fatalf("error must explain why: %s", res.body)
	}
	// Nothing was written for the batch (reject, not partial store).
	res, err = httpPost(srv.URL+"/api/v1/memory/banks/"+bank+"/recall", map[string]any{"query": "databases", "limit": 5})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(res.body, &out)
	if entries, _ := out["entries"].([]any); len(entries) != 0 {
		t.Fatalf("rejected batch must not partially store: %s", res.body)
	}
}
