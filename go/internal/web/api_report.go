// /api/query-graph and /api/graph/report endpoints, ported from the deleted
// Rust src/web/query_graph_api.rs, src/graph/nl_query.rs (QueryGraphResult
// pipeline) and GraphEngine::generate_graph_report + to_markdown.
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// queryGraphNode is US-GF-03 QueryGraphNode.
type queryGraphNode struct {
	QualifiedName string `json:"qualified_name"`
	Name          string `json:"name"`
	ElementType   string `json:"element_type"`
	FilePath      string `json:"file_path"`
	IsSeed        bool   `json:"is_seed"`
}

// queryGraphEdge is US-GF-03 QueryGraphEdge.
type queryGraphEdge struct {
	From            string  `json:"from"`
	To              string  `json:"to"`
	RelType         string  `json:"rel_type"`
	Confidence      float64 `json:"confidence"`
	ConfidenceLabel string  `json:"confidence_label"`
}

// pathHop is US-GF-01 PathHop.
type pathHop struct {
	From            string  `json:"from"`
	To              string  `json:"to"`
	RelType         string  `json:"rel_type"`
	Confidence      float64 `json:"confidence"`
	ConfidenceLabel string  `json:"confidence_label"`
	SourceFile      string  `json:"source_file"`
}

// shortestPathResult is US-GF-01 ShortestPathResult.
type shortestPathResult struct {
	Source string    `json:"source"`
	Target string    `json:"target"`
	Hops   int       `json:"hops"`
	Path   []pathHop `json:"path"`
}

// queryGraphResult is US-GF-03 / FR-GF-05 QueryGraphResult.
type queryGraphResult struct {
	Question       string              `json:"question"`
	Seeds          []string            `json:"seeds"`
	Nodes          []queryGraphNode    `json:"nodes"`
	Edges          []queryGraphEdge    `json:"edges"`
	Hops           int                 `json:"hops"`
	Truncated      bool                `json:"truncated"`
	TokenBudget    int                 `json:"token_budget"`
	TokensEstimate int                 `json:"tokens_estimate"`
	Path           *shortestPathResult `json:"path,omitempty"`
}

const (
	nlDefaultTokenBudget = 2000
	nlDefaultMaxDepth    = 2
	nlMaxSeeds           = 8
	nlMaxSeedHitsPerTerm = 3
	nlMaxFrontierVisits  = 40
)

var nlStopWords = map[string]struct{}{}

func init() {
	for _, w := range []string{
		"a", "an", "the", "is", "are", "was", "were", "be", "been", "being",
		"what", "which", "who", "whom", "whose", "where", "when", "why", "how",
		"do", "does", "did", "can", "could", "should", "would", "will", "may",
		"might", "must", "of", "in", "on", "at", "to", "for", "from", "by",
		"with", "about", "into", "through", "during", "before", "after",
		"above", "below", "between", "under", "again", "further", "then",
		"once", "here", "there", "all", "both", "each", "few", "more", "most",
		"other", "some", "such", "no", "nor", "not", "only", "own", "same",
		"so", "than", "too", "very", "just", "also",
		"connect", "connects", "connected", "connecting", "connection",
		"link", "links", "linked", "related", "relation", "relations",
		"relationship", "path", "paths", "show", "me", "find", "give", "tell",
		"please", "graph", "subgraph", "and", "or", "vs", "versus",
	} {
		nlStopWords[w] = struct{}{}
	}
}

// expandTerm is the lightweight synonym expansion (Rust expand_term).
func expandTerm(term string) []string {
	lower := strings.ToLower(term)
	out := []string{lower}
	switch lower {
	case "db", "database":
		out = append(out, "db", "database", "repo", "repository", "store")
	case "auth", "authentication":
		out = append(out, "auth", "authentication", "login", "session", "authorize")
	case "api":
		out = append(out, "api", "handler", "controller", "route")
	}
	sort.Strings(out)
	dedup := out[:0]
	for i, t := range out {
		if i == 0 || t != out[i-1] {
			dedup = append(dedup, t)
		}
	}
	return dedup
}

// extractKeywords splits on non-alphanumerics and drops stop words/shorts.
func extractKeywords(question string) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, raw := range strings.FieldsFunc(question, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' && r != '-'
	}) {
		lower := strings.ToLower(raw)
		if len(lower) < 2 {
			continue
		}
		if _, stop := nlStopWords[lower]; stop {
			continue
		}
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, lower)
	}
	return out
}

var connectPairPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)connects?\s+(\w[\w-]*)\s+to\s+(?:the\s+)?(\w[\w-]*)`),
	regexp.MustCompile(`(?i)from\s+(\w[\w-]*)\s+to\s+(?:the\s+)?(\w[\w-]*)`),
	regexp.MustCompile(`(?i)between\s+(\w[\w-]*)\s+and\s+(?:the\s+)?(\w[\w-]*)`),
	regexp.MustCompile(`(?i)(\w[\w-]*)\s+(?:->|→|↔)\s+(\w[\w-]*)`),
}

// extractConnectPair detects "connects X to Y" / "from X to Y" / "between
// X and Y" / arrow phrasings; falls back to the first two keywords.
func extractConnectPair(question string) (string, string, bool) {
	for _, re := range connectPairPatterns {
		if m := re.FindStringSubmatch(strings.ToLower(question)); m != nil {
			a, b := m[1], m[2]
			_, sa := nlStopWords[a]
			_, sb := nlStopWords[b]
			if !sa && !sb {
				return a, b, true
			}
		}
	}
	kws := extractKeywords(question)
	if len(kws) >= 2 {
		return kws[0], kws[1], true
	}
	return "", "", false
}

// rankSeedType prefers functions/methods, then types, files, modules.
func rankSeedType(elementType string) int {
	switch elementType {
	case "function", "method":
		return 0
	case "class", "struct", "interface", "type":
		return 1
	case "file":
		return 2
	case "module", "package":
		return 3
	default:
		return 4
	}
}

func labelRank(label string) int {
	switch label {
	case "EXTRACTED":
		return 0
	case "INFERRED":
		return 1
	default:
		return 2
	}
}

// resolveSeedTerms ports resolve_seed_terms over the snapshot: exact
// qualified name / exact element name / lowercase substring name match,
// ranked by element type. (The Rust mega-graph branch only throttled DB
// scans; the Go snapshot is already in memory, so one path suffices.)
func resolveSeedTerms(snap *snapshot, term string) []string {
	seen := map[string]struct{}{}
	byQN := snap.byQN
	addHit := func(qn string) {
		if _, dup := seen[qn]; dup {
			return
		}
		if _, ok := byQN[qn]; ok {
			seen[qn] = struct{}{}
		}
	}

	for _, variant := range expandTerm(term) {
		if _, ok := byQN[variant]; ok {
			addHit(variant)
		}
		if len(seen) >= nlMaxSeedHitsPerTerm {
			break
		}
		for _, e := range snap.elements {
			if len(seen) >= nlMaxSeedHitsPerTerm {
				break
			}
			if e.Name == variant {
				addHit(e.QualifiedName)
			}
		}
		if len(seen) >= nlMaxSeedHitsPerTerm {
			break
		}
		for _, e := range snap.elements {
			if len(seen) >= nlMaxSeedHitsPerTerm {
				break
			}
			if strings.Contains(strings.ToLower(e.Name), variant) {
				addHit(e.QualifiedName)
			}
		}
		if len(seen) >= nlMaxSeedHitsPerTerm {
			break
		}
	}

	found := make([]store.Element, 0, len(seen))
	for qn := range seen {
		found = append(found, byQN[qn])
	}
	sort.Slice(found, func(i, j int) bool {
		ri, rj := rankSeedType(found[i].ElementType), rankSeedType(found[j].ElementType)
		if ri != rj {
			return ri < rj
		}
		return found[i].QualifiedName < found[j].QualifiedName
	})
	if len(found) > nlMaxSeedHitsPerTerm {
		found = found[:nlMaxSeedHitsPerTerm]
	}
	out := make([]string, 0, len(found))
	for _, e := range found {
		out = append(out, e.QualifiedName)
	}
	return out
}

// rankElementType is the Rust rank_element_type priority.
func rankElementType(t string) int {
	switch t {
	case "function", "method", "constructor":
		return 0
	case "class", "struct", "interface", "enum", "trait":
		return 1
	case "route", "module", "property", "field":
		return 2
	case "file":
		return 3
	case "directory", "folder":
		return 4
	default:
		return 5
	}
}

// resolveToQualified ports resolve_to_qualified over the snapshot: exact
// qualified name > exact element name (type-ranked) > substring name >
// qualified-name suffix (path-like inputs).
func resolveToQualified(snap *snapshot, input string) (string, bool) {
	if input == "" {
		return "", false
	}
	if e, ok := snap.byQN[input]; ok {
		return e.QualifiedName, true
	}
	lower := strings.ToLower(input)

	byName := []store.Element{}
	for _, e := range snap.elements {
		if e.Name == input {
			byName = append(byName, e)
		}
	}
	if len(byName) > 0 {
		sort.Slice(byName, func(i, j int) bool {
			return rankElementType(byName[i].ElementType) < rankElementType(byName[j].ElementType)
		})
		return byName[0].QualifiedName, true
	}
	for _, e := range snap.elements {
		if strings.Contains(strings.ToLower(e.Name), lower) {
			return e.QualifiedName, true
		}
	}
	if strings.Contains(input, "/") || strings.Contains(input, "::") {
		for _, e := range snap.elements {
			if strings.HasSuffix(e.QualifiedName, input) {
				return e.QualifiedName, true
			}
		}
		for _, e := range snap.elements {
			if strings.Contains(e.QualifiedName, input) {
				return e.QualifiedName, true
			}
		}
		for _, e := range snap.elements {
			if strings.Contains(strings.ToLower(e.QualifiedName), lower) {
				return e.QualifiedName, true
			}
		}
	}
	return "", false
}

func qnMatchesTerm(qn, term string) bool {
	t := strings.ToLower(term)
	q := strings.ToLower(qn)
	if strings.Contains(q, t) {
		return true
	}
	if i := strings.LastIndex(q, "::"); i >= 0 {
		return strings.Contains(q[i+2:], t)
	}
	return false
}

func sourceFileOf(rel store.Relationship) string {
	if s, ok := rel.Metadata["source_file"].(string); ok {
		return s
	}
	return ""
}

// nlEngine is the loaded-graph working set for one query_graph call.
type nlEngine struct {
	snap *snapshot
	out  map[string][]store.Relationship
	in   map[string][]store.Relationship
}

func newNLEngine(snap *snapshot, rels []store.Relationship) *nlEngine {
	out := map[string][]store.Relationship{}
	in := map[string][]store.Relationship{}
	for _, r := range rels {
		out[r.Source] = append(out[r.Source], r)
		in[r.Target] = append(in[r.Target], r)
	}
	return &nlEngine{snap: snap, out: out, in: in}
}

// neighborsOf returns the undirected relationship set of a node, sorted by
// provenance rank (EXTRACTED first), like the Rust BFS ordering.
func (nl *nlEngine) neighborsOf(qn string) []store.Relationship {
	neighbors := make([]store.Relationship, 0, len(nl.out[qn])+len(nl.in[qn]))
	neighbors = append(neighbors, nl.out[qn]...)
	neighbors = append(neighbors, nl.in[qn]...)
	sort.SliceStable(neighbors, func(i, j int) bool {
		return labelRank(confidenceLabelFor(neighbors[i])) < labelRank(confidenceLabelFor(neighbors[j]))
	})
	return neighbors
}

// neighborOf returns the far end of a relationship relative to node.
func neighborOf(rel store.Relationship, node string) (string, bool) {
	if rel.Source == node {
		return rel.Target, true
	}
	if rel.Target == node {
		return rel.Source, true
	}
	return "", false
}

// shortestPath ports GraphEngine::shortest_path over loaded slices: BFS by
// hop level keeping the best-provenance path per node; visits capped at 120.
func (nl *nlEngine) shortestPath(source, target string, maxHops int) *shortestPathResult {
	if maxHops < 1 {
		maxHops = 1
	}
	if maxHops > 10 {
		maxHops = 10
	}
	src, ok := resolveToQualified(nl.snap, source)
	if !ok {
		return nil
	}
	tgt, ok := resolveToQualified(nl.snap, target)
	if !ok {
		return nil
	}
	if src == tgt {
		return &shortestPathResult{Source: src, Target: tgt}
	}

	const maxVisits = 120
	pathScore := func(path []pathHop) int {
		s := 0
		for _, h := range path {
			s += labelRank(h.ConfidenceLabel)
		}
		return s
	}

	type frontierItem struct {
		node string
		path []pathHop
	}
	frontier := []frontierItem{{src, nil}}
	visits := 0
	var best *shortestPathResult

	for range maxHops {
		if len(frontier) == 0 {
			break
		}
		var next []frontierItem
		bestAtLevel := map[string]int{}
		foundTarget := false

		for _, fi := range frontier {
			if visits > maxVisits {
				break
			}
			visits++
			for _, rel := range nl.neighborsOf(fi.node) {
				nextNode, ok := neighborOf(rel, fi.node)
				if !ok {
					continue
				}
				newPath := append(append([]pathHop{}, fi.path...), pathHop{
					From:            rel.Source,
					To:              rel.Target,
					RelType:         rel.RelType,
					Confidence:      rel.Confidence,
					ConfidenceLabel: confidenceLabelFor(rel),
					SourceFile:      sourceFileOf(rel),
				})
				if nextNode == tgt {
					candidate := &shortestPathResult{Source: src, Target: tgt, Hops: len(newPath), Path: newPath}
					replace := best == nil || candidate.Hops < best.Hops ||
						(candidate.Hops == best.Hops && pathScore(candidate.Path) < pathScore(best.Path))
					if replace {
						best = candidate
					}
					foundTarget = true
					continue
				}
				score := pathScore(newPath)
				if prev, seen := bestAtLevel[nextNode]; seen && score >= prev {
					continue
				}
				bestAtLevel[nextNode] = score
				next = append(next, frontierItem{nextNode, newPath})
			}
		}
		if foundTarget {
			break
		}
		frontier = next
	}
	return best
}

func ensureNodeStub(nodes map[string]*queryGraphNode, qn string, isSeed bool) {
	if n, ok := nodes[qn]; ok {
		if isSeed {
			n.IsSeed = true
		}
		return
	}
	name := qn
	if i := strings.LastIndex(qn, "::"); i >= 0 {
		name = qn[i+2:]
	}
	nodes[qn] = &queryGraphNode{QualifiedName: qn, Name: name, IsSeed: isSeed}
}

// expandFromSeeds ports expand_from_seeds: bounded BFS around seeds.
func (nl *nlEngine) expandFromSeeds(seeds []string, maxDepth int) (map[string]*queryGraphNode, []queryGraphEdge, int) {
	nodes := map[string]*queryGraphNode{}
	var edges []queryGraphEdge
	edgeKeys := map[[3]string]struct{}{}
	visited := map[string]int{}
	type qi struct {
		qn   string
		dist int
	}
	queue := []qi{}
	for _, s := range seeds {
		if _, dup := visited[s]; dup {
			continue
		}
		visited[s] = 0
		queue = append(queue, qi{s, 0})
		ensureNodeStub(nodes, s, true)
	}

	maxHop := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(visited) > nlMaxFrontierVisits {
			break
		}
		if cur.dist > maxHop {
			maxHop = cur.dist
		}
		if cur.dist >= maxDepth {
			continue
		}
		for _, rel := range nl.neighborsOf(cur.qn) {
			nextNode, ok := neighborOf(rel, cur.qn)
			if !ok {
				continue
			}
			key := [3]string{rel.Source, rel.Target, rel.RelType}
			if _, dup := edgeKeys[key]; !dup {
				edgeKeys[key] = struct{}{}
				edges = append(edges, queryGraphEdge{
					From:            rel.Source,
					To:              rel.Target,
					RelType:         rel.RelType,
					Confidence:      rel.Confidence,
					ConfidenceLabel: confidenceLabelFor(rel),
				})
			}
			nextDist := cur.dist + 1
			if prev, seen := visited[nextNode]; !seen || nextDist < prev {
				visited[nextNode] = nextDist
				ensureNodeStub(nodes, nextNode, false)
				queue = append(queue, qi{nextNode, nextDist})
			}
		}
	}
	return nodes, edges, maxHop
}

// runQueryGraph runs the full Rust query_graph pipeline over loaded slices.
func (nl *nlEngine) runQueryGraph(question string, tokenBudget, maxDepth *int) (*queryGraphResult, error) {
	budget := nlDefaultTokenBudget
	if tokenBudget != nil {
		budget = *tokenBudget
	}
	budget = min(max(budget, 200), 20_000)
	depth := nlDefaultMaxDepth
	if maxDepth != nil {
		depth = *maxDepth
	}
	depth = min(max(depth, 1), 5)

	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("question must not be empty")
	}

	mega := nl.snap.isMegaGraph()
	connectA, connectB, hasPair := extractConnectPair(question)
	keywords := extractKeywords(question)

	seedQNs := []string{}
	seedSet := map[string]struct{}{}
	addSeeds := func(qns []string) {
		for _, qn := range qns {
			if len(seedQNs) >= nlMaxSeeds {
				return
			}
			if _, dup := seedSet[qn]; dup {
				continue
			}
			seedSet[qn] = struct{}{}
			seedQNs = append(seedQNs, qn)
		}
	}

	if hasPair {
		addSeeds(resolveSeedTerms(nl.snap, connectA))
		addSeeds(resolveSeedTerms(nl.snap, connectB))
	}
	if !mega || !hasPair {
		for _, kw := range keywords {
			if len(seedQNs) >= nlMaxSeeds {
				break
			}
			if hasPair && (kw == connectA || kw == connectB) {
				continue
			}
			addSeeds(resolveSeedTerms(nl.snap, kw))
		}
	}

	var pathSummary *shortestPathResult
	nodes := map[string]*queryGraphNode{}
	edges := []queryGraphEdge{}
	hopsUsed := 0

	if !mega && hasPair {
		src, sOK := pickSeedForTerm(seedQNs, connectA)
		if !sOK {
			src, sOK = resolveToQualified(nl.snap, connectA)
		}
		tgt, tOK := pickSeedForTerm(seedQNs, connectB)
		if !tOK {
			tgt, tOK = resolveToQualified(nl.snap, connectB)
		}
		if sOK && tOK {
			if path := nl.shortestPath(src, tgt, depth); path != nil {
				hopsUsed = path.Hops
				for _, hop := range path.Path {
					edges = pushEdgeFromHop(edges, hop)
					_, seedA := seedSet[hop.From]
					ensureNodeStub(nodes, hop.From, seedA)
					_, seedB := seedSet[hop.To]
					ensureNodeStub(nodes, hop.To, seedB)
				}
				if path.Hops == 0 {
					ensureNodeStub(nodes, path.Source, true)
				}
				pathSummary = path
			}
		}
	}

	if len(seedQNs) > 0 {
		expandDepth := depth
		if mega && expandDepth > 1 {
			expandDepth = 1
		}
		expNodes, expEdges, expHops := nl.expandFromSeeds(seedQNs, expandDepth)
		hopsUsed = max(hopsUsed, expHops)
		for qn, n := range expNodes {
			if _, ok := nodes[qn]; !ok {
				nodes[qn] = n
			}
		}
		edges = mergeEdges(edges, expEdges)
	}

	// Enrich stubs with element metadata.
	for qn, n := range nodes {
		if e, ok := nl.snap.byQN[qn]; ok {
			n.Name = e.Name
			n.ElementType = e.ElementType
			n.FilePath = e.FilePath
		}
	}

	result := &queryGraphResult{
		Question:    question,
		Seeds:       seedQNs,
		Nodes:       make([]queryGraphNode, 0, len(nodes)),
		Edges:       edges,
		Hops:        hopsUsed,
		TokenBudget: budget,
		Path:        pathSummary,
	}
	for _, n := range nodes {
		result.Nodes = append(result.Nodes, *n)
	}
	sort.Slice(result.Nodes, func(i, j int) bool {
		if result.Nodes[i].IsSeed != result.Nodes[j].IsSeed {
			return result.Nodes[i].IsSeed
		}
		return result.Nodes[i].QualifiedName < result.Nodes[j].QualifiedName
	})
	sort.Slice(result.Edges, func(i, j int) bool {
		if result.Edges[i].From != result.Edges[j].From {
			return result.Edges[i].From < result.Edges[j].From
		}
		return result.Edges[i].To < result.Edges[j].To
	})

	trimToBudget(result, budget)
	result.TokensEstimate = estimateTokens(result)
	return result, nil
}

func pickSeedForTerm(seeds []string, term string) (string, bool) {
	for _, qn := range seeds {
		if qnMatchesTerm(qn, term) {
			return qn, true
		}
	}
	return "", false
}

func pushEdgeFromHop(edges []queryGraphEdge, hop pathHop) []queryGraphEdge {
	for _, e := range edges {
		if e.From == hop.From && e.To == hop.To && e.RelType == hop.RelType {
			return edges
		}
	}
	return append(edges, queryGraphEdge{
		From: hop.From, To: hop.To, RelType: hop.RelType,
		Confidence: hop.Confidence, ConfidenceLabel: hop.ConfidenceLabel,
	})
}

func mergeEdges(into, extra []queryGraphEdge) []queryGraphEdge {
	for _, e := range extra {
		dup := false
		for _, x := range into {
			if x.From == e.From && x.To == e.To && x.RelType == e.RelType {
				dup = true
				break
			}
		}
		if !dup {
			into = append(into, e)
		}
	}
	return into
}

func estimateTokens(r *queryGraphResult) int {
	b, err := json.Marshal(r)
	if err != nil {
		return 0
	}
	return len(b) / 4
}

// syncPathWithEdges keeps path hops aligned with surviving edges.
func syncPathWithEdges(r *queryGraphResult) {
	if r.Path == nil {
		return
	}
	edgeKeys := map[[3]string]struct{}{}
	for _, e := range r.Edges {
		edgeKeys[[3]string{e.From, e.To, e.RelType}] = struct{}{}
	}
	kept := r.Path.Path[:0]
	for _, hop := range r.Path.Path {
		if _, ok := edgeKeys[[3]string{hop.From, hop.To, hop.RelType}]; ok {
			kept = append(kept, hop)
		}
	}
	r.Path.Path = kept
	r.Path.Hops = len(kept)
	if len(kept) == 0 {
		r.Path = nil
		return
	}
	r.Path.Source = kept[0].From
	r.Path.Target = kept[len(kept)-1].To
}

func pruneOrphanNodes(r *queryGraphResult) {
	keep := map[string]struct{}{}
	for _, s := range r.Seeds {
		keep[s] = struct{}{}
	}
	for _, e := range r.Edges {
		keep[e.From] = struct{}{}
		keep[e.To] = struct{}{}
	}
	kept := r.Nodes[:0]
	for _, n := range r.Nodes {
		if _, ok := keep[n.QualifiedName]; ok || n.IsSeed {
			kept = append(kept, n)
		}
	}
	r.Nodes = kept
}

// trimToBudget drops AMBIGUOUS then INFERRED edges, then least-connected
// non-seed nodes, until the estimate fits the budget.
func trimToBudget(r *queryGraphResult, budget int) {
	if estimateTokens(r) <= budget {
		return
	}
	r.Truncated = true

	r.Edges = dropLabel(r.Edges, "AMBIGUOUS")
	syncPathWithEdges(r)
	pruneOrphanNodes(r)
	if estimateTokens(r) <= budget {
		return
	}

	r.Edges = dropLabel(r.Edges, "INFERRED")
	syncPathWithEdges(r)
	pruneOrphanNodes(r)
	if estimateTokens(r) <= budget {
		return
	}

	for {
		if estimateTokens(r) <= budget {
			break
		}
		degrees := map[string]int{}
		for _, e := range r.Edges {
			degrees[e.From]++
			degrees[e.To]++
		}
		victim := -1
		for i, n := range r.Nodes {
			if n.IsSeed {
				continue
			}
			if victim < 0 || degrees[n.QualifiedName] < degrees[r.Nodes[victim].QualifiedName] {
				victim = i
			}
		}
		if victim < 0 {
			// Path summary can dominate the JSON budget even when edges are
			// gone.
			if r.Path != nil {
				r.Path = nil
				continue
			}
			if len(r.Edges) == 0 {
				break
			}
			r.Edges = r.Edges[:len(r.Edges)-1]
			syncPathWithEdges(r)
			pruneOrphanNodes(r)
			continue
		}
		qn := r.Nodes[victim].QualifiedName
		keptNodes := r.Nodes[:0]
		for _, n := range r.Nodes {
			if n.QualifiedName != qn {
				keptNodes = append(keptNodes, n)
			}
		}
		r.Nodes = keptNodes
		keptEdges := r.Edges[:0]
		for _, e := range r.Edges {
			if e.From != qn && e.To != qn {
				keptEdges = append(keptEdges, e)
			}
		}
		r.Edges = keptEdges
		syncPathWithEdges(r)
	}
}

func dropLabel(edges []queryGraphEdge, label string) []queryGraphEdge {
	kept := edges[:0]
	for _, e := range edges {
		if e.ConfidenceLabel != label {
			kept = append(kept, e)
		}
	}
	return kept
}

// --- POST /api/query-graph ---

func (h *apiH) queryGraph(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question    string `json:"question"`
		TokenBudget *int   `json:"token_budget"`
		MaxDepth    *int   `json:"max_depth"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeEnvelope(w, failEnvelope("invalid JSON body: "+err.Error()))
		return
	}
	q := strings.TrimSpace(req.Question)
	if q == "" {
		writeEnvelope(w, failEnvelope("question must not be empty"))
		return
	}

	snap, err := loadSnapshot(h.engine.Store())
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	rels, err := allRelationships(h.engine.Store())
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	nl := newNLEngine(snap, rels)
	result, err := nl.runQueryGraph(q, req.TokenBudget, req.MaxDepth)
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	writeEnvelope(w, okEnvelope(result))
}

