// Package ab_test is the W7 A/B benchmark harness for the Go engine.
//
// It measures the five primitive costs the Rust A/B comparison needs
// (see README.md in this directory): cold directory indexing, the L1/L2
// retrieval rungs, the L3 vector scan, and memory FTS search. Every
// fixture is generated from a fixed seed, so repeated runs index and
// query identical bytes.
//
// Run:
//
//	go test ./benchmark/ab/ -bench=. -benchmem -run=^$
package ab_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

const (
	corpusFiles = 100
	vecModel    = "bench-minilm-384"
	vecDims     = 384
	vecCount    = 1000
	vecTopK     = 10
)

// benchRand returns a seeded PCG RNG; fixtures must be byte-identical run
// to run, so every generator comes from a fixed seed pair.
func benchRand(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) }

// writeCorpus generates n synthetic .go files under dir with deterministic
// content: one struct, its constructor + String method, and three package
// functions per file. Element names are stable (e.g. handler000) so the
// query benchmarks can pin an exact target.
func writeCorpus(b *testing.B, dir string, n int) {
	b.Helper()
	src := benchRand(1)
	for i := 0; i < n; i++ {
		var sb strings.Builder
		fmt.Fprintf(&sb, "package gen%03d\n\nimport (\n\t\"fmt\"\n\t\"io\"\n)\n\n", i)
		fmt.Fprintf(&sb, "// Rec%03d is a generated record type.\ntype Rec%03d struct {\n\tID    int\n\tName  string\n\tTags  []string\n}\n\n", i, i)
		fmt.Fprintf(&sb, "// NewRec%03d builds a Rec%03d.\nfunc NewRec%03d(id int, name string) *Rec%03d {\n\treturn &Rec%03d{ID: id, Name: name, Tags: []string{\"gen\", \"n%d\"}}\n}\n\n", i, i, i, i, i, i)
		fmt.Fprintf(&sb, "// String renders the record for logs.\nfunc (r *Rec%03d) String() string {\n\treturn fmt.Sprintf(\"rec %%d %%s\", r.ID, r.Name)\n}\n\n", i)
		fmt.Fprintf(&sb, "// parseConfig%03d decodes raw settings into a record.\nfunc parseConfig%03d(raw map[string]any) (*Rec%03d, error) {\n\tid, _ := raw[\"id\"].(int)\n\tname, _ := raw[\"name\"].(string)\n\tif name == \"\" {\n\t\treturn nil, fmt.Errorf(\"config %d: empty name\", id)\n\t}\n\treturn NewRec%03d(id, name), nil\n}\n\n", i, i, i, i, i)
		fmt.Fprintf(&sb, "// handler%03d writes the rendered record.\nfunc handler%03d(w io.Writer, cfg string) error {\n\trec := NewRec%03d(%d, cfg)\n\t_, err := fmt.Fprintln(w, rec.String())\n\treturn err\n}\n\n", i, i, i, i)
		fmt.Fprintf(&sb, "// helper%03d tags the record with a stable pseudo-random suffix.\nfunc helper%03d(r *Rec%03d) string {\n\tr.Tags = append(r.Tags, fmt.Sprintf(\"t%d\"))\n\treturn r.String()\n}\n", i, i, i, src.IntN(1000))
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%03d.go", i)), []byte(sb.String()), 0o644); err != nil {
			b.Fatalf("write corpus file %d: %v", i, err)
		}
	}
}

// freshStore opens a brand-new RW store at dir/.leankg, wiping any previous
// database so each iteration pays the full cold index.
func freshStore(b *testing.B, dir string) store.Backend {
	b.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		st.Close()
		b.Fatalf("migrate: %v", err)
	}
	return st
}

// indexedCorpus builds the 100-file corpus once against a warm store and
// returns the backend plus a cleanup func.
func indexedCorpus(b *testing.B) (store.Backend, func()) {
	b.Helper()
	dir := b.TempDir()
	writeCorpus(b, dir, corpusFiles)
	st := freshStore(b, dir)
	res, err := index.IndexDir(context.Background(), st, dir)
	if err != nil {
		st.Close()
		b.Fatalf("warm index: %v", err)
	}
	if res.Files != corpusFiles || res.Elements == 0 {
		st.Close()
		b.Fatalf("warm index produced %+v", res)
	}
	return st, func() { st.Close() }
}

