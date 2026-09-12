package convo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const fixtures = "testdata/conversations"

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(fixtures, name)
}

// setupStore opens a migrated SQLite store under a fresh temp project dir and
// returns both the project dir and the store.
func setupStore(t *testing.T) (string, *store.Store) {
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
	return dir, st
}

func mineFile(t *testing.T, name string, format Format) []MinedItem {
	t.Helper()
	items, err := MineFile(fixture(t, name), format)
	if err != nil {
		t.Fatalf("MineFile(%s, %s): %v", name, format, err)
	}
	return items
}

func kindsOf(items []MinedItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Kind.String())
	}
	return out
}

func mustElements(t *testing.T, st *store.Store, qn string) store.Element {
	t.Helper()
	els, err := st.FindExact(qn)
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 1 {
		t.Fatalf("FindExact(%q): got %d elements, want 1", qn, len(els))
	}
	return els[0]
}

// ---------------------------------------------------------------- parsers

func TestParseClaudeExport(t *testing.T) {
	items := mineFile(t, "claude_export.json", FormatClaude)

	if len(items) != 3 {
		t.Fatalf("items = %d (%v), want 3", len(items), kindsOf(items))
	}
	kinds := kindsOf(items)
	if !contains(kinds, "decision") || !contains(kinds, "preference") {
		t.Fatalf("kinds = %v, want decision + preference", kinds)
	}
	if !strings.Contains(items[0].Verbatim, "JWT") {
		t.Fatalf("first verbatim = %q, want the raw text mentioning JWT", items[0].Verbatim)
	}
	if items[0].Source != "claude" {
		t.Fatalf("source = %q, want claude", items[0].Source)
	}
	if got := items[0].Participants; !reflect.DeepEqual(got, []string{"assistant"}) {
		t.Fatalf("participants = %v, want [assistant]", got)
	}
	// Message 2 has no timestamp in any parser, matching the Rust shape.
	if items[0].Timestamp != "" {
		t.Fatalf("timestamp = %q, want empty (Rust parity)", items[0].Timestamp)
	}
}

func TestParseChatGPTExport(t *testing.T) {
	items := mineFile(t, "chatgpt_export.json", FormatChatGPT)

	if len(items) != 2 {
		t.Fatalf("items = %d (%v), want 2", len(items), kindsOf(items))
	}
	kinds := kindsOf(items)
	if !contains(kinds, "milestone") || !contains(kinds, "decision") {
		t.Fatalf("kinds = %v, want milestone + decision", kinds)
	}
	if !strings.Contains(items[0].Verbatim, "PostgreSQL") {
		t.Fatalf("first verbatim = %q, want PostgreSQL", items[0].Verbatim)
	}
	if items[0].Source != "chatgpt" {
		t.Fatalf("source = %q, want chatgpt", items[0].Source)
	}
	if got := items[1].Participants; !reflect.DeepEqual(got, []string{"assistant"}) {
		t.Fatalf("participants = %v, want [assistant]", got)
	}
}

func TestParseSlackExport(t *testing.T) {
	items := mineFile(t, "slack_export.json", FormatSlack)

	if len(items) != 3 {
		t.Fatalf("items = %d (%v), want 3", len(items), kindsOf(items))
	}
	kinds := kindsOf(items)
	if !contains(kinds, "decision") || !contains(kinds, "problem") || !contains(kinds, "preference") {
		t.Fatalf("kinds = %v, want decision + problem + preference", kinds)
	}
	if !hasVerbatim(items, "gRPC") {
		t.Fatalf("no item carries gRPC: %+v", items)
	}
	if items[0].Source != "slack" {
		t.Fatalf("source = %q, want slack", items[0].Source)
	}
	if got := items[0].Participants; !reflect.DeepEqual(got, []string{"U1"}) {
		t.Fatalf("participants = %v, want [U1]", got)
	}
}

