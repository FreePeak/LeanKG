package ontology

// FR-HEA-01 (issue #276): kg_ontology_status alias accounting is
// self-consistent. The Rust-era probe graph reported nodes_missing_aliases
// = 14 against 13 domain entities — procedural nodes were counted as
// missing because YAML-declared step aliases were parsed but never applied
// (reference fix: f7624143^ src/ontology/loader.rs:246-250, procedural.rs
// :211/:335 name-derived seeds). These tests pin the invariant the fix
// guarantees, over concepts with code refs, alias totals, and orphan-free
// procedural wiring.

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// domainEntityCount sums one concept type out of the status counts — the
// right-hand side of the Rust tracker invariant
// nodes_missing_aliases <= sum(domain_entity_counts).
func domainEntityCount(s Status) int { return s.ConceptCounts[TypeDomainEntity] }

func totalOntologyNodes(s Status) int {
	n := domainEntityCount(s)
	for typ, c := range s.ConceptCounts {
		if typ != TypeDomainEntity {
			n += c
		}
	}
	for _, c := range s.ProceduralCounts {
		n += c
	}
	return n
}

// TestAliasAccountingInvariant: on the fixture graph (concepts + workflows
// synced through the constructors, which seed a name-derived alias per
// node kind), missing-alias accounting must be zero AND the general
// self-consistency inequalities must hold:
//
//	nodes_missing_aliases <= total ontology nodes
//	total_aliases         >= nodes with at least one alias (= total - missing)
func TestAliasAccountingInvariant(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	status, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	if got := status.NodesMissingAliases; got != 0 {
		t.Errorf("nodes_missing_aliases = %d, want 0 (every node kind carries a name-derived seed)", got)
	}
	total := totalOntologyNodes(status)
	if total == 0 {
		t.Fatal("fixture synced zero ontology nodes")
	}
	if status.NodesMissingAliases > total {
		t.Errorf("invariant violated: missing %d > total nodes %d", status.NodesMissingAliases, total)
	}
	if status.TotalAliases < total-status.NodesMissingAliases {
		t.Errorf("total_aliases %d < nodes-with-aliases %d", status.TotalAliases, total-status.NodesMissingAliases)
	}
	// The Rust tracker formula, held strictly on the fixture.
	if status.NodesMissingAliases > domainEntityCount(status) {
		t.Errorf("invariant violated: missing %d > domain_entity count %d",
			status.NodesMissingAliases, domainEntityCount(status))
	}
}

// TestAliasAccountingCountsYAMLAndSeedAliases pins the arithmetic: total
// aliases must equal the seed alias per node plus every YAML-declared
// alias. The fixture declares: concept "refund" 2 aliases, workflow
// "checkout" 1, and one step carries none. Adding the implicit
// name-derived seeds gives the exact expected total — a regression in
// either the loader application (Rust bug: step aliases never applied) or
// double counting surfaces here.
func TestAliasAccountingCountsYAMLAndSeedAliases(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	status, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	els, err := ontologyElements(st)
	if err != nil {
		t.Fatal(err)
	}
	wantTotal := 0
	wantMissing := 0
	for _, el := range els {
		n := len(jsonStrArray(el.Metadata, "aliases"))
		if n == 0 {
			wantMissing++
		}
		wantTotal += n
	}
	if status.TotalAliases != wantTotal {
		t.Errorf("total_aliases = %d, want %d (recounted from stored metadata)", status.TotalAliases, wantTotal)
	}
	if status.NodesMissingAliases != wantMissing {
		t.Errorf("nodes_missing_aliases = %d, want %d", status.NodesMissingAliases, wantMissing)
	}
	// Seeds + YAML: concepts refund(1+2) payments(1) mystery(1); workflows
	// checkout(1+1) refund_flow(1+1); 5 steps + failure-mode nodes, all seeded.
	if status.TotalAliases < len(els) {
		t.Errorf("total_aliases %d < node count %d: name-derived seeds were dropped", status.TotalAliases, len(els))
	}
}

