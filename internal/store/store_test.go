package store

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := openSQLite(context.Background(), filepath.Join(t.TempDir(), ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
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
	if err := s.UpsertElements(context.Background(), els); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.FindExact(context.Background(), "handle") // case-insensitive
	if err != nil {
		t.Fatalf("find exact: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 hits, got %d: %+v", len(got), got)
	}

	// Re-upsert same QN replaces, never duplicates (real PK semantics).
	els[0].Content = "updated"
	if err := s.UpsertElements(context.Background(), els[:1]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	n, _ := s.ElementCount(context.Background())
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
	if err := s.UpsertElements(context.Background(), els); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Free text must not be able to inject FTS5 syntax.
	for _, q := range []string{"parse config", "parse*", "config NEAR yaml"} {
		hits, _, err := s.FindFuzzy(context.Background(), q, 10)
		if err != nil {
			t.Fatalf("fuzzy %q: %v", q, err)
		}
		if len(hits) == 0 || hits[0].Element.Name != "parseConfig" {
			t.Fatalf("fuzzy %q: want parseConfig first, got %+v", q, hits)
		}
	}
	// Hostile input must never error (FTS5 syntax injection); matches optional.
	for _, q := range []string{"\"parse", `) OR (1=1`, "parse AND NOT config"} {
		if _, _, err := s.FindFuzzy(context.Background(), q, 10); err != nil {
			t.Fatalf("fuzzy %q must not error: %v", q, err)
		}
	}
}

func TestDeleteByFile(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "a.A", ElementType: "function", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b.B", ElementType: "function", Name: "B", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships(context.Background(), []Relationship{{Source: "a.A", Target: "b.B", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteByFile(context.Background(), "a.go"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ElementCount(context.Background()); n != 1 {
		t.Fatalf("element count = %d, want 1", n)
	}
	// FTS must not return the deleted element.
	hits, _, err := s.FindFuzzy(context.Background(), "A", 10)
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
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "a.A", ElementType: "function", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b.B", ElementType: "function", Name: "B", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships(context.Background(), []Relationship{
		{Source: "b.B", Target: "a.A", RelType: "calls"}, // into a.go: survives DeleteByFile as a dangling edge
		{Source: "b.B", Target: "b.B", RelType: "uses"},  // fully live: must survive gc
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteByFile(context.Background(), "a.go"); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteOrphanRelationships(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d edges, want 1", n)
	}
	rels, err := s.RelationshipsAll(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 || rels[0].Source != "b.B" || rels[0].Target != "b.B" {
		t.Fatalf("live survivor missing: %+v", rels)
	}

	// Idempotent, and the watermark must not move when nothing went:
	// a no-op gc may not mark a fresh project possibly_stale.
	seq1, _, _ := s.Watermark(context.Background())
	n2, err := s.DeleteOrphanRelationships(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	seq2, _, _ := s.Watermark(context.Background())
	if n2 != 0 || seq2 != seq1 {
		t.Fatalf("second gc: purged %d, seq %d->%d; want 0 and no bump", n2, seq1, seq2)
	}
}

// #411: DeleteByFile owns elements + edges, so nothing used to reclaim the
// vectors of deleted elements — VectorCoverage reported the orphans forever.
// The purge is per-element-QN (applies to every model collection), keeps
// live rows untouched, and never bumps the watermark on a no-op.
func TestDeleteOrphanVectors(t *testing.T) {
	s := openTestStore(t)
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "a.A", ElementType: "function", Name: "A", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b.B", ElementType: "function", Name: "B", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	stamp := ModelStamp{ModelID: "m1", Revision: "r1", Dimensions: 4, Distance: "cosine", Provider: "test", ChunkerVersion: 1}
	if err := s.WriteStamp(context.Background(), stamp); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertVectors(context.Background(), "m1", []VectorRow{
		{QualifiedName: "a.A", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "b.B", Vec: []float32{0, 1, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmbeddingStates(context.Background(), "m1", map[string]string{"a.A": "h1", "b.B": "h2"}); err != nil {
		t.Fatal(err)
	}
	// The bug setup: delete b.B's element+edges (file removed); its vector and
	// state row survive — that is the permanent residue.
	if err := s.DeleteByFile(context.Background(), "b.go"); err != nil {
		t.Fatal(err)
	}
	if _, orphans, _ := s.VectorCoverage(context.Background(), "m1"); orphans != 1 {
		t.Fatalf("pre-purge orphans = %d, want 1 (the residue #411 describes)", orphans)
	}

	n, err := s.DeleteOrphanVectors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("purged %d vectors, want 1", n)
	}
	if covered, orphans, _ := s.VectorCoverage(context.Background(), "m1"); orphans != 0 || covered != 1 {
		t.Fatalf("post-purge coverage = (%d,%d), want (1,0) — live row kept, orphan gone", covered, orphans)
	}
	if states, _ := s.EmbeddingStateMap(context.Background(), "m1"); len(states) != 1 || states["a.A"] != "h1" {
		t.Fatalf("state rows wrong after purge: %+v", states)
	}

	// No-op purge must not bump the watermark (a gc on a clean store leaves
	// freshness exactly where it was).
	seq1, _, _ := s.Watermark(context.Background())
	if n2, err := s.DeleteOrphanVectors(context.Background()); err != nil || n2 != 0 {
		t.Fatalf("second purge = (%d,%v), want (0,nil)", n2, err)
	}
	if seq2, _, _ := s.Watermark(context.Background()); seq2 != seq1 {
		t.Fatalf("no-op purge bumped the watermark %d -> %d", seq1, seq2)
	}
}

func TestWatermarkBumpsOnWrite(t *testing.T) {
	s := openTestStore(t)
	seq0, _, _ := s.Watermark(context.Background())
	if err := s.UpsertElements(context.Background(), []Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	seq1, _, _ := s.Watermark(context.Background())
	if seq1 <= seq0 {
		t.Fatalf("watermark did not advance: %d -> %d", seq0, seq1)
	}
}

func TestModelStampAndVectors(t *testing.T) {
	s := openTestStore(t)
	st := ModelStamp{ModelID: "test-model", Revision: "40-hex-revision", Dimensions: 4, Distance: "cosine", Provider: "test"}
	if err := s.WriteStamp(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp(context.Background(), "test-model")
	if err != nil || got == nil || got.Revision != "40-hex-revision" {
		t.Fatalf("stamp round-trip failed: %+v %v", got, err)
	}
	if none, _ := s.Stamp(context.Background(), "missing"); none != nil {
		t.Fatalf("missing model stamp = %+v, want nil", none)
	}

	// Vector hits join their element (RS-05: a vector without one is an
	// orphan and never a hit), so the QNs must exist as elements.
	if err := s.UpsertElements(context.Background(), []Element{
		{QualifiedName: "a", ElementType: "function", Name: "a", FilePath: "a.go", Language: "go"},
		{QualifiedName: "b", ElementType: "function", Name: "b", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	vecs := []VectorRow{
		{QualifiedName: "a", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "b", Vec: []float32{0, 1, 0, 0}},
	}
	if err := s.UpsertVectors(context.Background(), "test-model", vecs); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmbeddingStates(context.Background(), "test-model", map[string]string{"a": "ha", "b": "hb"}); err != nil {
		t.Fatal(err)
	}
	state, _ := s.EmbeddingStateMap(context.Background(), "test-model")
	if state["a"] != "ha" {
		t.Fatalf("state = %+v", state)
	}
	hits, err := s.SearchVectors(context.Background(), VectorQuery{ModelID: "test-model", Vec: []float32{1, 0, 0, 0}, K: 1})
	if err != nil || len(hits) != 1 {
		t.Fatalf("search: %v %+v", err, hits)
	}
	if hits[0].Element.QualifiedName != "a" || math.Abs(hits[0].Similarity-1) > 1e-6 {
		t.Fatalf("top hit = %+v, want a with sim 1", hits[0])
	}
	// Full rebuild: ClearVectors wipes vectors + state for the model.
	if err := s.ClearVectors(context.Background(), "test-model"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.VectorCount(context.Background(), "test-model"); n != 0 {
		t.Fatalf("vector count after clear = %d", n)
	}
	if state, _ := s.EmbeddingStateMap(context.Background(), "test-model"); len(state) != 0 {
		t.Fatalf("state after clear = %+v", state)
	}
}

func TestRelationshipUpsertDedups(t *testing.T) {
	s := openTestStore(t)
	rels := []Relationship{{Source: "a", Target: "b", RelType: "calls", Confidence: 0.5}}
	if err := s.UpsertRelationships(context.Background(), rels); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships(context.Background(), rels); err != nil { // duplicate must overwrite, not duplicate
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM relationships`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("relationship count = %d err %v, want 1", n, err)
	}
}

func TestEmbedRuns(t *testing.T) {
	s := openTestStore(t)
	id, err := s.StartEmbedRun(context.Background(), "m", "incremental", 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishEmbedRun(context.Background(), id, "ok", 5, 2, 0, 1, 0); err != nil {
		t.Fatal(err)
	}
	run, err := s.LastEmbedRun(context.Background(), "m")
	if err != nil || run == nil {
		t.Fatalf("last run: %v %+v", err, run)
	}
	if run.Status != "ok" || run.Embedded != 5 || run.Truncations != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func TestInventoryFreshness(t *testing.T) {
	s := openTestStore(t)
	if inv, _ := s.LoadInventory(context.Background()); inv != nil {
		t.Fatalf("no inventory expected, got %+v", inv)
	}
	if err := s.UpsertElements(context.Background(), []Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInventory(context.Background(), Inventory{TotalElements: 1, ElementsByType: map[string]int{"function": 1}}); err != nil {
		t.Fatal(err)
	}
	inv, err := s.LoadInventory(context.Background())
	if err != nil || inv == nil || inv.TotalElements != 1 {
		t.Fatalf("inventory: %v %+v", err, inv)
	}
	seq, _, _ := s.Watermark(context.Background())
	if inv.LastInventorySeq != seq {
		t.Fatalf("inventory seq %d != watermark %d", inv.LastInventorySeq, seq)
	}
	// Any write after inventory => possibly_stale (seq moves past inv seq).
	if err := s.UpsertRelationships(context.Background(), []Relationship{{Source: "x", Target: "x", RelType: "calls"}}); err != nil {
		t.Fatal(err)
	}
	seq2, _, _ := s.Watermark(context.Background())
	if seq2 <= inv.LastInventorySeq {
		t.Fatalf("write after inventory did not advance watermark past %d", inv.LastInventorySeq)
	}
}

func TestReadOnlyMode(t *testing.T) {
	dir := t.TempDir()
	rw, err := openSQLite(context.Background(), filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := rw.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := rw.UpsertElements(context.Background(), []Element{{QualifiedName: "x", ElementType: "function", Name: "x", FilePath: "x.go", Language: "go"}}); err != nil {
		t.Fatal(err)
	}
	rw.Close()

	ro, err := openSQLite(context.Background(), filepath.Join(dir, ".leankg", "leankg.db"), RO)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got, err := ro.FindExact(context.Background(), "x"); err != nil || len(got) != 1 {
		t.Fatalf("ro read: %v %+v", err, got)
	}
	if err := ro.Migrate(context.Background()); err == nil {
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
	if err := s.WriteStamp(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp(context.Background(), want.ModelID)
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
	legacy, err := s.Stamp(context.Background(), "legacy")
	if err != nil {
		t.Fatal(err)
	}
	if legacy == nil || legacy.ChunkerVersion != 0 || legacy.QueryPrefix != "" || legacy.DocumentPrefix != "" {
		t.Fatalf("legacy stamp = %+v, want zero chunker/prefixes", legacy)
	}
	stamps, err := s.Stamps(context.Background())
	if err != nil || len(stamps) != 2 {
		t.Fatalf("Stamps() = %+v, %v; want 2 rows", stamps, err)
	}
}

// TestStampTextCapRoundTrip pins the recorded per-text cap (migration 017):
// it persists through WriteStamp, is read back by Stamp and Stamps, an upsert
// replaces it, and a row written before the column existed reads back as 0
// ("cap not recorded") rather than as a guessed value.
func TestStampTextCapRoundTrip(t *testing.T) {
	s := openTestStore(t)
	want := ModelStamp{
		ModelID: "cap-model", Revision: "r1", Dimensions: 4, Distance: "cosine",
		Provider: "local", TextCap: 8000,
	}
	if err := s.WriteStamp(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp(context.Background(), want.ModelID)
	if err != nil || got == nil || got.TextCap != 8000 {
		t.Fatalf("Stamp() = %+v, %v; want TextCap 8000", got, err)
	}

	want.TextCap = 4000
	if err := s.WriteStamp(context.Background(), want); err != nil {
		t.Fatal(err)
	}
	if got, err = s.Stamp(context.Background(), want.ModelID); err != nil || got == nil || got.TextCap != 4000 {
		t.Fatalf("upsert must replace TextCap: %+v, %v; want 4000", got, err)
	}
	all, err := s.Stamps(context.Background())
	if err != nil || len(all) != 1 || all[0].TextCap != 4000 {
		t.Fatalf("Stamps() = %+v, %v; want one row with TextCap 4000", all, err)
	}

	if _, err := s.db.Exec(`INSERT INTO emb_stamp (model_id, revision, dimensions, distance, provider)
		VALUES ('cap-legacy', 'old', 8, 'cosine', 'local')`); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Stamp(context.Background(), "cap-legacy")
	if err != nil || legacy == nil || legacy.TextCap != 0 {
		t.Fatalf("legacy stamp = %+v, %v; want TextCap 0 (not recorded)", legacy, err)
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
	if err := s.UpsertElements(context.Background(), []Element{
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
	before, _, err := s.Watermark(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceFileVectors(context.Background(), "m", rows, states); err != nil {
		t.Fatal(err)
	}
	after, _, err := s.Watermark(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after != before+1 {
		t.Fatalf("watermark %d -> %d; one write unit must bump once", before, after)
	}
	if n, err := s.VectorCount(context.Background(), "m"); err != nil || n != 2 {
		t.Fatalf("VectorCount = %d, %v; want 2", n, err)
	}
	if st, err := s.EmbeddingStateMap(context.Background(), "m"); err != nil || st["a.go::f1"] != "h1" || st["a.go::f2"] != "h2" {
		t.Fatalf("embedding state = %+v, %v", st, err)
	}

	// Re-run with a changed vector for one element: replaced in place, no
	// duplicate row, state re-stamped.
	if err := s.ReplaceFileVectors(context.Background(), "m",
		[]VectorRow{{QualifiedName: "a.go::f1", Vec: []float32{0, 0, 1, 0}}},
		map[string]string{"a.go::f1": "h1b"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.VectorCount(context.Background(), "m"); n != 2 {
		t.Fatalf("vector count after replace = %d, want 2 (no duplicate)", n)
	}
	hits, err := s.SearchVectors(context.Background(), VectorQuery{ModelID: "m", Vec: []float32{0, 0, 1, 0}, K: 1})
	if err != nil || len(hits) != 1 || hits[0].Element.QualifiedName != "a.go::f1" {
		t.Fatalf("replaced vector not searchable: %+v, %v", hits, err)
	}
	if st, _ := s.EmbeddingStateMap(context.Background(), "m"); st["a.go::f1"] != "h1b" {
		t.Fatalf("state after replace = %+v", st)
	}

	// An empty unit is a caller bug, not a no-op: it would carry no rows for
	// the file it claims to replace.
	if err := s.ReplaceFileVectors(context.Background(), "m", nil, nil); err == nil {
		t.Fatal("empty vector set must be refused")
	}
}

// TestIdentifierTermsReachFTS pins RS-13: a camelCase name is findable by its
// words on the keyword rung, for new rows and for rows written before the
// change (the Migrate backfill).
func TestIdentifierTermsReachFTS(t *testing.T) {
	for name, want := range map[string]string{
		"RotateStagingCredentials": "rotate staging credentials",
		"parseHTTPResponse":        "parse http response",
		"load_config_file":         "load config file",
		"Widget":                   "",
	} {
		if got := SplitIdentifier(name); got != want {
			t.Errorf("SplitIdentifier(%q) = %q, want %q", name, got, want)
		}
	}
	s := openTestStore(t)
	if err := s.UpsertElements(context.Background(), []Element{{QualifiedName: "a.go::RotateStagingCredentials", ElementType: "function",
		Name: "RotateStagingCredentials", FilePath: "a.go", Language: "go", Content: "func RotateStagingCredentials() {}"}}); err != nil {
		t.Fatal(err)
	}
	hits, _, err := s.FindFuzzy(context.Background(), "rotate staging credentials", 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("split terms not searchable: %v %v", err, hits)
	}
	// Simulate a pre-RS-13 store: plain FTS row, backfill marker cleared.
	if _, err := s.db.Exec(`DELETE FROM elements_fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO elements_fts (rowid, name, qualified_name, content) SELECT id, name, qualified_name, content FROM code_elements`); err != nil {
		t.Fatal(err)
	}
	if err := s.KVSet(context.Background(), "fts", "terms_version", "0"); err != nil {
		t.Fatal(err)
	}
	if hits, _, _ := s.FindFuzzy(context.Background(), "staging", 5); len(hits) != 0 {
		t.Fatal("precondition: old-style row should not match the split word")
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits, _, _ := s.FindFuzzy(context.Background(), "staging", 5); len(hits) == 0 {
		t.Fatal("backfill did not add split terms to existing rows")
	}
}
