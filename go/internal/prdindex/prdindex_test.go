package prdindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// fixturePRD exercises every extraction form: table rows (with compound ids),
// definition blocks with priorities, AC lines, code paths and prose mentions.
const fixturePRD = `# Product requirements

## Milestone A

| ID | Priority | Focus | Intent |
|----|----------|-------|--------|
| US-ONT-PROC-01 | Must Have | **P0** | Procedural ontology stays fresh while LeanKG is in use |
| FR-ONT-PROC-01 | Must Have | **P0** | Watch ` + "`ontology/workflows.yaml`" + ` during MCP/serve |
| US-SURF-01 / FR-SURF-01 / FR-SURF-02 | Must Have | **P1** | Fix ` + "`semantic_search`" + ` dual-path docstring |

**FR-ZCP-02 — Lazy auto-attach (Must Have, P0)**

- First query against an unindexed repo answers from what exists and kicks off background indexing.
- Never block a query on indexing; surface state via freshness.
- AC: a query against an unindexed repo returns an immediate non-error response.
- AC: background index completes within the SLA; the second query hits the graph.

**US-SM-02 — Harness memory recall (Should Have, P1)**

- The assistant recalls prior session memory through the ` + "`internal/memory/banks.go`" + ` banks.
- AC: recall injects at most 8 memories.

## Milestone B

Notes referencing ` + "`src/web/handlers.rs::api_graph_expand_service`" + ` and FR-UI2-13 in prose.
`

// setupStore opens a migrated SQLite store under a fresh temp dir.
func setupStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st, dir
}

