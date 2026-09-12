package orgknowledge

import (
	"path/filepath"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// openStore gives the package a real migrated SQLite store, which is the
// storage these surfaces ship against.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

func openKnowledge(t *testing.T) (*Knowledge, *store.Store) {
	t.Helper()
	st := openStore(t)
	k := New(st)
	k.now = func() int64 { return 1700000000 }
	return k, st
}

func strPtr(s string) *string { return &s }

func i64Ptr(n int64) *int64 { return &n }

// incident builds a valid incident, overridable per case.
func incident(id string, mutate ...func(*store.Incident)) store.Incident {
	inc := store.Incident{
		ID:               id,
		Env:              "production",
		Title:            "Database connection pool exhausted",
		Severity:         "P1",
		OccurredAt:       1000,
		RootCause:        "api leaked database connections",
		Resolution:       "restart api workers",
		AffectedServices: []string{"api", "database"},
		Tags:             []string{"db"},
		Author:           "oncall",
	}
	for _, m := range mutate {
		m(&inc)
	}
	return inc
}

func TestIncidentCRUDTable(t *testing.T) {
	cases := []struct {
		name    string
		inc     store.Incident
		wantErr bool
	}{
		{name: "valid p1", inc: incident("inc-1")},
		{name: "optional fields set", inc: incident("inc-2", func(i *store.Incident) {
			i.Prevention = strPtr("add pool limits")
			i.TriggerPattern = strPtr("connection timeout")
			i.LinkedTicket = strPtr("LEAN-42")
			i.ResolvedAt = i64Ptr(2000)
			i.Tags = []string{"db", "p1"}
		})},
		{name: "missing title", inc: incident("inc-3", func(i *store.Incident) { i.Title = "  " }), wantErr: true},
		{name: "bad severity", inc: incident("inc-4", func(i *store.Incident) { i.Severity = "P9" }), wantErr: true},
		{name: "no affected service", inc: incident("inc-5", func(i *store.Incident) { i.AffectedServices = nil }), wantErr: true},
		{name: "missing root cause", inc: incident("inc-6", func(i *store.Incident) { i.RootCause = "" }), wantErr: true},
		{name: "missing resolution", inc: incident("inc-7", func(i *store.Incident) { i.Resolution = "" }), wantErr: true},
		{name: "missing occurred_at", inc: incident("inc-8", func(i *store.Incident) { i.OccurredAt = 0 }), wantErr: true},
		{name: "missing author", inc: incident("inc-9", func(i *store.Incident) { i.Author = "" }), wantErr: true},
		{name: "empty ticket", inc: incident("inc-10", func(i *store.Incident) { i.LinkedTicket = strPtr("") }), wantErr: true},
		{name: "ticket without prefix", inc: incident("inc-11", func(i *store.Incident) { i.LinkedTicket = strPtr("abc") }), wantErr: true},
		{name: "hash ticket", inc: incident("inc-12", func(i *store.Incident) { i.LinkedTicket = strPtr("#42") })},
		{name: "resolved before occurred", inc: incident("inc-13", func(i *store.Incident) { i.ResolvedAt = i64Ptr(500) }), wantErr: true},
		{name: "negative resolved", inc: incident("inc-14", func(i *store.Incident) { i.ResolvedAt = i64Ptr(-1) }), wantErr: true},
		{name: "id assigned when empty", inc: incident("")},
	}

	k, _ := openKnowledge(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := k.CreateIncident(tc.inc)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("CreateIncident(%+v) = %+v, want error", tc.inc, got)
				}
				// A rejected incident must not be persisted.
				if tc.inc.ID != "" {
					if _, found, _ := k.GetIncident(tc.inc.ID); found {
						t.Fatalf("rejected incident %s was stored", tc.inc.ID)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateIncident: %v", err)
			}
			if got.ID == "" {
				t.Fatal("CreateIncident left the id empty")
			}
			read, found, err := k.GetIncident(got.ID)
			if err != nil || !found {
				t.Fatalf("GetIncident(%s) = %+v, found=%v, err=%v", got.ID, read, found, err)
			}
			if read.Title != got.Title || read.Severity != got.Severity || read.Env != got.Env ||
				read.RootCause != got.RootCause || read.Resolution != got.Resolution {
				t.Fatalf("round-trip mismatch:\n got %+v\nwant %+v", read, got)
			}
			if len(read.AffectedServices) != len(got.AffectedServices) {
				t.Fatalf("affected services round-trip: got %v want %v", read.AffectedServices, got.AffectedServices)
			}
			if (read.Prevention == nil) != (got.Prevention == nil) ||
				(read.LinkedTicket == nil) != (got.LinkedTicket == nil) ||
				(read.ResolvedAt == nil) != (got.ResolvedAt == nil) {
				t.Fatalf("optional column round-trip: got %+v want %+v", read, got)
			}

			// Update replaces in place.
			got.Title = "updated title"
			if _, err := k.UpdateIncident(got); err != nil {
				t.Fatalf("UpdateIncident: %v", err)
			}
			if read, _, _ := k.GetIncident(got.ID); read.Title != "updated title" {
				t.Fatalf("update did not replace the row: %+v", read)
			}

			// Delete removes, and repeating it is a no-op.
			if err := k.DeleteIncident(got.ID); err != nil {
				t.Fatalf("DeleteIncident: %v", err)
			}
			if _, found, _ := k.GetIncident(got.ID); found {
				t.Fatalf("incident %s survived delete", got.ID)
			}
			if err := k.DeleteIncident(got.ID); err != nil {
				t.Fatalf("DeleteIncident on absent id must be a no-op: %v", err)
			}
			if left, err := k.QueryIncidents("", "", "", 0); err != nil {
				t.Fatalf("query after delete: %v", err)
			} else if len(left) != 0 {
				t.Fatalf("incident rows after delete = %d; want 0", len(left))
			}
		})
	}
}

