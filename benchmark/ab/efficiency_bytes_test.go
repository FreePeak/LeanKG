package ab_test

// Bytes-per-answer measurement for the search ladder (efficiency work).
//
// Gated by LEANKG_BYTES_BENCH=1 so `make go-test` never runs it. It indexes
// this repository into a temp SQLite store, runs a fixed set of search
// questions through the engine's budgeted answer path (the same bytes an MCP
// client receives), and reports per question: the response size, the rank of
// the expected code hit in the returned hits, and the raw keyword-arm rank
// before demotion. With LEANKG_BYTES_BENCH_OUT set, the rows are written there
// as JSON.
//
//	LEANKG_BYTES_BENCH=1 LEANKG_BYTES_BENCH_OUT=/path/out.json \
//	  go test ./benchmark/ab/ -run TestSearchAnswerBytes -count=1 -v

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/store"
)

// bytesBenchCase is one question and the files that hold its expected code
// element. file_path matching is the criterion: any element in one of these
// files counts as the expected hit.
type bytesBenchCase struct {
	Query    string   `json:"query"`
	Expected []string `json:"expected"`
}

var bytesBenchCases = []bytesBenchCase{
	{Query: "embedded version source of truth", Expected: []string{"cmd/leankg-embed/version.go", "cmd/leankg/version.go"}},
	{Query: "authenticate", Expected: []string{"internal/mcp/authenticate.go"}},
	{Query: "ReplaceFileRelationships", Expected: []string{"internal/store/elements.go"}},
	{Query: "HTTPHandler", Expected: []string{"internal/mcp/server.go"}},
	{Query: "store.Backend", Expected: []string{"internal/store/backend.go"}},
	{Query: "bearer gate", Expected: []string{"internal/auth/resolve.go", "internal/auth/http.go", "internal/mcp/server.go"}},
}

// bytesBenchRow is one question's measurement.
type bytesBenchRow struct {
	Query   string   `json:"query"`
	Bytes   int      `json:"bytes"`
	HitRank int      `json:"hit_rank"` // 1-based rank in the returned hits, 0 = absent
	RawRank int      `json:"raw_rank"` // 1-based rank in the keyword arm before demotion, 0 = absent
	Hits    int      `json:"hits"`     // number of returned hits
	Top5    []string `json:"top5"`     // file_path::qualified_name of the first five hits
}

func TestSearchAnswerBytes(t *testing.T) {
	if os.Getenv("LEANKG_BYTES_BENCH") != "1" {
		t.Skip("set LEANKG_BYTES_BENCH=1 to measure answer bytes")
	}
	ctx := context.Background()
	_, file, _, _ := runtime.Caller(0)
	repo, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenBackend(ctx, store.OpenOptions{Engine: store.EngineSQLite, DBPath: filepath.Join(t.TempDir(), "leankg.db"), Mode: store.RW})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	reg := langs.DefaultRegistry()
	if _, err := reg.Activate(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if _, err := index.IndexDirWith(ctx, st, repo, reg); err != nil {
		t.Fatal(err)
	}
	e := core.New(st, core.Options{ProjectDir: repo, Langs: reg})

	rows := make([]bytesBenchRow, 0, len(bytesBenchCases))
	for _, c := range bytesBenchCases {
		ans, err := e.QueryAnswer(ctx, core.QueryRequest{Query: c.Query})
		if err != nil {
			t.Fatalf("%q: %v", c.Query, err)
		}
		body, err := json.Marshal(ans.Body)
		if err != nil {
			t.Fatal(err)
		}
		row := bytesBenchRow{Query: c.Query, Bytes: len(body), HitRank: rankOf(hitFiles(ans.Raw), c.Expected)}
		raw, _, err := st.FindFuzzy(ctx, c.Query, 200)
		if err != nil {
			t.Fatal(err)
		}
		rawFiles := make([]string, 0, len(raw))
		for _, m := range raw {
			rawFiles = append(rawFiles, m.Element.FilePath)
		}
		row.RawRank = rankOf(rawFiles, c.Expected)
		hits := hitsList(ans.Raw)
		row.Hits = len(hits)
		for i, h := range hits {
			if i == 5 {
				break
			}
			row.Top5 = append(row.Top5, h["qualified_name"].(string))
		}
		rows = append(rows, row)
		t.Logf("bytes=%d hit_rank=%d raw_rank=%d hits=%d %q", row.Bytes, row.HitRank, row.RawRank, row.Hits, c.Query)
	}
	if out := os.Getenv("LEANKG_BYTES_BENCH_OUT"); out != "" {
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// hitsList returns the returned hits of an answer as file/name maps.
func hitsList(raw map[string]any) []map[string]any {
	switch hs := raw["hits"].(type) {
	case []map[string]any:
		return hs
	case []any:
		out := make([]map[string]any, 0, len(hs))
		for _, h := range hs {
			if m, ok := h.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func hitFiles(raw map[string]any) []string {
	var files []string
	for _, h := range hitsList(raw) {
		fp, _ := h["file_path"].(string)
		files = append(files, fp)
	}
	return files
}

// rankOf is the 1-based position of the first file in expected, 0 when absent.
func rankOf(files []string, expected []string) int {
	want := map[string]bool{}
	for _, e := range expected {
		want[e] = true
	}
	for i, f := range files {
		if want[f] {
			return i + 1
		}
	}
	return 0
}
