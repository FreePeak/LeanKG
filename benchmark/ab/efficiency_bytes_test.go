package ab_test

// Bytes-per-answer measurement for the search ladder (efficiency work).
//
// Gated by LEANKG_BYTES_BENCH=1 so `make go-test` never runs it. It indexes a
// copy of the callers truth's source commit (pinnedCorpus) into a temp SQLite
// store, runs a fixed set of search questions through the engine's budgeted
// answer path (the same bytes an MCP client receives), and reports per question:
// the response size, the rank of the expected code hit in the returned hits, and
// the raw keyword-arm rank before demotion. With LEANKG_BYTES_BENCH_OUT set, the
// rows are written there as JSON.
//
// Each question has a ceiling on its response bytes (the measured size plus
// 25 percent, rounded up) and a worst allowed hit rank (the measured rank). A
// question whose expected hit is absent fails. Re-measure and reset the
// ceilings after an intended change to the answer shape.
//
//	LEANKG_BYTES_BENCH=1 LEANKG_BYTES_BENCH_OUT=/path/out.json \
//	  go test ./benchmark/ab/ -run TestSearchAnswerBytes -count=1 -v

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	Query      string   `json:"query"`
	Expected   []string `json:"expected"`
	MaxBytes   int      `json:"max_bytes"`    // measured bytes x 1.25, rounded up
	MaxHitRank int      `json:"max_hit_rank"` // measured hit rank (worst allowed)
}

var bytesBenchCases = []bytesBenchCase{
	{Query: "embedded version source of truth", Expected: []string{"cmd/leankg-embed/version.go", "cmd/leankg/version.go"}, MaxBytes: 6128, MaxHitRank: 3},
	{Query: "authenticate", Expected: []string{"internal/mcp/authenticate.go"}, MaxBytes: 5040, MaxHitRank: 1},
	{Query: "ReplaceFileRelationships", Expected: []string{"internal/store/elements.go"}, MaxBytes: 1857, MaxHitRank: 1},
	{Query: "HTTPHandler", Expected: []string{"internal/mcp/server.go"}, MaxBytes: 659, MaxHitRank: 1},
	{Query: "store.Backend", Expected: []string{"internal/store/backend.go"}, MaxBytes: 4448, MaxHitRank: 1},
	{Query: "bearer gate", Expected: []string{"internal/auth/resolve.go", "internal/auth/http.go", "internal/mcp/server.go"}, MaxBytes: 5747, MaxHitRank: 3},
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
	repo, commit := pinnedCorpus(t, loadCallersTruth(t, callersTruthPath).SourceCommit)
	t.Logf("corpus commit %s", commit)
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
		t.Logf("bytes=%d (ceiling %d) hit_rank=%d (worst %d) raw_rank=%d hits=%d %q",
			row.Bytes, c.MaxBytes, row.HitRank, c.MaxHitRank, row.RawRank, row.Hits, c.Query)
		if row.Bytes > c.MaxBytes {
			t.Errorf("%q: %d bytes exceeds the ceiling %d", c.Query, row.Bytes, c.MaxBytes)
		}
		if row.HitRank == 0 || row.HitRank > c.MaxHitRank {
			t.Errorf("%q: expected hit at rank %d, worst allowed %d (0 = absent)", c.Query, row.HitRank, c.MaxHitRank)
		}
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
