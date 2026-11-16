package store

import (
	"context"
	"reflect"
	"testing"
)

// nameTokenFinder and lastRunReader are the engine-level methods the shared
// readers serve. Neither is on the Backend contract, so tests reach them
// through an assertion; both engines implement each one.
type nameTokenFinder interface {
	FindByNameToken(ctx context.Context, query string, limit int, f ElementFilter) ([]FuzzyMatch, error)
}

type lastRunReader interface {
	LastEmbedRun(ctx context.Context, modelID string) (*EmbedRun, error)
}

// eachEngine runs fn once per engine on a fresh, migrated RW store. The
// PostgreSQL leg skips unless LEANKG_TEST_PG_URL is set, as every PG test does.
func eachEngine(t *testing.T, fn func(t *testing.T, b Backend)) {
	t.Helper()
	t.Run("sqlite", func(t *testing.T) { fn(t, openSQLiteConformance(t)) })
	t.Run("postgres", func(t *testing.T) { fn(t, openPGTest(t)) })
}

// TestSharedRowReadersPinBehaviour pins what the shared row readers return on
// both engines: element and relationship metadata round-trip, a nil metadata
// stays nil, and the fuzzy and name-token arms carry the element metadata.
// The values were read off the pre-CX-51 per-engine implementations.
func TestSharedRowReadersPinBehaviour(t *testing.T) {
	eachEngine(t, func(t *testing.T, b Backend) {
		ctx := context.Background()
		meta := map[string]any{"kind": "handler", "n": float64(2)}
		if err := b.UpsertElements(ctx, []Element{
			{QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha", FilePath: "pkg/a.go",
				LineStart: 1, LineEnd: 3, Language: "go", Content: "func Alpha() {}", Metadata: meta},
			{QualifiedName: "pkg.Beta", ElementType: "function", Name: "Beta", FilePath: "pkg/a.go", Language: "go"},
		}); err != nil {
			t.Fatalf("UpsertElements: %v", err)
		}
		if err := b.UpsertRelationships(ctx, []Relationship{
			{Source: "pkg.Alpha", Target: "pkg.Beta", RelType: "calls", Confidence: 1, Metadata: map[string]any{"via": "direct"}},
			{Source: "pkg.Beta", Target: "pkg.Alpha", RelType: "calls", Confidence: 0.5},
		}); err != nil {
			t.Fatalf("UpsertRelationships: %v", err)
		}

		exact, err := b.FindExact(ctx, "Alpha")
		if err != nil || len(exact) != 1 {
			t.Fatalf("FindExact = %d elements, err %v; want 1", len(exact), err)
		}
		if exact[0].QualifiedName != "pkg.Alpha" || exact[0].LineEnd != 3 || exact[0].Content != "func Alpha() {}" {
			t.Errorf("FindExact element = %+v", exact[0])
		}
		if !reflect.DeepEqual(exact[0].Metadata, meta) {
			t.Errorf("FindExact metadata = %v; want %v", exact[0].Metadata, meta)
		}

		all, err := b.Elements(ctx)
		if err != nil || len(all) != 2 {
			t.Fatalf("Elements = %d, err %v; want 2", len(all), err)
		}
		for _, e := range all {
			if e.QualifiedName == "pkg.Beta" && e.Metadata != nil {
				t.Errorf("Elements: pkg.Beta metadata = %v; want nil for an empty object", e.Metadata)
			}
		}

		out, err := b.Outgoing(ctx, "pkg.Alpha")
		if err != nil || len(out) != 1 || !reflect.DeepEqual(out[0].Metadata, map[string]any{"via": "direct"}) {
			t.Errorf("Outgoing(pkg.Alpha) = %+v, err %v; want one edge with metadata via=direct", out, err)
		}
		in, err := b.Incoming(ctx, "pkg.Alpha")
		if err != nil || len(in) != 1 || in[0].Confidence != 0.5 || in[0].Metadata != nil {
			t.Errorf("Incoming(pkg.Alpha) = %+v, err %v; want one edge, confidence 0.5, nil metadata", in, err)
		}
		rels, err := b.RelationshipsAll(ctx, 10)
		if err != nil || len(rels) != 2 {
			t.Errorf("RelationshipsAll = %d, err %v; want 2", len(rels), err)
		}

		fuzzy, _, err := b.FindFuzzy(ctx, "Alpha", 10)
		if err != nil {
			t.Fatalf("FindFuzzy: %v", err)
		}
		if !hasMatchWithMeta(fuzzy, "pkg.Alpha", meta) {
			t.Errorf("FindFuzzy(Alpha) = %+v; want pkg.Alpha carrying metadata %v", fuzzy, meta)
		}

		byToken, err := b.(nameTokenFinder).FindByNameToken(ctx, "Alpha", 10, ElementFilter{})
		if err != nil {
			t.Fatalf("FindByNameToken: %v", err)
		}
		if !hasMatchWithMeta(byToken, "pkg.Alpha", meta) {
			t.Errorf("FindByNameToken(Alpha) = %+v; want pkg.Alpha carrying metadata %v", byToken, meta)
		}
	})
}