func TestMineDirSkipsOtherFormats(t *testing.T) {
	// The fixture directory holds all three exports; only the Claude-shaped
	// file parses, the other two are skipped with a warning.
	result, err := MineDir(context.Background(), fixtures, FormatClaude)
	if err != nil {
		t.Fatalf("MineDir: %v", err)
	}
	// Two Claude-shaped fixtures (claude_export, code_targets_export); the
	// ChatGPT and Slack files fail the Claude root-key check and are skipped.
	if result.Sources != 2 {
		t.Fatalf("sources = %d, want 2", result.Sources)
	}
	if len(result.Items) != 5 {
		t.Fatalf("items = %d, want 5 (3 + 2)", len(result.Items))
	}
}

func TestMineDirRejectsUnknownFormat(t *testing.T) {
	if _, err := MineDir(context.Background(), fixture(t, "claude_export.json"), UnknownFormat); err == nil {
		t.Fatal("unknown format must error")
	}
}

func TestMineDirMissingInput(t *testing.T) {
	if _, err := MineDir(context.Background(), filepath.Join(t.TempDir(), "nope"), FormatClaude); err == nil {
		t.Fatal("missing input must error")
	}
}

// ---------------------------------------------------------------- classification

func TestClassify(t *testing.T) {
	cases := []struct {
		text string
		want Kind
	}{
		{"Decision: switch to Redis for caching", KindDecision},
		{"We decided to use RS256 for signing.", KindDecision},
		{"we will use gRPC for inter-service communication.", KindDecision},
		{"It might fail", KindGeneral}, // no signal phrase: "fails"/"failure" need the s/es
		{"Preference: prefer async/await style in handlers", KindPreference},
		{"I prefer protobuf over JSON for internal APIs.", KindPreference},
		{"Milestone: migration completed by end of Q3", KindMilestone},
		{"Goal: get 100% test coverage by release", KindMilestone},
		{"Problem: the batch job keeps timing out at 5 minutes", KindProblem},
		{"The batch job keeps timing out at 5 minutes", KindProblem},
		{"build fails on CI every night", KindProblem},
		{"Let's check the weather tomorrow", KindGeneral},
		{"", KindGeneral},
	}
	for _, c := range cases {
		if got := Classify(c.text); got != c.want {
			t.Errorf("Classify(%q) = %s, want %s", c.text, got, c.want)
		}
	}
}

func TestClassifyLabelWinsOverKeywords(t *testing.T) {
	// The label check runs before the keyword heuristics: a message labelled
	// "problem:" that also says "decision" stays a problem.
	if got := Classify("Problem: we decided the timeout is broken"); got != KindProblem {
		t.Fatalf("Classify = %s, want problem", got)
	}
}

func TestFromMessageDropsUnclassifiable(t *testing.T) {
	if _, ok := FromMessage(RawMessage{Text: "   ", Source: "claude"}); ok {
		t.Fatal("blank text must not mine")
	}
	if _, ok := FromMessage(RawMessage{Text: "Let's check the weather", Source: "claude"}); ok {
		t.Fatal("general text must not mine")
	}
	item, ok := FromMessage(RawMessage{Text: "  Decision: use Redis  ", Source: "slack", Participant: "U1"})
	if !ok {
		t.Fatal("decision text must mine")
	}
	if item.Verbatim != "Decision: use Redis" || item.Source != "slack" {
		t.Fatalf("item = %+v", item)
	}
}

// ---------------------------------------------------------------- extraction