// --- GET /api/graph/report ---

type GodNode struct {
	QualifiedName string `json:"qualified_name"`
	Name          string `json:"name"`
	ElementType   string `json:"element_type"`
	Degree        int    `json:"degree"`
}

type labelCount struct {
	Label string  `json:"label"`
	Count int     `json:"count"`
	Pct   float64 `json:"pct"`
}

type surprisingEdge struct {
	SourceQualified string `json:"source_qualified"`
	TargetQualified string `json:"target_qualified"`
	ClusterSource   string `json:"cluster_source"`
	ClusterTarget   string `json:"cluster_target"`
	Reason          string `json:"reason"`
}

type GraphReport struct {
	Project                string           `json:"project"`
	TotalElements          int              `json:"total_elements"`
	TotalRelationships     int              `json:"total_relationships"`
	FileCount              int              `json:"file_count"`
	FunctionCount          int              `json:"function_count"`
	ClassCount             int              `json:"class_count"`
	GodNodes               []GodNode        `json:"god_nodes"`
	ConfidenceDistribution []labelCount     `json:"confidence_distribution"`
	SurprisingEdges        []surprisingEdge `json:"surprising_edges"`
	SuggestedQuestions     []string         `json:"suggested_questions"`
}

// getGodNodes ports get_god_nodes(limit, excludeHubsPercentile): top
// in+out degree elements, hub super-nodes above the percentile excluded,
// deterministic tie-break by qualified name.
func getGodNodes(snap *snapshot, rels []store.Relationship, limit, excludeHubsPercentile int) []GodNode {
	limit = min(max(limit, 1), 200)
	degree := map[string]int{}
	for _, r := range rels {
		degree[r.Source]++
		degree[r.Target]++
	}
	type qd struct {
		qn string
		d  int
	}
	nodes := make([]qd, 0, len(degree))
	for qn, d := range degree {
		nodes = append(nodes, qd{qn, d})
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].d != nodes[j].d {
			return nodes[i].d > nodes[j].d
		}
		return nodes[i].qn < nodes[j].qn
	})
	if excludeHubsPercentile > 0 && len(nodes) > 0 {
		cutoff := max(int(float64(len(nodes))*(100.0-float64(excludeHubsPercentile))/100.0), 1)
		nodes = nodes[:min(cutoff, len(nodes))]
	}
	nodes = nodes[:min(limit, len(nodes))]

	out := make([]GodNode, 0, len(nodes))
	for _, n := range nodes {
		g := GodNode{QualifiedName: n.qn, Name: n.qn, Degree: n.d}
		if e, ok := snap.byQN[n.qn]; ok {
			g.Name = e.Name
			g.ElementType = e.ElementType
		}
		out = append(out, g)
	}
	return out
}

