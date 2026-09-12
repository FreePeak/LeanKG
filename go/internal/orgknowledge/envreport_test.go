package orgknowledge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func TestEnvConflictReport(t *testing.T) {
	k, st := openKnowledge(t)

	if err := st.UpsertRelationships([]store.Relationship{
		{Source: "payments", Target: "billing-api", RelType: "conflicts_with", Confidence: 0.8},
		{Source: "BILLING-worker", Target: "ledger", RelType: "conflicts_with", Confidence: 0.5},
		// Not the report's relation type, and not a matching endpoint.
		{Source: "billing-api", Target: "ledger", RelType: "calls", Confidence: 1},
		{Source: "other", Target: "thing", RelType: "conflicts_with", Confidence: 1},
	}); err != nil {
		t.Fatalf("seed relationships: %v", err)
	}

	// billing-api lives in two environments; billing-only in one.
	for _, env := range []string{"local", "production"} {
		if err := k.SnapshotElements(env, []store.Element{{
			QualifiedName: "billing-api", ElementType: "service", Name: "billing-api",
			FilePath: "service.yaml", Metadata: map[string]any{"version": "v1"},
		}}); err != nil {
			t.Fatalf("SnapshotElements(%s): %v", env, err)
		}
	}
	if err := k.SnapshotElements("local", []store.Element{{
		QualifiedName: "billing-only", ElementType: "service", Name: "billing-only",
		FilePath: "service.yaml", Metadata: map[string]any{"version": "v1"},
	}}); err != nil {
		t.Fatalf("SnapshotElements: %v", err)
	}

	rep, err := k.EnvConflictReport("billing")
	if err != nil {
		t.Fatalf("EnvConflictReport: %v", err)
	}
	if len(rep.Pairs) != 2 {
		t.Fatalf("pairs = %+v, want the 2 conflicts_with edges touching billing", rep.Pairs)
	}
	if rep.VariantTotal != 1 || len(rep.Variants) != 1 || rep.Variants[0].QualifiedName != "billing-api" {
		t.Fatalf("variants = %+v (total %d), want only billing-api", rep.Variants, rep.VariantTotal)
	}
	if got, want := rep.Variants[0].Envs, []string{"local", "production"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("variant envs = %v, want %v", got, want)
	}
	if rep.Empty() {
		t.Fatal("report with findings must not be empty")
	}

	var sb strings.Builder
	if err := rep.WriteText(&sb); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	want := "Environment conflicts for service 'billing':\n" +
		"  - BILLING-worker <-> ledger (confidence: 0.50)\n" +
		"  - payments <-> billing-api (confidence: 0.80)\n" +
		"\nCross-environment element variants for 'billing':\n" +
		"  - billing-api (envs: local, production)\n"
	if sb.String() != want {
		t.Fatalf("rendered text mismatch:\n got %q\nwant %q", sb.String(), want)
	}
}

func TestEnvConflictReportCapsVariants(t *testing.T) {
	k, _ := openKnowledge(t)

	// 22 cross-environment names plus one single-environment name that must
	// not count.
	for i := 0; i < 22; i++ {
		for _, env := range []string{"local", "production"} {
			els := []store.Element{{
				QualifiedName: fmt.Sprintf("svc-%02d", i), ElementType: "service",
				Name: fmt.Sprintf("svc-%02d", i), FilePath: "service.yaml",
			}}
			if err := k.SnapshotElements(env, els); err != nil {
				t.Fatalf("seed %s: %v", env, err)
			}
		}
	}
	if err := k.SnapshotElements("local", []store.Element{{
		QualifiedName: "svc-single", ElementType: "service", Name: "svc-single", FilePath: "service.yaml",
	}}); err != nil {
		t.Fatalf("seed single: %v", err)
	}

	rep, err := k.EnvConflictReport("svc")
	if err != nil {
		t.Fatalf("EnvConflictReport: %v", err)
	}
	if rep.VariantTotal != 22 || len(rep.Variants) != cliVariantCap {
		t.Fatalf("variants = %d of %d, want %d of 22", len(rep.Variants), rep.VariantTotal, cliVariantCap)
	}
	var sb strings.Builder
	if err := rep.WriteText(&sb); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if !strings.HasSuffix(sb.String(), "  ... and 2 more\n") {
		t.Fatalf("cap note missing:\n%s", sb.String())
	}

	// Nothing matches -> the Rust "no conflicts" line.
	empty, err := k.EnvConflictReport("nothing-here")
	if err != nil {
		t.Fatalf("EnvConflictReport(none): %v", err)
	}
	if !empty.Empty() {
		t.Fatalf("report should be empty: %+v", empty)
	}
	sb.Reset()
	if err := empty.WriteText(&sb); err != nil {
		t.Fatalf("WriteText(empty): %v", err)
	}
	if sb.String() != "No environment conflicts found for service 'nothing-here'\n" {
		t.Fatalf("empty render = %q", sb.String())
	}
}
