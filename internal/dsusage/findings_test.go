//go:build dshusage

package dsusage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFindingLog(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "--work-menu--", "session-findings")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.v4.jsonl")
	body := `{"type":"user/message","time":1000,"data":{"text":"trace menu cadence"}}
{"type":"tool/call","time":1001,"data":{"callId":"c1","name":"mcp__leankg__query","arguments":"{\"query\":\"menu\"}"}}
{"type":"tool/result","time":1002,"data":{"message":{"source":{"kind":"tool","callId":"c1"},"content":[{"type":"text","text":"{\"hits\":[{\"id\":\"menu\"}],\"retrieval\":{\"rung\":\"L2\",\"reason\":\"FTS5 keyword match\"}}"}],"isError":false}}}
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindingsStorePersistsAndSkipsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	path := writeFindingLog(t, root)
	cachePath := filepath.Join(t.TempDir(), "findings.json")
	store := NewFindingsStore(cachePath)

	first, err := store.Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Steps) != 1 || first.Steps[0].Rung != "L2" {
		t.Fatalf("first scan = %+v", first)
	}
	if len(first.Findings) != 1 || first.Findings[0].Status != "open" {
		t.Fatalf("first findings = %+v", first.Findings)
	}
	raw, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read findings file: %v", err)
	}
	var persisted persistedFindings
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.Steps != 1 || persisted.Status.Findings != 1 {
		t.Fatalf("persisted status = %+v", persisted.Status)
	}

	// Same size and mtime must use the cached record, not reparse the file.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", len(original))), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	second, err := store.Scan([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Steps) != 1 || second.Steps[0].Rung != "L2" {
		t.Fatalf("unchanged fingerprint was reparsed: %+v", second.Steps)
	}

	// A restarted watcher gets the same evidence without opening the log.
	reloaded := NewFindingsStore(cachePath)
	loaded := reloaded.Snapshot()
	if len(loaded.Steps) != 1 || len(loaded.Findings) != 1 {
		t.Fatalf("reloaded cache = %+v", loaded)
	}
}

func TestFindingsAPIReadsPersistedSnapshot(t *testing.T) {
	root := t.TempDir()
	writeFindingLog(t, root)
	store := NewFindingsStore(filepath.Join(t.TempDir(), "findings.json"))
	if _, err := store.Scan([]string{root}); err != nil {
		t.Fatal(err)
	}
	// If an API handler falls back to ScanRoots, this removal makes it return
	// an empty result and the test fails. The store must serve the last scan.
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	h := Handler([]string{root}, LayaClient{}, nil, store)
	stepsReq := httptest.NewRequest(http.MethodGet, "/api/steps", nil)
	stepsRes := httptest.NewRecorder()
	h.ServeHTTP(stepsRes, stepsReq)
	if stepsRes.Code != http.StatusOK {
		t.Fatalf("steps status = %d: %s", stepsRes.Code, stepsRes.Body.String())
	}
	var stepsBody struct {
		Steps []Step `json:"steps"`
	}
	if err := json.Unmarshal(stepsRes.Body.Bytes(), &stepsBody); err != nil {
		t.Fatal(err)
	}
	if len(stepsBody.Steps) != 1 {
		t.Fatalf("cached steps = %+v", stepsBody.Steps)
	}

	findingsReq := httptest.NewRequest(http.MethodGet, "/api/findings", nil)
	findingsRes := httptest.NewRecorder()
	h.ServeHTTP(findingsRes, findingsReq)
	if findingsRes.Code != http.StatusOK {
		t.Fatalf("findings status = %d: %s", findingsRes.Code, findingsRes.Body.String())
	}
	var findingsBody struct {
		Findings []Finding `json:"findings"`
	}
	if err := json.Unmarshal(findingsRes.Body.Bytes(), &findingsBody); err != nil {
		t.Fatal(err)
	}
	if len(findingsBody.Findings) != 1 || findingsBody.Findings[0].SessionID != "session-findings" {
		t.Fatalf("agent findings = %+v", findingsBody.Findings)
	}
}

func TestFindingsAPIRejectsUnknownSeverity(t *testing.T) {
	store := NewFindingsStore(filepath.Join(t.TempDir(), "findings.json"))
	h := Handler(nil, LayaClient{}, nil, store)
	req := httptest.NewRequest(http.MethodGet, "/api/findings?min_severity=urgent", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.Code)
	}
}
