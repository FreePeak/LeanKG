// Tests for the PostgreSQL full-text search tier (issue #273). The fusion math
// and the tsquery-parse guard are pure and run everywhere; everything that
// needs a real index is gated behind LEANKG_TEST_PG_URL like the rest of the
// PG suite.
package store

import (
	"math"
	"strings"
	"testing"
)

// pgFloatClose exists because ts_rank and similarity are computed in float4 on
// the server: two runs can differ in the last mantissa bits.
func pgFloatClose(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %v; want %v", what, got, want)
	}
}

func TestFuseRRFSingleListPreservesOrderAndScores(t *testing.T) {
	fused := FuseRRF([]RankList{{Name: ArmVector, Keys: []string{"a", "b", "c"}}})
	if len(fused) != 3 {
		t.Fatalf("len = %d; want 3 (%+v)", len(fused), fused)
	}
	want := []string{"a", "b", "c"}
	for i, f := range fused {
		if f.Key != want[i] {
			t.Fatalf("rank %d key = %q; want %q", i, f.Key, want[i])
		}
		pgFloatClose(t, f.Score, 1/float64(60+i+1), f.Key+" score")
		if f.Ranks[ArmVector] != i+1 {
			t.Fatalf("%s ranks = %v; want rank %d", f.Key, f.Ranks, i+1)
		}
		if len(f.Ranks) != 1 {
			t.Fatalf("%s ranks = %v; want exactly one contributing arm", f.Key, f.Ranks)
		}
	}
}

// The whole point of fusion: a document several arms agree on outranks the top
// of any single arm, even though the arms' scores are on unrelated scales.
func TestFuseRRFCreditsMultiArmAgreement(t *testing.T) {
	fused := FuseRRF([]RankList{
		{Name: ArmVector, Keys: []string{"solo", "shared"}},
		{Name: ArmTSVector, Keys: []string{"shared"}},
		{Name: ArmTrigram, Keys: []string{"shared"}},
	})
	if len(fused) != 2 {
		t.Fatalf("len = %d; want 2 (%+v)", len(fused), fused)
	}
	if fused[0].Key != "shared" {
		t.Fatalf("top = %q; want shared", fused[0].Key)
	}
	pgFloatClose(t, fused[0].Score, 1/62.0+2/61.0, "shared score")
	pgFloatClose(t, fused[1].Score, 1/61.0, "solo score")
	// Absent from a list = no rank entry, not a phantom zero.
	if _, ok := fused[1].Ranks[ArmTSVector]; ok {
		t.Fatalf("solo ranks = %v; want no tsvector entry", fused[1].Ranks)
	}
	wantRanks := map[string]int{ArmVector: 2, ArmTSVector: 1, ArmTrigram: 1}
	for name, rank := range wantRanks {
		if fused[0].Ranks[name] != rank {
			t.Fatalf("shared rank %s = %d; want %d", name, fused[0].Ranks[name], rank)
		}
	}
}

// Ties must break on arm order (vector before keyword), never on Go's random
// map iteration, so the same input always renders the same result page.
func TestFuseRRFTiesBreakOnFirstAppearance(t *testing.T) {
	for _, lists := range [][]RankList{
		{{Name: ArmVector, Keys: []string{"x"}}, {Name: ArmTSVector, Keys: []string{"y"}}},
		{{Name: ArmTSVector, Keys: []string{"y"}}, {Name: ArmVector, Keys: []string{"x"}}},
	} {
		fused := FuseRRF(lists)
		if len(fused) != 2 || fused[0].Score != fused[1].Score {
			t.Fatalf("expected two tied hits, got %+v", fused)
		}
		if want := lists[0].Keys[0]; fused[0].Key != want {
			t.Fatalf("tie broken to %q; want the first arm's key %q", fused[0].Key, want)
		}
	}
}

