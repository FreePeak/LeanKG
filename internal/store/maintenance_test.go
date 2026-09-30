package store

import (
	"path/filepath"
	"testing"
)

// newVacuumFixture opens a migrated store with enough rows to delete most of,
// so the freelist is a real fraction of the file rather than a rounding error.
func newVacuumFixture(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leankg.db")
	st, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 400 rows, then delete 300 of them: ~75% of the pages end up free.
	for i := 0; i < 400; i++ {
		p := filepath.Join("src", "f"+string(rune('a'+i%26))+itoa(i)+".go")
		if err := st.UpsertElements([]Element{{
			QualifiedName: "pkg.Fn" + itoa(i),
			ElementType:   "function",
			Name:          "Fn" + itoa(i),
			FilePath:      p,
			Language:      "go",
			Content:       "func Fn" + itoa(i) + "() {}\n" + itoa(i) + itoa(i) + itoa(i),
		}}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	for i := 0; i < 300; i++ {
		p := filepath.Join("src", "f"+string(rune('a'+i%26))+itoa(i)+".go")
		if err := st.DeleteByFile(p); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	return st
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestSpaceReportsFreelist(t *testing.T) {
	st := newVacuumFixture(t)
	rep, err := st.Space()
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	if rep.Engine != EngineSQLite {
		t.Errorf("engine = %q, want %q", rep.Engine, EngineSQLite)
	}
	if rep.SizeBytes <= 0 || rep.PageSize <= 0 {
		t.Fatalf("unusable report: %+v", rep)
	}
	if rep.FreeBytes == 0 {
		t.Fatalf("expected a freelist after 300 deletes, got %+v", rep)
	}
	if rep.LiveBytes != rep.SizeBytes-rep.FreeBytes {
		t.Errorf("live = %d, want size-free = %d", rep.LiveBytes, rep.SizeBytes-rep.FreeBytes)
	}
	if f := rep.BloatFraction(); f <= 0 || f >= 1 {
		t.Errorf("bloat fraction = %v, want 0<f<1", f)
	}
}

func TestEnsureIncrementalVacuumEnablesAutoVacuum(t *testing.T) {
	st := newVacuumFixture(t)

	var mode int64
	if err := st.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("read auto_vacuum: %v", err)
	}
	if mode != 0 {
		t.Fatalf("fixture precondition: auto_vacuum = %d, want 0 (NONE)", mode)
	}

	if err := st.EnsureIncrementalVacuum(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := st.db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		t.Fatalf("re-read auto_vacuum: %v", err)
	}
	if mode != 2 {
		t.Fatalf("auto_vacuum = %d, want 2 (INCREMENTAL)", mode)
	}
	// Idempotent: a second call must not VACUUM again.
	if err := st.EnsureIncrementalVacuum(); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
}

func TestIncrementalVacuumReturnsPagesToDisk(t *testing.T) {
	st := newVacuumFixture(t)
	// Enable the mode FIRST, then delete. Otherwise the one-time header
	// upgrade's own VACUUM reclaims the entire freelist and there is nothing
	// left for the incremental pass to demonstrate.
	if err := st.ensureIncrementalVacuum(); err != nil {
		t.Fatalf("enable incremental vacuum: %v", err)
	}
	// The fixture already deleted 300 of its 400 files; delete the surviving
	// 100 too so the freelist that follows is made under INCREMENTAL mode.
	for i := 300; i < 400; i++ {
		p := filepath.Join("src", "f"+string(rune('a'+i%26))+itoa(i)+".go")
		if err := st.DeleteByFile(p); err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	before, err := st.Space()
	if err != nil {
		t.Fatalf("space before: %v", err)
	}
	if before.FreeBytes == 0 {
		t.Fatalf("fixture precondition: freelist is empty: %+v", before)
	}
	// A cap below the freelist size: the pass must be bounded, so the file
	// shrinks by at most that many pages.
	const cap = 8
	if err := st.IncrementalVacuum(cap); err != nil {
		t.Fatalf("incremental vacuum: %v", err)
	}
	after, err := st.Space()
	if err != nil {
		t.Fatalf("space after: %v", err)
	}
	if after.SizeBytes >= before.SizeBytes {
		t.Fatalf("no reclaim: %d -> %d", before.SizeBytes, after.SizeBytes)
	}
	if reclaimed := before.SizeBytes - after.SizeBytes; reclaimed > cap*after.PageSize {
		t.Fatalf("reclaimed %d bytes, want <= cap(%d)*page(%d) — the pass is not bounded",
			reclaimed, cap, after.PageSize)
	}
	// An unbounded pass drains the rest.
	if err := st.IncrementalVacuum(0); err != nil {
		t.Fatalf("unbounded incremental vacuum: %v", err)
	}
	if err := st.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	final, err := st.Space()
	if err != nil {
		t.Fatalf("space final: %v", err)
	}
	if final.FreeBytes != 0 {
		t.Fatalf("freelist after an unbounded pass: %d, want 0", final.FreeBytes)
	}
}

func TestVacuumShrinksFileToLiveSize(t *testing.T) {
	st := newVacuumFixture(t)
	before, err := st.Space()
	if err != nil {
		t.Fatalf("space before: %v", err)
	}
	if err := st.Vacuum(); err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	after, err := st.Space()
	if err != nil {
		t.Fatalf("space after: %v", err)
	}
	if after.FreeBytes != 0 {
		t.Errorf("freelist after full vacuum = %d, want 0", after.FreeBytes)
	}
	if after.SizeBytes >= before.SizeBytes {
		t.Errorf("full vacuum did not shrink: %d -> %d", before.SizeBytes, after.SizeBytes)
	}
	// The data survived: 100 elements remain.
	if n, err := st.ElementCount(); err != nil || n != 100 {
		t.Errorf("element count = %d (err %v), want 100", n, err)
	}
}

func TestCheckpointTruncatesWAL(t *testing.T) {
	st := newVacuumFixture(t)
	if err := st.BumpWatermark(); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if err := st.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	rep, err := st.Space()
	if err != nil {
		t.Fatalf("space: %v", err)
	}
	if rep.WALBytes != 0 {
		t.Fatalf("wal sidecar = %d bytes after TRUNCATE checkpoint, want 0", rep.WALBytes)
	}
}

func TestVacuumRefusesReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ro.db")
	rw, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open rw: %v", err)
	}
	if err := rw.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close rw: %v", err)
	}
	ro, err := Open(path, RO)
	if err != nil {
		t.Fatalf("open ro: %v", err)
	}
	defer ro.Close()
	if err := ro.Vacuum(); err == nil {
		t.Fatal("read-only store accepted a full vacuum")
	}
	// Space is a no-write probe and must work there, and the header upgrade
	// is a no-op rather than an error.
	if _, err := ro.Space(); err != nil {
		t.Errorf("read-only space probe: %v", err)
	}
	if err := ro.EnsureIncrementalVacuum(); err != nil {
		t.Errorf("read-only ensure: %v", err)
	}
}
