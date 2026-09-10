// Package graph implements pure-Go graph traversal verbs over store.Backend
// (Rust graph/query.rs parity): blast radius, shortest path, callers/callees,
// focused context, and explain. All traversal is in-process BFS — no SQL
// recursion — deterministic by qualified name, cycle-safe via visited sets,
// and depth-bounded (DefaultDepth 2, hard MaxDepth 5).
package graph

import (
	"errors"
	"sort"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const (
	// DefaultDepth is the traversal depth used when callers pass <= 0.
	DefaultDepth = 2
	// MaxDepth is the hard ceiling for any traversal depth.
	MaxDepth = 5
)

// ErrUnknownNode is returned when a traversal seed resolves to neither an
// element nor any edge endpoint. errors.Is-able.
var ErrUnknownNode = errors.New("graph: unknown node")

// Hit is one node in a blast-radius result.
type Hit struct {
	QN    string `json:"qn"`
	Depth int    `json:"depth"`
}

// clampDepth normalizes a caller-supplied depth: <= 0 becomes DefaultDepth,
// values above MaxDepth are clamped to MaxDepth.
func clampDepth(depth int) int {
	if depth <= 0 {
		return DefaultDepth
	}
	if depth > MaxDepth {
		return MaxDepth
	}
	return depth
}

// hasNode reports whether qn is known to the graph: an element with that
// qualified name exists, or at least one edge touches it.
func hasNode(st store.Backend, qn string) (bool, error) {
	els, err := st.FindExact(qn)
	if err != nil {
		return false, err
	}
	if len(els) > 0 {
		return true, nil
	}
	out, err := st.Outgoing(qn)
	if err != nil {
		return false, err
	}
	if len(out) > 0 {
		return true, nil
	}
	in, err := st.Incoming(qn)
	if err != nil {
		return false, err
	}
	return len(in) > 0, nil
}

// Impact returns the blast radius of qn: BFS over INCOMING edges (who depends
// on it), seed included at depth 0. Deterministic: sorted by qualified name.
func Impact(st store.Backend, qn string, depth int) ([]Hit, error) {
	depth = clampDepth(depth)
	ok, err := hasNode(st, qn)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnknownNode
	}

	visited := map[string]bool{qn: true}
	frontier := []string{qn}
	hits := []Hit{{QN: qn, Depth: 0}}

	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []string
		for _, node := range frontier {
			rels, err := st.Incoming(node)
			if err != nil {
				return nil, err
			}
			for _, r := range rels {
				if !visited[r.Source] {
					visited[r.Source] = true
					next = append(next, r.Source)
				}
			}
		}
		sort.Strings(next)
		for _, q := range next {
			hits = append(hits, Hit{QN: q, Depth: d})
		}
		frontier = next
	}
	return hits, nil
}

// ShortestPath finds the shortest path from from to to over the undirected
// view of the graph, inclusive of both endpoints. Returns nil (no error) when
// unreachable. maxDepth is clamped like every traversal depth.
func ShortestPath(st store.Backend, from, to string, maxDepth int) ([]string, error) {
	maxDepth = clampDepth(maxDepth)
	for _, seed := range []string{from, to} {
		ok, err := hasNode(st, seed)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrUnknownNode
		}
	}
	if from == to {
		return []string{from}, nil
	}

	parent := map[string]string{from: ""}
	frontier := []string{from}
	for d := 0; d < maxDepth && len(frontier) > 0; d++ {
		var next []string
		for _, node := range frontier {
			neighbors, err := undirectedNeighbors(st, node)
			if err != nil {
				return nil, err
			}
			for _, nb := range neighbors {
				if _, seen := parent[nb]; seen {
					continue
				}
				parent[nb] = node
				if nb == to {
					return reconstruct(parent, to), nil
				}
				next = append(next, nb)
			}
		}
		sort.Strings(next)
		frontier = next
	}
	return nil, nil
}

// undirectedNeighbors returns both directions of edges touching node,
// deduplicated and sorted.
func undirectedNeighbors(st store.Backend, node string) ([]string, error) {
	out, err := st.Outgoing(node)
	if err != nil {
		return nil, err
	}
	in, err := st.Incoming(node)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var nb []string
	for _, r := range out {
		if !seen[r.Target] {
			seen[r.Target] = true
			nb = append(nb, r.Target)
		}
	}
	for _, r := range in {
		if !seen[r.Source] {
			seen[r.Source] = true
			nb = append(nb, r.Source)
		}
	}
	sort.Strings(nb)
	return nb, nil
}

