// PostgreSQL full-text search for the keyword rung (issue #273): a GIN-indexed
// tsvector generated column (pgmigrate.go migration 012) ranked with ts_rank,
// ILIKE + pg_trgm similarity as the fallback for queries that carry no
// lexemes, and reciprocal-rank fusion (FuseRRFWeighted) of the vector / tsvector /
// trigram rankings behind the PostgreSQL semantic rung.
//
// SQLite's FTS5 path is deliberately untouched: nothing in this file is
// reachable from *Store, and callers keep depending on store.Backend and
// type-assert for FTSBackend.
package store

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// pgTSConfig is the lexing configuration pinned in every tsvector expression
// here, as a SQL literal. 'simple' lowercases and splits on word boundaries
// with no stemming and no stopword list, so identical text lexes identically
// on every PostgreSQL version. Passing the config explicitly is also what
// makes to_tsvector IMMUTABLE (the one-argument form reads
// default_text_search_config, is STABLE, and is therefore unusable in the
// generated column of migration 012).
const pgTSConfig = "'simple'"

// Ranking-arm names, as they appear in FuseRRFWeighted's RankList labels and in the
// provenance strings returned by SearchElementsFTS and HybridSearch.
const (
	ArmVector   = "vector"
	ArmTSVector = "tsvector"
	ArmTrigram  = "trigram"
	ArmILIKE    = "ilike"
)

// maxTsqueryWords bounds how much text is handed to websearch_to_tsquery. That
// function is designed never to raise on malformed input (it treats the whole
// string as plain words), but PostgreSQL still recurses over the operand tree:
// a ~40k-word query dies with SQLSTATE 54001 "stack depth limit exceeded"
// (measured on the 18.6 dev fixture). Anything past this bound goes down the
// ILIKE+trgm path, which has no such ceiling.
const maxTsqueryWords = 1024

// tsqueryUsable reports whether query can drive the tsvector path: at least
// one word and no more than maxTsqueryWords. A word here is a run of letters
// and digits, which is exactly what the 'simple' parser turns into a lexeme.
// The check matters because websearch_to_tsquery('simple', q) yields the EMPTY
// tsquery for punctuation-only input ('***', '--'), and an empty tsquery
// matches no row at all: without this guard a symbol query would read as a
// genuine zero-hit keyword rung instead of degrading to substring recall, where
// '%%*%%' really can match a name containing it.
func tsqueryUsable(query string) bool {
	words := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	return len(words) > 0 && len(words) <= maxTsqueryWords
}

// hybridWindow is how deep each arm is read before fusion. RRF only sees the
// ranks it is given, so a document ranked 30th by the vector arm and 1st by
// the keyword arm needs both lists to reach 30 before that combination is
// credited. 3*limit with a 50 floor mirrors the Rust pipeline's candidate
// headroom (top_k = (limit+offset).max(50), src/mcp/handler.rs:4729) and keeps
// fusion O(limit).
func hybridWindow(limit int) int {
	if w := 3 * limit; w > 50 {
		return w
	}
	return 50
}

// RRFK is the reciprocal-rank-fusion damping constant: a document's fused
// score is Σ_lists 1/(k + rank), rank 1-based. k=60 is the constant from the
// original RRF paper's own examples and the default the mainstream engines ship
// (Elasticsearch/Lucene, Weaviate, Qdrant); the fused order is insensitive to k
// across roughly 40..80, so this is a convention rather than a tuned value.
//
// Reference note: the deleted Rust tree had neither RRF nor tsvector. It merged
// its two incomparable score scales (cross-encoder vs cosine) by min-max
// normalizing each pool and interleaving by the normalized score
// (src/mcp/handler.rs:4845-4970), and its L2 fused by fixed arm order plus a
// qualified_name dedupe (src/mcp/router.rs:368-412). Rank fusion replaces both
// here: the PostgreSQL arms produce three genuinely different scales (cosine
// similarity, ts_rank, trigram similarity) whose only comparable quantity is
// rank, and a document absent from a pool must contribute nothing rather than
// the fabricated 0.0 that normalization turns it into.

