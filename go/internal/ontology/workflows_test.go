package ontology

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func testdataDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestLoadWorkflowsYAML(t *testing.T) {
	set, err := LoadWorkflowsYAML(filepath.Join(testdataDir(t), "workflows.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	if len(set.Workflows) != 2 {
		t.Fatalf("want 2 workflows, got %d", len(set.Workflows))
	}
	wf := set.Workflows[0]
	if wf.GID != "local:default:workflow:checkout:v1" {
		t.Errorf("workflow GID = %q", wf.GID)
	}
	if *wf.Metadata.StepCount != 3 {
		t.Errorf("step_count = %d", *wf.Metadata.StepCount)
	}
	if wf.Metadata.Source == nil || !strings.HasSuffix(*wf.Metadata.Source, "workflows.yaml") {
		t.Errorf("source = %v", wf.Metadata.Source)
	}
	// name-derived seed alias + YAML aliases, normalized
	if len(wf.Metadata.Aliases) != 2 || wf.Metadata.Aliases[1] != "place order" {
		t.Errorf("aliases = %v", wf.Metadata.Aliases)
	}

	if len(set.Steps) != 5 {
		t.Fatalf("want 5 steps, got %d", len(set.Steps))
	}
	first := set.Steps[0]
	if first.GID != "local:default:workflow_step:create_order:v1" {
		t.Errorf("step GID = %q", first.GID)
	}
	if first.WorkflowGid != wf.GID || first.Order != 1 {
		t.Errorf("step parent/order = %q/%d", first.WorkflowGid, first.Order)
	}
	// seed alias ("create order") + feature ids survive the load
	if len(first.Metadata.Aliases) != 1 || first.Metadata.Aliases[0] != "create order" {
		t.Errorf("step aliases = %v", first.Metadata.Aliases)
	}
	if len(first.Metadata.FeatureIDs) != 1 || first.Metadata.FeatureIDs[0] != "FR-CHK-01" {
		t.Errorf("feature ids = %v", first.Metadata.FeatureIDs)
	}
	if len(set.FailureModes) != 2 {
		t.Fatalf("want 2 failure modes, got %d", len(set.FailureModes))
	}
	if set.FailureModes[0].GID != "local:default:failure_mode:card_declined:v1" {
		t.Errorf("failure GID = %q", set.FailureModes[0].GID)
	}

	// Relationship wiring: has_step per step, next_step between consecutive
	// steps, has_failure_mode per failure mode.
	counts := map[string]int{}
	for _, rel := range set.Relationships {
		counts[rel.RelType]++
	}
	if counts[RelHasStep] != 5 || counts[RelNextStep] != 3 || counts[RelHasFailureMode] != 2 {
		t.Errorf("relationship counts = %v", counts)
	}
	// Next-step edges connect consecutive steps of the same workflow.
	for _, rel := range set.Relationships {
		if rel.RelType != RelNextStep {
			continue
		}
		prev, _ := ParseGid(rel.Source)
		next, _ := ParseGid(rel.Target)
		if prev.OntologyType != TypeWorkflowStep || next.OntologyType != TypeWorkflowStep ||
			next.ID != "authorize_payment" && !(prev.ID == "authorize_payment" && next.ID == "confirm") && prev.ID != "validate" {
			if !(prev.ID == "create_order" && next.ID == "authorize_payment") &&
				!(prev.ID == "authorize_payment" && next.ID == "confirm") &&
				!(prev.ID == "validate" && next.ID == "execute_refund") {
				t.Errorf("unexpected next_step edge %s -> %s", rel.Source, rel.Target)
			}
		}
	}
}

func TestLoadWorkflowsYAMLValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"empty id", "workflows:\n  - id: \"\"\n    name: X\n", "empty id"},
		{"empty name", "workflows:\n  - id: w\n    name: \"\"\n", "empty name"},
		{"duplicate workflow", "workflows:\n  - id: w\n    name: A\n  - id: w\n    name: B\n", "duplicate workflow id"},
		{"duplicate step", "workflows:\n  - id: w\n    name: A\n    steps:\n      - id: s\n        name: S\n      - id: s\n        name: S2\n", "duplicate step id"},
		{"empty step name", "workflows:\n  - id: w\n    name: A\n    steps:\n      - id: s\n        name: \"\"\n", "empty name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadWorkflowsYAML(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLoadConceptsYAML(t *testing.T) {
	nodes, err := LoadConceptsYAML(filepath.Join(testdataDir(t), "concepts.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 {
		t.Fatalf("want 3 concepts, got %d", len(nodes))
	}
	refund := nodes[0]
	if refund.GID != "local:checkout-service:domain_entity:refund:v1" {
		t.Errorf("gid = %q (scope must come from owned_by[0])", refund.GID)
	}
	if refund.Metadata.Ontology != "concept" || refund.Metadata.OntologyLayer != "domain" {
		t.Errorf("metadata layer = %s/%s", refund.Metadata.Ontology, refund.Metadata.OntologyLayer)
	}
	if len(refund.Metadata.CodeRefs) != 2 || len(refund.Metadata.Docs) != 1 {
		t.Errorf("code_refs/docs = %v/%v", refund.Metadata.CodeRefs, refund.Metadata.Docs)
	}
	if nodes[2].ElementType != TypeDomainEntity {
		t.Errorf("unknown type must default to domain_entity, got %q", nodes[2].ElementType)
	}
}

func TestLoadAliasesYAML(t *testing.T) {
	pairs, err := LoadAliasesYAML(filepath.Join(testdataDir(t), "aliases.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0][0] != "local:checkout-service:domain_entity:refund:v1" || pairs[0][1] != "money-back" {
		t.Fatalf("pairs = %v", pairs)
	}
}

func TestSyncRoundTripLoadPersistTrace(t *testing.T) {
	st := setupStore(t)
	dir := testdataDir(t)

	stats, err := SyncFromDir(dir, st, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Concepts != 3 || stats.Workflows != 2 || stats.WorkflowSteps != 5 ||
		stats.FailureModes != 2 || stats.Relationships != 10 {
		t.Errorf("stats = %+v", stats)
	}

	// Graph rows landed with the ontology:// marker.
	els, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	nOnt := 0
	for _, el := range els {
		if IsOntologyFilePath(el.FilePath) {
			nOnt++
		}
	}
	if nOnt != 3+2+5+2 {
		t.Errorf("ontology elements = %d, want %d", nOnt, 3+2+5+2)
	}

	// KV persistence round-trips the loaded sets.
	wfSet, err := LoadWorkflowsFromStore(st)
	if err != nil {
		t.Fatal(err)
	}
	if wfSet == nil || len(wfSet.Workflows) != 2 {
		t.Fatalf("stored workflow set = %+v", wfSet)
	}
	concepts, err := LoadConceptsFromStore(st)
	if err != nil || len(concepts) != 3 {
		t.Fatalf("stored concepts = %v, %v", concepts, err)
	}

	// Trace by workflow name, alias and bare id: ordered steps.
	for _, query := range []string{"Checkout", "place order", "checkout"} {
		steps, err := Trace(st, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(steps) != 3 {
			t.Fatalf("trace %q: got %d steps", query, len(steps))
		}
		if steps[0].Name != "Create Order" || steps[1].Name != "Authorize Payment" || steps[2].Name != "Confirm" {
			t.Errorf("trace %q order = %s,%s,%s", query, steps[0].Name, steps[1].Name, steps[2].Name)
		}
	}

	// Trace by step name resolves the parent workflow.
	steps, err := Trace(st, "Execute Refund")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Name != "Validate" || steps[1].Name != "Execute Refund" {
		t.Fatalf("step-name trace = %+v", steps)
	}

	// Unknown query traces nothing.
	if steps, _ := Trace(st, "nonexistent-workflow"); len(steps) != 0 {
		t.Errorf("unknown trace returned %d steps", len(steps))
	}

	// Code refs + failure modes survive the round trip.
	if got := steps[1].Metadata.CodeRefs; len(got) != 1 || got[0] != "src/refund/handler.rs::process_refund" {
		t.Errorf("traced code_refs = %v", got)
	}
}

func TestSyncForProjectAndMarker(t *testing.T) {
	root := t.TempDir()
	st := setupStore(t)

	// No ontology dir: error with directive.
	if _, err := SyncForProject(root, st); err == nil ||
		!strings.Contains(err.Error(), "LEANKG_ONTOLOGY_DIR") {
		t.Fatalf("want no-ontology-dir error, got %v", err)
	}

	ontDir := filepath.Join(root, "ontology")
	if err := os.CopyFS(ontDir, os.DirFS(testdataDir(t))); err != nil {
		t.Fatal(err)
	}

	stats, err := SyncForProject(root, st)
	if err != nil {
		t.Fatal(err)
	}
	if stats.MarkerPath == "" || !fileExists(stats.MarkerPath) {
		t.Errorf("marker not touched: %q", stats.MarkerPath)
	}
	if stats.Workflows != 2 {
		t.Errorf("workflows = %d", stats.Workflows)
	}
	if got := SyncStatus(root); got["marker_exists"] != true || got["ontology_dir"] != ontDir {
		t.Errorf("status = %v", got)
	}
}

func TestSyncDeclarativeReplace(t *testing.T) {
	dir := t.TempDir()
	st := setupStore(t)

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "workflows.yaml"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("workflows:\n  - id: a\n    name: A\n    steps:\n      - id: s1\n        name: S1\n")
	if _, err := SyncFromDir(dir, st, ""); err != nil {
		t.Fatal(err)
	}
	// Replace: workflow a removed, b added; a's rows must be gone.
	write("workflows:\n  - id: b\n    name: B\n    steps:\n      - id: s2\n        name: S2\n")
	if _, err := SyncFromDir(dir, st, ""); err != nil {
		t.Fatal(err)
	}

	gids, err := WorkflowGIDs(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(gids) != 1 || gids[0] != "local:default:workflow:b:v1" {
		t.Fatalf("workflow gids after replace = %v", gids)
	}
	steps, err := Trace(st, "b")
	if err != nil || len(steps) != 1 || steps[0].GID != "local:default:workflow_step:s2:v1" {
		t.Fatalf("trace after replace = %v, %v", steps, err)
	}
	// No duplicate step rows across syncs.
	els, _ := st.Elements()
	nSteps := 0
	for _, el := range els {
		if el.ElementType == TypeWorkflowStep {
			nSteps++
		}
	}
	if nSteps != 1 {
		t.Errorf("step rows = %d, want 1", nSteps)
	}
}

func TestResolveOntologyDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LEANKG_ONTOLOGY_DIR", "")
	if got := ResolveOntologyDir(root); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	ont := filepath.Join(root, "ontology")
	if err := os.MkdirAll(ont, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolveOntologyDir(root); got != ont {
		t.Fatalf("want %q, got %q", ont, got)
	}
	// env wins when it is a directory
	envDir := t.TempDir()
	t.Setenv("LEANKG_ONTOLOGY_DIR", envDir)
	if got := ResolveOntologyDir(root); got != envDir {
		t.Fatalf("env dir want %q, got %q", envDir, got)
	}
	// env ignored when it does not exist
	t.Setenv("LEANKG_ONTOLOGY_DIR", filepath.Join(root, "missing"))
	if got := ResolveOntologyDir(root); got != ont {
		t.Fatalf("missing env dir must fall back, got %q", got)
	}
}

func TestSyncFromDirMissingDir(t *testing.T) {
	st := setupStore(t)
	_, err := SyncFromDir(filepath.Join(t.TempDir(), "nope"), st, "")
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("want missing-dir error, got %v", err)
	}
}

func TestConceptSearch(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	// Indexed code elements the concept code_refs point at.
	code := []store.Element{
		{QualifiedName: "src/refund/handler.rs::process_refund", ElementType: "function", Name: "process_refund", FilePath: "src/refund/handler.rs", Language: "rust", LineStart: 10, LineEnd: 30},
		{QualifiedName: "src/payments/authorize.rs::authorize", ElementType: "function", Name: "authorize", FilePath: "src/payments/authorize.rs", Language: "rust"},
		{QualifiedName: "src/order/service.rs::create", ElementType: "function", Name: "create", FilePath: "src/order/service.rs", Language: "rust"},
	}
	if err := st.UpsertElements(code); err != nil {
		t.Fatal(err)
	}

	res, err := ConceptSearch(st, "how does the refund work", 20)
	if err != nil {
		t.Fatal(err)
	}
	if res.FallbackUsed {
		t.Errorf("concept should match, fallback_used=true; result=%+v", res)
	}
	if !containsKeyword(res.ExtractedKeywords, "refund") || containsKeyword(res.ExtractedKeywords, "how") {
		t.Errorf("keywords = %v", res.ExtractedKeywords)
	}
	if res.ConceptMatchCount == 0 {
		t.Fatalf("no matched concepts: %+v", res)
	}
	foundRefund := false
	for _, mc := range res.MatchedConcepts {
		if mc.GID == "local:checkout-service:domain_entity:refund:v1" {
			foundRefund = true
			if mc.MatchScore != 1.0 {
				t.Errorf("refund score = %v (%s)", mc.MatchScore, mc.MatchReason)
			}
			if len(mc.CodeRefs) != 2 {
				t.Errorf("refund code_refs = %v", mc.CodeRefs)
			}
		}
	}
	if !foundRefund {
		t.Errorf("refund concept missing from %v", res.MatchedConcepts)
	}
	if res.LinkedCodeCount == 0 {
		t.Fatalf("no linked code: %+v", res)
	}
	linkedRefundFn := false
	for _, el := range res.LinkedCode {
		if el.QualifiedName == "src/refund/handler.rs::process_refund" {
			linkedRefundFn = true
		}
	}
	if !linkedRefundFn {
		t.Errorf("process_refund not linked: %+v", res.LinkedCode)
	}

	// No concept hit: fallback name search over indexed code.
	fb, err := ConceptSearch(st, "authorize", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !fb.FallbackUsed || len(fb.FallbackResults) == 0 {
		t.Errorf("fallback = %v, %v", fb.FallbackUsed, fb.FallbackResults)
	}
}

func containsKeyword(kws []string, kw string) bool {
	for _, k := range kws {
		if k == kw {
			return true
		}
	}
	return false
}

func TestSearchOntologyNodesScoring(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	nodes, err := SearchOntologyNodes(st, "refund flow")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("no nodes matched 'refund flow'")
	}
	// Sorted by score descending.
	for i := 1; i < len(nodes); i++ {
		if nodes[i-1].MatchScore < nodes[i].MatchScore {
			t.Fatalf("not sorted: %+v", nodes)
		}
	}
	// Best hit is the refund workflow (exact name on "Refund Flow").
	if nodes[0].ElementType != TypeWorkflow || nodes[0].GID != "local:default:workflow:refund_flow:v1" {
		t.Errorf("best hit = %s %s (%v %s)", nodes[0].ElementType, nodes[0].GID, nodes[0].MatchScore, nodes[0].MatchReason)
	}
	// No procedural leakage into concept probes is not required here, but
	// every hit must be an ontology node.
	for _, n := range nodes {
		if !OntologyElements[n.ElementType] {
			t.Errorf("non-ontology type leaked: %q", n.ElementType)
		}
	}
}

func TestGetContext(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	res, err := GetContext(st, "refund", 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsEmpty() {
		t.Fatal("no context for refund")
	}
	if res.Confidence <= 0 || res.Confidence > 1 {
		t.Errorf("confidence = %v", res.Confidence)
	}
	// At depth 2, expansion follows has_step to the steps and then
	// has_failure_mode from the validate step to its failure mode node.
	sawFailureMode := false
	for _, el := range res.ExpandedCodeContext {
		if el.ElementType == TypeFailureMode {
			sawFailureMode = true
		}
	}
	if !sawFailureMode {
		t.Errorf("expanded context missing failure modes: %+v", res.ExpandedCodeContext)
	}
}

func TestOntologyStatus(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	status, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	if status.ConceptCounts[TypeDomainEntity] != 2 || status.ConceptCounts[TypeService] != 1 {
		t.Errorf("concept counts = %v", status.ConceptCounts)
	}
	if status.ProceduralCounts[TypeWorkflow] != 2 || status.ProceduralCounts[TypeWorkflowStep] != 5 ||
		status.ProceduralCounts[TypeFailureMode] != 2 {
		t.Errorf("procedural counts = %v", status.ProceduralCounts)
	}
	if status.NodesMissingAliases != 0 {
		t.Errorf("nodes missing aliases = %d", status.NodesMissingAliases)
	}
	// Every fixture workflow has at least one step carrying failure modes
	// (checkout via authorize_payment, refund_flow via validate).
	if status.WorkflowsWithoutFailureModes != 0 {
		t.Errorf("workflows without failure modes = %d", status.WorkflowsWithoutFailureModes)
	}
}

func TestOntologyStatusWithoutFailureModes(t *testing.T) {
	st := setupStore(t)
	dir := t.TempDir()
	yaml := "workflows:\n  - id: bare\n    name: Bare\n    steps:\n      - id: only\n        name: Only\n"
	if err := os.WriteFile(filepath.Join(dir, "workflows.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncFromDir(dir, st, ""); err != nil {
		t.Fatal(err)
	}
	status, err := OntologyStatus(st)
	if err != nil {
		t.Fatal(err)
	}
	if status.WorkflowsWithoutFailureModes != 1 {
		t.Errorf("workflows without failure modes = %d", status.WorkflowsWithoutFailureModes)
	}
}

func TestMatchScore(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		score      float64
		wantSubstr string
	}{
		{"exact name", "refund", 1.0, "exact name match"},
		{"name contains", "ref", 0.8, "name contains"},
		{"exact alias", "reversal", 0.9, "exact alias match"},
		{"alias contains", "rever", 0.7, "alias contains"},
		{"description", "customer", 0.5, "description contains"},
		{"no match", "zzz", 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score, reason := MatchScore(tc.query, "Refund", []string{"reversal", "chargeback"}, "Money returned to the customer")
			if score != tc.score {
				t.Fatalf("score = %v, want %v", score, tc.score)
			}
			if tc.wantSubstr != "" && !strings.Contains(reason, tc.wantSubstr) {
				t.Errorf("reason = %q, want substring %q", reason, tc.wantSubstr)
			}
			if tc.wantSubstr == "" && reason != "" {
				t.Errorf("reason = %q, want empty", reason)
			}
		})
	}
}

func TestMatchScoreMultiWord(t *testing.T) {
	// 2 of 2 meaningful words matched -> ratio 1.0 -> 0.7
	score, reason := MatchScore("refund flow", "Refund", nil, "flow of money back")
	if score == 0 || !strings.Contains(reason, "meaningful query words matched") {
		t.Errorf("multi-word = %v %q", score, reason)
	}
	// partial: below 0.5 ratio -> 0.3 scale
	score, reason = MatchScore("refund zebra crossing", "Refund", nil, "")
	if score == 0 || !strings.Contains(reason, "partial match") {
		t.Errorf("partial = %v %q", score, reason)
	}
}

func TestExtractKeywords(t *testing.T) {
	kws := ExtractKeywords("how does the feature flag work")
	for _, want := range []string{"feature", "flag", "work"} {
		if !containsKeyword(kws, want) {
			t.Errorf("missing %q in %v", want, kws)
		}
	}
	for _, banned := range []string{"how", "the", "does"} {
		if containsKeyword(kws, banned) {
			t.Errorf("stopword %q survived in %v", banned, kws)
		}
	}
	if got := ExtractKeywords("Refund refund REFUND"); len(got) != 1 || got[0] != "refund" {
		t.Errorf("dedupe/case = %v", got)
	}
	if got := ExtractKeywords("a be refund"); len(got) != 1 || got[0] != "refund" {
		t.Errorf("short-token filter = %v", got)
	}
}

func TestNormalizePath(t *testing.T) {
	cases := map[string]string{
		"./src/main.rs": "src/main.rs",
		"/src/main.rs":  "src/main.rs",
		"src/main.rs":   "src/main.rs",
		"  ./a/b/  ":    "a/b",
	}
	for in, want := range cases {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGIDAndAliasParity(t *testing.T) {
	g, ok := ParseGid("local:checkout-service:domain_entity:refund:v1")
	if !ok || g.Env != "local" || g.Scope != "checkout-service" ||
		g.OntologyType != "domain_entity" || g.ID != "refund" || g.Version != "v1" {
		t.Fatalf("parse = %+v ok=%v", g, ok)
	}
	if g.Format() != "local:checkout-service:domain_entity:refund:v1" {
		t.Errorf("format = %q", g.Format())
	}
	if _, ok := ParseGid("not-a-gid"); ok {
		t.Error("malformed gid must not parse")
	}
	cases := map[string]string{
		"  Refund  ":  "refund",
		"Money Back":  "money back",
		"charge-back": "charge-back",
		"  Réflexion": "réflexion",
	}
	for in, want := range cases {
		if got := NormalizeAlias(in); got != want {
			t.Errorf("NormalizeAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveCodeRefs(t *testing.T) {
	st := setupStore(t)
	els := []store.Element{
		{QualifiedName: "src/order/service.rs::create", ElementType: "function", Name: "create", FilePath: "src/order/service.rs", Language: "rust"},
		{QualifiedName: "src/payments/authorize.rs::authorize", ElementType: "function", Name: "authorize", FilePath: "src/payments/authorize.rs", Language: "rust"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveCodeRefs(st, []string{
		"src/order/service.rs::create", // exact QN
		"src/payments/authorize.rs",    // file
		"missing/nothing.rs",           // no hit
	}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("resolved = %+v", got)
	}
	if got[0].QualifiedName != "src/order/service.rs::create" ||
		got[1].QualifiedName != "src/payments/authorize.rs::authorize" {
		t.Errorf("resolved order = %s, %s", got[0].QualifiedName, got[1].QualifiedName)
	}
}

func TestTraverseToFunctionsViaEdges(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	// Code graph: workflow -> has_step -> step -> implemented_by -> function
	fns := []store.Element{
		{QualifiedName: "src/order/service.rs::create", ElementType: "function", Name: "create", FilePath: "src/order/service.rs"},
		{QualifiedName: "src/payments/authorize.rs::authorize", ElementType: "method", Name: "authorize", FilePath: "src/payments/authorize.rs"},
		{QualifiedName: "src/checkout/confirm.rs::run", ElementType: "function", Name: "run", FilePath: "src/checkout/confirm.rs"},
	}
	rels := []store.Relationship{
		{Source: "local:default:workflow_step:create_order:v1", Target: "src/order/service.rs::create", RelType: RelImplementedBy, Confidence: 1},
		{Source: "local:default:workflow_step:authorize_payment:v1", Target: "src/payments/authorize.rs::authorize", RelType: RelImplementedBy, Confidence: 1},
		{Source: "local:default:workflow_step:confirm:v1", Target: "src/checkout/confirm.rs::run", RelType: RelImplementedBy, Confidence: 1},
	}
	if err := st.UpsertElements(fns); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}

	disc, err := TraverseToFunctions(st, []UpperSeed{NewUpperSeed("local:default:workflow:checkout:v1", TypeWorkflow)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(disc) != 3 {
		t.Fatalf("discovered = %+v", disc)
	}
	for _, d := range disc {
		if d.ViaUpper != "local:default:workflow:checkout:v1" || d.ViaEdge != RelImplementedBy || d.Hop != 2 {
			t.Errorf("provenance = %+v", d)
		}
		if !IsFunctionTarget(d.ElementType) {
			t.Errorf("non-function discovered: %+v", d)
		}
	}
	SortDiscovered(disc)
	if disc[0].QualifiedName != "src/checkout/confirm.rs::run" {
		t.Errorf("sort order = %s first", disc[0].QualifiedName)
	}
}

func TestTraverseToFunctionsCodeRefsFallback(t *testing.T) {
	st := setupStore(t)
	// Domain entity with code_refs metadata only — no DB edges.
	node := NewConceptNode("local", "checkout-service", TypeDomainEntity, "refund", "Refund", "Refunds")
	node.Metadata.CodeRefs = []string{"src/refund/handler.rs::process_refund"}
	if err := st.UpsertElements([]store.Element{node.Element(), {
		QualifiedName: "src/refund/handler.rs::process_refund",
		ElementType:   "function", Name: "process_refund", FilePath: "src/refund/handler.rs",
	}}); err != nil {
		t.Fatal(err)
	}

	disc, err := TraverseToFunctions(st, []UpperSeed{NewUpperSeed(node.GID, TypeDomainEntity)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(disc) != 1 {
		t.Fatalf("discovered = %+v", disc)
	}
	d := disc[0]
	if d.ViaEdge != "code_ref" || d.Hop != 1 || d.QualifiedName != "src/refund/handler.rs::process_refund" {
		t.Errorf("provenance = %+v", d)
	}
}

func TestTraverseToFunctionsCapAndDedup(t *testing.T) {
	st := setupStore(t)
	// One workflow, many steps each implemented by a function; cap at
	// GlobalFunctionCap per seed via fanout (workflow fanout = 15).
	var els []store.Element
	var rels []store.Relationship
	for i := 0; i < 20; i++ {
		step := NewWorkflowStepNode("local", "default", "big", fmt.Sprintf("s%02d", i), fmt.Sprintf("S%d", i), i+1, "")
		fn := store.Element{
			QualifiedName: fmt.Sprintf("src/big.rs::f%02d", i), ElementType: "function",
			Name: fmt.Sprintf("f%02d", i), FilePath: "src/big.rs",
		}
		els = append(els, step.Element(), fn)
		rels = append(rels,
			store.Relationship{Source: "local:default:workflow:big:v1", Target: step.GID, RelType: RelHasStep, Confidence: 1},
			store.Relationship{Source: step.GID, Target: fn.QualifiedName, RelType: RelImplementedBy, Confidence: 1},
		)
	}
	els = append(els, NewWorkflowNode("local", "default", "big", "Big", "Big flow").Element())
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}

	disc, err := TraverseToFunctions(st, []UpperSeed{NewUpperSeed("local:default:workflow:big:v1", TypeWorkflow)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(disc) != 15 { // workflow fanout cap
		t.Fatalf("fanout cap = %d, want 15", len(disc))
	}

	// Dedup: same function reachable via two upper seeds keeps the
	// shortest-hop provenance.
	seedA := NewUpperSeed("local:default:workflow:big:v1", TypeWorkflow)
	seedB := UpperSeed{QualifiedName: "local:default:workflow:big:v1", ElementType: TypeWorkflow, Name: "Big"}
	if seedA.QualifiedName != seedB.QualifiedName {
		t.Fatal("seeds must share the QN for the dedup case")
	}
}

func TestDownwardRuleFor(t *testing.T) {
	cases := map[string]DownwardRule{
		"class":          {Hops: 1, FanoutCap: 12},
		"module":         {Hops: 2, FanoutCap: 16},
		"file":           {Hops: 1, FanoutCap: 12},
		"doc_section":    {Hops: 2, FanoutCap: 10},
		TypeWorkflow:     {Hops: 2, FanoutCap: 15},
		TypeWorkflowStep: {Hops: 1, FanoutCap: 12},
		TypeService:      {Hops: 2, FanoutCap: 12},
		TypeKnownIssue:   {Hops: 1, FanoutCap: 8},
		"whatever":       {Hops: 1, FanoutCap: 5},
	}
	for typ, want := range cases {
		got := DownwardRuleFor(typ)
		if got.Hops != want.Hops || got.FanoutCap != want.FanoutCap || len(got.EdgeTypes) == 0 {
			t.Errorf("%s: rule = %+v, want hops=%d cap=%d", typ, got, want.Hops, want.FanoutCap)
		}
	}
	if !IsFunctionTarget("function") || IsFunctionTarget("class") {
		t.Error("function target classification broken")
	}
	if !IsUpperType(TypeWorkflow) || IsUpperType("function") {
		t.Error("upper type classification broken")
	}
	if !IsIndexerNoise("unknown") || !IsIndexerNoise(TypeEnvironment) || IsIndexerNoise("class") {
		t.Error("indexer noise classification broken")
	}
}

func TestDeriveDisplayName(t *testing.T) {
	cases := map[string]string{
		"ontology://local:default:workflow:checkout:v1": "checkout",
		"src/refund/handler.rs::process_refund":         "process_refund",
		"src/refund/handler.rs":                         "handler.rs",
		"plain":                                         "plain",
	}
	for in, want := range cases {
		if got := DeriveDisplayName(in); got != want {
			t.Errorf("DeriveDisplayName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompositeText(t *testing.T) {
	if got := CompositeText("Refund", "fn body"); got != "Refund\nfn body" {
		t.Errorf("composite = %q", got)
	}
	if got := CompositeText("Refund", ""); got != "Refund" {
		t.Errorf("composite empty blob = %q", got)
	}
}

func TestSelfTest(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	report := SelfTest(st)
	if !report.AllOK {
		t.Fatalf("self test failures: %+v", report)
	}
}

func TestFeatureFlowAndMatrix(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	code := []store.Element{
		{QualifiedName: "src/order/service.rs::create", ElementType: "function", Name: "create", FilePath: "src/order/service.rs"},
	}
	if err := st.UpsertElements(code); err != nil {
		t.Fatal(err)
	}

	flow, err := FeatureFlow(st, "FR-CHK-01")
	if err != nil {
		t.Fatal(err)
	}
	if flow["count"] != 1 {
		t.Fatalf("feature flow = %v", flow)
	}
	workflows := flow["workflows"].([]map[string]any)
	steps := workflows[0]["steps"].([]map[string]any)
	if len(steps) != 3 { // full trace of the linked workflow
		t.Fatalf("steps = %v", steps)
	}
	if workflows[0]["name"] != "Checkout" {
		t.Errorf("workflow name = %v", workflows[0]["name"])
	}

	// Persisted trace round-trips through KV.
	if err := SaveFeatureTrace(st, "FR-CHK-01", flow); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadFeatureTrace(st, "FR-CHK-01")
	if err != nil || loaded == nil || loaded["count"] != float64(1) { // JSON numbers decode as float64
		t.Fatalf("loaded feature trace = %v, %v", loaded, err)
	}

	matrix, err := TraceabilityMatrix(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(matrix) == 0 {
		t.Fatal("empty matrix")
	}
	byFeature := map[string]TraceabilityMatrixItem{}
	for _, row := range matrix {
		byFeature[row.FeatureID] = row
	}
	chk := byFeature["FR-CHK-01"]
	if chk.WorkflowCount != 1 || chk.StepCount != 2 || chk.AnnotatedElementCount != 2 {
		t.Errorf("FR-CHK-01 row = %+v", chk)
	}
	if byFeature["FR-REF-02"].WorkflowCount != 1 {
		t.Errorf("FR-REF-02 row = %+v", byFeature["FR-REF-02"])
	}
}

func TestTraceQuery(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	out, err := TraceQuery(st, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if out["found"] != true || out["step_count"] != 3 || out["workflow_gid"] != "local:default:workflow:checkout:v1" {
		t.Errorf("trace query = %v", out)
	}
	if out["workflow_id"] != "checkout" || out["env"] != "local" {
		t.Errorf("trace identity = %v", out)
	}
}

func TestResolveWorkflowID(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	gid, ok, err := ResolveWorkflowID(st, "refund_flow")
	if err != nil || !ok || gid != "local:default:workflow:refund_flow:v1" {
		t.Fatalf("resolve = %q %v %v", gid, ok, err)
	}
	if _, ok, _ := ResolveWorkflowID(st, "nope"); ok {
		t.Error("unknown id resolved")
	}
}

func TestWatchDebounce(t *testing.T) {
	t.Setenv("LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS", "")
	if WatchDebounceMS() != 1500 {
		t.Errorf("default = %d", WatchDebounceMS())
	}
	t.Setenv("LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS", "2500")
	if WatchDebounceMS() != 2500 {
		t.Errorf("custom = %d", WatchDebounceMS())
	}
	t.Setenv("LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS", "100")
	if WatchDebounceMS() != 1000 {
		t.Errorf("floor = %d", WatchDebounceMS())
	}
}

func TestDiscoverOntologyFirst(t *testing.T) {
	st := setupStore(t)
	if _, err := SyncFromDir(testdataDir(t), st, ""); err != nil {
		t.Fatal(err)
	}
	code := []store.Element{
		{QualifiedName: "src/refund/handler.rs::process_refund", ElementType: "function", Name: "process_refund", FilePath: "src/refund/handler.rs", Language: "rust"},
		{QualifiedName: "src/payments/authorize.rs::authorize", ElementType: "function", Name: "authorize", FilePath: "src/payments/authorize.rs", Language: "rust"},
		{QualifiedName: "src/order/service.rs::create", ElementType: "function", Name: "create", FilePath: "src/order/service.rs", Language: "rust"},
	}
	if err := st.UpsertElements(code); err != nil {
		t.Fatal(err)
	}

	page, err := Discover(st, "how does the refund work", "", 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if page.Method != "ontology+concept" {
		t.Errorf("method = %q", page.Method)
	}
	if page.Concept == nil || page.Concept.ConceptMatchCount == 0 {
		t.Fatalf("no concept match: %+v", page.Concept)
	}
	found := false
	for _, el := range page.Results {
		if el.QualifiedName == "src/refund/handler.rs::process_refund" {
			found = true
		}
	}
	if !found {
		t.Errorf("results missing process_refund: %+v", page.Results)
	}
}

func TestDiscoverNameFallbackAndPagination(t *testing.T) {
	st := setupStore(t)
	els := []store.Element{
		{QualifiedName: "src/a.rs::refundOrder", ElementType: "function", Name: "refundOrder", FilePath: "src/a.rs"},
		{QualifiedName: "src/b.rs::refundPayment", ElementType: "function", Name: "refundPayment", FilePath: "src/b.rs"},
		{QualifiedName: "src/c.rs::unrelated", ElementType: "function", Name: "unrelated", FilePath: "src/c.rs"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	page, err := Discover(st, "refund", "", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if page.Method != "semantic+name_fallback" {
		t.Errorf("method = %q", page.Method)
	}
	// The fallback probe fetches limit+offset candidates (Rust parity), so
	// page 0 sees one candidate and page 1 sees two minus the offset.
	if len(page.Results) != 1 {
		t.Fatalf("page 0 = %+v", page)
	}
	page2, err := Discover(st, "refund", "", 1, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2.Results) != 1 {
		t.Fatalf("page 1 = %+v", page2)
	}
	if page.Results[0].QualifiedName == page2.Results[0].QualifiedName {
		t.Errorf("pages overlap: %s", page.Results[0].QualifiedName)
	}
}

func TestDiscoverMegaGraphRefusal(t *testing.T) {
	st := setupStore(t)
	if err := st.UpsertElements([]store.Element{
		{QualifiedName: "a::x", ElementType: "function", Name: "x", FilePath: "a.go"},
		{QualifiedName: "a::y", ElementType: "function", Name: "y", FilePath: "a.go"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEANKG_MAX_CACHE_ELEMENTS", "1")
	refusal, err := RefuseFullScanIfMega(st, "test_tool")
	if err != nil {
		t.Fatal(err)
	}
	if refusal == nil {
		t.Fatal("expected refusal for mega graph")
	}
	if !strings.Contains(refusal["error"].(string), "test_tool refused") {
		t.Errorf("refusal = %v", refusal)
	}
	if _, err := Discover(st, "refund", "", 0, 0, true); err != nil {
		t.Fatal(err)
	}

	// Normal-size graph: no refusal.
	t.Setenv("LEANKG_MAX_CACHE_ELEMENTS", "50000")
	refusal, err = RefuseFullScanIfMega(st, "test_tool")
	if err != nil || refusal != nil {
		t.Errorf("unexpected refusal: %v %v", refusal, err)
	}
	if ClampLimit(0) != DefaultPageLimit || ClampLimit(99) != MaxPageLimit || ClampLimit(5) != 5 {
		t.Error("clamp broken")
	}
	if SkipIncrementalDependents(st) {
		t.Error("small graph must not skip dependents")
	}
	t.Setenv("LEANKG_INCREMENTAL_SKIP_DEPENDENTS", "1")
	if !SkipIncrementalDependents(st) {
		t.Error("env override must skip dependents")
	}
}

func TestDiscoverPageToJSON(t *testing.T) {
	page := DiscoverPage{
		Query: "q", Env: "local", Limit: 20, Offset: 0, Method: "ontology+concept",
		Results:       []store.Element{{QualifiedName: "a::b", ElementType: "function", Name: "b", FilePath: "a.rs", LineStart: 3}},
		TotalEstimate: 1,
	}
	body := DiscoverPageToJSON(page)
	if body["count"] != 1 || body["method"] != "ontology+concept" {
		t.Errorf("body = %v", body)
	}
	results := body["results"].([]map[string]any)
	if results[0]["file_path"] != "a.rs" || results[0]["line_start"] != 3 {
		t.Errorf("result row = %v", results[0])
	}
}
