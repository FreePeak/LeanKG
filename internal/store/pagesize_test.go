package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// createLegacyStore creates a store the way the engine used to: page_size
// 4096. It is the starting point of the upgrade test, and the proof that an
// existing store is never rewritten behind the operator's back.
func createLegacyStore(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE legacy (a BLOB)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE legacy`); err != nil {
		t.Fatalf("drop: %v", err)
	}
}

// vectorHash hashes every vector blob in the store, in order. It is the
// losslessness witness: a rewrite that changed a vector byte would change
// this hash.
func vectorHash(t *testing.T, path string) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT vec FROM embedding_vectors ORDER BY qualified_name`)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan: %v", err)
		}
		h.Write(b)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestNewStoreUsesTheLargerPageSize: with page_size 4096 the max local
// payload is usable−35 = 4,061 bytes and one vector row is 2,134 bytes, so
// two rows never fit in one page — every vector owned a whole 4 KiB page with
// 1,948 bytes (48 %) of it empty. Measured on the live leankg store: 22,256,720
// bytes of unused page space in the vector table, 16 % of the whole store.
func TestNewStoreUsesTheLargerPageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leankg.db")
	st, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	size, err := PageSize(path)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	if size != DefaultPageSize {
		t.Fatalf("a new store has page_size %d, want %d", size, DefaultPageSize)
	}
}

// TestExistingStoreKeepsItsPageSize: the page size of an existing store is
// the operator's business. SQLite adopts a new one only while a VACUUM
// rewrites the file, so a store must never be rewritten on the side.
func TestExistingStoreKeepsItsPageSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leankg.db")
	createLegacyStore(t, path)
	st, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A write, and the VACUUM the auto-vacuum header upgrade performs.
	if err := st.UpsertElements([]Element{{
		QualifiedName: "pkg.Fn", ElementType: "function", Name: "Fn",
		FilePath: "a.go", Language: "go", Content: "func Fn() {}",
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := st.EnsureIncrementalVacuum(); err != nil {
		t.Fatalf("ensure incremental vacuum: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	size, err := PageSize(path)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	if size != 4096 {
		t.Fatalf("the existing store was rewritten to page_size %d without being asked", size)
	}
}

// TestUpgradePageSizePreservesEveryVector is the losslessness proof of the
// opt-in rewrite: every vector survives byte for byte, the count is
// unchanged, and the file gets smaller.
func TestUpgradePageSizePreservesEveryVector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leankg.db")
	createLegacyStore(t, path)
	st, err := Open(path, RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := st.WriteStamp(ModelStamp{
		ModelID: "m", Revision: "rev", Dimensions: 512, Distance: "cosine", Provider: "local",
	}); err != nil {
		t.Fatalf("write stamp: %v", err)
	}
	const n = 300
	vec := make([]float32, 512)
	for i := range n {
		for j := range vec {
			vec[j] = float32(i*j%97) / 7
		}
		if err := st.UpsertElements([]Element{{
			QualifiedName: fmt.Sprintf("pkg.Fn%d", i), ElementType: "function",
			Name: fmt.Sprintf("Fn%d", i), FilePath: fmt.Sprintf("src/f%d.go", i),
			Language: "go", Content: "func Fn() {}",
		}}); err != nil {
			t.Fatalf("upsert element: %v", err)
		}
		if err := st.UpsertVectors("m", []VectorRow{{
			QualifiedName: fmt.Sprintf("pkg.Fn%d", i), Vec: vec,
		}}); err != nil {
			t.Fatalf("upsert vector: %v", err)
		}
	}
	before := vectorHash(t, path)
	sizeBefore, err := PageSize(path)
	if err != nil {
		t.Fatalf("page size: %v", err)
	}
	if sizeBefore != 4096 {
		t.Fatalf("the fixture store is at page_size %d, want 4096", sizeBefore)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res, err := UpgradePageSize(path, DefaultPageSize)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if res.Before != 4096 {
		t.Fatalf("the store started at page_size %d, want 4096", res.Before)
	}
	if res.After != DefaultPageSize {
		t.Fatalf("the store is now at page_size %d, want %d", res.After, DefaultPageSize)
	}
	if res.Vectors != n {
		t.Errorf("the rewrite holds %d vectors, want %d", res.Vectors, n)
	}
	after := vectorHash(t, path)
	if before != after {
		t.Fatal("the rewrite changed the vectors")
	}
	if res.AfterBytes >= res.BeforeBytes {
		t.Errorf("the rewrite did not shrink the store: %d -> %d bytes", res.BeforeBytes, res.AfterBytes)
	}
}

// TestUpgradePageSizeRefusesAnImpossibleSize: SQLite page sizes are powers
// of two in a fixed range, and a silent correction would be a rewrite nobody
// asked for.
func TestUpgradePageSizeRefusesAnImpossibleSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leankg.db")
	createLegacyStore(t, path)
	for _, size := range []int{0, 100, 4095, 1 << 20} {
		if _, err := UpgradePageSize(path, size); err == nil {
			t.Errorf("page size %d was accepted", size)
		}
	}
}