// FuseRRFWeights scales each arm's contribution in FuseRRFWeighted, the one
// fusion both engines run. Zero fields leave the arm at 1; the slice (not the
// struct) is how you drop an arm entirely unless Explicit makes a zero mean off.
type FuseRRFWeights struct {
	Vector   float64
	TSVector float64
	Trigram  float64
	ILIKE    float64
	// Keyword scales whichever keyword arm the engine has — TSVector on
	// PostgreSQL, FTS5 on SQLite — so a caller's "<vector>,<keyword>" pair
	// means the same thing on both backends.
	Keyword float64
	// Explicit marks Vector/Keyword as CALLER-SET, so a zero means "off"
	// rather than "unset": the documented "1,0" zero-weights the keyword arm
	// (its rows stay in the list with score 0 and sort last), while the zero
	// value of this struct keeps every arm at 1.
	Explicit bool
}

// DefaultFuseWeights is the fusion applied when the caller names no weight:
// vector:keyword = 3:1. Under plain 1:1 RRF a lexical-only row at keyword rank
// 1 (1/61) ties vector rank 1 (1/61) and outranks vector ranks 2 to 8 (1/62 to
// 1/68), which is what the real-server sweeps measured. Not Explicit, so the
// lexical arms (tsvector, trigram, ilike, name) keep weight 1. Both engines read
// this one value. The 3:1 figure was measured on the SQLite engine, which has two
// lexical arms; PostgreSQL can have three (tsvector, trigram, name), so see
// docs/prd.md for what that means for the default.
func DefaultFuseWeights() FuseRRFWeights {
	return FuseRRFWeights{Vector: 3, Keyword: 1}
}

const RRFK = 60

// RankList is one arm's ranked output feeding FuseRRFWeighted: document keys in
// descending relevance (rank 1 first). Name labels the arm for provenance.
type RankList struct {
	Name string
	Keys []string
}

// FusedHit is one document of a reciprocal-rank fusion result: its key, the
// summed reciprocal-rank score, and the rank each contributing arm gave it (an
// arm that did not return the document has no entry — the standard RRF
// treatment of a missing rank).
type FusedHit struct {
	Key   string         `json:"key"`
	Score float64        `json:"score"`
	Ranks map[string]int `json:"ranks"`
}

// armWeight maps an arm name to its weight for weighted fusion.
func (w FuseRRFWeights) armWeight(name string) float64 {
	switch name {
	case ArmVector:
		if w.Explicit {
			return w.Vector
		}
		if w.Vector != 0 {
			return w.Vector
		}
	case ArmFTS5:
		return w.keywordWeight(0)
	case ArmTSVector:
		return w.keywordWeight(w.TSVector)
	case ArmTrigram:
		return w.keywordWeight(w.Trigram)
	case ArmILIKE:
		return w.keywordWeight(w.ILIKE)
	}
	return 1.0
}

// keywordWeight resolves a lexical arm's weight. A per-arm field the caller set
// wins. Otherwise the arm takes the Keyword slot: an explicit zero means off, and
// an unset Keyword means 1. Every keyword arm (FTS5 on SQLite; tsvector, trigram
// and ILIKE on PostgreSQL) resolves here, so "<vector>,<keyword>" means the same
// thing on both engines. The name arm is not a keyword arm and stays at 1.
func (w FuseRRFWeights) keywordWeight(arm float64) float64 {
	if arm != 0 {
		return arm
	}
	if w.Explicit {
		return w.Keyword
	}
	if w.Keyword != 0 {
		return w.Keyword
	}
	return 1.0
}