func (h *apiH) graphReport(w http.ResponseWriter, _ *http.Request) {
	report, err := buildGraphReport(h.projectDir, h.engine, "")
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	md := reportToMarkdown(report)

	// Rust also wrote .leankg/GRAPH_REPORT.md; keep that side effect.
	if h.projectDir != "" {
		outPath := filepath.Join(h.projectDir, ".leankg", "GRAPH_REPORT.md")
		_ = os.MkdirAll(filepath.Dir(outPath), 0o755)
		_ = os.WriteFile(outPath, []byte(md), 0o644)
	}

	writeEnvelope(w, okEnvelope(map[string]any{"markdown": md}))
}

// buildGraphReport assembles the US-GF-06 graph report over the engine's
// store (Rust GraphEngine::generate_graph_report). projectName overrides the
// display name; "" falls back to the project directory's basename, then
// "project".
func buildGraphReport(projectDir string, engine *core.Engine, projectName string) (GraphReport, error) {
	project := projectNameFor(projectDir, projectName)

	snap, err := loadSnapshot(engine.Store())
	if err != nil {
		return GraphReport{}, err
	}
	rels, err := allRelationships(engine.Store())
	if err != nil {
		return GraphReport{}, err
	}

	gods := getGodNodes(snap, rels, 10, 90)

	byLabel := map[string]int{}
	for _, r := range rels {
		byLabel[confidenceLabelFor(r)]++
	}
	total := len(rels)
	confDist := make([]labelCount, 0, 3)
	for _, l := range []string{"EXTRACTED", "INFERRED", "AMBIGUOUS"} {
		count := byLabel[l]
		pct := 0.0
		if total > 0 {
			pct = float64(count) * 100.0 / float64(total)
		}
		confDist = append(confDist, labelCount{Label: l, Count: count, Pct: pct})
	}

	fileCount, functionCount, classCount := 0, 0, 0
	for _, e := range snap.elements {
		switch e.ElementType {
		case "file":
			fileCount++
		case "function":
			functionCount++
		case "class", "struct":
			classCount++
		}
	}

	// The Rust engine read the persisted cluster_id column; the Go store has
	// none, so clusters come from the ported Louvain detector.
	clusters := detectCommunities(snap, rels)
	clusterOf := map[string]string{}
	for _, c := range clusters {
		for _, m := range c.Members {
			clusterOf[m] = c.ID
		}
	}

	godQN := map[string]struct{}{}
	for _, g := range gods {
		godQN[g.QualifiedName] = struct{}{}
	}
	maxEdges := 1000
	if v := os.Getenv("LEANKG_MAX_REPORT_EDGES"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n > 0 {
			maxEdges = n
		}
	}
	surprising := make([]surprisingEdge, 0, 10)
	for i, r := range rels {
		if i >= maxEdges || len(surprising) >= 10 {
			break
		}
		_, srcInGod := godQN[r.Source]
		_, tgtInGod := godQN[r.Target]
		if !srcInGod && !tgtInGod {
			continue
		}
		srcCluster := clusterOf[r.Source]
		tgtCluster := clusterOf[r.Target]
		if srcCluster == "" && tgtCluster == "" {
			continue
		}
		if srcCluster == tgtCluster {
			continue
		}
		surprising = append(surprising, surprisingEdge{
			SourceQualified: r.Source,
			TargetQualified: r.Target,
			ClusterSource:   orNone(srcCluster),
			ClusterTarget:   orNone(tgtCluster),
			Reason: fmt.Sprintf("%s (cluster %s) connects to %s (cluster %s)",
				r.Source, orNone(srcCluster), r.Target, orNone(tgtCluster)),
		})
	}

	topGodName := ""
	if len(gods) > 0 {
		topGodName = gods[0].Name
	}
	questions := []string{
		fmt.Sprintf("Which functions in %s are most central to the call graph? (use explain_node on top god nodes)", project),
		"Find the shortest path from a hot entry point to a low-level helper (use shortest_path)",
		fmt.Sprintf("How many of the %d relationships are AMBIGUOUS vs EXTRACTED? Where are the AMBIGUOUS edges clustered?", total),
		"Which directories hold the most cross-cluster traffic (use query_graph or shortest_path across cluster boundaries)?",
		fmt.Sprintf("What is the impact radius of the highest-degree %s (use get_impact_radius)?", topGodName),
	}

	return GraphReport{
		Project:                project,
		TotalElements:          snap.count(),
		TotalRelationships:     total,
		FileCount:              fileCount,
		FunctionCount:          functionCount,
		ClassCount:             classCount,
		GodNodes:               gods,
		ConfidenceDistribution: confDist,
		SurprisingEdges:        surprising,
		SuggestedQuestions:     questions,
	}, nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// reportToMarkdown ports GraphReport::to_markdown.
func reportToMarkdown(r GraphReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Graph Report: %s\n\n", r.Project)
	b.WriteString("## Overview\n\n")
	fmt.Fprintf(&b, "- Total elements: %d\n", r.TotalElements)
	fmt.Fprintf(&b, "- Total relationships: %d\n", r.TotalRelationships)
	fmt.Fprintf(&b, "- Files: %d\n", r.FileCount)
	fmt.Fprintf(&b, "- Functions: %d\n", r.FunctionCount)
	fmt.Fprintf(&b, "- Classes/Structs: %d\n\n", r.ClassCount)

	b.WriteString("## Confidence Distribution\n\n")
	b.WriteString("| Label | Count | % |\n|---|---|---|\n")
	for _, c := range r.ConfidenceDistribution {
		fmt.Fprintf(&b, "| %s | %d | %.1f%% |\n", c.Label, c.Count, c.Pct)
	}
	b.WriteString("\n")

	b.WriteString("## Surprising Cross-Cluster Edges\n\n")
	if len(r.SurprisingEdges) == 0 {
		b.WriteString("_No cross-cluster edges detected._\n\n")
	} else {
		b.WriteString("| Source | Target | Source Cluster | Target Cluster | Reason |\n|---|---|---|---|---|\n")
		for _, e := range r.SurprisingEdges {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				e.SourceQualified, e.TargetQualified, e.ClusterSource, e.ClusterTarget, e.Reason)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Top God Nodes\n\n")
	if len(r.GodNodes) == 0 {
		b.WriteString("_No relationships indexed yet._\n\n")
	} else {
		b.WriteString("| Qualified Name | Type | Degree |\n|---|---|---|\n")
		for _, n := range r.GodNodes {
			fmt.Fprintf(&b, "| %s | %s | %d |\n", n.QualifiedName, n.ElementType, n.Degree)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Suggested Questions\n\n")
	for i, q := range r.SuggestedQuestions {
		fmt.Fprintf(&b, "%d. %s\n", i+1, q)
	}
	return b.String()
}