// reconstruct walks the parent chain back to the seed and reverses it.
func reconstruct(parent map[string]string, to string) []string {
	var path []string
	for n := to; ; n = parent[n] {
		path = append(path, n)
		if parent[n] == "" {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Callers returns the deduplicated, sorted set of elements with a "calls"
// edge targeting qn.
func Callers(st store.Backend, qn string) ([]string, error) {
	rels, err := st.Incoming(qn)
	if err != nil {
		return nil, err
	}
	return collectEnds(rels, true), nil
}

// Callees returns the deduplicated, sorted set of elements that qn's "calls"
// edges target.
func Callees(st store.Backend, qn string) ([]string, error) {
	rels, err := st.Outgoing(qn)
	if err != nil {
		return nil, err
	}
	return collectEnds(rels, false), nil
}

// collectEnds extracts the deduplicated, sorted opposite endpoint of each
// "calls" edge: sources when incoming is true, targets otherwise.
func collectEnds(rels []store.Relationship, incoming bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rels {
		if r.RelType != "calls" {
			continue
		}
		end := r.Target
		if incoming {
			end = r.Source
		}
		if !seen[end] {
			seen[end] = true
			out = append(out, end)
		}
	}
	sort.Strings(out)
	return out
}

// Context returns a focused view of qn: the element itself plus its
// "incoming" and "outgoing" edges grouped by rel_type as sorted QN lists,
// each list capped at limit entries (limit <= 0 means uncapped).
func Context(st store.Backend, qn string, limit int) (map[string]any, error) {
	els, err := st.FindExact(qn)
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, ErrUnknownNode
	}
	in, err := st.Incoming(qn)
	if err != nil {
		return nil, err
	}
	out, err := st.Outgoing(qn)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"element":  els[0],
		"incoming": groupByRelType(in, limit, relSource),
		"outgoing": groupByRelType(out, limit, relTarget),
	}, nil
}

func relSource(r store.Relationship) string { return r.Source }
func relTarget(r store.Relationship) string { return r.Target }

// groupByRelType buckets edges by rel_type into sorted, capped QN lists;
// pick selects the opposite endpoint (source for incoming, target for outgoing).
func groupByRelType(rels []store.Relationship, limit int, pick func(store.Relationship) string) map[string]any {
	grouped := map[string][]string{}
	for _, r := range rels {
		grouped[r.RelType] = append(grouped[r.RelType], pick(r))
	}
	res := map[string]any{}
	for rt, qns := range grouped {
		sort.Strings(qns)
		if limit > 0 && len(qns) > limit {
			qns = qns[:limit]
		}
		anyQNs := make([]any, len(qns))
		for i, q := range qns {
			anyQNs[i] = q
		}
		res[rt] = anyQNs
	}
	return res
}

// Explain returns qn's element plus degree counts, per-rel-type counts, and up
// to 5 sample edges per direction (source/target/rel_type/confidence each).
func Explain(st store.Backend, qn string) (map[string]any, error) {
	els, err := st.FindExact(qn)
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, ErrUnknownNode
	}
	in, err := st.Incoming(qn)
	if err != nil {
		return nil, err
	}
	out, err := st.Outgoing(qn)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"element":     els[0],
		"in_degree":   len(in),
		"out_degree":  len(out),
		"in_by_type":  countByRelType(in),
		"out_by_type": countByRelType(out),
		"in_samples":  sampleEdges(in),
		"out_samples": sampleEdges(out),
	}, nil
}

// countByRelType tallies edges per rel_type.
func countByRelType(rels []store.Relationship) map[string]int {
	counts := map[string]int{}
	for _, r := range rels {
		counts[r.RelType]++
	}
	return counts
}

// sampleEdges returns up to 5 edges as maps, deterministically ordered.
func sampleEdges(rels []store.Relationship) []map[string]any {
	sorted := make([]store.Relationship, len(rels))
	copy(sorted, rels)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.RelType < b.RelType
	})
	if len(sorted) > 5 {
		sorted = sorted[:5]
	}
	out := make([]map[string]any, 0, len(sorted))
	for _, r := range sorted {
		out = append(out, map[string]any{
			"source":     r.Source,
			"target":     r.Target,
			"rel_type":   r.RelType,
			"confidence": r.Confidence,
		})
	}
	return out
}