func TestIncidentQueryFilters(t *testing.T) {
	k, _ := openKnowledge(t)
	seed := []store.Incident{
		incident("inc-api", func(i *store.Incident) {
			i.Title, i.OccurredAt = "API timeout", 100
			i.AffectedServices = []string{"api"}
		}),
		incident("inc-db", func(i *store.Incident) {
			i.Title, i.OccurredAt = "Pool exhausted", 300
			i.RootCause = "slow query"
			i.AffectedServices = []string{"database", "api"}
		}),
		incident("inc-staging", func(i *store.Incident) {
			i.Title, i.OccurredAt = "API timeout in staging", 200
			i.Env = "staging"
			i.AffectedServices = []string{"api"}
		}),
		incident("inc-percent", func(i *store.Incident) {
			i.Title, i.OccurredAt = "Odd service name", 50
			i.AffectedServices = []string{"a%b"}
		}),
		incident("inc-underscore", func(i *store.Incident) {
			i.Title, i.OccurredAt = "Another odd name", 40
			i.AffectedServices = []string{"axb"}
		}),
	}
	for _, inc := range seed {
		if _, err := k.CreateIncident(inc); err != nil {
			t.Fatalf("seed %s: %v", inc.ID, err)
		}
	}

	cases := []struct {
		name        string
		service     string
		pattern     string
		env         string
		limit       int
		wantIDs     []string
		wantOrdered bool
	}{
		{name: "service substring, newest first", service: "api", env: "production", wantIDs: []string{"inc-db", "inc-api"}, wantOrdered: true},
		{name: "pattern matches title", pattern: "timeout", env: "production", wantIDs: []string{"inc-api"}},
		{name: "pattern matches root cause", pattern: "slow", env: "production", wantIDs: []string{"inc-db"}},
		{name: "env narrows", service: "api", env: "staging", wantIDs: []string{"inc-staging"}},
		{name: "empty env is every environment", pattern: "api timeout", wantIDs: []string{"inc-staging", "inc-api"}, wantOrdered: true},
		{name: "literal percent is not a wildcard", service: "a%b", env: "production", wantIDs: []string{"inc-percent"}},
		{name: "literal underscore is not a wildcard", service: "axb", env: "production", wantIDs: []string{"inc-underscore"}},
		{name: "limit keeps the newest", service: "api", env: "production", limit: 1, wantIDs: []string{"inc-db"}},
		{name: "no match", service: "nope", env: "production", wantIDs: nil},
		{name: "limit 0 is unbounded", service: "api", env: "production", wantIDs: []string{"inc-db", "inc-api"}, wantOrdered: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := k.QueryIncidents(tc.service, tc.pattern, tc.env, tc.limit)
			if err != nil {
				t.Fatalf("QueryIncidents: %v", err)
			}
			var ids []string
			for _, inc := range got {
				ids = append(ids, inc.ID)
			}
			if len(ids) != len(tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
			if tc.wantOrdered {
				for i := range ids {
					if ids[i] != tc.wantIDs[i] {
						t.Fatalf("ids = %v, want ordered %v", ids, tc.wantIDs)
					}
				}
				return
			}
			seen := map[string]bool{}
			for _, id := range ids {
				seen[id] = true
			}
			for _, want := range tc.wantIDs {
				if !seen[want] {
					t.Fatalf("ids = %v, missing %s", ids, want)
				}
			}
		})
	}
}