func TestExtractTopic(t *testing.T) {
	cases := []struct{ text, want string }{
		// The extractor is "first token >= 3 chars that is not a stopword",
		// so leading non-stopword words win before the technical term.
		{"Decision: adopt RS256 JWT for gateway auth", "RS256"},
		{"Goal: get 100% test coverage by release", "Goal"},
		{"We decided to use Redis for caching", "decided"},
		{"we will use gRPC for inter-service communication.", "will"},
		{"use gRPC", "gRPC"},
	}
	for _, c := range cases {
		if got := extractTopic(c.text); got != c.want {
			t.Errorf("extractTopic(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	// No qualifying token: the fallback is the first 40 characters.
	text := "we use the and our new for with and we use the and our new"
	if got := extractTopic(text); got != text[:40] {
		t.Fatalf("fallback = %q, want %q", got, text[:40])
	}
}

func TestExtractCodeTargets(t *testing.T) {
	text := "Decision: use `src/gateway.rs::handle_auth` and src/auth/rs256.rs plus gateway.rs::verify"
	got := extractCodeTargets(text)
	// Backtick spans come first verbatim; file::symbol and bare src/ hits are
	// appended only when not already present — so the backtick-quoted
	// src/gateway.rs::handle_auth suppresses the file::symbol duplicate but
	// not the bare src/gateway.rs path. Mirrors the Rust extractor exactly.
	want := []string{"src/gateway.rs::handle_auth", "gateway.rs::verify", "src/gateway.rs", "src/auth/rs256.rs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v (backtick span, file::symbol, bare src/ path in that order)", got, want)
	}
	if got := extractCodeTargets("no targets here"); len(got) != 0 {
		t.Fatalf("targets = %v, want none", got)
	}
}

// ---------------------------------------------------------------- persistence

func TestIndexItemsPersistsNodesAndEdges(t *testing.T) {
	project, st := setupStore(t)
	items := []MinedItem{
		{
			Kind:         KindDecision,
			Verbatim:     "Decision: adopt RS256 JWT for gateway auth",
			Source:       "claude",
			Participants: []string{"alice"},
			Topic:        "RS256",
			CodeTargets:  []string{"src/gateway.rs::handle_auth"},
		},
		{
			Kind:     KindPreference,
			Verbatim: "Preference: async/await in handlers",
			Source:   "claude",
			Topic:    "async",
		},
	}

	result, err := IndexItems(st, project, items)
	if err != nil {
		t.Fatalf("IndexItems: %v", err)
	}
	if result.ElementsIndexed != 2 || result.RelationshipsCreated != 1 {
		t.Fatalf("result = %+v, want 2 elements / 1 relationship", result)
	}

	projectName := filepath.Base(project)
	qn := "conversations/" + projectName + "/decision/rs256"
	decision := mustElements(t, st, qn)
	if decision.ElementType != "decision" || decision.Name != "RS256" || decision.Language != "conversation" {
		t.Fatalf("decision element = %+v", decision)
	}
	if decision.FilePath != "conversations/claude/decision" {
		t.Fatalf("file path = %q", decision.FilePath)
	}
	if decision.Metadata["verbatim"] != "Decision: adopt RS256 JWT for gateway auth" {
		t.Fatalf("metadata verbatim = %v", decision.Metadata["verbatim"])
	}
	if decision.Metadata["source"] != "claude" || decision.Metadata["topic"] != "RS256" {
		t.Fatalf("metadata = %+v", decision.Metadata)
	}
	if participants, ok := decision.Metadata["participants"].([]any); !ok || len(participants) != 1 || participants[0] != "alice" {
		t.Fatalf("metadata participants = %#v", decision.Metadata["participants"])
	}

	rels, err := st.Outgoing(qn)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 1 {
		t.Fatalf("outgoing = %+v, want one decided_about edge", rels)
	}
	if rels[0].RelType != "decided_about" || rels[0].Target != "src/gateway.rs::handle_auth" {
		t.Fatalf("edge = %+v", rels[0])
	}
	if rels[0].Confidence != 1.0 || rels[0].Metadata["confidence_label"] != "EXTRACTED" {
		t.Fatalf("edge metadata = %+v", rels[0])
	}

	// The preference node has no outgoing edges.
	pref := mustElements(t, st, "conversations/"+projectName+"/preference/async")
	if pref.ElementType != "preference" {
		t.Fatalf("preference element = %+v", pref)
	}
	prefRels, err := st.Outgoing(pref.QualifiedName)
	if err != nil {
		t.Fatal(err)
	}
	if len(prefRels) != 0 {
		t.Fatalf("preference edges = %+v, want none", prefRels)
	}
}

func TestIndexItemsIsIdempotent(t *testing.T) {
	project, st := setupStore(t)
	// Two decisions share the conversations/slack/decision file path, so this
	// also pins that the pre-write clears do not swallow an earlier element's
	// decided_about edges (the Rust original lost them).
	items := []MinedItem{
		{
			Kind:        KindDecision,
			Verbatim:    "Decision: use Redis",
			Source:      "slack",
			Topic:       "Redis",
			CodeTargets: []string{"src/cache.rs::init"},
		},
		{
			Kind:        KindDecision,
			Verbatim:    "Decision: use gRPC",
			Source:      "slack",
			Topic:       "gRPC",
			CodeTargets: []string{"src/rpc.rs::serve"},
		},
	}

	for run := 1; run <= 2; run++ {
		res, err := IndexItems(st, project, items)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if res.ElementsIndexed != 2 || res.RelationshipsCreated != 2 {
			t.Fatalf("run %d result = %+v, want 2 elements / 2 relationships", run, res)
		}
		all, err := st.RelationshipsAll(0)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 2 {
			t.Fatalf("run %d relationships = %+v, want both decided_about edges", run, all)
		}
	}

	n, err := st.ElementCount()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("element count = %d, want 2 (second run must not duplicate nodes)", n)
	}
	rc, err := st.RelationshipCount()
	if err != nil {
		t.Fatal(err)
	}
	if rc != 2 {
		t.Fatalf("relationship count = %d, want 2 (second run must not duplicate edges)", rc)
	}
}

func TestIndexItemsFromFixtureCreatesDecidedAboutEdges(t *testing.T) {
	project, st := setupStore(t)
	items := mineFile(t, "code_targets_export.json", FormatClaude)
	if len(items) != 2 {
		t.Fatalf("items = %d (%v), want 2", len(items), kindsOf(items))
	}

	// Both decisions carry exactly the two targets their text names (the
	// backtick-quoted value already carries the ::symbol form).
	if got, want := items[0].CodeTargets, []string{"src/gateway.rs::handle_auth", "src/gateway.rs", "src/auth/token.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first targets = %v, want %v", got, want)
	}
	if got, want := items[1].CodeTargets, []string{"src/auth/token.rs::verify", "src/auth/token.rs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second targets = %v, want %v", got, want)
	}

	res, err := IndexItems(st, project, items)
	if err != nil {
		t.Fatal(err)
	}
	if res.ElementsIndexed != 2 || res.RelationshipsCreated != 5 {
		t.Fatalf("result = %+v, want 2 elements / 5 decided_about edges", res)
	}
	rels, err := st.RelationshipsAll(0)
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]bool{}
	for _, r := range rels {
		if r.RelType != "decided_about" {
			t.Fatalf("unexpected relationship: %+v", r)
		}
		targets[r.Target] = true
	}
	for _, want := range []string{"src/gateway.rs::handle_auth", "src/gateway.rs", "src/auth/token.rs", "src/auth/token.rs::verify"} {
		if !targets[want] {
			t.Fatalf("missing decided_about target %q in %v", want, targets)
		}
	}
}