// BenchmarkIndexDir100Files measures a cold IndexDir over 100 synthetic
// .go files: regex extraction plus batched element/relationship/file
// writes into a fresh SQLite store.
func BenchmarkIndexDir100Files(b *testing.B) {
	dir := b.TempDir()
	writeCorpus(b, dir, corpusFiles)
	dbDir := filepath.Join(dir, ".leankg")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if err := os.RemoveAll(dbDir); err != nil {
			b.Fatal(err)
		}
		st := freshStore(b, dir)
		b.StartTimer()

		res, err := index.IndexDir(context.Background(), st, dir)

		b.StopTimer()
		st.Close()
		b.StartTimer()
		if err != nil {
			b.Fatalf("IndexDir: %v", err)
		}
		if res.Files != corpusFiles {
			b.Fatalf("indexed %d files, want %d", res.Files, corpusFiles)
		}
	}
}

// BenchmarkQueryL1Exact measures the L1 rung: name/qualified-name exact
// lookup over code_elements (SQL, COLLATE NOCASE).
func BenchmarkQueryL1Exact(b *testing.B) {
	st, cleanup := indexedCorpus(b)
	defer cleanup()

	const name = "handler000" // generated by writeCorpus (file 000)
	hits, err := st.FindExact(name)
	if err != nil {
		b.Fatalf("FindExact: %v", err)
	}
	if len(hits) == 0 {
		b.Fatalf("sanity: no exact hit for %s", name)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.FindExact(name); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkQueryL2Fuzzy measures the L2 rung: tokenized FTS5 OR query with
// score re-ranking over code_elements.
func BenchmarkQueryL2Fuzzy(b *testing.B) {
	st, cleanup := indexedCorpus(b)
	defer cleanup()

	const q = "parse config handler"
	hits, err := st.FindFuzzy(q, 50)
	if err != nil {
		b.Fatalf("FindFuzzy: %v", err)
	}
	if len(hits) == 0 {
		b.Fatalf("sanity: no fuzzy hits for %q", q)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.FindFuzzy(q, 50); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSearchVectors1k measures the L3 rung as implemented today:
// in-process exact cosine over 1,000 x 384-dim vectors, top-10, with
// code_elements hydration. This is the O(n) scan ceiling documented in
// internal/store/vectors.go — the number to weigh against pgvector HNSW.
func BenchmarkSearchVectors1k(b *testing.B) {
	dir := b.TempDir()
	st := freshStore(b, dir)
	defer st.Close()

	if err := st.WriteStamp(store.ModelStamp{
		ModelID: vecModel, Revision: "bench", Dimensions: vecDims,
		Distance: "cosine", Provider: "bench",
	}); err != nil {
		b.Fatalf("WriteStamp: %v", err)
	}

	rng := benchRand(42)
	rows := make([]store.VectorRow, vecCount)
	for i := range rows {
		v := make([]float32, vecDims)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		rows[i] = store.VectorRow{QualifiedName: fmt.Sprintf("gen%03d::handler%03d", i, i), Vec: v}
	}
	if err := st.UpsertVectors(vecModel, rows); err != nil {
		b.Fatalf("UpsertVectors: %v", err)
	}

	q := make([]float32, vecDims)
	qr := benchRand(7)
	for j := range q {
		q[j] = qr.Float32()*2 - 1
	}
	sanity, err := st.SearchVectors(vecModel, q, vecTopK)
	if err != nil {
		b.Fatalf("SearchVectors sanity: %v", err)
	}
	if len(sanity) != vecTopK {
		b.Fatalf("sanity: got %d hits, want %d", len(sanity), vecTopK)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.SearchVectors(vecModel, q, vecTopK); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkMemoryFTSSearch measures memory FTS5 search over 50 seeded
// memory files (per-line index rows, unicode61 tokenizer).
func BenchmarkMemoryFTSSearch(b *testing.B) {
	m, err := memory.Open(b.TempDir(), false)
	if err != nil {
		b.Fatalf("memory.Open: %v", err)
	}
	defer m.Close()

	rng := benchRand(11)
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "watcher", "ledger", "checkpoint"}
	for i := 0; i < 50; i++ {
		var sb strings.Builder
		for line := 0; line < 20; line++ {
			for t := 0; t < 8; t++ {
				sb.WriteString(words[rng.IntN(len(words))])
				sb.WriteByte(' ')
			}
			fmt.Fprintf(&sb, "note%02d line%d\n", i, line)
		}
		if err := m.Create(fmt.Sprintf("topics/note%02d.md", i), sb.String()); err != nil {
			b.Fatalf("memory.Create %d: %v", i, err)
		}
	}

	const q = "alpha ledger"
	sanity, err := m.Search(q, 10)
	if err != nil {
		b.Fatalf("Search sanity: %v", err)
	}
	if len(sanity) == 0 {
		b.Fatalf("sanity: no hits for %q", q)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.Search(q, 10); err != nil {
			b.Fatal(err)
		}
	}
}
