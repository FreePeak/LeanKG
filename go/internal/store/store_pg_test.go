// Tests for the PostgreSQL+pgvector backend. Skipped unless LEANKG_TEST_PG_URL
// is set (e.g. postgres://postgres:postgres@localhost:5433/leankg?sslmode=disable
// against a pgvector image). Each test uses its own project dir, and therefore
// its own schema, dropped on cleanup.
package store

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Compile-time proof: PGStore implements the full Backend contract.
var _ Backend = (*PGStore)(nil)

const pgTestURLEnv = "LEANKG_TEST_PG_URL"

// openPGTest opens a migrated RW store under a unique temp project dir.
func openPGTest(t *testing.T) *PGStore {
	t.Helper()
	return openPGAt(t, t.TempDir())
}

func openPGAt(t *testing.T, dir string) *PGStore {
	t.Helper()
	url := os.Getenv(pgTestURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL backend tests", pgTestURLEnv)
	}
	b, err := OpenPG(context.Background(), url, dir, RW)
	if err != nil {
		t.Fatalf("OpenPG: %v", err)
	}
	s := b.(*PGStore)
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+s.schema+` CASCADE`)
		_ = s.Close()
	})
	return s
}

func TestPGSchemaIsolation(t *testing.T) {
	a := openPGAt(t, t.TempDir())
	b := openPGAt(t, t.TempDir())

	if err := a.UpsertElements([]Element{{
		QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha",
		FilePath: "a.go", Language: "go",
	}}); err != nil {
		t.Fatal(err)
	}

	if n, err := b.ElementCount(); err != nil || n != 0 {
		t.Fatalf("b.ElementCount() = %d, %v; want 0", n, err)
	}
	if hits, err := b.FindExact("Alpha"); err != nil || len(hits) != 0 {
		t.Fatalf("b.FindExact(Alpha) = %d hits, %v; want 0", len(hits), err)
	}
}