func findReq(t *testing.T, res Result, id string) Requirement {
	t.Helper()
	for _, r := range res.Requirements {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("requirement %s not parsed; got %v", id, idsOf(res))
	return Requirement{}
}

func findUS(t *testing.T, res Result, id string) UserStory {
	t.Helper()
	for _, s := range res.UserStories {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("user story %s not parsed; got %v", id, idsUSOf(res))
	return UserStory{}
}

func idsOf(res Result) []string {
	out := make([]string, 0, len(res.Requirements))
	for _, r := range res.Requirements {
		out = append(out, r.ID)
	}
	return out
}

func idsUSOf(res Result) []string {
	out := make([]string, 0, len(res.UserStories))
	for _, s := range res.UserStories {
		out = append(out, s.ID)
	}
	return out
}

func TestParseTableRows(t *testing.T) {
	res := Parse(fixturePRD)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	for _, id := range []string{"FR-ONT-PROC-01", "FR-SURF-01", "FR-SURF-02"} {
		if findReq(t, res, id).ID != id {
			t.Fatal("unreachable")
		}
	}
	for _, id := range []string{"US-ONT-PROC-01", "US-SURF-01"} {
		if findUS(t, res, id).ID != id {
			t.Fatal("unreachable")
		}
	}
}

// Port of the Rust test_parse_prd_table_rows / test_extract_code_paths pair:
// table rows carry their priority, focus, description and backtick code paths.
func TestParseTableRowFieldsAndCodePaths(t *testing.T) {
	res := Parse(fixturePRD)
	r := findReq(t, res, "FR-ONT-PROC-01")
	if r.Priority != "Must Have" || r.Focus != "P0" {
		t.Errorf("priority/focus = %q/%q, want Must Have/P0", r.Priority, r.Focus)
	}
	if !strings.Contains(r.Description, "ontology/workflows.yaml") {
		t.Errorf("description = %q, want it to mention ontology/workflows.yaml", r.Description)
	}
	if len(r.CodePaths) != 1 || r.CodePaths[0] != "ontology/workflows.yaml" {
		t.Errorf("code paths = %v, want [ontology/workflows.yaml]", r.CodePaths)
	}
	if r.Line == 0 || r.LineEnd != r.Line {
		t.Errorf("table-row lines = %d..%d, want a single-line span", r.Line, r.LineEnd)
	}

	// Compound row: US-SURF-01 links both FRs, each FR links back to the US
	// and to its sibling FR (the Rust related_fr_ids).
	r1 := findReq(t, res, "FR-SURF-01")
	if got := strings.Join(r1.UserStoryIDs, ","); got != "US-SURF-01" {
		t.Errorf("FR-SURF-01 user_story_ids = %q, want US-SURF-01", got)
	}
	if got := strings.Join(r1.RelatedFRIDs, ","); got != "FR-SURF-02" {
		t.Errorf("FR-SURF-01 related_fr_ids = %q, want FR-SURF-02", got)
	}
	s := findUS(t, res, "US-SURF-01")
	if got := strings.Join(s.FeatureIDs, ","); got != "FR-SURF-01,FR-SURF-02" {
		t.Errorf("US-SURF-01 feature_ids = %q, want FR-SURF-01,FR-SURF-02", got)
	}
}

// The definition-block form this repository's PRD uses: the block heading
// carries the title, the parenthetical the priority and focus, the body the
// description, and "- AC:" lines the acceptance criteria.
func TestParseDefinitionBlocks(t *testing.T) {
	res := Parse(fixturePRD)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	r := findReq(t, res, "FR-ZCP-02")
	if r.Title != "Lazy auto-attach" {
		t.Errorf("title = %q, want Lazy auto-attach", r.Title)
	}
	if r.Priority != "Must Have" || r.Focus != "P0" {
		t.Errorf("priority/focus = %q/%q, want Must Have/P0", r.Priority, r.Focus)
	}
	if !strings.Contains(r.Description, "Never block a query on indexing") {
		t.Errorf("description = %q, want the body lines", r.Description)
	}
	wantAC := []string{
		"a query against an unindexed repo returns an immediate non-error response.",
		"background index completes within the SLA; the second query hits the graph.",
	}
	if len(r.AcceptanceCriteria) != 2 {
		t.Fatalf("acceptance criteria = %v, want %v", r.AcceptanceCriteria, wantAC)
	}
	for i := range wantAC {
		if r.AcceptanceCriteria[i] != wantAC[i] {
			t.Errorf("AC[%d] = %q, want %q", i, r.AcceptanceCriteria[i], wantAC[i])
		}
	}
	if r.Line == 0 || r.LineEnd <= r.Line {
		t.Errorf("block lines = %d..%d, want a multi-line span", r.Line, r.LineEnd)
	}

	// A user-story definition block parses the same way.
	s := findUS(t, res, "US-SM-02")
	if s.Title != "Harness memory recall" {
		t.Errorf("US title = %q, want Harness memory recall", s.Title)
	}
	if len(s.AcceptanceCriteria) != 1 || !strings.Contains(s.AcceptanceCriteria[0], "at most 8") {
		t.Errorf("US acceptance criteria = %v, want one recall bound", s.AcceptanceCriteria)
	}
	if len(s.CodePaths) != 1 || s.CodePaths[0] != "internal/memory/banks.go" {
		t.Errorf("US code paths = %v, want [internal/memory/banks.go]", s.CodePaths)
	}
}

// Prose mentions that no table or definition introduced still yield rows
// (Rust parity), but without descriptions.
func TestParseStandaloneMentions(t *testing.T) {
	res := Parse(fixturePRD)
	r := findReq(t, res, "FR-UI2-13")
	if r.Title != r.ID {
		t.Errorf("title = %q, want the id (prose mention carries no title)", r.Title)
	}
	if r.Description != "" || len(r.CodePaths) != 0 {
		t.Errorf("prose requirement unexpectedly filled: %+v", r)
	}
}

// A fenced code block containing FR-shaped strings must not be parsed.
func TestParseIgnoresFencedCodeBlocks(t *testing.T) {
	md := "# PRD\n\n```go\nconst id = \"FR-ONT-PROC-01\"\n// AC: never indexed\n```\n"
	res := Parse(md)
	if len(res.Requirements) != 0 {
		t.Errorf("requirements from fenced block: %v", idsOf(res))
	}
}

func TestParseConflictingDefinitionsError(t *testing.T) {
	md := "# PRD\n\n**FR-X-Y-01 — One (Must Have, P0)**\n\n**FR-X-Y-01 — Two (Should Have, P1)**\n"
	res := Parse(md)
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "FR-X-Y-01") {
		t.Fatalf("errors = %v, want one conflict on FR-X-Y-01", res.Errors)
	}
}

// Port of the Rust test_requirements_to_knowledge_entries: the row shape
// (ids, knowledge_type, feature_id/user_story_id cross-links, tags, author).
func TestKnowledgeEntriesProjection(t *testing.T) {
	res := Parse(fixturePRD)
	entries := KnowledgeEntries(res, "production")
	if len(entries) != len(res.Requirements)+len(res.UserStories) {
		t.Fatalf("entries = %d, want %d", len(entries), len(res.Requirements)+len(res.UserStories))
	}
	var req *store.KnowledgeEntry
	for i := range entries {
		if entries[i].ID == "prd-req-FR-ZCP-02" {
			req = &entries[i]
		}
	}
	if req == nil {
		t.Fatal("missing prd-req-FR-ZCP-02 entry")
	}
	if req.KnowledgeType != "prd_mapping" || req.Author != "prd_indexer" || req.Environment != "production" {
		t.Errorf("entry header = %+v", req)
	}
	if req.FeatureID == nil || *req.FeatureID != "FR-ZCP-02" {
		t.Errorf("feature_id = %v, want FR-ZCP-02", req.FeatureID)
	}
	if req.Tags != "Must-Have,P0" {
		t.Errorf("tags = %q, want Must-Have,P0", req.Tags)
	}
	if !strings.Contains(req.Content, "Acceptance criteria:") {
		t.Errorf("content = %q, want the acceptance-criteria section", req.Content)
	}

	var us *store.KnowledgeEntry
	for i := range entries {
		if entries[i].ID == "prd-us-US-SURF-01" {
			us = &entries[i]
		}
	}
	if us == nil {
		t.Fatal("missing prd-us-US-SURF-01 entry")
	}
	if us.UserStoryID == nil || *us.UserStoryID != "US-SURF-01" {
		t.Errorf("user_story_id = %v, want US-SURF-01", us.UserStoryID)
	}
	if us.FeatureID == nil || !strings.Contains(*us.FeatureID, "FR-SURF-01") {
		t.Errorf("feature_id = %v, want the compound FR list", us.FeatureID)
	}
}

// upsertWorkflow pins one workflow with two steps: one carrying the
// requirement's code path, one carrying its feature id in metadata.
func upsertWorkflow(t *testing.T, st *store.Store) {
	t.Helper()
	wf := store.Element{
		QualifiedName: "local:default:workflow:pr-index:v1",
		ElementType:   "workflow",
		Name:          "pr-index",
		FilePath:      "ontology://local:default:workflow:pr-index:v1",
		Language:      "ontology",
	}
	stepA := store.Element{
		QualifiedName:   "local:default:workflow_step:pr-index:watch:v1",
		ElementType:     "workflow_step",
		Name:            "watch",
		FilePath:        "ontology://local:default:workflow_step:pr-index:watch:v1",
		Language:        "ontology",
		ParentQualified: wf.QualifiedName,
		Metadata:        map[string]any{"code_refs": []any{"ontology/workflows.yaml"}},
	}
	stepB := store.Element{
		QualifiedName:   "local:default:workflow_step:pr-index:attach:v1",
		ElementType:     "workflow_step",
		Name:            "attach",
		FilePath:        "ontology://local:default:workflow_step:pr-index:attach:v1",
		Language:        "ontology",
		ParentQualified: wf.QualifiedName,
		Metadata:        map[string]any{"feature_ids": []any{"FR-ZCP-02"}},
	}
	// A second workflow matched by neither signal must stay unlinked.
	other := store.Element{
		QualifiedName: "local:default:workflow:unrelated:v1",
		ElementType:   "workflow",
		Name:          "unrelated",
		FilePath:      "ontology://local:default:workflow:unrelated:v1",
		Language:      "ontology",
	}
	if err := st.UpsertElements([]store.Element{wf, stepA, stepB, other}); err != nil {
		t.Fatal(err)
	}
}

func mustIndex(t *testing.T, st *store.Store, content string) IndexResult {
	t.Helper()
	res, err := Index(context.Background(), st, "docs/prd.md", content, "local")
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	return res
}

func TestIndexLandsQueryableEntities(t *testing.T) {
	st, _ := setupStore(t)
	res := mustIndex(t, st, fixturePRD)

	if res.Requirements != len(Parse(fixturePRD).Requirements) || res.Elements == 0 {
		t.Fatalf("result = %+v", res)
	}
	if res.Created != res.Elements || res.Updated != 0 {
		t.Errorf("created/updated = %d/%d, want %d/0 (fresh store)", res.Created, res.Updated, res.Elements)
	}

	req, ok, err := RequirementByID(st, "FR-ZCP-02")
	if err != nil || !ok {
		t.Fatalf("RequirementByID: ok=%v err=%v", ok, err)
	}
	if req.ElementType != TypeRequirement || req.FilePath != "prd://docs/prd.md" {
		t.Errorf("element = %+v", req)
	}
	if req.Name != "FR-ZCP-02" {
		t.Errorf("name = %q (an exact query must find the FR by id)", req.Name)
	}
	if req.Metadata[MetaTitle] != "FR-ZCP-02 Lazy auto-attach" {
		t.Errorf("title metadata = %v", req.Metadata[MetaTitle])
	}
	acs, _ := req.Metadata[MetaAC].([]any)
	if len(acs) != 2 {
		t.Errorf("metadata acceptance criteria = %v, want 2", req.Metadata[MetaAC])
	}

	if _, ok, err := UserStoryByID(st, "US-SURF-01"); err != nil || !ok {
		t.Errorf("UserStoryByID: ok=%v err=%v", ok, err)
	}
	reqs, err := Requirements(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != len(Parse(fixturePRD).Requirements) {
		t.Errorf("Requirements() = %d entities", len(reqs))
	}
	uss, err := UserStories(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(uss) != len(Parse(fixturePRD).UserStories) {
		t.Errorf("UserStories() = %d entities", len(uss))
	}
}

func TestIndexIsIdempotent(t *testing.T) {
	st, _ := setupStore(t)
	first := mustIndex(t, st, fixturePRD)
	if first.Elements == 0 {
		t.Fatal("fixture produced no elements")
	}

	second := mustIndex(t, st, fixturePRD)
	if second.Created != 0 || second.Updated != first.Elements {
		t.Fatalf("second run created/updated = %d/%d, want 0/%d (pure re-index)",
			second.Created, second.Updated, first.Elements)
	}
	reqs, err := Requirements(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != first.Requirements {
		t.Fatalf("entity count after re-index = %d, want %d", len(reqs), first.Requirements)
	}
	relCount := func() int {
		all, err := st.RelationshipsAll(1000)
		if err != nil {
			t.Fatal(err)
		}
		return len(all)
	}
	afterFirst, afterSecond := relCount(), relCount()
	if afterFirst != afterSecond {
		t.Fatalf("relationship count %d -> %d on re-index", afterFirst, afterSecond)
	}
}

// The re-index is a delete-by-file rebuild: requirements removed from the
// document disappear from the store on the next run.
func TestIndexRebuildsOnRemovedRequirements(t *testing.T) {
	st, _ := setupStore(t)
	mustIndex(t, st, fixturePRD)

	short := "# PRD\n\n**FR-KEEP-01 — Kept (Must Have, P0)**\n\n- Only this one remains.\n"
	mustIndex(t, st, short)

	if _, ok, err := RequirementByID(st, "FR-ZCP-02"); err != nil || ok {
		t.Errorf("FR-ZCP-02 survived the rebuild (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := RequirementByID(st, "FR-KEEP-01"); err != nil || !ok {
		t.Errorf("FR-KEEP-01 missing after the rebuild (ok=%v err=%v)", ok, err)
	}
	reqs, err := Requirements(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 {
		t.Fatalf("requirements after rebuild = %v, want one entity", idsOfElements(reqs))
	}
	// The knowledge projection is rebuilt with the entities: a row for a
	// requirement the document dropped must not survive.
	if _, ok, err := st.KnowledgeEntryByID("prd-req-FR-ZCP-02"); err != nil || ok {
		t.Errorf("knowledge row prd-req-FR-ZCP-02 survived the rebuild (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := st.KnowledgeEntryByID("prd-req-FR-KEEP-01"); err != nil || !ok {
		t.Errorf("knowledge row prd-req-FR-KEEP-01 missing (ok=%v err=%v)", ok, err)
	}
}

// The Rust knowledge surface (search_knowledge / get_knowledge_by_feature /
// get_workflows_for_feature) reads the rows Index writes.
func TestIndexKnowledgeSurface(t *testing.T) {
	st, _ := setupStore(t)
	res := mustIndex(t, st, fixturePRD)
	parsed := Parse(fixturePRD)
	if res.KnowledgeEntries != len(parsed.Requirements)+len(parsed.UserStories) {
		t.Fatalf("knowledge entries = %d, want one row per entity", res.KnowledgeEntries)
	}

	row, ok, err := st.KnowledgeEntryByID("prd-req-FR-ZCP-02")
	if err != nil || !ok {
		t.Fatalf("knowledge row: ok=%v err=%v", ok, err)
	}
	if row.KnowledgeType != "prd_mapping" || row.Author != "prd_indexer" || row.Environment != "local" {
		t.Errorf("row header = %+v", row)
	}
	if row.FeatureID == nil || *row.FeatureID != "FR-ZCP-02" {
		t.Errorf("feature_id = %v, want FR-ZCP-02", row.FeatureID)
	}
	if row.CreatedAt == 0 || row.CreatedAt != row.UpdatedAt {
		t.Errorf("timestamps = %d/%d", row.CreatedAt, row.UpdatedAt)
	}

	byFeature, err := st.KnowledgeEntriesByFeature("FR-SURF-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(byFeature) != 1 || byFeature[0].ID != "prd-req-FR-SURF-01" {
		t.Errorf("KnowledgeEntriesByFeature = %+v", byFeature)
	}

	rows := knowledgeRowsForEnv(t, st, "local")
	if len(rows) != res.KnowledgeEntries {
		t.Errorf("environment rows = %d, want %d", len(rows), res.KnowledgeEntries)
	}

	// Re-index keeps the row count stable (upsert by id, not append).
	mustIndex(t, st, fixturePRD)
	rows = knowledgeRowsForEnv(t, st, "local")
	if len(rows) != res.KnowledgeEntries {
		t.Errorf("rows after re-index = %d, want %d", len(rows), res.KnowledgeEntries)
	}
}

// knowledgeRowsForEnv lists the knowledge rows of one environment.
func knowledgeRowsForEnv(t *testing.T, st *store.Store, env string) []store.KnowledgeEntry {
	t.Helper()
	rows, err := st.KnowledgeEntriesByEnvironment(env, 0)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func idsOfElements(els []store.Element) []string {
	out := make([]string, 0, len(els))
	for _, e := range els {
		out = append(out, e.Name)
	}
	return out
}

// Port of the Rust index_prd auto-link: a requirement whose code path appears
// in a workflow step's metadata links to that step's parent workflow; a step
// listing the feature id links the same way.
func TestIndexLinksRequirementsToWorkflows(t *testing.T) {
	st, _ := setupStore(t)
	upsertWorkflow(t, st)

	res := mustIndex(t, st, fixturePRD)
	// FR-ONT-PROC-01 (code path ontology/workflows.yaml) and FR-ZCP-02
	// (feature id on a step) both link to the pr-index workflow.
	if res.WorkflowLinks != 2 {
		t.Fatalf("workflow links = %d, want 2", res.WorkflowLinks)
	}

	for _, fid := range []string{"FR-ONT-PROC-01", "FR-ZCP-02"} {
		wfs, err := WorkflowsForFeature(st, fid)
		if err != nil {
			t.Fatal(err)
		}
		if len(wfs) != 1 || wfs[0] != "local:default:workflow:pr-index:v1" {
			t.Errorf("WorkflowsForFeature(%s) = %v", fid, wfs)
		}
	}

	// The unrelated workflow never links.
	wfs, err := WorkflowsForFeature(st, "FR-SURF-01")
	if err != nil {
		t.Fatal(err)
	}
	if len(wfs) != 0 {
		t.Errorf("FR-SURF-01 unexpectedly linked to %v", wfs)
	}

	// Reverse read: the workflow reports both features.
	fids, err := FeaturesForWorkflow(st, "local:default:workflow:pr-index:v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(fids) != 2 {
		t.Fatalf("FeaturesForWorkflow = %v, want 2 features", fids)
	}
	// Sorted for determinism.
	if !(fids[0] == "FR-ONT-PROC-01" && fids[1] == "FR-ZCP-02") {
		t.Errorf("FeaturesForWorkflow order = %v", fids)
	}

	// Both edge endpoints resolve to elements (the doctor --deep orphaned-edge
	// contract).
	for _, fid := range []string{"FR-ONT-PROC-01", "FR-ZCP-02"} {
		if _, ok, err := RequirementByID(st, fid); err != nil || !ok {
			t.Errorf("link source %s is not an element (ok=%v err=%v)", fid, ok, err)
		}
	}
	all, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, e := range all {
		present[e.QualifiedName] = true
	}
	rels, err := st.RelationshipsAll(1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rels {
		if !present[r.Source] || !present[r.Target] {
			t.Errorf("edge %s -%s-> %s dangles", r.Source, r.RelType, r.Target)
		}
	}
}

// Trace is the read side of the traceability link: requirement fields joined
// with the workflows that implement them.
func TestTraceJoinsRequirementsWithWorkflows(t *testing.T) {
	st, _ := setupStore(t)
	upsertWorkflow(t, st)
	mustIndex(t, st, fixturePRD)

	rows, err := Trace(st, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(Parse(fixturePRD).Requirements) {
		t.Fatalf("rows = %d, want one per requirement", len(rows))
	}
	if rows[0].ID >= rows[1].ID {
		t.Errorf("rows are not ordered by id: %s, %s", rows[0].ID, rows[1].ID)
	}

	var linked *Coverage
	var unlinked *Coverage
	for i := range rows {
		switch rows[i].ID {
		case "FR-ONT-PROC-01":
			linked = &rows[i]
		case "FR-SURF-01":
			unlinked = &rows[i]
		}
	}
	if linked == nil || unlinked == nil {
		t.Fatalf("fixture rows missing: %+v", rows)
	}
	if len(linked.Workflows) != 1 || linked.Workflows[0] != "local:default:workflow:pr-index:v1" {
		t.Errorf("FR-ONT-PROC-01 workflows = %v", linked.Workflows)
	}
	if linked.Priority != "Must Have" || linked.Focus != "P0" {
		t.Errorf("FR-ONT-PROC-01 priority/focus = %q/%q", linked.Priority, linked.Focus)
	}
	if len(linked.CodePaths) != 1 || linked.CodePaths[0] != "ontology/workflows.yaml" {
		t.Errorf("FR-ONT-PROC-01 code paths = %v", linked.CodePaths)
	}
	if len(unlinked.Workflows) != 0 {
		t.Errorf("FR-SURF-01 unexpectedly linked: %v", unlinked.Workflows)
	}

	one, err := Trace(st, "FR-ZCP-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].ID != "FR-ZCP-02" || len(one[0].AC) != 2 {
		t.Errorf("Trace(FR-ZCP-02) = %+v", one)
	}

	none, err := Trace(st, "FR-NOPE-99")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("Trace(unknown) = %+v", none)
	}
}

// The requirement entities coexist with the document elements docindex writes
// for the same markdown file (their file paths differ).
func TestIndexCoexistsWithDocindex(t *testing.T) {
	st, _ := setupStore(t)
	mustIndex(t, st, fixturePRD)
	// A doc-section element keyed to the same relative path.
	doc := store.Element{
		QualifiedName: "docs/prd.md#milestone-a",
		ElementType:   "doc",
		Name:          "Milestone A",
		FilePath:      "docs/prd.md",
		Language:      "markdown",
	}
	if err := st.UpsertElements([]store.Element{doc}); err != nil {
		t.Fatal(err)
	}

	// Re-indexing the PRD must not touch the doc element.
	if _, err := Index(context.Background(), st, "docs/prd.md", fixturePRD, "local"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := elementByQN(st, "docs/prd.md#milestone-a"); err != nil || !ok {
		t.Errorf("doc element destroyed by PRD re-index (ok=%v err=%v)", ok, err)
	}
}

func TestIndexDocument(t *testing.T) {
	st, dir := setupStore(t)
	abs := filepath.Join(dir, "docs", "prd.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(fixturePRD), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := IndexDocument(context.Background(), st, dir, "docs/prd.md", "local")
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "docs/prd.md" || res.Requirements == 0 {
		t.Fatalf("IndexDocument result = %+v", res)
	}
	if _, ok, err := RequirementByID(st, "FR-ZCP-02"); err != nil || !ok {
		t.Errorf("FR-ZCP-02 not indexed from disk (ok=%v err=%v)", ok, err)
	}

	// Missing documents error (the Rust index_prd contract).
	if _, err := IndexDocument(context.Background(), st, dir, "docs/missing.md", "local"); err == nil {
		t.Error("IndexDocument on a missing file must error")
	}
}

func TestIndexRespectsContextCancel(t *testing.T) {
	st, _ := setupStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Index(ctx, st, "docs/prd.md", fixturePRD, "local"); err == nil {
		t.Fatal("Index with a cancelled context must error")
	}
}

// The element projection is byte-stable apart from the timestamps, which
// keeps re-indexed rows identical.
func TestElementsAreDeterministic(t *testing.T) {
	res := Parse(fixturePRD)
	a := Elements(res, "docs/prd.md", "local", 1000)
	b := Elements(res, "docs/prd.md", "local", 1000)
	if len(a) != len(b) {
		t.Fatalf("len = %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].QualifiedName != b[i].QualifiedName || a[i].Content != b[i].Content {
			t.Errorf("element %d drifted between runs", i)
		}
	}
	if a[0].Metadata[MetaCreatedAt] != int64(1000) {
		t.Errorf("created_at = %v, want the caller-provided stamp", a[0].Metadata[MetaCreatedAt])
	}
}

func BenchmarkParse(b *testing.B) {
	// ~1200 requirement rows, the scale of the real PRD's changelog tables.
	var sb strings.Builder
	sb.WriteString("# PRD\n\n| ID | Priority | Focus | Intent |\n|---|---|---|---|\n")
	for i := range 300 {
		fmt.Fprintf(&sb, "| US-BENCH-%03d / FR-BENCH-A-%03d / FR-BENCH-B-%03d | Must Have | **P0** | Row %d uses `src/bench/row.go` |\n", i, i, i, i)
	}
	content := sb.String()
	b.ResetTimer()
	for range b.N {
		if res := Parse(content); len(res.Requirements) < 600 {
			b.Fatal("parse lost requirements")
		}
	}
}
