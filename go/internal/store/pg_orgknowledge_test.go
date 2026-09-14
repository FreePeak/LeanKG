// PostgreSQL parity tests for migration 010 (org knowledge: incidents,
// knowledge_entries, service_metadata, env_snapshots). Skipped unless
// LEANKG_TEST_PG_URL is set (see openPGTest in store_pg_test.go); each test
// gets its own project schema, dropped on cleanup.
//
// Why this file exists: migration 010's PG dialect had no test at all — the
// sqlite path is covered indirectly through internal/orgknowledge, so arrays,
// JSON metadata and nullable BIGINT columns on Postgres were unverified.
package store

import "testing"

func TestPGIncidentRoundTrip(t *testing.T) {
	s := openPGTest(t)
	pattern := "disk-pressure"
	resolved := int64(500)
	inc := Incident{
		ID: "inc-1", Env: "production", Title: "disk full", Severity: "P1",
		OccurredAt: 100, ResolvedAt: &resolved, RootCause: "no rotation",
		Resolution: "rotate daily", AffectedServices: []string{"svc-a", "svc-b"},
		TriggerPattern: &pattern, Tags: []string{"ops", "storage"},
	}
	if err := s.IncidentUpsert(inc); err != nil {
		t.Fatalf("incident upsert: %v", err)
	}
	got, found, err := s.IncidentByID("inc-1")
	if err != nil || !found {
		t.Fatalf("incident by id = %+v, %v, %v", got, found, err)
	}
	if got.Severity != "P1" || got.RootCause != "no rotation" || len(got.AffectedServices) != 2 {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
	if got.ResolvedAt == nil || *got.ResolvedAt != 500 {
		t.Fatalf("nullable int64 lost: %+v", got.ResolvedAt)
	}
	if got.TriggerPattern == nil || *got.TriggerPattern != pattern {
		t.Fatalf("nullable text lost: %+v", got.TriggerPattern)
	}
	rows, err := s.IncidentsQuery(IncidentQuery{Service: "svc-b", Env: "production", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("incidents query by service = %d rows (%v)", len(rows), err)
	}
	if err := s.IncidentDelete("inc-1"); err != nil {
		t.Fatalf("incident delete: %v", err)
	}
	if _, found, _ := s.IncidentByID("inc-1"); found {
		t.Fatal("incident still present after delete")
	}
}

func TestPGKnowledgeEntryRoundTrip(t *testing.T) {
	s := openPGTest(t)
	qn := "src/a.go::Alpha"
	e := KnowledgeEntry{
		ID: "k-1", KnowledgeType: "note", Title: "why", Content: "because",
		ElementQualified: &qn, Tags: "note", Environment: "production",
		Author: "ops", CreatedAt: 10, UpdatedAt: 10,
	}
	if err := s.KnowledgeEntryUpsert(e); err != nil {
		t.Fatalf("knowledge upsert: %v", err)
	}
	if got, found, err := s.KnowledgeEntryByID("k-1"); err != nil || !found || got.Content != "because" || got.ElementQualified == nil {
		t.Fatalf("knowledge by id = %+v, %v, %v", got, found, err)
	}
	byEl, err := s.KnowledgeEntriesByElement(qn)
	if err != nil || len(byEl) != 1 {
		t.Fatalf("by element = %d rows (%v)", len(byEl), err)
	}
	byEnv, err := s.KnowledgeEntriesByEnvironment("production", 10)
	if err != nil || len(byEnv) != 1 {
		t.Fatalf("by environment = %d rows (%v)", len(byEnv), err)
	}
	if hits, err := s.KnowledgeEntriesSearch("why", "note", "production", 10); err != nil || len(hits) != 1 {
		t.Fatalf("search = %d rows (%v)", len(hits), err)
	}
	if err := s.KnowledgeEntryDelete("k-1"); err != nil {
		t.Fatalf("knowledge delete: %v", err)
	}
}

func TestPGServiceMetadataRoundTrip(t *testing.T) {
	s := openPGTest(t)
	team, version := "platform", "1.2.3"
	p99 := int64(250)
	m := ServiceMetadata{
		ServiceName: "svc-a", Env: "production", Team: &team, RepoURL: nil,
		SLOP99Ms: &p99, IncidentCount: 3, Tags: "core", Version: &version,
	}
	if err := s.ServiceMetadataUpsert(m); err != nil {
		t.Fatalf("service metadata upsert: %v", err)
	}
	got, found, err := s.ServiceMetadataGet("svc-a", "production")
	if err != nil || !found {
		t.Fatalf("service metadata get = %+v, %v, %v", got, found, err)
	}
	if got.Team == nil || *got.Team != team || got.SLOP99Ms == nil || *got.SLOP99Ms != 250 || got.IncidentCount != 3 {
		t.Fatalf("round-trip lost fields: %+v", got)
	}
	if got.RepoURL != nil {
		t.Fatalf("absent optional became non-nil: %+v", got.RepoURL)
	}
}

func TestPGEnvSnapshotsRoundTrip(t *testing.T) {
	s := openPGTest(t)
	snaps := []EnvSnapshot{{
		Env: "production", QualifiedName: "src/a.go::Alpha", ElementType: "function",
		Name: "Alpha", FilePath: "src/a.go",
		Metadata: map[string]any{"schema_version": float64(2), "config": "x"}, CapturedAt: 42,
	}}
	if err := s.EnvSnapshotsPut(snaps); err != nil {
		t.Fatalf("env snapshots put: %v", err)
	}
	got, found, err := s.EnvSnapshotGet("production", "src/a.go::Alpha")
	if err != nil || !found {
		t.Fatalf("env snapshot get = %+v, %v, %v", got, found, err)
	}
	// JSON metadata must survive the PG dialect (jsonb round-trip).
	if got.Metadata["config"] != "x" {
		t.Fatalf("metadata jsonb round-trip = %+v", got.Metadata)
	}
	if got.CapturedAt != 42 {
		t.Fatalf("captured_at = %d, want 42", got.CapturedAt)
	}
}
