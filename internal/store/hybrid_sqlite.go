package store

import "strings"

// ArmFTS5 labels the SQLite keyword arm in hybrid provenance.
const ArmFTS5 = "fts5"

// ArmName labels the exact-symbol-name arm in hybrid provenance.
const ArmName = "name"

// HybridSearcher is the capability the L3 rung fuses through (RS-12). It was
// part of FTSBackend, which only PGStore implements, so SQLite's L3 was plain
// cosine while PostgreSQL fused keyword and vector ranks.
type HybridSearcher interface {
	HybridSearch(modelID, query string, qvec []float32, limit int) ([]HybridHit, string, error)
}

var (
	_ HybridSearcher = (*Store)(nil)
	_ HybridSearcher = (*PGStore)(nil)
)

// HybridSearch is the SQLite semantic rung: the cosine ranking and the FTS5
// bm25 ranking of one query merged by reciprocal rank fusion (FuseRRF, k=60),
// the same fusion PostgreSQL applies to its arms. Only the vector arm failing
// is an error (core checked the stamp before embedding the query); a keyword
// arm error or an empty keyword arm leaves a vector-only fusion.
func (s *Store) HybridSearch(modelID, query string, qvec []float32, limit int) ([]HybridHit, string, error) {
	if limit <= 0 {
		limit = 50
	}
	window := hybridWindow(limit)
	byQN := map[string]*HybridHit{}
	var lists []RankList

	if len(qvec) > 0 {
		hits, err := s.SearchVectors(modelID, qvec, window)
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
	if strings.TrimSpace(query) != "" {
		if matches, err := s.FindFuzzy(query, window); err == nil && len(matches) > 0 {
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
			lists = append(lists, RankList{Name: ArmFTS5, Keys: keys})
		}
	}
	// The name arm: the exact L1 lookup, fused in. Without it, a bare symbol
	// name — the commonest question an agent asks — is ranked by the vector and
	// keyword arms, which read content and not the name column. Measured on a
	// real 10,000-element store: the exact symbol for "MultiProject" ranked 9th
	// of 10 at L3, behind four functions that merely sounded related.
	if name := strings.TrimSpace(query); name != "" && !strings.ContainsAny(name, " \t\n") {
		if exact, err := s.FindExact(name); err == nil && len(exact) > 0 {
			keys := make([]string, 0, len(exact))
			for _, e := range exact {
				if _, ok := byQN[e.QualifiedName]; !ok {
					byQN[e.QualifiedName] = &HybridHit{Element: e}
				}
				keys = append(keys, e.QualifiedName)
			}
			lists = append(lists, RankList{Name: ArmName, Keys: keys})
		}
	}
	if len(lists) == 0 {
		return nil, "", nil
	}
	fused := FuseRRF(lists)
	if len(fused) > limit {
		fused = fused[:limit]
	}
	out := make([]HybridHit, 0, len(fused))
	for _, f := range fused {
		h := *byQN[f.Key]
		h.Score, h.Ranks = f.Score, f.Ranks
		out = append(out, h)
	}
	names := make([]string, 0, len(lists))
	for _, l := range lists {
		names = append(names, l.Name)
	}
	return out, "rrf(" + strings.Join(names, "+") + ")", nil
}
