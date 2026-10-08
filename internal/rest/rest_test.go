package rest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/store"
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

// TestSessionAndOntologyEndpoints covers the wiring for internal/session and
// internal/ontology over REST (both parse bodies at the trust boundary).
func TestSessionAndOntologyEndpoints(t *testing.T) {
	e, _ := newEngine(t)
	dir := t.TempDir()
	e.SetProjectDir(dir)
	srv := httptest.NewServer(Handler(e, nil))
	defer srv.Close()

	// session offload via the import tool, then recall via REST.
	res, err := httpPost(srv.URL+"/api/v1/import", map[string]any{
		"action": "session", "command": "offload",
		"args": map[string]any{"session_id": "s1", "node_id": "node-1", "payload": "bulky", "summary": "s"},
	})
	if err != nil || res.status != 200 {
		t.Fatalf("session offload: %d %v %s", res.status, err, res.body)
	}
	res, err = httpPost(srv.URL+"/api/v1/session/read", map[string]any{
		"command": "recall", "session_id": "s1", "node_id": "node-1",
	})
	if err != nil || res.status != 200 {
		t.Fatalf("session recall: %d %v %s", res.status, err, res.body)
	}
	var out map[string]any
	_ = json.Unmarshal(res.body, &out)
	if out["payload"] != "bulky" {
		t.Fatalf("recall payload: %s", res.body)
	}
	// unknown node is an error, not a 200 with empty payload.
	res, _ = httpPost(srv.URL+"/api/v1/session/read", map[string]any{
		"command": "recall", "session_id": "s1", "node_id": "missing-node",
	})
	if res.status == 200 {
		t.Fatalf("missing node must error: %s", res.body)
	}

	// ontology: seed an element, match a catalog, read matches back.
	_ = e.Store().UpsertElements([]store.Element{
		{QualifiedName: "pkg.Login", ElementType: "function", Name: "Login", FilePath: "auth.go", Language: "go"},
	})
	catPath := filepath.Join(dir, "c.json")
	if err := os.WriteFile(catPath, []byte(`{"concepts":[{"id":"auth","label":"Auth","aliases":["login"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = httpPost(srv.URL+"/api/v1/ontology/match", map[string]any{"catalog": "c.json"})
	if err != nil || res.status != 200 {
		t.Fatalf("ontology match: %d %v %s", res.status, err, res.body)
	}
	// A catalog outside the project is refused, never read (RS-01).
	outside := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(outside, []byte(`{"concepts":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, _ = httpPost(srv.URL+"/api/v1/ontology/match", map[string]any{"catalog": outside}); res.status != 400 ||
		!strings.Contains(string(res.body), "LEANKG_ERROR_PATH_OUTSIDE_PROJECT") {
		t.Fatalf("outside catalog: %d %s", res.status, res.body)
	}
	res, err = httpGet(srv.URL + "/api/v1/ontology/matches")
	if err != nil || res.status != 200 {
		t.Fatalf("ontology matches: %d %v %s", res.status, err, res.body)
	}
	_ = json.Unmarshal(res.body, &out)
	if matches, ok := out["matches"].([]any); !ok || len(matches) != 1 {
		t.Fatalf("persisted matches: %s", res.body)
	}
}

// getJSON GETs url (optionally decoding a JSON body into out) and returns the
// status code — the read-side twin of postJSON.
func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode %s response: %v", url, err)
		}
	}
	return resp.StatusCode
}

// TestNativeRetainAcknowledgesWhatItWrote pins RS-07: a retain without a
// cursor lands (it used to read as cursor 0 and be dropped), and a batch the
// cursor gate skips is reported as skipped — never as `retained: N`.
func TestNativeRetainAcknowledgesWhatItWrote(t *testing.T) {
	e, mem := newEngine(t)
	srv := httptest.NewServer(Handler(e, mem))
	defer srv.Close()
	post := func(body map[string]any) map[string]any {
		t.Helper()
		res, err := httpPost(srv.URL+"/api/v1/memory/banks/b1/memories", body)
		if err != nil || res.status != 200 {
			t.Fatalf("retain: %d %v %s", res.status, err, res.body)
		}
		var out map[string]any
		_ = json.Unmarshal(res.body, &out)
		return out
	}
	recall := func(q string) int {
		res, _ := httpPost(srv.URL+"/api/v1/memory/banks/b1/recall", map[string]any{"query": q})
		var out struct {
			Entries []any `json:"entries"`
		}
		_ = json.Unmarshal(res.body, &out)
		return len(out.Entries)
	}
	if out := post(map[string]any{"entries": []any{map[string]any{"content": "zebras without a cursor"}}}); out["retained"] != float64(1) {
		t.Fatalf("no-cursor retain: %v", out)
	}
	if recall("zebras") != 1 {
		t.Fatal("no-cursor retain was not written")
	}
	if out := post(map[string]any{"entries": []any{map[string]any{"content": "giraffes at turn one"}}, "through_user_turn": 1}); out["retained"] != float64(1) {
		t.Fatalf("cursor 1: %v", out)
	}
	out := post(map[string]any{"entries": []any{map[string]any{"content": "lions again at one"}}, "through_user_turn": 1})
	if out["retained"] != float64(0) || out["skipped"] != float64(1) {
		t.Fatalf("repeated cursor must report skipped, got %v", out)
	}
	if recall("lions") != 0 {
		t.Fatal("skipped batch was written")
	}
}