func TestFuseRRFEdgeCases(t *testing.T) {
	if got := FuseRRF(nil); len(got) != 0 {
		t.Fatalf("no lists -> %+v; want empty", got)
	}
	if got := FuseRRF([]RankList{{Name: ArmVector}, {Name: ArmTSVector, Keys: []string{}}}); len(got) != 0 {
		t.Fatalf("empty lists -> %+v; want empty", got)
	}
	// A key repeated inside one arm (an arm that emits the same document twice)
	// is credited once, at its best rank.
	got := FuseRRF([]RankList{{Name: ArmVector, Keys: []string{"dup", "dup"}}})
	if len(got) != 1 {
		t.Fatalf("dup keys fused to %d hits; want 1", len(got))
	}
	pgFloatClose(t, got[0].Score, 1/61.0, "dup score")
	if got[0].Ranks[ArmVector] != 1 {
		t.Fatalf("dup rank = %d; want 1", got[0].Ranks[ArmVector])
	}
}

func TestRRFKIsSixty(t *testing.T) {
	// The k=60 constant is load-bearing for score comparability across
	// releases and for the documented insensitivity window; pin it.
	if RRFK != 60 {
		t.Fatalf("RRFK = %d; want 60", RRFK)
	}
}

func TestTSQueryUsable(t *testing.T) {
	cases := []struct {
		query string
		want  bool
		why   string
	}{
		{"ParseConfig", true, "one identifier"},
		{"socket close resume", true, "plain words"},
		{"pkg.MyFunc", true, "dotted path"},
		{"parse_int", true, "underscored name"},
		{"retry after 500", true, "digits count as words"},
		{"héllo wörld", true, "unicode letters"},
		{"AND OR NOT", true, "operator words are lexemes under the simple config"},
		{"", false, "empty"},
		{"   ", false, "blanks"},
		{"***", false, "punctuation only: websearch yields an empty tsquery"},
		{"-- --", false, "dashes only: empty tsquery"},
		{`""`, false, "empty phrase: empty tsquery"},
		{strings.Repeat("w ", maxTsqueryWords), true, "at the operand budget"},
		{strings.Repeat("w ", maxTsqueryWords+1), false, "past the operand budget"},
		{strings.Repeat("w ", maxTsqueryWords-1), true, "inside the operand budget"},
	}
	for _, c := range cases {
		if got := tsqueryUsable(c.query); got != c.want {
			t.Errorf("tsqueryUsable(%q) = %v; want %v (%s)", c.query, got, c.want, c.why)
		}
	}
}

func TestHybridWindowDepth(t *testing.T) {
	for _, c := range []struct{ limit, want int }{{0, 50}, {10, 50}, {20, 60}, {-1, 50}, {100, 300}} {
		if got := hybridWindow(c.limit); got != c.want {
			t.Errorf("hybridWindow(%d) = %d; want %d", c.limit, got, c.want)
		}
	}
}

// --- PostgreSQL-gated behavior ---

