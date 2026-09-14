package ontology

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Ontology-first, paginated discovery for mega-graphs (port of
// safe_discover.rs). Large workspaces must never materialize the full
// graph for discovery; callers go through the concept ontology first, then
// bounded name search, then targeted lookups.

// DefaultPageLimit is the discovery page size; MaxPageLimit is the hard
// ceiling for any single page.
const (
	DefaultPageLimit = 20
	MaxPageLimit     = 50
)

// MegaGraphThreshold returns the element count above which full-scan tools
// refuse (LEANKG_MAX_CACHE_ELEMENTS, default 50000; parity with
// mega_graph_threshold).
func MegaGraphThreshold() int {
	if v := os.Getenv("LEANKG_MAX_CACHE_ELEMENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50000
}

// ClampLimit clamps a requested page size to [DefaultPageLimit,
// MaxPageLimit]; 0 means the default (parity with clamp_limit).
func ClampLimit(limit int) int {
	if limit <= 0 {
		return DefaultPageLimit
	}
	if limit > MaxPageLimit {
		return MaxPageLimit
	}
	return limit
}

// IsMegaGraph reports whether the graph exceeds the mega-graph threshold.
// ponytail: counts elements per call via COUNT(*); the Rust engine cached a
// limit-1 probe to avoid counting 662k rows. Upgrade path: cache the count
// on the engine (invalidate on watermark bump) if this shows up in profiles.
func IsMegaGraph(st store.Backend) (bool, error) {
	n, err := st.ElementCount()
	if err != nil {
		return false, err
	}
	return n > MegaGraphThreshold(), nil
}

// MegaGraphRefusal is the standard refusal payload when a tool would
// full-scan a mega-graph (parity with mega_graph_refusal).
func MegaGraphRefusal(tool string, elementCount int) map[string]any {
	max := MegaGraphThreshold()
	return map[string]any{
		"error": fmt.Sprintf(
			"%s refused: graph has %d elements (max %d for full-scan tools)",
			tool, elementCount, max),
		"element_count": elementCount,
		"max_full_scan": max,
		"hint":          "Use concept_search, semantic_search, or search_code (ontology-first, paginated with limit/offset). Avoid get_clusters / full-tree scans on mega-graphs.",
		"recommended_tools": []string{
			"leankg_context", "concept_search", "semantic_search", "search_code", "kg_context",
		},
	}
}

// RefuseFullScanIfMega returns a refusal payload when the graph is a
// mega-graph, else nil (parity with refuse_full_scan_if_mega).
func RefuseFullScanIfMega(st store.Backend, tool string) (map[string]any, error) {
	mega, err := IsMegaGraph(st)
	if err != nil {
		return map[string]any{
			"error": fmt.Sprintf("%s refused: failed to count elements: %v", tool, err),
			"hint":  "Use concept_search / semantic_search with pagination.",
		}, nil
	}
	if !mega {
		return nil, nil
	}
	n, err := st.ElementCount()
	if err != nil {
		return map[string]any{
			"error": fmt.Sprintf("%s refused: failed to count elements: %v", tool, err),
			"hint":  "Use concept_search / semantic_search with pagination.",
		}, nil
	}
	return MegaGraphRefusal(tool, n), nil
}

// DiscoverPage is one page of ontology-first discovery (parity with
// DiscoverPage).
type DiscoverPage struct {
	Query         string               `json:"query"`
	Env           string               `json:"env"`
	Limit         int                  `json:"limit"`
	Offset        int                  `json:"offset"`
	Method        string               `json:"method"`
	Concept       *ConceptSearchResult `json:"concept,omitempty"`
	Results       []store.Element      `json:"results"`
	TotalEstimate int                  `json:"total_estimate"`
	HasMore       bool                 `json:"has_more"`
}

// Discover implements ontology-first discovery with pagination (parity
// with discover): concept search first; when it yields nothing, a scored
// bounded name search over the query keywords (never a full-table load).
func Discover(st store.Backend, query, env string, limit, offset int, preferOntology bool) (DiscoverPage, error) {
	limit = ClampLimit(limit)
	if env == "" {
		env = "local"
	}

	if preferOntology {
		// Fetch a slightly larger concept page then slice for offset.
		fetch := limit + offset
		if max := MaxPageLimit * 4; fetch > max {
			fetch = max
		}
		if fetch < limit {
			fetch = limit
		}
		concept, err := ConceptSearch(st, query, fetch)
		if err != nil {
			return DiscoverPage{}, err
		}
		if concept.ConceptMatchCount > 0 || len(concept.LinkedCode) > 0 {
			merged := concept.LinkedCode
			if len(merged) == 0 {
				merged = concept.FallbackResults
			}
			total := len(merged)
			page := slicePage(merged, offset, limit)
			return DiscoverPage{
				Query:         query,
				Env:           env,
				Limit:         limit,
				Offset:        offset,
				Method:        "ontology+concept",
				Concept:       &concept,
				Results:       page,
				TotalEstimate: total,
				HasMore:       offset+len(page) < total,
			}, nil
		}
	}

	// Semantic / name fallback: keyword probes with bounded search only.
	queryLower := strings.ToLower(query)
	probes := strings.Fields(queryLower)
	if len(probes) == 0 {
		probes = []string{query}
	}
	if len(probes) > 8 {
		probes = probes[:8]
	}

	seen := map[string]bool{}
	type scored struct {
		score int
		el    store.Element
	}
	var hits []scored
	for _, probe := range probes {
		fetch := limit + offset
		if fetch < limit {
			fetch = limit
		}
		matches, err := searchByName(st, probe, fetch, false)
		if err != nil {
			return DiscoverPage{}, err
		}
		for _, element := range matches {
			if seen[element.QualifiedName] {
				continue
			}
			seen[element.QualifiedName] = true
			nameLower := strings.ToLower(element.Name)
			qnLower := strings.ToLower(element.QualifiedName)
			score := 0
			if nameLower == queryLower {
				score += 100
			}
			if strings.Contains(nameLower, queryLower) {
				score += 40
			}
			for _, kw := range probes {
				if strings.Contains(nameLower, kw) {
					score += 10
				}
				if strings.Contains(qnLower, kw) {
					score += 3
				}
			}
			if score > 0 {
				hits = append(hits, scored{score: score, el: element})
			}
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	total := len(hits)
	page := make([]store.Element, 0, limit)
	for i, h := range hits {
		if i < offset {
			continue
		}
		if len(page) >= limit {
			break
		}
		page = append(page, h.el)
	}
	method := "semantic+name"
	if preferOntology {
		method = "semantic+name_fallback"
	}
	return DiscoverPage{
		Query:         query,
		Env:           env,
		Limit:         limit,
		Offset:        offset,
		Method:        method,
		Results:       page,
		TotalEstimate: total,
		HasMore:       offset+len(page) < total,
	}, nil
}

func slicePage(els []store.Element, offset, limit int) []store.Element {
	if offset >= len(els) {
		return nil
	}
	end := offset + limit
	if end > len(els) {
		end = len(els)
	}
	return els[offset:end]
}

// DiscoverPageToJSON renders a page in the MCP payload shape (parity with
// discover_page_to_json).
func DiscoverPageToJSON(page DiscoverPage) map[string]any {
	results := make([]map[string]any, 0, len(page.Results))
	for _, e := range page.Results {
		results = append(results, map[string]any{
			"qualified_name": e.QualifiedName,
			"name":           e.Name,
			"type":           e.ElementType,
			"element_type":   e.ElementType,
			"file":           e.FilePath,
			"file_path":      e.FilePath,
			"line":           e.LineStart,
			"line_start":     e.LineStart,
			"language":       e.Language,
		})
	}
	body := map[string]any{
		"query":          page.Query,
		"env":            page.Env,
		"method":         page.Method,
		"results":        results,
		"count":          len(results),
		"limit":          page.Limit,
		"offset":         page.Offset,
		"total_estimate": page.TotalEstimate,
		"has_more":       page.HasMore,
		"pagination": map[string]any{
			"limit":    page.Limit,
			"offset":   page.Offset,
			"has_more": page.HasMore,
		},
	}
	if page.Concept != nil {
		concepts := make([]map[string]any, 0, len(page.Concept.MatchedConcepts))
		for _, c := range page.Concept.MatchedConcepts {
			concepts = append(concepts, map[string]any{
				"gid":          c.GID,
				"name":         c.Name,
				"element_type": c.ElementType,
				"score":        c.MatchScore,
				"reason":       c.MatchReason,
				"code_refs":    c.CodeRefs,
			})
		}
		body["concepts"] = concepts
		body["concept_match_count"] = page.Concept.ConceptMatchCount
		body["fallback_used"] = page.Concept.FallbackUsed
	}
	return body
}

// SkipIncrementalDependents reports whether incremental indexing should
// skip full-graph dependent expansion: true when
// LEANKG_INCREMENTAL_SKIP_DEPENDENTS=1/true, or when the graph is a
// mega-graph (parity with skip_incremental_dependents).
func SkipIncrementalDependents(st store.Backend) bool {
	if v := os.Getenv("LEANKG_INCREMENTAL_SKIP_DEPENDENTS"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	mega, err := IsMegaGraph(st)
	return err == nil && mega
}