// FuseRRFWeighted merges pre-ranked lists by reciprocal rank fusion and returns
// every distinct key, best score first. Each arm adds weight[arm] / (RRFK + rank
// + 1), so a caller can upweight or downweight a ranking arm (the primitive map's
// "learned reranker replaces fixed RRFK") without perturbing how missing ranks
// are treated. The zero FuseRRFWeights leaves every arm at weight 1. Ties break
// on the order keys first appeared across the lists in the order given, so the
// earlier arm wins a tie and the result never depends on map iteration.
func FuseRRFWeighted(lists []RankList, w FuseRRFWeights) []FusedHit {
	type slot struct{ hit FusedHit }
	var slots []*slot
	byKey := map[string]*slot{}
	for _, l := range lists {
		weight := w.armWeight(l.Name)
		seen := map[string]bool{}
		for rank, key := range l.Keys {
			if seen[key] {
				continue
			}
			seen[key] = true
			s := byKey[key]
			if s == nil {
				s = &slot{hit: FusedHit{Key: key, Ranks: map[string]int{}}}
				byKey[key] = s
				slots = append(slots, s)
			}
			s.hit.Ranks[l.Name] = rank + 1
			s.hit.Score += weight / float64(RRFK+rank+1)
		}
	}
	sort.SliceStable(slots, func(i, j int) bool { return slots[i].hit.Score > slots[j].hit.Score })
	out := make([]FusedHit, len(slots))
	for i, s := range slots {
		out[i] = s.hit
	}
	return out
}

// HybridHit is one fused result of HybridSearch: the hydrated element plus the
// fused score and the raw score of every arm that carried it (0 where an arm
// did not, mirroring FusedHit.Ranks).
type HybridHit struct {
	Element      Element        `json:"element"`
	Score        float64        `json:"score"`
	Ranks        map[string]int `json:"ranks"`
	Similarity   float64        `json:"similarity,omitempty"`
	KeywordScore float64        `json:"keyword_score,omitempty"`
	TrigramScore float64        `json:"trigram_score,omitempty"`
}

// FTSBackend is the optional capability of a Backend that ranks keyword hits
// from a real full-text index and fuses several rankings. Only PGStore
// implements it. Callers (core, orgknowledge) keep store.Backend as the
// contract and type-assert, so the SQLite FTS5/bm25 rung stays exactly as it is
// in both source and behavior.
type FTSBackend interface {
	// SearchElementsFTS ranks code_elements by tsvector and reports which arm
	// served the hits (ArmTSVector, ArmTrigram or ArmILIKE).
	SearchElementsFTS(query string, limit int) ([]FuzzyMatch, string, error)
	// SearchKnowledgeFTS ranks knowledge_entries by tsvector over
	// (title, content) with the same filters as KnowledgeEntriesSearch.
	SearchKnowledgeFTS(query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error)
	// HybridSearch fuses the vector, tsvector and trigram rankings of one
	// query and reports the contributing arms.
	HybridSearch(modelID, query string, qvec []float32, limit int) ([]HybridHit, string, error)
}

var _ FTSBackend = (*PGStore)(nil)

// SearchElementsFTS is the PostgreSQL keyword rung: ts_rank over the
// migration-012 tsvector (the @@ predicate is GIN-served), degrading to
// pg_trgm similarity ranking and then to plain ILIKE substring recall. The
// tsvector side is skipped when the query carries no lexemes and whenever it
// errors — a refused tsquery, or a schema that predates migration 012 — so
// availability never depends on the index or the extension (the same
// degrade-not-fail policy as the Rust 007_trgm_fuzzy.sql tier).
func (s *PGStore) SearchElementsFTS(query string, limit int) ([]FuzzyMatch, string, error) {
	if strings.TrimSpace(query) == "" {
		return nil, "", nil
	}
	if limit <= 0 {
		limit = 50
	}
	if !tsqueryUsable(query) {
		matches, method, err := s.searchElementsSubstring(query, limit)
		return matches, method, err
	}
	matches, err := s.searchElementsTS(query, limit)
	if err == nil && len(matches) > 0 {
		return matches, ArmTSVector, nil
	}
	// An empty tsvector result is not an error but is not an answer either:
	// a conjunctive tsquery over (name, qualified_name, content) legitimately
	// matches nothing for a multi-word query whose terms never co-occur, and
	// sqlite's FTS5 rung would still return bm25 substring-ish hits. Degrade so
	// the two engines keep comparable recall (issue #273).
	fallback, method, ferr := s.searchElementsSubstring(query, limit)
	if ferr != nil {
		return nil, "", fmt.Errorf("store: element full-text search %q: %v (fallback: %w)", query, err, ferr)
	}
	return fallback, method, nil
}