func TestPGFTSMigration(t *testing.T) {
	s := openPGTest(t)

	var name string
	if err := s.pool.QueryRow(pgCtx, `SELECT name FROM schema_migrations WHERE version = 12`).Scan(&name); err != nil {
		t.Fatalf("migration 12 not recorded: %v", err)
	}
	if name != "fts-tsvector-l2" {
		t.Fatalf("migration 12 name = %q; want fts-tsvector-l2", name)
	}

	// Both columns exist, are STORED generated and carry the pinned 'simple'
	// config — the property that lets inserts/upserts stay unchanged.
	for _, c := range []struct{ table, column string }{
		{"code_elements", "fts"}, {"knowledge_entries", "fts"},
	} {
		var gen string
		var typ string
		if err := s.pool.QueryRow(pgCtx, `SELECT att.attgenerated, typ.typname
			FROM pg_attribute att JOIN pg_type typ ON typ.oid = att.atttypid
			WHERE att.attrelid = $1::regclass AND att.attname = 'fts'`, c.table).Scan(&gen, &typ); err != nil {
			t.Fatalf("%s.fts missing: %v", c.table, err)
		}
		if typ != "tsvector" || gen != "s" {
			t.Fatalf("%s.%s: type=%q generated=%q; want tsvector/s", c.table, c.column, typ, gen)
		}
	}
	for _, idx := range []string{"idx_code_elements_fts", "idx_knowledge_entries_fts"} {
		var am string
		if err := s.pool.QueryRow(pgCtx, `SELECT a.amname FROM pg_class c
			JOIN pg_am a ON a.oid = c.relam
			WHERE c.oid = $1::regclass`, idx).Scan(&am); err != nil {
			t.Fatalf("index %s missing: %v", idx, err)
		}
		if am != "gin" {
			t.Fatalf("index %s access method = %q; want gin", idx, am)
		}
	}
	// The generation expression must pin 'simple' rather than fall back to
	// default_text_search_config, which is per-database and STABLE.
	for _, table := range []string{"code_elements", "knowledge_entries"} {
		var expr string
		if err := s.pool.QueryRow(pgCtx, `SELECT pg_get_expr(d.adbin, d.adrelid)
			FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
			WHERE a.attrelid = $1::regclass AND a.attname = 'fts'`, table).Scan(&expr); err != nil {
			t.Fatalf("%s.fts expression: %v", table, err)
		}
		if !strings.Contains(expr, "'simple'::regconfig") {
			t.Fatalf("%s.fts generated by %s; want the 'simple' config pinned", table, expr)
		}
	}
}

