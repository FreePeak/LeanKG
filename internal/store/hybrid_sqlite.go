package store

import (
	"context"
	"strings"
)

// Hybrid is the SQLite semantic rung: the cosine ranking, the FTS5 bm25 ranking
// and the name arm of one query merged by reciprocal rank fusion, the same
// fusion PostgreSQL applies to its arms. Every arm applies q.Filter in its WHERE
// before its LIMIT (the vector arm filters inside its scan). Only the vector arm
// failing is an error (core checked the stamp before embedding the query); a
// keyword arm error or an empty keyword arm leaves a vector-only fusion. The
// returned Arm is ArmFTS5 when the keyword arm contributed, else empty.
func (s *Store) Hybrid(ctx context.Context, q HybridQuery) ([]HybridHit, Arm, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	window := hybridWindow(limit)
	byQN := map[string]*HybridHit{}
	var lists []RankList
	var kw Arm

	if len(q.Vec) > 0 {
		hits, err := s.SearchVectors(ctx, VectorQuery{ModelID: q.ModelID, Vec: q.Vec, K: window, Filter: q.Filter})
		if err != nil {
			return nil, "", err
		}
		keys := make([]string, 0, len(hits))
		for _, h := range hits {
			byQN[h.Element.QualifiedName] = &HybridHit{Element: h.Element, Similarity: h.Similarity}
			keys = append(keys, h.Element.QualifiedName)
		}
		if len(keys) > 0 {
			lists = append(lists, RankList{Name: ArmVector, Keys: keys})
		}
	}
	if strings.TrimSpace(q.Text) != "" {
		if matches, arm, err := s.findFuzzy(ctx, q.Text, window, q.Filter); err == nil && len(matches) > 0 {
			keys := make([]string, 0, len(matches))
			for _, m := range matches {
				h := byQN[m.Element.QualifiedName]
				if h == nil {
					h = &HybridHit{Element: m.Element}
					byQN[m.Element.QualifiedName] = h
				}
				h.KeywordScore = m.Score
				keys = append(keys, m.Element.QualifiedName)
			}
			if len(keys) > 0 {
				lists = append(lists, RankList{Name: arm, Keys: keys})
				kw = arm
			}
		}
	}
	// The name arm: symbol-name evidence fused in, gated on RARITY in this
	// corpus rather than on the query's shape.
	//
	// The shape gate this replaces required a query with no whitespace — a bare
	// symbol name — and measured on all 84 labelled questions in
	// docs/retrieval-label-set.md it fired on ZERO of them, because agents ask
	// in prose ("take the single-flight lock for embedding") and the
	// informative token is a plain lowercase word (`lock`, `stamp`, `runes`).
	// Rarity asks the right question instead: does this token appear in so many
	// symbol names that it discriminates nothing? On this repository's 10,885
	// elements `read` is in 347 names and `stamp` in 33, so the same bound
	// rejects the first and keeps the second.
	if matches, err := s.FindByNameToken(ctx, q.Text, window, q.Filter); err == nil && len(matches) > 0 {
		keys := make([]string, 0, len(matches))
		for _, m := range matches {
			if _, ok := byQN[m.Element.QualifiedName]; !ok {
				byQN[m.Element.QualifiedName] = &HybridHit{Element: m.Element}
			}
			keys = append(keys, m.Element.QualifiedName)
		}
		if len(keys) > 0 {
			lists = append(lists, RankList{Name: ArmName, Keys: keys})
		}
	}
	// A cancelled ctx is an error, never a degraded answer: the arms above
	// swallow their own failures, so the ctx is checked here.
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if len(lists) == 0 {
		return nil, "", nil
	}
	// An explicit weight of 0 drops that arm's score (the documented "1,0"
	// drops the keyword arm); the arm still contributes its provenance.
	return fuseHybridHits(lists, q.Weights, limit, func(qn string) HybridHit { return *byQN[qn] }), kw, nil
}