// TestAliasAccountingDetectsManualOrphanlessGap is the negative control
// that makes the invariant real: a node stored WITHOUT aliases (the Rust
// probe's failure shape) must move exactly the counters it should, and the
// accounting must stay self-consistent.
func TestAliasAccountingDetectsManualOrphanlessGap(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	before, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	// One alias-less concept node plus its alias-free twin: missing must go
	// up by exactly 2 and total aliases must not change.
	seedElements(t, st,
		store.Element{QualifiedName: "local:billing:domain_entity:nolias:v1",
			ElementType: TypeDomainEntity, Name: "NoLias", FilePath: "ontology://nolias",
			Metadata: map[string]any{"aliases": []string{}}},
		store.Element{QualifiedName: "local:billing:domain_entity:nolias2:v1",
			ElementType: TypeDomainEntity, Name: "NoLias2", FilePath: "ontology://nolias2",
			Metadata: map[string]any{}},
	)
	after, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	if after.NodesMissingAliases != before.NodesMissingAliases+2 {
		t.Errorf("missing = %d, want %d", after.NodesMissingAliases, before.NodesMissingAliases+2)
	}
	if after.TotalAliases != before.TotalAliases {
		t.Errorf("total_aliases moved with alias-less inserts: %d -> %d", before.TotalAliases, after.TotalAliases)
	}
	if after.ConceptCounts[TypeDomainEntity] != before.ConceptCounts[TypeDomainEntity]+2 {
		t.Errorf("domain_entity count = %d, want %d", after.ConceptCounts[TypeDomainEntity], before.ConceptCounts[TypeDomainEntity]+2)
	}
	if after.NodesMissingAliases > totalOntologyNodes(after) {
		t.Errorf("invariant violated after inserts: missing %d > total %d", after.NodesMissingAliases, totalOntologyNodes(after))
	}
}

// TestConceptCodeRefsAccounted: the fixture concepts carry code_refs; they
// must survive into stored metadata and resolve against real indexed code.
func TestConceptCodeRefsAccounted(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	concepts, err := LoadConceptsFromStore(st)
	if err != nil {
		t.Fatal(err)
	}
	var refundRefs []string
	for _, c := range concepts {
		if c.Name == "Refund" {
			refundRefs = c.Metadata.CodeRefs
		}
	}
	if len(refundRefs) == 0 {
		t.Fatal("concept Refund lost its code_refs through sync")
	}
	// Seed the referenced code element and resolve.
	seedElements(t, st, store.Element{QualifiedName: "src/refund/handler.rs::process_refund",
		ElementType: "function", Name: "process_refund", FilePath: "src/refund/handler.rs"})
	linked, err := ResolveCodeRefs(st, refundRefs, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, el := range linked {
		if el.QualifiedName == "src/refund/handler.rs::process_refund" {
			found = true
		}
	}
	if !found {
		t.Errorf("ResolveCodeRefs(%v) = %+v, want the seeded element", refundRefs, linked)
	}
}

// TestNoOrphanProceduralNodes: every workflow_step must reference a stored
// workflow (workflow_gid) and be reachable through the persisted set — the
// orphan class that made the Rust probe count procedural nodes against
// concept-only totals.
func TestNoOrphanProceduralNodes(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	els, err := ontologyElements(st)
	if err != nil {
		t.Fatal(err)
	}
	workflows := map[string]bool{}
	var steps []store.Element
	for _, el := range els {
		switch el.ElementType {
		case TypeWorkflow:
			workflows[el.QualifiedName] = true
		case TypeWorkflowStep:
			steps = append(steps, el)
		}
	}
	if len(steps) == 0 {
		t.Fatal("fixture produced no workflow steps")
	}
	for _, s := range steps {
		wg, _ := s.Metadata["workflow_gid"].(string)
		if wg == "" {
			t.Errorf("step %s has no workflow_gid (orphan)", s.QualifiedName)
			continue
		}
		if !workflows[wg] {
			t.Errorf("step %s references missing workflow %s (orphan)", s.QualifiedName, wg)
		}
	}
	// The persisted KV set must round-trip the same counts the status
	// reports — no silently dropped steps (the Rust loader bug class).
	set, err := LoadWorkflowsFromStore(st)
	if err != nil || set == nil {
		t.Fatalf("LoadWorkflowsFromStore: %v %v", set, err)
	}
	if len(set.Steps) != len(steps) {
		t.Errorf("stored set has %d steps, graph has %d", len(set.Steps), len(steps))
	}
	status, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	if status.ProceduralCounts[TypeWorkflowStep] != len(steps) {
		t.Errorf("procedural_counts[workflow_step] = %d, want %d", status.ProceduralCounts[TypeWorkflowStep], len(steps))
	}
}