func hasMatchWithMeta(ms []FuzzyMatch, qn string, meta map[string]any) bool {
	for _, m := range ms {
		if m.Element.QualifiedName == qn && reflect.DeepEqual(m.Element.Metadata, meta) {
			return true
		}
	}
	return false
}

// TestSharedStampAndRunReadersPinBehaviour pins the stamp and embed-run readers:
// every stamp field round-trips, a missing row is (nil, nil) on both engines,
// and the embed run carries its counters and status.
func TestSharedStampAndRunReadersPinBehaviour(t *testing.T) {
	eachEngine(t, func(t *testing.T, b Backend) {
		ctx := context.Background()
		st := ModelStamp{ModelID: "m-one", Revision: "rev1", Dimensions: 4, Distance: "cosine",
			Provider: "deterministic", ChunkerVersion: 2, QueryPrefix: "q:", DocumentPrefix: "d:", TextCap: 512}
		if err := b.WriteStamp(ctx, st); err != nil {
			t.Fatalf("WriteStamp: %v", err)
		}
		got, err := b.Stamp(ctx, "m-one")
		if err != nil || got == nil || *got != st {
			t.Fatalf("Stamp(m-one) = %+v, err %v; want %+v", got, err, st)
		}
		if missing, err := b.Stamp(ctx, "absent"); err != nil || missing != nil {
			t.Errorf("Stamp(absent) = %+v, err %v; want nil, nil", missing, err)
		}
		all, err := b.Stamps(ctx)
		if err != nil || len(all) != 1 || all[0] != st {
			t.Errorf("Stamps = %+v, err %v; want [%+v]", all, err, st)
		}

		if last, err := b.(lastRunReader).LastEmbedRun(ctx, "absent"); err != nil || last != nil {
			t.Errorf("LastEmbedRun(absent) = %+v, err %v; want nil, nil", last, err)
		}
		if last, err := b.LastEmbedRunAny(ctx); err != nil || last != nil {
			t.Errorf("LastEmbedRunAny(empty) = %+v, err %v; want nil, nil", last, err)
		}
		id, err := b.StartEmbedRun(ctx, "m-one", "full", 3)
		if err != nil {
			t.Fatalf("StartEmbedRun: %v", err)
		}
		if err := b.FinishEmbedRun(ctx, id, "ok", 1, 2, 3, 4, 5); err != nil {
			t.Fatalf("FinishEmbedRun: %v", err)
		}
		run, err := b.(lastRunReader).LastEmbedRun(ctx, "m-one")
		if err != nil || run == nil {
			t.Fatalf("LastEmbedRun(m-one) = %+v, err %v", run, err)
		}
		if run.ID != id || run.ModelID != "m-one" || run.Mode != "full" || run.Dirty != 3 ||
			run.Embedded != 1 || run.Skipped != 2 || run.Failed != 3 || run.Truncations != 4 ||
			run.Orphans != 5 || run.Status != "ok" || run.FinishedAt == nil {
			t.Errorf("LastEmbedRun = %+v; want the finished run with counters 1,2,3,4,5 and status ok", run)
		}
		if any, err := b.LastEmbedRunAny(ctx); err != nil || any == nil || any.ID != id {
			t.Errorf("LastEmbedRunAny = %+v, err %v; want run %d", any, err, id)
		}
	})
}