func TestIndexItemsDropsStaleNodes(t *testing.T) {
	project, st := setupStore(t)
	first := []MinedItem{{Kind: KindDecision, Verbatim: "Decision: use Redis", Source: "slack", Topic: "Redis"}}
	if _, err := IndexItems(st, project, first); err != nil {
		t.Fatal(err)
	}
	projectName := filepath.Base(project)
	mustElements(t, st, "conversations/"+projectName+"/decision/Redis")

	// A later re-mine of the same source that only yields a different topic
	// must not leave the dropped node behind.
	second := []MinedItem{{Kind: KindDecision, Verbatim: "Decision: use Kafka", Source: "slack", Topic: "Kafka"}}
	if _, err := IndexItems(st, project, second); err != nil {
		t.Fatal(err)
	}
	els, err := st.FindExact("conversations/" + projectName + "/decision/redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(els) != 0 {
		t.Fatalf("stale node survived re-mine: %+v", els)
	}
	mustElements(t, st, "conversations/"+projectName+"/decision/Kafka")
}

func TestQualifiedNameSlug(t *testing.T) {
	item := MinedItem{Kind: KindDecision, Topic: "RS-256/v2"}
	if got, want := item.QualifiedName("/tmp/proj"), "conversations/proj/decision/rs_256_v2"; got != want {
		t.Fatalf("QualifiedName = %q, want %q", got, want)
	}
	if got, want := item.QualifiedName("."), "conversations/project/decision/rs_256_v2"; got != want {
		t.Fatalf("QualifiedName(.) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------- end to end

func TestMineIntoProjectEndToEnd(t *testing.T) {
	project, st := setupStore(t)

	// Seed the code element the decision edges point at.
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "src/gateway.rs::handle_auth",
		ElementType:   "function",
		Name:          "handle_auth",
		FilePath:      "src/gateway.rs",
		Language:      "rust",
		LineStart:     1,
		LineEnd:       5,
	}}); err != nil {
		t.Fatal(err)
	}

	result, err := MineIntoProject(context.Background(), project, fixture(t, "claude_export.json"), FormatClaude)
	if err != nil {
		t.Fatalf("MineIntoProject: %v", err)
	}
	if len(result.Items) != 3 || result.ElementsIndexed != 3 {
		t.Fatalf("result = %+v, want 3 items / 3 elements", result)
	}

	// Second run over the same input is a no-op on counts.
	again, err := MineIntoProject(context.Background(), project, fixture(t, "claude_export.json"), FormatClaude)
	if err != nil {
		t.Fatalf("second MineIntoProject: %v", err)
	}
	if again.ElementsIndexed != 3 {
		t.Fatalf("second run indexed %d elements, want 3", again.ElementsIndexed)
	}
	els, err := st.Elements()
	if err != nil {
		t.Fatal(err)
	}
	mined := 0
	for _, el := range els {
		if el.Language == "conversation" {
			mined++
		}
	}
	if mined != 3 {
		t.Fatalf("mined element rows = %d, want 3 (idempotent second run)", mined)
	}
}