// searchElementsSubstring is the two-arm keyword fallback behind
// SearchElementsFTS: pg_trgm similarity ranking when the extension resolves,
// ILIKE substring recall when it does not.
func (s *PGStore) searchElementsSubstring(query string, limit int) ([]FuzzyMatch, string, error) {
	matches, err := s.searchElementsTrigram(query, limit)
	if err == nil {
		return matches, ArmTrigram, nil
	}
	fallback, ie := s.FindFuzzy(query, limit)
	if ie != nil {
		return nil, "", fmt.Errorf("store: element substring search %q: %w (trigram: %v)", query, ie, err)
	}
	return fallback, ArmILIKE, nil
}

// searchElementsTS ranks code_elements by the generated tsvector column. Row
// shape mirrors FindFuzzy: the element columns plus a trailing score. That
// score is ts_rank, a small positive float — NOT comparable with sqlite's
// bm25 values, which are negative and lower-is-better.
func (s *PGStore) searchElementsTS(query string, limit int) ([]FuzzyMatch, error) {
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgElementCols+`,
		ts_rank(ce.fts, websearch_to_tsquery(`+pgTSConfig+`, $1)) AS score
		FROM code_elements ce
		WHERE ce.fts @@ websearch_to_tsquery(`+pgTSConfig+`, $1)
		ORDER BY score DESC, ce.qualified_name ASC
		LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: element tsvector search: %w", err)
	}
	return scanFuzzyMatches(rows)
}

// searchElementsTrigram is the fuzzy bridge: trigram similarity ranks
// name/qualified_name while wildcard ILIKE widens recall (the % operator has
// its own threshold and drops short needles). Ported from the Rust
// FUZZY_FIND_TRGM_SQL (src/db/backend.rs:1843-1857), adapted to this schema's
// column set. It needs pg_trgm (migration 001 installs it best-effort); where
// the extension is absent both similarity() and the % operator raise and the
// caller degrades.
func (s *PGStore) searchElementsTrigram(query string, limit int) ([]FuzzyMatch, error) {
	needle := "%" + escapeLike(query) + "%"
	rows, err := s.pool.Query(pgCtx, `SELECT `+pgElementCols+`,
		GREATEST(similarity(ce.name, $1), similarity(ce.qualified_name, $1)) AS score
		FROM code_elements ce
		WHERE ce.name % $1 OR ce.qualified_name % $1
			OR ce.name ILIKE $2 ESCAPE '\' OR ce.qualified_name ILIKE $2 ESCAPE '\'
		ORDER BY score DESC, ce.qualified_name ASC
		LIMIT $3`, query, needle, limit)
	if err != nil {
		return nil, fmt.Errorf("store: element trigram search: %w", err)
	}
	return scanFuzzyMatches(rows)
}

// SearchKnowledgeFTS is the knowledge_entries keyword search over the
// migration-012 tsvector of (title, content): relevance-ranked with recency
// breaking ties, degrading to KnowledgeEntriesSearch (substring, newest first)
// for a query with no lexemes or any tsvector-side error. The filters match
// KnowledgeEntriesSearch exactly, so the two are interchangeable at the call
// site.
func (s *PGStore) SearchKnowledgeFTS(query, knowledgeType, environment string, limit int) ([]KnowledgeEntry, error) {
	if !tsqueryUsable(query) {
		return s.KnowledgeEntriesSearch(query, knowledgeType, environment, limit)
	}
	// $1 is the query text, bound once and reused by the predicate and the
	// rank expression; the optional filters and the limit follow it.
	b := &whereBuilder{ph: func(n int) string { return fmt.Sprintf("$%d", n) }}
	tsq := b.bind(query)
	conds := []string{"fts @@ websearch_to_tsquery(" + pgTSConfig + ", " + tsq + ")"}
	if knowledgeType != "" {
		conds = append(conds, "knowledge_type = "+b.bind(knowledgeType))
	}
	if environment != "" {
		conds = append(conds, "environment = "+b.bind(environment))
	}
	clause := limitSQL(limit, b)
	args := b.args

	rows, err := s.pool.Query(pgCtx, `SELECT `+pgKnowledgeCols+`,
		ts_rank(fts, websearch_to_tsquery(`+pgTSConfig+`, `+tsq+`)) AS score
		FROM knowledge_entries
		WHERE `+strings.Join(conds, " AND ")+`
		ORDER BY score DESC, updated_at DESC, id ASC`+clause, args...)
	if err != nil {
		return s.KnowledgeEntriesSearch(query, knowledgeType, environment, limit)
	}
	return s.scanKnowledgeRanked(rows)
}

// scanKnowledgeRanked reads pgKnowledgeCols plus a trailing rank column.
func (s *PGStore) scanKnowledgeRanked(rows pgx.Rows) ([]KnowledgeEntry, error) {
	defer rows.Close()
	var out []KnowledgeEntry
	for rows.Next() {
		var r pgKnowledgeRow
		var score float64
		if err := rows.Scan(append(r.dest(), &score)...); err != nil {
			return nil, fmt.Errorf("store: knowledge full-text scan: %w", err)
		}
		out = append(out, r.entry())
	}
	return out, rows.Err()
}

// HybridSearch is the PostgreSQL semantic rung: the three rankings one query
// supports — cosine over the model's vectors, ts_rank over the tsvector, and
// pg_trgm similarity — merged by reciprocal rank fusion. Fusing ranks is the
// point: the three scores live on unrelated scales, so no weighted sum of them
// means anything, and a document missing from one list must neither win nor
// lose on a fabricated value.
//
// Arms degrade independently: a query without lexemes skips the tsvector arm,
// a tsvector-side error skips it too, and pg_trgm absent turns the third arm
// into ILIKE. Only the vector arm failing is a hard error, because that is the
// arm the caller already proved exists (core checks the model stamp before
// embedding the query). The second result names the contributing arms, e.g.
// "rrf(vector+tsvector+trigram)".
func (s *PGStore) HybridSearch(modelID, query string, qvec []float32, limit int) ([]HybridHit, string, error) {
	return s.HybridSearchScoped(modelID, query, qvec, limit, nil)
}

func (s *PGStore) HybridSearchScoped(modelID, query string, qvec []float32, limit int, keep func(Element) bool) ([]HybridHit, string, error) {
	return s.HybridSearchScopedWeighted(modelID, query, qvec, limit, keep, DefaultFuseWeights())
}

// HybridSearchScopedWeighted is HybridSearchScoped with the caller's per-arm
// scaling (WeightedHybridSearcher). The plain method passes DefaultFuseWeights;
// the zero FuseRRFWeights is every arm at 1.
func (s *PGStore) HybridSearchScopedWeighted(modelID, query string, qvec []float32, limit int, keep func(Element) bool, w FuseRRFWeights) ([]HybridHit, string, error) {
	if limit <= 0 {
		limit = 50
	}
	window := hybridWindow(limit)

	type armScores struct {
		el      Element
		sim     float64
		kw      float64
		trigram float64
	}
	byQN := map[string]*armScores{}
	record := func(qn string, el Element) *armScores {
		a := byQN[qn]
		if a == nil {
			a = &armScores{el: el}
			byQN[qn] = a
			return a
		}
		if a.el.QualifiedName == "" {
			a.el = el
		}
		return a
	}

	var lists []RankList
	var kwErr error

	if len(qvec) > 0 {
		hits, err := s.SearchVectorsScoped(modelID, qvec, window, keep)
		if err != nil {
			return nil, "", fmt.Errorf("store: hybrid vector arm: %w", err)
		}
		keys := make([]string, 0, len(hits))
		for _, h := range hits {
			if h.Element.QualifiedName == "" {
				continue
			}
			record(h.Element.QualifiedName, h.Element).sim = h.Similarity
			keys = append(keys, h.Element.QualifiedName)
		}
		if len(keys) > 0 {
			lists = append(lists, RankList{Name: ArmVector, Keys: keys})
		}
	}

	if tsqueryUsable(query) {
		matches, err := s.searchElementsTS(query, window)
		if err != nil {
			kwErr = err
		} else if len(matches) > 0 {
			keys := make([]string, 0, len(matches))
			for _, m := range matches {
				// The caller's population applies to EVERY arm (see
				// hybrid_sqlite.go): scoping only the vector arm lets the
				// keyword arms put back exactly what the caller excluded.
				if keep != nil && !keep(m.Element) {
					continue
				}
				record(m.Element.QualifiedName, m.Element).kw = m.Score
				keys = append(keys, m.Element.QualifiedName)
			}
			if len(keys) > 0 {
				lists = append(lists, RankList{Name: ArmTSVector, Keys: keys})
			}
		}
	}

	// The trigram arm runs last so the sharper tsvector ranking wins a fused
	// tie between two documents the two keyword arms disagree about.
	if strings.TrimSpace(query) != "" {
		matches, err := s.searchElementsTrigram(query, window)
		name := ArmTrigram
		if err != nil {
			kwErr = err
			matches, err = s.FindFuzzy(query, window)
			name = ArmILIKE
		}
		if err != nil {
			kwErr = err
		} else if len(matches) > 0 {
			keys := make([]string, 0, len(matches))
			for _, m := range matches {
				if keep != nil && !keep(m.Element) {
					continue
				}
				record(m.Element.QualifiedName, m.Element).trigram = m.Score
				keys = append(keys, m.Element.QualifiedName)
			}
			if len(keys) > 0 {
				lists = append(lists, RankList{Name: name, Keys: keys})
			}
		}
	}

	// The name arm, for parity with SQLite (see hybrid_sqlite.go for the
	// measurement that motivated it: the shape gate fired on 0 of 84 labelled
	// questions because agents ask in prose, so the gate is corpus rarity).
	if matches, err := s.FindByNameToken(query, window); err == nil && len(matches) > 0 {
		keys := make([]string, 0, len(matches))
		for _, m := range matches {
			// The name arm is scoped like the others: it reads symbol names,
			// and a fixture's name is exactly what a name query matches.
			if keep != nil && !keep(m.Element) {
				continue
			}
			record(m.Element.QualifiedName, m.Element)
			keys = append(keys, m.Element.QualifiedName)
		}
		if len(keys) > 0 {
			lists = append(lists, RankList{Name: ArmName, Keys: keys})
		}
	}

	if len(lists) == 0 {
		return nil, "", kwErr
	}

	fused := FuseRRFWeighted(lists, w)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	out := make([]HybridHit, 0, len(fused))
	for _, f := range fused {
		a := byQN[f.Key]
		out = append(out, HybridHit{
			Element: a.el, Score: f.Score, Ranks: f.Ranks,
			Similarity: a.sim, KeywordScore: a.kw, TrigramScore: a.trigram,
		})
	}
	names := make([]string, 0, len(lists))
	for _, l := range lists {
		names = append(names, l.Name)
	}
	return out, "rrf(" + strings.Join(names, "+") + ")", nil
}
