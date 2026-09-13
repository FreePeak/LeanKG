// Portfolio registry storage tests (issue #376, migration 013): the typed table
// access on both backends. The PG mirror rides the same LEANKG_TEST_PG_URL gate
// as the rest of the store package (one schema per test).
package store

import (
	"path/filepath"
	"testing"
)

func TestProjectRegistrySQLite(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RW)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	// A fresh registry is empty, not nil-shaped.
	rows, err := s.ProjectList()
	if err != nil || len(rows) != 0 {
		t.Fatalf("ProjectList() = %v, %v; want empty", rows, err)
	}
	if _, ok, err := s.ProjectGet("/nope"); err != nil || ok {
		t.Fatalf("ProjectGet(missing) = ok=%v err=%v; want false, nil", ok, err)
	}
	if err := s.ProjectUpsert(ProjectRecord{Dir: "", Name: "x", RegisteredAt: "t"}); err == nil {
		t.Fatal("upsert must reject an empty dir")
	}

	// Registration: dir-keyed, counts stamped, last_indexed carried.
	at := "2026-09-13T10:00:00.000Z"
	if err := s.ProjectUpsert(ProjectRecord{
		Dir: "/fleet/api", Name: "api", RegisteredAt: "2026-09-01T00:00:00.000Z",
		LastIndexed: &at, ElementCount: 42, FileCount: 7,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, ok, err := s.ProjectGet("/fleet/api")
	if err != nil || !ok {
		t.Fatalf("ProjectGet after upsert: ok=%v err=%v", ok, err)
	}
	if got.Name != "api" || got.ElementCount != 42 || got.FileCount != 7 ||
		got.LastIndexed == nil || *got.LastIndexed != at {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Re-register with NO new stamp: the known last_indexed survives (the
	// COALESCE contract — a re-register never erases an index fact) and
	// registered_at keeps the FIRST registration.
	if err := s.ProjectUpsert(ProjectRecord{
		Dir: "/fleet/api", Name: "api2", RegisteredAt: "2026-09-13T11:00:00.000Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.ProjectGet("/fleet/api")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "api2" || got.LastIndexed == nil || *got.LastIndexed != at {
		t.Fatalf("re-register kept name=%q last_indexed=%v; want api2 and %q", got.Name, got.LastIndexed, at)
	}
	if got.RegisteredAt != "2026-09-01T00:00:00.000Z" {
		t.Fatalf("re-register rewrote registered_at = %q", got.RegisteredAt)
	}

	// List is name-ordered with dir as tie-break: two same-name projects stay
	// deterministic.
	if err := s.ProjectUpsert(ProjectRecord{
		Dir: "/fleet/aa", Name: "api2", RegisteredAt: "2026-09-13T11:00:00.000Z",
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ProjectList()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Dir != "/fleet/aa" || rows[1].Dir != "/fleet/api" {
		t.Fatalf("ProjectList order: %+v", rows)
	}

	// Forget reports what went, once (an unknown dir is a no-op, not an error).
	gone, err := s.ProjectForget("/fleet/aa")
	if err != nil || !gone {
		t.Fatalf("Forget(existing) = %v, %v; want true, nil", gone, err)
	}
	if gone, err := s.ProjectForget("/fleet/aa"); err != nil || gone {
		t.Fatalf("Forget(again) = %v, %v; want false, nil", gone, err)
	}

	// RW-only writes: a read-only handle refuses to mutate the registry but
	// keeps reading it.
	ro, err := Open(filepath.Join(dir, ".leankg", "leankg.db"), RO)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if err := ro.ProjectUpsert(ProjectRecord{Dir: "/x", Name: "x", RegisteredAt: at}); err == nil {
		t.Fatal("ProjectUpsert on RO store must fail")
	}
	if _, err := ro.ProjectForget("/fleet/api"); err == nil {
		t.Fatal("ProjectForget on RO store must fail")
	}
	if _, ok, err := ro.ProjectGet("/fleet/api"); err != nil || !ok {
		t.Fatalf("RO read must keep working: ok=%v err=%v", ok, err)
	}
}

func TestProjectRegistryPG(t *testing.T) {
	s := openPGTest(t)

	at := "2026-09-13T10:00:00.000Z"
	if err := s.ProjectUpsert(ProjectRecord{
		Dir: "/fleet/pg-api", Name: "pg-api", RegisteredAt: "2026-09-01T00:00:00.000Z",
		LastIndexed: &at, ElementCount: 5, FileCount: 2,
	}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.ProjectGet("/fleet/pg-api")
	if err != nil || !ok {
		t.Fatalf("ProjectGet: ok=%v err=%v", ok, err)
	}
	if got.Name != "pg-api" || got.ElementCount != 5 || got.FileCount != 2 ||
		got.LastIndexed == nil || *got.LastIndexed != at {
		t.Fatalf("PG round-trip mismatch: %+v", got)
	}

	// The same COALESCE contract on PG: re-register without a stamp keeps the
	// old one, and registered_at still belongs to the first registration.
	if err := s.ProjectUpsert(ProjectRecord{
		Dir: "/fleet/pg-api", Name: "pg-api", RegisteredAt: "2026-09-13T11:00:00.000Z",
	}); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.ProjectGet("/fleet/pg-api")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastIndexed == nil || *got.LastIndexed != at {
		t.Fatalf("PG re-register lost last_indexed: %+v", got)
	}
	if got.RegisteredAt != "2026-09-01T00:00:00.000Z" {
		t.Fatalf("PG re-register rewrote registered_at: %q", got.RegisteredAt)
	}

	if gone, err := s.ProjectForget("/fleet/pg-api"); err != nil || !gone {
		t.Fatalf("PG forget = %v, %v", gone, err)
	}
	if rows, err := s.ProjectList(); err != nil || len(rows) != 0 {
		t.Fatalf("ProjectList after forget: %v, %v; want empty", rows, err)
	}
}