// TestSharedAuditReadersPinBehaviour pins the audit row reader: details
// round-trip, a nil details map reads back nil, the tail is ascending, and the
// chain verifies on both engines.
func TestSharedAuditReadersPinBehaviour(t *testing.T) {
	eachEngine(t, func(t *testing.T, b Backend) {
		ctx := context.Background()
		first := &AuditEntry{Actor: "a", Action: "login", Target: "t1", Details: map[string]any{"k": "v"}}
		second := &AuditEntry{Actor: "b", Action: "logout", Target: "t2"}
		for _, e := range []*AuditEntry{first, second} {
			if err := b.AppendAudit(ctx, e); err != nil {
				t.Fatalf("AppendAudit: %v", err)
			}
		}
		tail, err := b.AuditTail(ctx, 10)
		if err != nil || len(tail) != 2 {
			t.Fatalf("AuditTail = %d, err %v; want 2", len(tail), err)
		}
		if tail[0].Seq != 1 || tail[1].Seq != 2 {
			t.Errorf("AuditTail seq order = %d,%d; want ascending 1,2", tail[0].Seq, tail[1].Seq)
		}
		if !reflect.DeepEqual(tail[0].Details, map[string]any{"k": "v"}) {
			t.Errorf("AuditTail details = %v; want k=v", tail[0].Details)
		}
		if tail[1].Details != nil {
			t.Errorf("AuditTail nil details read back as %v; want nil", tail[1].Details)
		}
		if tail[0].Hash != first.Hash || tail[1].PrevHash != first.Hash {
			t.Errorf("chain links: tail hashes do not match the appended entries")
		}
		ok, brokenAt, err := b.VerifyAuditChain(ctx)
		if err != nil || !ok || brokenAt != 0 {
			t.Errorf("VerifyAuditChain = %v,%d,%v; want true,0,nil", ok, brokenAt, err)
		}
	})
}

// TestSharedSweepPolicyPinBehaviour pins the stale-collection sweep rules on
// both engines: an empty or blank keep list is a no-op, and a store whose keep
// collection is absent is BEHIND, not stale, so nothing is dropped. A stale
// collection is dropped with its vectors and stamp.
func TestSharedSweepPolicyPinBehaviour(t *testing.T) {
	eachEngine(t, func(t *testing.T, b Backend) {
		ctx := context.Background()
		seed := func(id string) {
			t.Helper()
			if err := b.WriteStamp(ctx, ModelStamp{ModelID: id, Revision: "r", Dimensions: 2, Distance: "cosine", Provider: "p"}); err != nil {
				t.Fatalf("WriteStamp %s: %v", id, err)
			}
			if err := b.UpsertVectors(ctx, id, []VectorRow{{QualifiedName: "pkg." + id, Vec: []float32{1, 0}}}); err != nil {
				t.Fatalf("UpsertVectors %s: %v", id, err)
			}
		}
		seed("old")

		if sw, err := b.DeleteStaleStamps(ctx, nil); err != nil || !sw.Empty() {
			t.Errorf("DeleteStaleStamps(nil) = %+v, err %v; want an empty no-op", sw, err)
		}
		if sw, err := b.DeleteStaleStamps(ctx, []string{" ", ""}); err != nil || !sw.Empty() {
			t.Errorf("DeleteStaleStamps(blank) = %+v, err %v; want an empty no-op", sw, err)
		}
		if sw, err := b.DeleteStaleStamps(ctx, []string{"absent"}); err != nil || !sw.Empty() {
			t.Errorf("DeleteStaleStamps(keep absent) = %+v, err %v; want an empty no-op (BEHIND, not stale)", sw, err)
		}
		if st, err := b.Stamp(ctx, "old"); err != nil || st == nil {
			t.Fatalf("after no-op sweeps, stamp old = %+v, err %v; want it kept", st, err)
		}

		seed("stale")
		sw, err := b.DeleteStaleStamps(ctx, []string{"old", "old", " old "})
		if err != nil {
			t.Fatalf("DeleteStaleStamps(old): %v", err)
		}
		if sw.Models != 1 || sw.Vectors != 1 {
			t.Errorf("DeleteStaleStamps(old) = %+v; want Models=1 Vectors=1", sw)
		}
		if st, err := b.Stamp(ctx, "stale"); err != nil || st != nil {
			t.Errorf("stale stamp after sweep = %+v, err %v; want nil", st, err)
		}
		if st, err := b.Stamp(ctx, "old"); err != nil || st == nil {
			t.Errorf("kept stamp after sweep = %+v, err %v; want it present", st, err)
		}
	})
}