func TestNoteCRUD(t *testing.T) {
	k, _ := openKnowledge(t)

	note, err := k.AddNote("pkg.Service.Handle", "watch the retry storm", "", "")
	if err != nil {
		t.Fatalf("AddNote: %v", err)
	}
	if note.KnowledgeType != "general" || note.Tags != "note" || note.Title != "Note for pkg.Service.Handle" {
		t.Fatalf("note shape drifted from the Rust write: %+v", note)
	}
	if note.Environment != "local" {
		t.Fatalf("note env = %q, want local default", note.Environment)
	}
	if note.ElementQualified == nil || *note.ElementQualified != "pkg.Service.Handle" {
		t.Fatalf("note anchor = %v", note.ElementQualified)
	}

	notes, err := k.NotesFor("pkg.Service.Handle")
	if err != nil || len(notes) != 1 || notes[0].ID != note.ID {
		t.Fatalf("NotesFor = %+v, err %v", notes, err)
	}
	if other, err := k.NotesFor("pkg.Other"); err != nil || len(other) != 0 {
		t.Fatalf("NotesFor unknown target = %+v, err %v", other, err)
	}

	// Search covers content as well as title (the Rust PostgreSQL semantics).
	hits, err := k.SearchAnnotations("retry storm", "", "", 10)
	if err != nil || len(hits) != 1 || hits[0].ID != note.ID {
		t.Fatalf("SearchAnnotations(content) = %+v, err %v", hits, err)
	}
	if hits, err := k.SearchAnnotations("retry", "general", "local", 10); err != nil || len(hits) != 1 {
		t.Fatalf("SearchAnnotations(type+env) = %+v, err %v", hits, err)
	}
	if hits, err := k.SearchAnnotations("retry", "prd_mapping", "", 10); err != nil || len(hits) != 0 {
		t.Fatalf("SearchAnnotations(type filter) = %+v, err %v", hits, err)
	}
	if hits, err := k.SearchAnnotations("retry", "", "production", 10); err != nil || len(hits) != 0 {
		t.Fatalf("SearchAnnotations(env filter) = %+v, err %v", hits, err)
	}

	// Delete is idempotent; the row is gone afterwards.
	if err := k.DeleteAnnotation(note.ID); err != nil {
		t.Fatalf("DeleteAnnotation: %v", err)
	}
	if _, found, _ := k.Annotation(note.ID); found {
		t.Fatal("note survived delete")
	}
	if err := k.DeleteAnnotation(note.ID); err != nil {
		t.Fatalf("DeleteAnnotation on absent id: %v", err)
	}

	if _, err := k.AddNote("", "content", "", ""); err == nil {
		t.Fatal("empty note target must be rejected")
	}
	if _, err := k.AddNote("target", "  ", "", ""); err == nil {
		t.Fatal("empty note content must be rejected")
	}
}

func TestAnnotationsByFeatureAndPromotion(t *testing.T) {
	k, _ := openKnowledge(t)

	upcoming := store.KnowledgeEntry{
		ID: "prd-req-FR-1", KnowledgeType: "prd_mapping", Title: "FR-1",
		Content: "requirement", FeatureID: strPtr("FR-1"), Branch: strPtr("feat/x"),
		Environment: "upcoming", Author: "prd_indexer",
	}
	other := upcoming
	other.ID, other.Branch, other.FeatureID = "prd-req-FR-2", strPtr("feat/y"), strPtr("FR-2")
	noBranch := upcoming
	noBranch.ID, noBranch.Branch, noBranch.FeatureID = "prd-req-FR-3", nil, strPtr("FR-3")
	for _, e := range []store.KnowledgeEntry{upcoming, other, noBranch} {
		if _, err := k.PutAnnotation(e); err != nil {
			t.Fatalf("PutAnnotation %s: %v", e.ID, err)
		}
	}
	if got, err := k.AnnotationsForFeature("FR-1"); err != nil || len(got) != 1 || got[0].ID != "prd-req-FR-1" {
		t.Fatalf("AnnotationsForFeature = %+v, err %v", got, err)
	}

	promoted, err := k.PromoteEnvironment("feat/x", "production")
	if err != nil || promoted != 1 {
		t.Fatalf("PromoteEnvironment = %d, err %v; want 1", promoted, err)
	}
	moved, found, err := k.Annotation("prd-req-FR-1")
	if err != nil || !found || moved.Environment != "production" {
		t.Fatalf("promoted entry = %+v, found %v, err %v", moved, found, err)
	}
	if moved.UpdatedAt != k.now() {
		t.Fatalf("promotion did not bump updated_at: %d", moved.UpdatedAt)
	}
	staged, err := k.AnnotationsForEnvironment("upcoming", 0)
	if err != nil || len(staged) != 2 {
		t.Fatalf("upcoming after promotion = %+v, err %v; want the 2 unmoved entries", staged, err)
	}
	for _, e := range staged {
		if e.ID == "prd-req-FR-1" {
			t.Fatal("promoted entry is still staged in upcoming")
		}
	}
}