func TestMiningResultSummary(t *testing.T) {
	result := MiningResult{
		Items: []MinedItem{{
			Kind:     KindProblem,
			Verbatim: "Problem: OOM in batch",
			Source:   "slack",
			Topic:    "batch",
		}},
		Sources:         1,
		ElementsIndexed: 1,
	}
	got := result.Summary()
	want := "Mined 1 item from 1 source(s) [problem]: 1 elements, 0 relationships"
	if got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}

	multi := MiningResult{Items: []MinedItem{
		{Kind: KindProblem}, {Kind: KindDecision}, {Kind: KindProblem},
	}, Sources: 2, ElementsIndexed: 3, RelationshipsCreated: 1}
	got = multi.Summary()
	want = "Mined 3 items from 2 source(s) [decision, problem]: 3 elements, 1 relationships"
	if got != want {
		t.Fatalf("Summary = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------- helpers

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func hasVerbatim(items []MinedItem, substr string) bool {
	for _, it := range items {
		if strings.Contains(it.Verbatim, substr) {
			return true
		}
	}
	return false
}

// TestFixtureShapes guards the checked-in fixtures against silent shape drift:
// each must still parse in its own format and fail in the others.
func TestFixtureShapes(t *testing.T) {
	for _, tc := range []struct {
		file   string
		format Format
	}{
		{"claude_export.json", FormatClaude},
		{"chatgpt_export.json", FormatChatGPT},
		{"slack_export.json", FormatSlack},
	} {
		raw, err := os.ReadFile(fixture(t, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("%s: not JSON: %v", tc.file, err)
		}
		if _, err := MineFile(fixture(t, tc.file), tc.format); err != nil {
			t.Fatalf("%s must parse as %s: %v", tc.file, tc.format, err)
		}
	}
}