func TestPGSearchElementsFTS(t *testing.T) {
	s := openPGTest(t)
	seed := []Element{
		{QualifiedName: "pkg.parse_int", ElementType: "function", Name: "parse_int", FilePath: "a.go", Language: "go", Content: "strconv.Atoi"},
		{QualifiedName: "pkg.ParseConfig", ElementType: "function", Name: "ParseConfig", FilePath: "b.go", Language: "go", Content: "reads the config"},
		{QualifiedName: "pkg.Helper", ElementType: "function", Name: "Helper", FilePath: "c.go", Language: "go"},
	}
	if err := s.UpsertElements(seed); err != nil {
		t.Fatal(err)
	}

	// A lexeme hits its element; the 'simple' config keeps camelCase whole, so
	// "ParseConfig" matches by identity and "Config" alone does not.
	hits, method, err := s.SearchElementsFTS("ParseConfig", 10)
	if err != nil {
		t.Fatal(err)
	}
	if method != ArmTSVector {
		t.Fatalf("method = %q; want %s", method, ArmTSVector)
	}
	if len(hits) != 1 || hits[0].Element.QualifiedName != "pkg.ParseConfig" {
		t.Fatalf("hits = %+v; want only pkg.ParseConfig", hits)
	}
	if hits[0].Score <= 0 {
		t.Fatalf("ts_rank score = %v; want > 0", hits[0].Score)
	}
	if hits[0].Element.Name != "ParseConfig" || hits[0].Element.FilePath != "b.go" {
		t.Fatalf("hydration failed: %+v", hits[0].Element)
	}
	// Content is part of the tsvector (weight B), which is what makes the PG
	// keyword arm comparable with sqlite's fts5(name, qualified_name, content):
	// "Config" is not an identifier lexeme ('simple' never splits camelCase) but
	// it IS a content lexeme of b.go, so the rung answers instead of going dark.
	hits, method, err = s.SearchElementsFTS("Config", 10)
	if err != nil {
		t.Fatal(err)
	}
	if method != ArmTSVector || len(hits) != 1 || hits[0].Element.QualifiedName != "pkg.ParseConfig" {
		t.Fatalf("SearchElementsFTS(Config) = %+v, %q; want the content hit on pkg.ParseConfig via %s", hits, method, ArmTSVector)
	}
	// The no-stemming property, asserted on the tsvector arm itself so the
	// substring fallbacks cannot rescue it: "configs" lexemes nothing anywhere.
	if raw, err := s.searchElementsTS("configs", 10); err != nil || len(raw) != 0 {
		t.Fatalf("searchElementsTS(configs) = %+v, %v; want no rows (no stemming)", raw, err)
	}
	if got, _, err := s.SearchElementsFTS("parse int", 10); err != nil || len(got) != 1 ||
		got[0].Element.QualifiedName != "pkg.parse_int" {
		t.Fatalf("multi-word query = %+v, %v; want pkg.parse_int", got, err)
	}

	// Empty query is not an error and matches nothing (parity with FindFuzzy).
	if got, method, err := s.SearchElementsFTS("   ", 10); err != nil || len(got) != 0 || method != "" {
		t.Fatalf("blank query = %+v, %q, %v; want empty", got, method, err)
	}

	// The generated column follows UPDATE: re-upserting a qualified name under a
	// new name re-lexes with no sync code anywhere.
	if err := s.UpsertElements([]Element{
		{QualifiedName: "pkg.ParseConfig", ElementType: "function", Name: "ParseRuntime", FilePath: "b.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	// Asserted on the tsvector arm: after the rename the old lexeme must be
	// gone from the generated column. SearchElementsFTS could not show that —
	// its trigram fallback (by design) still fuzzy-matches ParseConfig to
	// ParseRuntime at similarity ~0.8, which is the fallback working, not a
	// stale index.
	if raw, err := s.searchElementsTS("ParseConfig", 10); err != nil || len(raw) != 0 {
		t.Fatalf("stale lexeme after upsert: %+v, %v", raw, err)
	}
	got, _, err := s.SearchElementsFTS("ParseRuntime", 10)
	if err != nil || len(got) != 1 || got[0].Element.Name != "ParseRuntime" {
		t.Fatalf("re-lexed search = %+v, %v; want one ParseRuntime hit", got, err)
	}
}

// A query with no lexemes must not read as "no keyword match": it has to go
// down the substring arms, where it can genuinely match.
func TestPGSearchElementsFTSFallbackWithoutLexemes(t *testing.T) {
	s := openPGTest(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "pkg.foo%bar", ElementType: "function", Name: "foo%bar", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}

	// The tsvector arm alone cannot serve this: no lexemes, no rows.
	if ts, err := s.searchElementsTS("%", 10); err != nil || len(ts) != 0 {
		t.Fatalf("searchElementsTS(%%) = %+v, %v; want no rows", ts, err)
	}
	hits, method, err := s.SearchElementsFTS("%", 10)
	if err != nil {
		t.Fatalf("fallback must not error: %v", err)
	}
	if method != ArmTrigram {
		t.Fatalf("method = %q; want %s", method, ArmTrigram)
	}
	if len(hits) != 1 || hits[0].Element.QualifiedName != "pkg.foo%bar" {
		t.Fatalf("fallback hits = %+v; want pkg.foo%%bar", hits)
	}
	// An over-budget query degrades the same way instead of dying on the
	// tsquery parser.
	big := strings.Repeat("word ", maxTsqueryWords+10)
	if got, method, err := s.SearchElementsFTS(big, 10); err != nil || len(got) != 0 || method != ArmTrigram {
		t.Fatalf("over-budget query = %d hits, %q, %v; want 0 hits via trigram, no error", len(got), method, err)
	}
}

// Migration 012 is what makes the tsvector arm possible; a schema that has it
// applied must keep serving search when the index is gone underneath it.
func TestPGSearchElementsFTSDegradesWithoutColumn(t *testing.T) {
	s := openPGTest(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "pkg.SocketHandler", ElementType: "function", Name: "SocketHandler", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(pgCtx, `ALTER TABLE code_elements DROP COLUMN fts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.searchElementsTS("SocketHandler", 10); err == nil {
		t.Fatal("searchElementsTS must fail without the tsvector column")
	}
	hits, method, err := s.SearchElementsFTS("SocketHandler", 10)
	if err != nil {
		t.Fatalf("degraded search must not error: %v", err)
	}
	if method != ArmTrigram {
		t.Fatalf("method = %q; want %s", method, ArmTrigram)
	}
	if len(hits) != 1 || hits[0].Element.QualifiedName != "pkg.SocketHandler" {
		t.Fatalf("degraded hits = %+v; want pkg.SocketHandler", hits)
	}
}

func TestPGSearchKnowledgeFTS(t *testing.T) {
	s := openPGTest(t)
	entries := []KnowledgeEntry{
		{ID: "K-1", KnowledgeType: "note", Title: "Socket close on resume",
			Content: "the transport detail is unrelated here", Environment: "production", Author: "a", UpdatedAt: 100},
		{ID: "K-2", KnowledgeType: "note", Title: "Unrelated title",
			Content: "the socket closed during resume unexpectedly", Environment: "production", Author: "a", UpdatedAt: 200},
		{ID: "K-3", KnowledgeType: "incident", Title: "Socket close on resume",
			Content: "same words, other type", Environment: "local", Author: "a", UpdatedAt: 300},
	}
	for _, e := range entries {
		if err := s.KnowledgeEntryUpsert(e); err != nil {
			t.Fatal(err)
		}
	}

	hits, err := s.SearchKnowledgeFTS("socket resume", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %+v; want all three entries", hits)
	}
	// Weight A (title) beats weight B (content). Verified against the fixture:
	// K-1 and K-3 carry both query lexemes in the title and tie at 0.9736
	// (ts_rank normalizes, so identical title lexeme sets rank identically);
	// K-2's hit sits in the content and lands at 0.3894 because 'simple' does
	// not stem 'closed' to 'close'. Recency then breaks the K-1/K-3 tie.
	if hits[0].ID != "K-3" || hits[1].ID != "K-1" || hits[2].ID != "K-2" {
		t.Fatalf("ranking = %s,%s,%s; want K-3,K-1,K-2 (title before content, recency for ties)",
			hits[0].ID, hits[1].ID, hits[2].ID)
	}

	// Filters behave exactly as on KnowledgeEntriesSearch.
	filtered, err := s.SearchKnowledgeFTS("socket resume", "note", "production", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("filtered = %+v; want K-1 and K-2", filtered)
	}
	for _, h := range filtered {
		if h.KnowledgeType != "note" || h.Environment != "production" {
			t.Fatalf("filter leaked: %+v", h)
		}
	}
	if one, err := s.SearchKnowledgeFTS("socket resume", "", "", 1); err != nil || len(one) != 1 || one[0].ID != "K-3" {
		t.Fatalf("limit 1 = %+v, %v; want only the top hit K-3", one, err)
	}
	// A query with no lexemes goes down the substring path, where it matches
	// nothing (and must not error).
	if fallback, err := s.SearchKnowledgeFTS("***", "", "", 10); err != nil || len(fallback) != 0 {
		t.Fatalf("fallback = %+v, %v; want no rows, no error", fallback, err)
	}
}

// The RRF path end to end: three arms, and the document two keyword arms agree
// on must outrank the vector arm's own top hit.
func TestPGHybridSearchFusesThreeArms(t *testing.T) {
	s := openPGTest(t)
	els := []Element{
		{QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha", FilePath: "a.go", Language: "go"},
		{QualifiedName: "pkg.Beta", ElementType: "function", Name: "Beta", FilePath: "b.go", Language: "go"},
		{QualifiedName: "pkg.Gamma", ElementType: "function", Name: "Gamma", FilePath: "c.go", Language: "go"},
		{QualifiedName: "pkg.Delta", ElementType: "function", Name: "Delta", FilePath: "d.go", Language: "go"},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	st := ModelStamp{ModelID: "hybrid-model", Revision: "r1", Dimensions: 4, Distance: "cosine", Provider: "unit"}
	if err := s.WriteStamp(st); err != nil {
		t.Fatal(err)
	}
	// Cosine order for the query vector: Alpha, Beta, Gamma, Delta.
	if err := s.UpsertVectors(st.ModelID, []VectorRow{
		{QualifiedName: "pkg.Alpha", Vec: []float32{1, 0, 0, 0}},
		{QualifiedName: "pkg.Beta", Vec: []float32{0.9, 0.1, 0, 0}},
		{QualifiedName: "pkg.Gamma", Vec: []float32{0.8, 0.2, 0, 0}},
		{QualifiedName: "pkg.Delta", Vec: []float32{0.01, 0.99, 0, 0}},
	}); err != nil {
		t.Fatal(err)
	}

	qvec := []float32{1, 0, 0, 0}
	hits, method, err := s.HybridSearch(st.ModelID, "Delta", qvec, 10)
	if err != nil {
		t.Fatal(err)
	}
	if method != "rrf("+ArmVector+"+"+ArmTSVector+"+"+ArmTrigram+")" {
		t.Fatalf("method = %q; want all three arms", method)
	}
	if len(hits) != 4 {
		t.Fatalf("hits = %+v; want 4", hits)
	}
	// Delta is last by cosine but first by both keyword arms: 2*(1/61) beats
	// Alpha's single 1/61.
	if hits[0].Element.QualifiedName != "pkg.Delta" {
		t.Fatalf("top hit = %s; want pkg.Delta", hits[0].Element.QualifiedName)
	}
	pgFloatClose(t, hits[0].Score, 2*(1/61.0)+1/64.0, "Delta fused score")
	if hits[1].Element.QualifiedName != "pkg.Alpha" {
		t.Fatalf("second hit = %s; want pkg.Alpha", hits[1].Element.QualifiedName)
	}
	if hits[0].Ranks[ArmVector] != 4 || hits[0].Ranks[ArmTSVector] != 1 {
		t.Fatalf("Delta ranks = %v; want vector 4, tsvector 1", hits[0].Ranks)
	}
	if hits[0].KeywordScore <= 0 || hits[0].TrigramScore <= 0 {
		t.Fatalf("Delta raw arm scores = %+v; want both positive", hits[0])
	}
	// Elements carry the arm that first named them, so hydration is complete
	// even for the vector-only tail.
	if hits[3].Element.FilePath == "" {
		t.Fatalf("tail hit not hydrated: %+v", hits[3])
	}

	// A query with no lexemes leaves only the vector arm plus (empty) substring
	// recall, so cosine order survives untouched.
	hits, method, err = s.HybridSearch(st.ModelID, "***", qvec, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 || hits[0].Element.QualifiedName != "pkg.Alpha" {
		t.Fatalf("no-lexeme hits = %+v; want cosine order", hits)
	}
	if method != "rrf("+ArmVector+")" {
		t.Fatalf("method = %q; want vector arm only", method)
	}

	// limit truncates the fused page, not just one arm.
	if cut, _, err := s.HybridSearch(st.ModelID, "Delta", qvec, 2); err != nil || len(cut) != 2 {
		t.Fatalf("limit 2 = %+v, %v; want 2 hits", cut, err)
	}
}

// With no vectors stored, the fused search still has to answer from the keyword
// arms — the degrade that keeps L3 useful on an un-embedded project.
func TestPGHybridSearchWithoutVectors(t *testing.T) {
	s := openPGTest(t)
	if err := s.UpsertElements([]Element{
		{QualifiedName: "pkg.Watermark", ElementType: "function", Name: "Watermark", FilePath: "a.go", Language: "go"},
	}); err != nil {
		t.Fatal(err)
	}
	hits, method, err := s.HybridSearch("absent-model", "Watermark", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Element.QualifiedName != "pkg.Watermark" {
		t.Fatalf("hits = %+v; want the keyword arms to carry the hit", hits)
	}
	if method != "rrf("+ArmTSVector+"+"+ArmTrigram+")" {
		t.Fatalf("method = %q; want the two keyword arms", method)
	}
	if hits[0].Similarity != 0 {
		t.Fatalf("similarity = %v; want 0 with no vector arm", hits[0].Similarity)
	}
}
