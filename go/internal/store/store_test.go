package store

import (
	"math"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestUpsertAndFindExact(t *testing.T) {
	s := openTestStore(t)
	els := []Element{
		{QualifiedName: "pkg.Service.Handle", ElementType: "method", Name: "Handle", FilePath: "svc.go", Language: "go", LineStart: 10, LineEnd: 20, Content: "func (s *Service) Handle()"},
		{QualifiedName: "pkg.Handle", ElementType: "function", Name: "Handle", FilePath: "util.go", Language: "go", LineStart: 1, LineEnd: 5},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.FindExact("handle") // case-insensitive
	if err != nil {
		t.Fatalf("find exact: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(got), got)
	}

	// Re-upsert same QN replaces, never duplicates (real PK semantics).
	els[0].Content = "updated"
	if err := s.UpsertElements(els[:1]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	n, _ := s.ElementCount()
	if n != 2 {
		t.Fatalf("element count after re-upsert = %d, want 2", n)
	}
}

func TestFindFuzzyFTS(t *testing.T) {
	s := openTestStore(t)
	els := []Element{
		{QualifiedName: "main.parseConfig", ElementType: "function", Name: "parseConfig", FilePath: "main.go", Language: "go", Content: "parses the yaml config file"},
		{QualifiedName: "main.unrelated", ElementType: "function", Name: "unrelated", FilePath: "main.go", Language: "go", Content: "does something else"},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Free text must not be able to inject FTS5 syntax.
	for _, q := range []string{"parse config", "parse*", "config NEAR yaml"} {
		hits, err := s.FindFuzzy(q, 10)
		if err != nil {
			t.Fatalf("fuzzy %q: %v", q, err)
		}
		if len(hits) == 0 || hits[0].Element.Name != "parseConfig" {
			t.Fatalf("fuzzy %q: want parseConfig first, got %+v", q, hits)
		}
	}
	// Hostile input must never error (FTS5 syntax injection); matches optional.
	for _, q := range []string{"\"parse", `) OR (1=1`, "parse AND NOT config"} {
		if _, err := s.FindFuzzy(q, 10); err != nil {
			t.Fatalf("fuzzy %q must not error: %v", q, err)
		}
	}
}

func TestDeleteByFile(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "a.A", ElementType: "function", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b.B", ElementType: "function", Name: "B", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships([]Relationship{{Source: "a.A", Target: "b.B", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteByFile("a.go"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ElementCount(); n != 1 {
		t.Fatalf("element count = %d, want 1", n)
	}
	// FTS must not return the deleted element.
	hits, err := s.FindFuzzy("A", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Element.QualifiedName == "a.A" {
			t.Fatalf("deleted element still in FTS: %+v", h)
		}
	}
}

// The reconcile ceiling that `leankg gc` repairs: DeleteByFile removes edges
// SOURCED by a file's elements, but edges pointing INTO the deleted file
// from surviving files remain — and incremental index never revisits them.
// gc purges exactly the dangling edges and leaves fully live ones alone.
func TestDeleteOrphanRelationships(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "a.A", ElementType: "function", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b.B", ElementType: "function", Name: "B", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships([]Relationship{
		{Source: "b.B", Target: "a.A", RelType: "calls"}, // into a.go: survives DeleteByFile as a dangling edge
		{Source: "b.B", Target: "b.B", RelType: "uses"},  // fully live: must survive gc
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteByFile("a.go"); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteOrphanRelationships()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d edges, want 1", n)
	}
	rels, err := s.RelationshipsAll(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Source != "b.B" || rels[0].Target != "b.B" {
		t.Fatalf("live survivor missing: %+v", rels)
	}

	// Idempotent, and the watermark must not move when nothing went:
	// a no-op gc may not mark a fresh project possibly_stale.
	seq1, _, _ := s.Watermark()
	n2, err := s.DeleteOrphanRelationships()
	if err != nil {
		t.Fatal(err)
	}
	seq2, _, _ := s.Watermark()
	if n2 != 0 || seq2 != seq1 {
		t.Fatalf("second gc: purged %d, seq %d->%d; want 0 and no bump", n2, seq1, seq2)
	}
}

func TestWatermarkBumpsOnWrite(t *testing.T) {
	s := openTestStore(t)
	seq0, _, _ := s.Watermark()
	if err := s.UpsertElements([]Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	seq1, _, _ := s.Watermark()
	if seq1 <= seq0 {
		t.Fatalf("watermark did not advance: %d -> %d", seq0, seq1)
	}
}

func TestModelStampAndVectors(t *testing.T) {
	s := openTestStore(t)
	st := ModelStamp{ModelID: "test-model", Revision: "40-hex-revision", Dimensions: 4, Distance: "cosine", Provider: "test"}
	if err := s.WriteStamp(st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp("test-model")
	if err != nil || got == nil || got.Revision != "40-hex-revision" {
		t.Fatalf("stamp round-trip failed: %+v %v", got, err)
	}
	if none, _ := s.Stamp("missing"); none != nil {
		t.Fatalf("missing model stamp = %+v, want nil", none)
	}

	vecs := []VectorRow{
		{QualifiedName: "a", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "b", Vec: []float32{0, 1, 0, 0}},
	}
	if err := s.UpsertVectors("test-model", vecs); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmbeddingStates("test-model", map[string]string{"a": "ha", "b": "hb"}); err != nil {
		t.Fatal(err)
	}
	state, _ := s.EmbeddingStateMap("test-model")
	if state["a"] != "ha" {
		t.Fatalf("state = %+v", state)
	}
	hits, err := s.SearchVectors("test-model", []float32{1, 0, 0, 0}, 1)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %v %+v", err, hits)
	}
	if hits[0].Element.QualifiedName != "a" || math.Abs(hits[0].Similarity-1) > 1e-6 {
		t.Fatalf("top hit = %+v, want a with sim 1", hits[0])
	}
	// Full rebuild: ClearVectors wipes vectors + state for the model.
	if err := s.ClearVectors("test-model"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.VectorCount("test-model"); n != 0 {
		t.Fatalf("vector count after clear = %d", n)
	}
	if state, _ := s.EmbeddingStateMap("test-model"); len(state) != 0 {
		t.Fatalf("state after clear = %+v", state)
	}
}

func TestRelationshipUpsertDedups(t *testing.T) {
	s := openTestStore(t)
	rels := []Relationship{{Source: "a", Target: "b", RelType: "calls", Confidence: 0.5}}
	if err := s.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships(rels); err != nil { // duplicate must overwrite, not duplicate
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("relationship count = %d err %v, want 1", n, err)
	}
}

func TestEmbedRuns(t *testing.T) {
	s := openTestStore(t)
	id, err := s.StartEmbedRun("m", "incremental", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishEmbedRun(id, "ok", 5, 2, 0, 1, 0); err != nil {
		t.Fatal(err)
	}
	run, err := s.LastEmbedRun("m")
	if err != nil || run == nil {
		t.Fatalf("last run: %v %+v", err, run)
	}
	if run.Status != "ok" || run.Embedded != 5 || run.Truncations != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func TestInventoryFreshness(t *testing.T) {
	s := openTestStore(t)
	if inv, _ := s.LoadInventory(); inv != nil {
		t.Fatalf("no inventory expected, got %+v", inv)
	}
	if err := s.UpsertElements([]Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInventory(Inventory{TotalElements: 1, ElementsByType: map[string]int{"function": 1}}); err != nil {
		t.Fatal(err)
	}
	inv, err := s.LoadInventory()
	if err != nil || inv == nil || inv.TotalElements != 1 {
		t.Fatalf("inventory: %v %+v", err, inv)
	}
	seq, _, _ := s.Watermark()
	if inv.LastInventorySeq != seq {
		t.Fatalf("inventory seq %d != watermark %d", inv.LastInventorySeq, seq)
	}
	// Any write after inventory => possibly_stale (seq moves past inv seq).
	if err := s.UpsertRelationships([]Relationship{{Source: "x", Target: "x", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	seq2, _, _ := s.Watermark()
	if seq2 <= inv.LastInventorySeq {
		t.Fatalf("write after inventory did not advance watermark past %d", inv.LastInventorySeq)
	}
}

func TestReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	rw, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := rw.UpsertElements([]Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RO)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got, err := ro.FindExact("x"); err != nil || len(got) != 1 {
		t.Fatalf("ro read: %v %+v", err, got)
	}
	if err := ro.Migrate(); err == nil {
		t.Fatal("migrate on RO must fail")
	}
}

// TestStampIdentityColumns pins the full stamp round-trip (issue #279):
// chunker version and both prefix halves are persisted, compared as part of
// the identity, and a pre-migration row still reads back as the zero identity
// (chunker 0 = unversioned) so writers treat it as a mismatch demanding a
// rebuild rather than silently accepting it.
func TestStampIdentityColumns(t *testing.T) {
	s := openTestStore(t)
	want := ModelStamp{
		ModelID: "m-4B", Revision: "abc123", Dimensions: 4, Distance: "cosine",
		Provider: "openai", ChunkerVersion: 3,
		QueryPrefix: "query: ", DocumentPrefix: "doc: ",
	}
	if err := s.WriteStamp(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp(want.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != want {
		t.Fatalf("stamp round-trip = %+v, want %+v", got, want)
	}
	// The identity is compared as a whole: changing only the chunker version
	// makes the stored stamp unequal (what the write guard keys on).
	drifted := want
	drifted.ChunkerVersion++
	if *got == drifted {
		t.Fatal("chunker version must participate in stamp equality")
	}
	drifted = want
	drifted.QueryPrefix = ""
	if *got == drifted {
		t.Fatal("query prefix must participate in stamp equality")
	}

	// Legacy row shape (pre-migration columns only): defaults must make it
	// readable and unambiguously not-current.
	if _, err := s.db.Exec(`INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider)
		VALUES ('legacy', 'old-rev', 8, 'cosine', 'local')`); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Stamp("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if legacy == nil || legacy.ChunkerVersion != 0 || legacy.QueryPrefix != "" || legacy.DocumentPrefix != "" {
		t.Fatalf("legacy stamp = %+v, want zero chunker/prefixes", legacy)
	}
	stamps, err := s.Stamps()
	if err != nil || len(stamps) != 2 {
		t.Fatalf("Stamps() = %+v, %v; want 2 rows", stamps, err)
	}
}

// TestReplaceFileVectorsIsOneAtomicUnit pins the per-file write contract:
// vectors and their embedding_state rows land together, a repeated call
// replaces (never duplicates) a qualified name, an empty set is refused, and
// the whole unit advances the freshness watermark exactly once — the
// observable difference between one transaction and the previous
// vectors-then-state pair.
func TestReplaceFileVectorsIsOneAtomicUnit(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "a.go::f1", ElementType: "function", Name: "f1", FilePath: "a.go", Language: "go"},
		{QualifiedName: "a.go::f2", ElementType: "function", Name: "f2", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	rows := []VectorRow{
		{QualifiedName: "a.go::f1", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "a.go::f2", Vec: []float32{0, 1, 0, 0}},
	}
	states := map[string]string{"a.go::f1": "h1", "a.go::f2": "h2"}
	before, _, err := s.Watermark()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceFileVectors("m", rows, states); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.Watermark()
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("watermark %d -> %d; one write unit must bump once", before, after)
	}
	if n, err := s.VectorCount("m"); err != nil || n != 2 {
		t.Fatalf("VectorCount = %d, %v; want 2", n, err)
	}
	if st, err := s.EmbeddingStateMap("m"); err != nil || st["a.go::f1"] != "h1" || st["a.go::f2"] != "h2" {
		t.Fatalf("embedding state = %+v, %v", st, err)
	}

	// Re-run with a changed vector for one element: replaced in place, no
	// duplicate row, state re-stamped.
	if err := s.ReplaceFileVectors("m",
		[]VectorRow{{QualifiedName: "a.go::f1", Vec: []float32{0, 0, 1, 0}}},
		map[string]string{"a.go::f1": "h1b"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.VectorCount("m"); n != 2 {
		t.Fatalf("vector count after replace = %d, want 2 (no duplicate)", n)
	}
	hits, err := s.SearchVectors("m", []float32{0, 0, 1, 0}, 1)
	if err != nil || len(hits) != 1 || hits[0].Element.QualifiedName != "a.go::f1" {
		t.Fatalf("replaced vector not searchable: %+v, %v", hits, err)
	}
	if st, _ := s.EmbeddingStateMap("m"); st["a.go::f1"] != "h1b" {
		t.Fatalf("state after replace = %+v", st)
	}

	// An empty unit is a caller bug, not a no-op: it would carry no rows for
	// the file it claims to replace.
	if err := s.ReplaceFileVectors("m", nil, nil); err == nil {
		t.Fatal("empty vector set must be refused")
	}
}
