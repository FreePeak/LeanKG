package web

import (
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// newTunnelStore builds a store whose only community signal is the folder
// fallback: elements under src/a/ and src/b/, relationships of a type that
// carries no clustering weight ("references"), so detectCommunities falls back
// to per-folder clusters exactly as the Rust engine's persisted column did.
func newTunnelStore(t *testing.T) store.Backend {
	t.Helper()
	st, err := store.Open(t.TempDir()+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	els := []store.Element{
		{QualifiedName: "src/a/x.go::x", ElementType: "function", Name: "x", FilePath: "src/a/x.go"},
		{QualifiedName: "src/a/x.go::x2", ElementType: "function", Name: "x2", FilePath: "src/a/x.go"},
		{QualifiedName: "src/b/y.go::y", ElementType: "function", Name: "y", FilePath: "src/b/y.go"},
		{QualifiedName: "src/b/y.go::y2", ElementType: "function", Name: "y2", FilePath: "src/b/y.go"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	rels := []store.Relationship{
		// Same-cluster edge: never a tunnel.
		{Source: "src/a/x.go::x", Target: "src/a/x.go::x2", RelType: "references", Confidence: 0.9,
			Metadata: map[string]any{"resolution_method": "typed"}},
		// Cross-cluster edges.
		{Source: "src/a/x.go::x", Target: "src/b/y.go::y", RelType: "references", Confidence: 0.4,
			Metadata: map[string]any{"resolution_method": "name"}},
		{Source: "src/a/x.go::x", Target: "src/b/y.go::y2", RelType: "references", Confidence: 0.7},
		{Source: "src/b/y.go::y", Target: "src/a/x.go::x", RelType: "references", Confidence: 0.99},
		// One endpoint outside every cluster: skipped, not a tunnel.
		{Source: "src/b/y.go::y2", Target: "missing::fn", RelType: "references", Confidence: 0.99},
	}
	if err := st.UpsertRelationships(rels); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
	return st
}

func TestClusterAssignments(t *testing.T) {
	st := newTunnelStore(t)
	cl, err := ClusterAssignments(st)
	if err != nil {
		t.Fatalf("ClusterAssignments: %v", err)
	}
	if len(cl.Clusters) != 2 {
		t.Fatalf("clusters = %+v, want 2", cl.Clusters)
	}
	for _, c := range cl.Clusters {
		if c.MemberCount != 2 {
			t.Fatalf("cluster %s member count = %d, want 2", c.ID, c.MemberCount)
		}
		if c.ID != "cluster_0" && c.ID != "cluster_1" {
			t.Fatalf("unexpected cluster id %q", c.ID)
		}
		if len(c.RepresentativeFiles) == 0 {
			t.Fatalf("cluster %s has no representative files", c.ID)
		}
	}
	if got := cl.Clusters[0].ID; got != "cluster_0" {
		t.Fatalf("first cluster = %s, want cluster_0 (member-count desc then id)", got)
	}
	if n := len(cl.Assignments); n != 4 {
		t.Fatalf("assignments = %d entries, want 4", n)
	}
	for qn := range cl.Assignments {
		if !strings.HasPrefix(qn, "src/a/") && !strings.HasPrefix(qn, "src/b/") {
			t.Fatalf("unexpected assignment key %q", qn)
		}
	}
	// src/a members share one cluster, distinct from src/b members.
	if cl.Assignments["src/a/x.go::x"] == cl.Assignments["src/b/y.go::y"] {
		t.Fatalf("src/a and src/b landed in one cluster: %v", cl.Assignments)
	}
}

func TestTunnels(t *testing.T) {
	st := newTunnelStore(t)
	ts, err := Tunnels(st)
	if err != nil {
		t.Fatalf("Tunnels: %v", err)
	}
	if len(ts) != 3 {
		t.Fatalf("tunnels = %+v, want 3 cross-cluster edges", ts)
	}
	// Sorted by confidence descending: 0.99, 0.7, 0.4.
	if ts[0].Confidence != 0.99 || ts[1].Confidence != 0.7 || ts[2].Confidence != 0.4 {
		t.Fatalf("confidence order = %v %v %v, want 0.99 0.7 0.4",
			ts[0].Confidence, ts[1].Confidence, ts[2].Confidence)
	}
	// The 0.99 tunnel is src/b -> src/a (y -> x).
	if ts[0].Source != "src/b/y.go::y" || ts[0].Target != "src/a/x.go::x" {
		t.Fatalf("top tunnel = %s -> %s", ts[0].Source, ts[0].Target)
	}
	for _, tn := range ts {
		if tn.SourceCluster == tn.TargetCluster {
			t.Fatalf("tunnel %s -> %s shares cluster %s", tn.Source, tn.Target, tn.SourceCluster)
		}
		if tn.SourceCluster == "" || tn.TargetCluster == "" {
			t.Fatalf("tunnel %s -> %s has empty cluster id(s)", tn.Source, tn.Target)
		}
		if tn.RelType != "references" {
			t.Fatalf("rel_type = %q, want references", tn.RelType)
		}
	}
	// Provenance labels calibrate like every other edge (ENT-9).
	if got := ts[0].ConfidenceLabel; got != "EXTRACTED" { // 0.99, no method
		t.Fatalf("label[0] = %q, want EXTRACTED", got)
	}
	if got := ts[1].ConfidenceLabel; got != "INFERRED" { // 0.7 in the threshold ladder
		t.Fatalf("label[1] = %q, want INFERRED", got)
	}
}

// The CLI prints the Rust tunnel output: a header line plus the numbered
// "src --[type]--> tgt  (0.99)  [c0 -> c1]" lines.
func TestRenderTunnels(t *testing.T) {
	tn := Tunnel{
		Source: "src/a/x.go::x", Target: "src/b/y.go::y", RelType: "references",
		Confidence: 0.99, ConfidenceLabel: "EXTRACTED",
		SourceCluster: "cluster_0", TargetCluster: "cluster_1",
	}
	want := "Found 1 cross-cluster tunnels:\n" +
		"  1. src/a/x.go::x --[references]--> src/b/y.go::y  (0.99)  [cluster_0 -> cluster_1]\n"
	if got := RenderTunnels([]Tunnel{tn}); got != want {
		t.Fatalf("RenderTunnels = %q, want %q", got, want)
	}
	if got := RenderTunnels(nil); got != "Found 0 cross-cluster tunnels:\n" {
		t.Fatalf("empty render = %q", got)
	}
}

func TestTunnelsEmptyGraph(t *testing.T) {
	st, err := store.Open(t.TempDir()+"/.leankg/leankg.db", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	ts, err := Tunnels(st)
	if err != nil {
		t.Fatalf("Tunnels on empty store: %v", err)
	}
	if len(ts) != 0 {
		t.Fatalf("tunnels = %v, want none", ts)
	}
}