func TestEnvConflictDetection(t *testing.T) {
	meta := func(version string, extra ...string) map[string]any {
		m := map[string]any{"version": version}
		if len(extra) == 2 {
			m[extra[0]] = extra[1]
		}
		return m
	}

	cases := []struct {
		name      string
		snapshots map[string]map[string]any
		want      []EnvConflict
	}{
		{
			name: "identical everywhere",
			snapshots: map[string]map[string]any{
				"local": meta("abc"), "staging": meta("abc"), "production": meta("abc"),
			},
			want: []EnvConflict{},
		},
		{
			name:      "missing everywhere",
			snapshots: map[string]map[string]any{},
			want: []EnvConflict{
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in local environment", Risk: "MEDIUM"},
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in staging environment", Risk: "MEDIUM"},
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in production environment", Risk: "HIGH"},
			},
		},
		{
			name: "version mismatch local vs staging",
			snapshots: map[string]map[string]any{
				"local": meta("v1"), "staging": meta("v2"),
			},
			want: []EnvConflict{
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in production environment", Risk: "HIGH"},
				{ConflictType: "schema_version", Detail: "Version mismatch: local has 'v1', staging has 'v2'", Risk: "HIGH"},
			},
		},
		{
			name: "same version, different config",
			snapshots: map[string]map[string]any{
				"local": meta("v1", "replicas", "1"), "production": meta("v1", "replicas", "3"),
			},
			want: []EnvConflict{
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in staging environment", Risk: "MEDIUM"},
				{ConflictType: "config_drift", Detail: "Metadata differs between local and production", Risk: "MEDIUM"},
			},
		},
		{
			name: "only staging present",
			snapshots: map[string]map[string]any{
				"staging": meta("v1"),
			},
			want: []EnvConflict{
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in local environment", Risk: "MEDIUM"},
				{ConflictType: "missing_in_env", Detail: "Service 'billing' is missing in production environment", Risk: "HIGH"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, _ := openKnowledge(t)
			for env, m := range tc.snapshots {
				els := []store.Element{{
					QualifiedName: "billing", ElementType: "service", Name: "billing",
					FilePath: "service.yaml", Metadata: m,
				}}
				if err := k.SnapshotElements(env, els); err != nil {
					t.Fatalf("SnapshotElements(%s): %v", env, err)
				}
			}
			got, err := k.FindEnvConflicts("billing")
			if err != nil {
				t.Fatalf("FindEnvConflicts: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("conflicts = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("conflict[%d] = %+v, want %+v (all: %+v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

// TestEntityTypesRoundTrip guards the nullable-column handling both backends
// share: absent optionals must come back absent, not as empty strings.
func TestEntityTypesRoundTrip(t *testing.T) {
	k, st := openKnowledge(t)
	inc := incident("inc-null", func(i *store.Incident) {
		i.TriggerPattern, i.Prevention, i.LinkedTicket, i.ResolvedAt = nil, nil, nil, nil
		i.Tags = nil
	})
	if _, err := k.CreateIncident(inc); err != nil {
		t.Fatalf("CreateIncident: %v", err)
	}
	got, _, err := k.GetIncident("inc-null")
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if got.Prevention != nil || got.TriggerPattern != nil || got.LinkedTicket != nil || got.ResolvedAt != nil {
		t.Fatalf("absent optionals came back set: %+v", got)
	}
	if len(got.Tags) != 0 {
		t.Fatalf("empty tags came back as %v", got.Tags)
	}

	if err := st.ServiceMetadataUpsert(store.ServiceMetadata{
		ServiceName: "billing", Env: "production", Team: strPtr("payments"),
		OnCall: strPtr("payments-oncall"), RepoURL: strPtr("https://example.test/billing"),
		Language: strPtr("go"), CreatedAt: 1, UpdatedAt: 2,
	}); err != nil {
		t.Fatalf("ServiceMetadataUpsert: %v", err)
	}
	m, found, err := st.ServiceMetadataGet("billing", "production")
	if err != nil || !found {
		t.Fatalf("ServiceMetadataGet = %+v, found %v, err %v", m, found, err)
	}
	if m.Team == nil || *m.Team != "payments" || m.Version != nil || m.SLOP99Ms != nil {
		t.Fatalf("service metadata round-trip: %+v", m)
	}
	if _, found, _ := st.ServiceMetadataGet("billing", "staging"); found {
		t.Fatal("service metadata leaked across environments")
	}
}