func TestPGElementOps(t *testing.T) {
	s := openPGTest(t)

	els := []Element{
		{QualifiedName: "pkg.MyFunc", ElementType: "function", Name: "MyFunc", FilePath: "a.go",
			LineStart: 1, LineEnd: 10, Language: "go", Content: "func MyFunc()", Metadata: map[string]any{"kind": "fn"}},
		{QualifiedName: "pkg.Helper", ElementType: "function", Name: "Helper", FilePath: "a.go",
			LineStart: 12, LineEnd: 20, Language: "go"},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertRelationships([]Relationship{
		{Source: "pkg.MyFunc", Target: "pkg.Helper", RelType: "calls", Confidence: 0.9, Metadata: map[string]any{"line": 5}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertFiles([]FileRecord{{Path: "a.go", Size: 100, MtimeNS: 5, ContentHash: "h1"}}); err != nil {
		t.Fatal(err)
	}

	// Upsert overwrite: same qualified name is replaced, not duplicated.
	if err := s.UpsertElements([]Element{{
		QualifiedName: "pkg.MyFunc", ElementType: "function", Name: "MyFunc", FilePath: "a.go",
		LineStart: 1, LineEnd: 10, Language: "go", Content: "v2",
	}}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ElementCount(); err != nil || n != 2 {
		t.Fatalf("ElementCount() = %d, %v; want 2", n, err)
	}

	// FindExact: case-insensitive on name and qualified name.
	hits, err := s.FindExact("myfunc")
	if err != nil || len(hits) != 1 || hits[0].QualifiedName != "pkg.MyFunc" || hits[0].Content != "v2" {
		t.Fatalf("FindExact(myfunc) = %+v, %v", hits, err)
	}
	hits, err = s.FindExact("PKG.HELPER")
	if err != nil || len(hits) != 1 || hits[0].Name != "Helper" {
		t.Fatalf("FindExact(PKG.HELPER) = %+v, %v", hits, err)
	}

	// FindFuzzy: substring over name/qualified_name, shortest name first.
	fz, err := s.FindFuzzy("help", 10)
	if err != nil || len(fz) != 1 || fz[0].Element.QualifiedName != "pkg.Helper" {
		t.Fatalf("FindFuzzy(help) = %+v, %v", fz, err)
	}
	if fz[0].Score >= 0 {
		t.Fatalf("FindFuzzy Score = %v; want negative (divergent -length ranking)", fz[0].Score)
	}

	// Relationship reads.
	rels, err := s.Outgoing("pkg.MyFunc")
	if err != nil || len(rels) != 1 || rels[0].Target != "pkg.Helper" || rels[0].Confidence != 0.9 {
		t.Fatalf("Outgoing = %+v, %v", rels, err)
	}
	rels, err = s.Incoming("pkg.Helper")
	if err != nil || len(rels) != 1 {
		t.Fatalf("Incoming = %+v, %v", rels, err)
	}
	rels, err = s.RelationshipsAll(10)
	if err != nil || len(rels) != 1 {
		t.Fatalf("RelationshipsAll = %+v, %v", rels, err)
	}
	if n, err := s.FileCount(); err != nil || n != 1 {
		t.Fatalf("FileCount() = %d, %v; want 1", n, err)
	}
	byType, err := s.ElementsByType()
	if err != nil || byType["function"] != 2 {
		t.Fatalf("ElementsByType = %+v, %v", byType, err)
	}

	// DeleteByFile removes elements + relationships; the file record stays.
	if err := s.DeleteByFile("a.go"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.ElementCount(); n != 0 {
		t.Fatalf("ElementCount after DeleteByFile = %d; want 0", n)
	}
	if n, _ := s.RelationshipCount(); n != 0 {
		t.Fatalf("RelationshipCount after DeleteByFile = %d; want 0", n)
	}
	if n, _ := s.FileCount(); n != 1 {
		t.Fatalf("FileCount after DeleteByFile = %d; want 1", n)
	}

	// DeleteFileRecord removes the code_files row.
	if err := s.DeleteFileRecord("a.go"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.FileCount(); n != 0 {
		t.Fatalf("FileCount after DeleteFileRecord = %d; want 0", n)
	}
}

func TestPGWatermarkBumps(t *testing.T) {
	s := openPGTest(t)

	seq0, at0, err := s.Watermark()
	if err != nil {
		t.Fatal(err)
	}
	if seq0 != 0 || at0 != 0 {
		t.Fatalf("fresh watermark = (%d, %d); want (0, 0)", seq0, at0)
	}
	if err := s.UpsertElements([]Element{{
		QualifiedName: "pkg.X", ElementType: "function", Name: "X", FilePath: "x.go", Language: "go",
	}}); err != nil {
		t.Fatal(err)
	}
	seq1, _, err := s.Watermark()
	if err != nil || seq1 != seq0+1 {
		t.Fatalf("seq after UpsertElements = %d, %v; want %d", seq1, err, seq0+1)
	}
	if err := s.KVSet("ns", "k", "v"); err != nil {
		t.Fatal(err)
	}
	seq2, _, err := s.Watermark()
	if err != nil || seq2 != seq1+1 {
		t.Fatalf("seq after KVSet = %d, %v; want %d", seq2, err, seq1+1)
	}
}

func TestPGStampsAndVectors(t *testing.T) {
	s := openPGTest(t)

	if err := s.UpsertElements([]Element{
		{QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha", FilePath: "a.go", Language: "go"},
		{QualifiedName: "pkg.Beta", ElementType: "function", Name: "Beta", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	st := ModelStamp{ModelID: "test-model-7B", Revision: "abc123", Dimensions: 4, Distance: "cosine", Provider: "unit"}
	if err := s.WriteStamp(st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Stamp("test-model-7B")
	if err != nil || got == nil || *got != st {
		t.Fatalf("Stamp() = %+v, %v; want %+v", got, err, st)
	}
	stamps, err := s.Stamps()
	if err != nil || len(stamps) != 1 || stamps[0] != st {
		t.Fatalf("Stamps() = %+v, %v", stamps, err)
	}

	// Per-model tables physically exist.
	san := sanitizeModelID(st.ModelID)
	var reg *string
	if err := s.pool.QueryRow(pgCtx,
		`SELECT to_regclass($1)::text`, s.schema+".embedding_vectors_"+san).Scan(&reg); err != nil {
		t.Fatal(err)
	}
	if reg == nil {
		t.Fatalf("per-model vector table was not created (to_regclass nil for %s)", san)
	}

	// Alpha's vector ≡ query → similarity 1; Beta orthogonal; one orphan.
	if err := s.UpsertVectors(st.ModelID, []VectorRow{
		{QualifiedName: "pkg.Alpha", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "pkg.Beta", Vec: []float32{0, 1, 0, 0}},
		{QualifiedName: "doc/blob", Vec: []float32{0.9, 0.1, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.VectorCount(st.ModelID); err != nil || n != 3 {
		t.Fatalf("VectorCount = %d, %v; want 3", n, err)
	}

	hits, err := s.SearchVectors(st.ModelID, []float32{1, 0, 0, 0}, 1)
	if err != nil || len(hits) != 1 {
		t.Fatalf("SearchVectors = %+v, %v", hits, err)
	}
	if hits[0].Element.QualifiedName != "pkg.Alpha" || hits[0].Similarity < 0.999 {
		t.Fatalf("top hit = %+v; want pkg.Alpha with sim≈1", hits[0])
	}
	if hits[0].Element.ElementType != "function" || hits[0].Element.Name != "Alpha" {
		t.Fatalf("hydration failed: %+v", hits[0].Element)
	}

	covered, orphans, err := s.VectorCoverage(st.ModelID)
	if err != nil || covered != 2 || orphans != 1 {
		t.Fatalf("VectorCoverage = (%d, %d), %v; want (2, 1)", covered, orphans, err)
	}

	if err := s.SetEmbeddingStates(st.ModelID, map[string]string{"pkg.Alpha": "h1"}); err != nil {
		t.Fatal(err)
	}
	states, err := s.EmbeddingStateMap(st.ModelID)
	if err != nil || states["pkg.Alpha"] != "h1" {
		t.Fatalf("EmbeddingStateMap = %+v, %v", states, err)
	}

	if err := s.ClearVectors(st.ModelID); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.VectorCount(st.ModelID); n != 0 {
		t.Fatalf("VectorCount after ClearVectors = %d; want 0", n)
	}
	if states, _ := s.EmbeddingStateMap(st.ModelID); len(states) != 0 {
		t.Fatalf("EmbeddingStateMap after ClearVectors = %+v; want empty", states)
	}
	covered, orphans, _ = s.VectorCoverage(st.ModelID)
	if covered != 0 || orphans != 0 {
		t.Fatalf("VectorCoverage after ClearVectors = (%d, %d); want (0, 0)", covered, orphans)
	}
}

func TestPGKVAndInventory(t *testing.T) {
	s := openPGTest(t)

	if err := s.KVSet("ontology", "cat", "a"); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.KVGet("ontology", "cat"); err != nil || !ok || v != "a" {
		t.Fatalf("KVGet = (%q, %v, %v)", v, ok, err)
	}
	// Namespaces are independent.
	if _, ok, err := s.KVGet("other", "cat"); err != nil || ok {
		t.Fatalf("KVGet(other, cat) = (_, %v, %v); want not-found", ok, err)
	}
	if err := s.KVSet("ontology", "cat", "b"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := s.KVGet("ontology", "cat"); v != "b" {
		t.Fatalf("KVGet after overwrite = %q; want b", v)
	}
	if _, ok, _ := s.KVGet("ontology", "missing"); ok {
		t.Fatal("KVGet(missing) reported found")
	}

	// Inventory round-trip: pins the watermark seq at compute time.
	if _, err := s.LoadInventory(); err != nil {
		t.Fatal(err)
	}
	seq, _, _ := s.Watermark()
	if err := s.UpsertElements([]Element{{
		QualifiedName: "pkg.X", ElementType: "function", Name: "X", FilePath: "x.go", Language: "go",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveInventory(Inventory{TotalElements: 1, TotalFiles: 0, TotalRelationships: 0,
		ElementsByType: map[string]int{"function": 1}}); err != nil {
		t.Fatal(err)
	}
	inv, err := s.LoadInventory()
	if err != nil || inv == nil {
		t.Fatalf("LoadInventory = %+v, %v", inv, err)
	}
	if inv.TotalElements != 1 || inv.ElementsByType["function"] != 1 {
		t.Fatalf("inventory payload mismatch: %+v", inv)
	}
	wantSeq, _, _ := s.Watermark()
	if inv.LastInventorySeq != wantSeq {
		t.Fatalf("LastInventorySeq = %d; want current watermark %d", inv.LastInventorySeq, wantSeq)
	}
	if inv.ComputedAt == 0 || inv.LastInventorySeq == seq {
		t.Fatalf("inventory not taken after the write: %+v (pre-write seq %d)", inv, seq)
	}
}

func TestPGAudit(t *testing.T) {
	s := openPGTest(t)

	prev := ""
	for i := 1; i <= 3; i++ {
		e := &AuditEntry{Actor: "test", Action: "index", Target: "proj", Details: map[string]any{"files": i}}
		if err := s.AppendAudit(e); err != nil {
			t.Fatal(err)
		}
		if e.Seq != int64(i) || e.Hash == "" || e.PrevHash != prev {
			t.Fatalf("entry %d: seq=%d hash=%q prev=%q wantPrev=%q", i, e.Seq, e.Hash, e.PrevHash, prev)
		}
		prev = e.Hash
	}

	tail, err := s.AuditTail(2)
	if err != nil || len(tail) != 2 {
		t.Fatalf("AuditTail(2) = %+v, %v", tail, err)
	}
	if tail[0].Seq != 2 || tail[1].Seq != 3 {
		t.Fatalf("AuditTail order = [%d, %d]; want ascending [2, 3]", tail[0].Seq, tail[1].Seq)
	}
	if tail[1].Details["files"] != float64(3) {
		t.Fatalf("details not round-tripped: %+v", tail[1].Details)
	}

	ok, brokenAt, err := s.VerifyAuditChain()
	if err != nil || !ok || brokenAt != 0 {
		t.Fatalf("VerifyAuditChain = (%v, %d, %v); want (true, 0, nil)", ok, brokenAt, err)
	}

	// Tamper with the middle record → chain verification breaks exactly there.
	if _, err := s.pool.Exec(pgCtx, `UPDATE audit_ledger SET target = 'evil' WHERE seq = 2`); err != nil {
		t.Fatal(err)
	}
	ok, brokenAt, err = s.VerifyAuditChain()
	if err != nil || ok || brokenAt != 2 {
		t.Fatalf("VerifyAuditChain after tamper = (%v, %d, %v); want (false, 2, nil)", ok, brokenAt, err)
	}
}

func TestPGReadOnly(t *testing.T) {
	url := os.Getenv(pgTestURLEnv)
	if url == "" {
		t.Skipf("%s not set; skipping PostgreSQL backend tests", pgTestURLEnv)
	}
	dir := t.TempDir()
	rw, err := OpenPG(context.Background(), url, dir, RW)
	if err != nil {
		t.Fatal(err)
	}
	rwS := rw.(*PGStore)
	if err := rw.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := rw.KVSet("t", "ro", "1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = rwS.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+rwS.schema+` CASCADE`)
		_ = rw.Close()
	})

	roB, err := OpenPG(context.Background(), url, dir, RO)
	if err != nil {
		t.Fatalf("RO open of existing store: %v", err)
	}
	ro := roB.(*PGStore)
	defer ro.Close()

	// Migrate in RO errors (sqlite parity).
	if err := ro.Migrate(); err == nil {
		t.Fatal("Migrate in RO mode succeeded; want error")
	}

	// Writes are rejected by the server (default_transaction_read_only).
	if err := ro.KVSet("t", "x", "y"); err == nil {
		t.Fatal("KVSet in RO succeeded; want error")
	}
	if err := ro.UpsertElements([]Element{{
		QualifiedName: "pkg.R", ElementType: "function", Name: "R", FilePath: "r.go", Language: "go",
	}}); err == nil {
		t.Fatal("UpsertElements in RO succeeded; want error")
	}
	if err := ro.AppendAudit(&AuditEntry{Actor: "ro", Action: "nope"}); err == nil {
		t.Fatal("AppendAudit in RO succeeded; want error")
	}

	// Reads work; bump is a silent no-op (sqlite parity).
	if err := ro.BumpWatermark(); err != nil {
		t.Fatalf("BumpWatermark in RO: %v", err)
	}
	seq, _, err := ro.Watermark()
	if err != nil {
		t.Fatal(err)
	}
	rwSeq, _, err := rw.Watermark()
	if err != nil || rwSeq != seq {
		t.Fatalf("RO watermark %d vs RW %d, %v", seq, rwSeq, err)
	}

	// RO open of a missing schema errors (sqlite missing-store parity).
	if _, err := OpenPG(context.Background(), url, t.TempDir(), RO); err == nil {
		t.Fatal("RO open of missing schema succeeded; want error")
	}
}

func TestPGMigrateIdempotent(t *testing.T) {
	s := openPGTest(t)

	if err := s.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var n int
	if err := s.pool.QueryRow(pgCtx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(pgMigrations) {
		t.Fatalf("schema_migrations rows = %d; want %d", n, len(pgMigrations))
	}
}

func TestPGEmbedRuns(t *testing.T) {
	s := openPGTest(t)

	if r, err := s.LastEmbedRunAny(); err != nil || r != nil {
		t.Fatalf("LastEmbedRunAny on empty store = %+v, %v; want nil", r, err)
	}
	if r, err := s.LastEmbedRun("m1"); err != nil || r != nil {
		t.Fatalf("LastEmbedRun on empty store = %+v, %v; want nil", r, err)
	}

	id, err := s.StartEmbedRun("m1", "full", 5)
	if err != nil || id == 0 {
		t.Fatalf("StartEmbedRun = %d, %v", id, err)
	}
	if err := s.FinishEmbedRun(id, "done", 4, 1, 0, 2, 3); err != nil {
		t.Fatal(err)
	}

	r, err := s.LastEmbedRunAny()
	if err != nil || r == nil {
		t.Fatalf("LastEmbedRunAny = %+v, %v", r, err)
	}
	if r.ID != id || r.ModelID != "m1" || r.Mode != "full" || r.Status != "done" ||
		r.Dirty != 5 || r.Embedded != 4 || r.Skipped != 1 || r.Truncations != 2 || r.Orphans != 3 {
		t.Fatalf("run record mismatch: %+v", r)
	}
	if r.StartedAt == "" || r.FinishedAt == nil || *r.FinishedAt == "" {
		t.Fatalf("timestamps not recorded: started=%q finished=%v", r.StartedAt, r.FinishedAt)
	}
	if r2, err := s.LastEmbedRun("m1"); err != nil || r2 == nil || r2.ID != id {
		t.Fatalf("LastEmbedRun = %+v, %v", r2, err)
	}
	if r3, err := s.LastEmbedRun("other"); err != nil || r3 != nil {
		t.Fatalf("LastEmbedRun(other) = %+v, %v; want nil", r3, err)
	}
}

// Guards against accidental regressions in error semantics: pgx.ErrNoRows
// must never leak out of the nil-able readers.
func TestPGNilReadersUseSentinel(t *testing.T) {
	s := openPGTest(t)

	if _, _, err := s.Watermark(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stamp("none"); err != nil {
		t.Fatal(err)
	}
	// Vector ops on a never-stamped model produce a missing-table error (the
	// sqlite twin would return empty instead) — this is the documented PG
	// divergence: per-model tables only exist after WriteStamp.
	_, err := s.VectorCount("never-stamped")
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("VectorCount on missing model tables = %v; want a defined-table error", err)
	}
}
