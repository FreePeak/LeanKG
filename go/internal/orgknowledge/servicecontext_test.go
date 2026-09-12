package orgknowledge

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// TestServiceContextAggregate mirrors the Rust
// tests/v2_env_incidents_tests.rs::graph_service_context_reads_env_scoped_data
// case (service element with metadata.version, a calls edge and one incident)
// and extends it to the fields that test left uncovered.
func TestServiceContextAggregate(t *testing.T) {
	k, st := openKnowledge(t)
	service := "api"

	if err := st.UpsertElements([]store.Element{
		{
			QualifiedName: service, ElementType: "service", Name: "api",
			FilePath: "service.yaml", Language: "go", Metadata: map[string]any{"version": "abc123"},
		},
		{
			QualifiedName: "./api/proto/user.proto", ElementType: "protobuf", Name: "UserSchema",
			FilePath: "./api/proto/user.proto", Language: "proto",
		},
		// Schema-shaped but outside ./api -> not this service's schemas.
		{
			QualifiedName: "./other/x.proto", ElementType: "protobuf", Name: "OtherSchema",
			FilePath: "./other/x.proto", Language: "proto",
		},
		// Inside ./api but not schema-shaped.
		{
			QualifiedName: "./api/main.go", ElementType: "function", Name: "main",
			FilePath: "./api/main.go", Language: "go",
		},
	}); err != nil {
		t.Fatalf("seed elements: %v", err)
	}
	if err := st.UpsertRelationships([]store.Relationship{
		{Source: service, Target: "database", RelType: "calls", Confidence: 1},
		{Source: "gateway", Target: service, RelType: "service_calls", Confidence: 1},
		// Wrong rel_type: must not appear in either direction.
		{Source: service, Target: "logger", RelType: "imports", Confidence: 1},
	}); err != nil {
		t.Fatalf("seed relationships: %v", err)
	}
	if err := st.ServiceMetadataUpsert(store.ServiceMetadata{
		ServiceName: service, Env: "production", Team: strPtr("payments"),
		OnCall: strPtr("payments-oncall"), RepoURL: strPtr("https://example.test/api"),
		Language: strPtr("go"), CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatalf("seed service metadata: %v", err)
	}

	open := incident("inc-open", func(i *store.Incident) {
		i.Title, i.OccurredAt, i.RootCause = "API timeout", 2000, "no timeout"
		i.Prevention = strPtr("add circuit breaker")
		i.AffectedServices = []string{"api"}
	})
	resolved := incident("inc-resolved", func(i *store.Incident) {
		i.Title, i.OccurredAt, i.ResolvedAt = "Old issue", 1000, i64Ptr(1500)
		i.AffectedServices = []string{"api"}
	})
	otherEnv := incident("inc-staging", func(i *store.Incident) {
		i.Title, i.OccurredAt, i.Env = "Staging issue", 500, "staging"
		i.AffectedServices = []string{"api"}
	})
	for _, inc := range []store.Incident{open, resolved, otherEnv} {
		if _, err := k.CreateIncident(inc); err != nil {
			t.Fatalf("seed incident %s: %v", inc.ID, err)
		}
	}

	ctx, err := k.ServiceContext(service, "production")
	if err != nil {
		t.Fatalf("ServiceContext: %v", err)
	}
	if ctx.Service != service || ctx.Env != "production" {
		t.Fatalf("identity: %+v", ctx)
	}
	if ctx.Version == nil || *ctx.Version != "abc123" {
		t.Fatalf("version = %v, want abc123", ctx.Version)
	}
	if ctx.Team == nil || *ctx.Team != "payments" || ctx.OnCall == nil || *ctx.OnCall != "payments-oncall" {
		t.Fatalf("team/on_call = %v/%v", ctx.Team, ctx.OnCall)
	}
	if ctx.RepoURL == nil || *ctx.RepoURL != "https://example.test/api" || ctx.Language == nil || *ctx.Language != "go" {
		t.Fatalf("repo_url/language = %v/%v", ctx.RepoURL, ctx.Language)
	}
	if len(ctx.Calls) != 1 || ctx.Calls[0] != "database" {
		t.Fatalf("calls = %v, want [database]", ctx.Calls)
	}
	if len(ctx.CalledBy) != 1 || ctx.CalledBy[0] != "gateway" {
		t.Fatalf("called_by = %v, want [gateway]", ctx.CalledBy)
	}
	if len(ctx.Schemas) != 1 || ctx.Schemas[0] != "UserSchema" {
		t.Fatalf("schemas = %v, want [UserSchema]", ctx.Schemas)
	}
	if ctx.OpenIncidents != 1 {
		t.Fatalf("open_incidents = %d, want 1 (the resolved one and the other env must not count)", ctx.OpenIncidents)
	}
	if len(ctx.RecentIncidents) != 2 || ctx.RecentIncidents[0] != "API timeout" || ctx.RecentIncidents[1] != "Old issue" {
		t.Fatalf("recent_incidents = %v, want newest first", ctx.RecentIncidents)
	}
	if ctx.LastIncident == nil || *ctx.LastIncident != "2000: API timeout" {
		t.Fatalf("last_incident = %v", ctx.LastIncident)
	}
	if len(ctx.KnownRisks) != 1 || ctx.KnownRisks[0] != "add circuit breaker (root: no timeout)" {
		t.Fatalf("known_risks = %v", ctx.KnownRisks)
	}

	// An env snapshot wins over the live element's metadata: it is that env's
	// recorded view (the Rust row was env-scoped).
	if err := k.SnapshotElements("production", []store.Element{{
		QualifiedName: service, ElementType: "service", Name: "api", FilePath: "service.yaml",
		Metadata: map[string]any{"version": "deployed-v9"},
	}}); err != nil {
		t.Fatalf("SnapshotElements: %v", err)
	}
	ctx, err = k.ServiceContext(service, "production")
	if err != nil {
		t.Fatalf("ServiceContext after snapshot: %v", err)
	}
	if ctx.Version == nil || *ctx.Version != "deployed-v9" {
		t.Fatalf("version = %v, want the snapshot's deployed-v9", ctx.Version)
	}

	// A service the engine has never seen still answers, empty.
	empty, err := k.ServiceContext("nowhere", "production")
	if err != nil {
		t.Fatalf("ServiceContext(unknown): %v", err)
	}
	if empty.Version != nil || len(empty.Calls) != 0 || empty.OpenIncidents != 0 || empty.LastIncident != nil {
		t.Fatalf("unknown service should be empty: %+v", empty)
	}
	if _, err := k.ServiceContext("  ", "production"); err == nil {
		t.Fatal("blank service must be rejected")
	}
}

// TestServiceContextRiskCap pins the known_risks bound and the empty-prevention
// skip (Rust take(5) over non-empty prevention values).
func TestServiceContextRiskCap(t *testing.T) {
	k, _ := openKnowledge(t)
	seedIncident := func(id string, at int64, prevention *string) {
		inc := incident(id, func(i *store.Incident) {
			i.OccurredAt = at
			i.Prevention = prevention
			i.AffectedServices = []string{"billing"}
			i.RootCause = "cause-" + id
		})
		if _, err := k.CreateIncident(inc); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	for i, at := range []int64{100, 200, 300, 400, 500, 600} {
		if i == 1 {
			seedIncident("inc-noprev", at, nil) // absent prevention must be skipped
			continue
		}
		seedIncident("inc-"+string(rune('a'+i)), at, strPtr("prevention-"+string(rune('a'+i))))
	}

	ctx, err := k.ServiceContext("billing", "production")
	if err != nil {
		t.Fatalf("ServiceContext: %v", err)
	}
	if len(ctx.KnownRisks) != 5 {
		t.Fatalf("known_risks = %v, want the first 5 non-empty preventions", ctx.KnownRisks)
	}
	if ctx.KnownRisks[0] != "prevention-f (root: cause-inc-f)" {
		t.Fatalf("known_risks order = %v, want newest incident first", ctx.KnownRisks)
	}
}

// TestServiceContextJSON pins the MCP payload shape: the same fields as the
// typed struct, with absent optionals rendered as JSON null (Rust serde).
func TestServiceContextJSON(t *testing.T) {
	k, st := openKnowledge(t)
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "api", ElementType: "service", Name: "api", FilePath: "service.yaml",
		Metadata: map[string]any{"version": "abc123"},
	}}); err != nil {
		t.Fatalf("seed element: %v", err)
	}
	if _, err := k.CreateIncident(incident("inc-json", func(i *store.Incident) {
		i.Title, i.OccurredAt = "API timeout", 2000
		i.AffectedServices = []string{"api"}
	})); err != nil {
		t.Fatalf("seed incident: %v", err)
	}

	payload, err := k.ServiceContextJSON("api", "production")
	if err != nil {
		t.Fatalf("ServiceContextJSON: %v", err)
	}
	if payload["service"] != "api" || payload["env"] != "production" || payload["version"] != "abc123" {
		t.Fatalf("payload identity = %+v", payload)
	}
	if payload["team"] != nil || payload["last_incident"] == nil {
		t.Fatalf("absent/present optionals wrong: team=%v last_incident=%v", payload["team"], payload["last_incident"])
	}
	// The seeded incident has no resolved_at, so it counts as open.
	if payload["open_incidents"].(float64) != 1 {
		t.Fatalf("open_incidents = %v", payload["open_incidents"])
	}
	if recent, ok := payload["recent_incidents"].([]any); !ok || len(recent) != 1 || recent[0] != "API timeout" {
		t.Fatalf("recent_incidents = %v", payload["recent_incidents"])
	}
}
