// Package graph implements pure-Go graph traversal verbs over the store reads it needs
// (Rust graph/query.rs parity): blast radius, shortest path, callers/callees,
// focused context, and explain. All traversal is in-process BFS — no SQL
// recursion — deterministic by qualified name, cycle-safe via visited sets,
// and depth-bounded (DefaultDepth 2, hard MaxDepth 5).
package graph

import (
	"context"
	"errors"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	// DefaultDepth is the traversal depth used when callers pass <= 0.
	DefaultDepth = 2
	// MaxDepth is the hard ceiling for any traversal depth.
	MaxDepth = 5
	// NodeBudget caps the nodes one BFS may admit (decision D9). A walk that
	// would go past it stops and reports Bounds.Truncated.
	NodeBudget = 5000
)

// ErrUnknownNode is returned when a traversal seed resolves to neither an
// element nor any edge endpoint. errors.Is-able.
var ErrUnknownNode = errors.New("graph: unknown node")

// Hit is one node in a blast-radius result.
type Hit struct {
	QN    string `json:"qn"`
	Depth int    `json:"depth"`
}

// Bounds is how a traversal ended: the depth it was allowed to walk after
// clamping, and whether the node budget stopped it before the answer was
// complete.
type Bounds struct {
	Depth     int
	Truncated bool
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

// graphStore is the store surface the traversal verbs call: element lookup and
// one-hop edges in each direction. The composed store satisfies it.
type graphStore interface {
	FindExact(ctx context.Context, name string) ([]store.Element, error)
	Elements(ctx context.Context) ([]store.Element, error)
	Outgoing(ctx context.Context, source string) ([]store.Relationship, error)
	Incoming(ctx context.Context, target string) ([]store.Relationship, error)
}

// Known reports whether qn is known to the graph: an element with that
// qualified name exists, or at least one edge touches it. It is the seed test
// every verb shares, so an answer can say whether a seed resolved.
func Known(ctx context.Context, st graphStore, qn string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	els, err := st.FindExact(ctx, qn)
	if err != nil {
		return false, err
	}
	if len(els) > 0 {
		return true, nil
	}
	out, err := st.Outgoing(ctx, qn)
	if err != nil {
		return false, err
	}
	if len(out) > 0 {
		return true, nil
	}
	in, err := st.Incoming(ctx, qn)
	if err != nil {
		return false, err
	}
	return len(in) > 0, nil
}

// Impact returns the blast radius of qn: BFS over INCOMING edges (who depends
// on it), seed included at depth 0. Deterministic: sorted by qualified name.
func Impact(ctx context.Context, st graphStore, qn string, depth int) ([]Hit, Bounds, error) {
	depth = clampDepth(depth)
	bounds := Bounds{Depth: depth}
	ok, err := Known(ctx, st, qn)
	if err != nil {
		return nil, Bounds{}, err
	}
	if !ok {
		return nil, Bounds{}, ErrUnknownNode
	}

	visited := map[string]bool{qn: true}
	frontier := []string{qn}
	hits := []Hit{{QN: qn, Depth: 0}}

	for d := 1; d <= depth && len(frontier) > 0 && !bounds.Truncated; d++ {
		var next []string
		for _, node := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, Bounds{}, err
			}
			rels, err := st.Incoming(ctx, node)
			if err != nil {
				return nil, Bounds{}, err
			}
			for _, r := range rels {
				if !visited[r.Source] {
					visited[r.Source] = true
					next = append(next, r.Source)
				}
			}
			// The level already holds more nodes than the budget: stop reading
			// its frontier. Everything admitted below is still the sorted prefix.
			if len(hits)+len(next) > NodeBudget {
				bounds.Truncated = true
				break
			}
		}
		sort.Strings(next)
		for _, q := range next {
			if len(hits) == NodeBudget {
				bounds.Truncated = true
				break
			}
			hits = append(hits, Hit{QN: q, Depth: d})
		}
		frontier = next
	}
	return hits, bounds, nil
}

// ShortestPath finds the shortest path from from to to over the undirected
// view of the graph, inclusive of both endpoints. Returns nil (no error) when
// unreachable. maxDepth is clamped like every traversal depth.
func ShortestPath(ctx context.Context, st graphStore, from, to string, maxDepth int) ([]string, Bounds, error) {
	maxDepth = clampDepth(maxDepth)
	bounds := Bounds{Depth: maxDepth}
	for _, seed := range []string{from, to} {
		ok, err := Known(ctx, st, seed)
		if err != nil {
			return nil, Bounds{}, err
		}
		if !ok {
			return nil, Bounds{}, ErrUnknownNode
		}
	}
	if from == to {
		return []string{from}, bounds, nil
	}

	parent := map[string]string{from: ""}
	frontier := []string{from}
	for d := 0; d < maxDepth && len(frontier) > 0; d++ {
		var next []string
		for _, node := range frontier {
			if err := ctx.Err(); err != nil {
				return nil, Bounds{}, err
			}
			neighbors, err := undirectedNeighbors(ctx, st, node)
			if err != nil {
				return nil, Bounds{}, err
			}
			for _, nb := range neighbors {
				if _, seen := parent[nb]; seen {
					continue
				}
				parent[nb] = node
				if nb == to {
					return reconstruct(parent, to), bounds, nil
				}
				next = append(next, nb)
				// Past the budget the target may still lie beyond what was read,
				// so the answer is unknown: report truncation, not unreachable.
				if len(parent) > NodeBudget {
					return nil, Bounds{Depth: maxDepth, Truncated: true}, nil
				}
			}
		}
		sort.Strings(next)
		frontier = next
	}
	return nil, bounds, nil
}

// undirectedNeighbors returns both directions of edges touching node,
// deduplicated and sorted.
func undirectedNeighbors(ctx context.Context, st graphStore, node string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := st.Outgoing(ctx, node)
	if err != nil {
		return nil, err
	}
	in, err := st.Incoming(ctx, node)
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

// Relationship types a site query can read. A use of a non-callable element is
// a references edge, so callers of such a seed read both (see siteEdges).
const (
	relCalls      = "calls"
	relReferences = "references"
	relImports    = "imports"
)

// nonCallable are the element types no call can target (the index's type,
// class, enum, constant and variable kinds). A use of one is a reference.
var nonCallable = map[string]bool{
	"type": true, "class": true, "enum": true, "constant": true, "variable": true,
}

// Site is one edge seen from a seed, located at the use itself.
type Site struct {
	QN         string  // element at the other end of the edge
	File       string  // file of the element that makes the use
	Line       int     // use-site line
	Confidence float64 // edge confidence
	RelType    string  // relationship the edge was read through
}

// edgeKeep reports whether one edge touching a seed belongs to its site query.
type edgeKeep func(r store.Relationship) bool

// callEdges returns the edges touching qn that keep accepts: incoming ones
// (callers) when incoming is true, outgoing ones (callees) otherwise.
func callEdges(ctx context.Context, st graphStore, qn string, incoming bool, keep edgeKeep) ([]store.Relationship, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rels []store.Relationship
	var err error
	if incoming {
		rels, err = st.Incoming(ctx, qn)
	} else {
		rels, err = st.Outgoing(ctx, qn)
	}
	if err != nil {
		return nil, err
	}
	kept := rels[:0]
	for _, r := range rels {
		if keep(r) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

// CallSites returns one Site per call line of every edge touching any seed,
// deduplicated and sorted by QN, file, line and rel type. incoming selects
// callers (QN is the caller) or callees (QN is the callee). The use sits in the
// element that makes it: the caller for incoming edges, the seed for outgoing
// ones. Its lines are the edge's "lines" metadata when present, else its "line"
// (see callLines). An edge with neither keeps its caller with Line noUseLine:
// the caller counts, and LinedSites leaves the site out of a site list.
//
// relType is an explicit filter and wins when set. When empty, a callee walk
// reads "calls", and a caller walk reads "calls" plus "references" (see
// siteEdges). A caller that is a method of the seed type itself is not an
// external user and is dropped.
func CallSites(ctx context.Context, st graphStore, seeds []string, incoming bool, relType string) ([]Site, error) {
	bySeed, err := SitesBySeed(ctx, st, seeds, incoming, relType)
	if err != nil {
		return nil, err
	}
	return UnionSites(seeds, bySeed), nil
}

// noUseLine is the Site line of an edge that carries no use line. Its caller
// still counts (SiteEnds lists it), and LinedSites leaves the site out.
const noUseLine = 0

// LinedSites returns the sites that carry a use line, for listing call sites.
// A caller whose edge has no use line is still a caller (SiteEnds of the full
// sites lists it), but it has no line to show.
func LinedSites(sites []Site) []Site {
	return slices.DeleteFunc(slices.Clone(sites), func(s Site) bool { return s.Line == noUseLine })
}

// UnionSites merges the per-seed sites of SitesBySeed into one list over the
// seeds, deduplicated and sorted by QN, file, line and rel type.
func UnionSites(seeds []string, bySeed map[string][]Site) []Site {
	seen := map[Site]bool{}
	var sites []Site
	for _, seed := range seeds {
		for _, site := range bySeed[seed] {
			if !seen[site] {
				seen[site] = true
				sites = append(sites, site)
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		a, b := sites[i], sites[j]
		if a.QN != b.QN {
			return a.QN < b.QN
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.RelType < b.RelType
	})
	return sites
}

// SitesBySeed answers CallSites one seed at a time: the sites of each seed
// alone, keyed by seed and deduplicated within it, in edge order. A caller uses
// it for per-seed counts; CallSites is its union over the seeds.
func SitesBySeed(ctx context.Context, st graphStore, seeds []string, incoming bool, relType string) (map[string][]Site, error) {
	holders := map[string]store.Element{}
	locateOnce := func(qn string) (store.Element, error) {
		if el, ok := holders[qn]; ok {
			return el, nil
		}
		el, err := locate(ctx, st, qn)
		if err != nil {
			return store.Element{}, err
		}
		holders[qn] = el
		return el, nil
	}
	out := map[string][]Site{}
	for _, seed := range seeds {
		if _, done := out[seed]; done {
			continue
		}
		keep, err := siteEdges(seed, incoming, relType, locateOnce)
		if err != nil {
			return nil, err
		}
		rels, err := callEdges(ctx, st, seed, incoming, keep)
		if err != nil {
			return nil, err
		}
		seen := map[Site]bool{}
		var sites []Site
		for _, r := range rels {
			site := Site{QN: r.Target, Confidence: r.Confidence, RelType: r.RelType}
			holder := seed
			if incoming {
				site.QN, holder = r.Source, r.Source
			}
			el, err := locateOnce(holder)
			if err != nil {
				return nil, err
			}
			if incoming && el.ElementType == "method" && isOwnMethod(el, seed) {
				continue
			}
			site.File = el.FilePath
			lines := callLines(r.Metadata)
			if len(lines) == 0 {
				lines = []int{noUseLine}
			}
			for _, line := range lines {
				site.Line = line
				if !seen[site] {
					seen[site] = true
					sites = append(sites, site)
				}
			}
		}
		out[seed] = sites
	}
	return out, nil
}

// siteEdges is the edge filter of one seed's site query. The explicit filter
// wins. A callee walk reads calls. A caller walk of a non-callable seed reads
// calls and every references edge. A caller walk of a callable seed reads calls
// and only the references edges that carry a use line (a value use such as a
// method value passed as an argument); a references edge with no line is a
// heuristic signature mention and is not a caller.
func siteEdges(seed string, incoming bool, filter string, locateOnce func(string) (store.Element, error)) (edgeKeep, error) {
	if filter != "" {
		return func(r store.Relationship) bool { return r.RelType == filter }, nil
	}
	if !incoming {
		return func(r store.Relationship) bool { return r.RelType == relCalls }, nil
	}
	el, err := locateOnce(seed)
	if err != nil {
		return nil, err
	}
	if nonCallable[el.ElementType] {
		return func(r store.Relationship) bool {
			return r.RelType == relCalls || r.RelType == relReferences
		}, nil
	}
	return func(r store.Relationship) bool {
		if r.RelType == relCalls {
			return true
		}
		return r.RelType == relReferences && len(callLines(r.Metadata)) > 0
	}, nil
}

// methodOwner is the qualified name of the type a method belongs to. The index
// spells a method "<rel>::<Type>.<name>", and a Go receiver is not a parent
// node, so without a recorded parent the owner is the name minus ".<name>".
func methodOwner(el store.Element) string {
	if el.ParentQualified != "" {
		return el.ParentQualified
	}
	return strings.TrimSuffix(el.QualifiedName, "."+el.Name)
}

// isOwnMethod reports whether method el is one of seed's own methods. A method
// declared in seed's file is owned by seed. A Go method may also sit in another
// file of the same package directory, so a method whose receiver has seed's
// name in that directory is owned by seed too. A type of that name in another
// directory is another package's type and is not matched.
func isOwnMethod(el store.Element, seed string) bool {
	owner := methodOwner(el)
	if owner == seed {
		return true
	}
	ownerFile, ownerName, okOwner := strings.Cut(owner, "::")
	seedFile, seedName, okSeed := strings.Cut(seed, "::")
	return okOwner && okSeed && ownerName == seedName && path.Dir(ownerFile) == path.Dir(seedFile)
}

// SiteEnds returns the deduplicated, sorted QN of each site.
func SiteEnds(sites []Site) []string {
	var out []string
	for i, s := range sites {
		if i == 0 || s.QN != sites[i-1].QN {
			out = append(out, s.QN)
		}
	}
	return out
}

// SiteRelTypes returns the distinct relationship types the sites were read
// through, sorted.
func SiteRelTypes(sites []Site) []string {
	var out []string
	for _, s := range sites {
		if !slices.Contains(out, s.RelType) {
			out = append(out, s.RelType)
		}
	}
	sort.Strings(out)
	return out
}

// locate returns the element whose qualified name is qn, or the zero Element
// when none is indexed (an edge can name an element the index never saw).
func locate(ctx context.Context, st graphStore, qn string) (store.Element, error) {
	els, err := st.FindExact(ctx, qn)
	if err != nil {
		return store.Element{}, err
	}
	for _, el := range els {
		if el.QualifiedName == qn {
			return el, nil
		}
	}
	return store.Element{}, nil
}

// callLines reads the call-site lines an edge carries. The "lines" metadata
// (every use line of the edge, written by the AST tier) wins when it holds any
// line; otherwise the single "line" metadata. The result is sorted and unique,
// and nil when the edge carries neither.
func callLines(meta map[string]any) []int {
	if raw, ok := meta["lines"]; ok {
		var out []int
		switch v := raw.(type) {
		case []any:
			for _, x := range v {
				if n, ok := metaInt(x); ok {
					out = append(out, n)
				}
			}
		case []int:
			out = append(out, v...)
		}
		if len(out) > 0 {
			sort.Ints(out)
			return slices.Compact(out)
		}
	}
	if n, ok := metaInt(meta["line"]); ok {
		return []int{n}
	}
	return nil
}

// metaInt reads one numeric metadata value (JSON numbers decode as float64).
func metaInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	}
	return 0, false
}

// Context returns a focused view of qn: the element itself plus its
// "incoming" and "outgoing" edges grouped by rel_type as sorted QN lists,
// each list capped at limit entries (limit <= 0 means uncapped).
func Context(ctx context.Context, st graphStore, qn string, limit int) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	els, err := st.FindExact(ctx, qn)
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, ErrUnknownNode
	}
	in, err := st.Incoming(ctx, qn)
	if err != nil {
		return nil, err
	}
	out, err := st.Outgoing(ctx, qn)
	if err != nil {
		return nil, err
	}
	res := map[string]any{
		"element":  els[0],
		"incoming": groupByRelType(in, limit, relSource),
		"outgoing": groupByRelType(out, limit, relTarget),
	}
	if summary, testOnly := importerSplit(in); summary != nil {
		res["incoming_imports_summary"] = summary
		res["incoming_imports_test_only"] = testOnly
	}
	return res, nil
}

// importerSplit splits the importers of an element into those that import it
// from non-test code and those whose every import is test-only (the edge
// metadata test_only). It returns nil when nothing imports the element.
func importerSplit(in []store.Relationship) (map[string]int, []string) {
	testOnlyBy := map[string]bool{}
	for _, r := range in {
		if r.RelType != relImports {
			continue
		}
		t, _ := r.Metadata["test_only"].(bool)
		if prev, ok := testOnlyBy[r.Source]; ok {
			t = prev && t
		}
		testOnlyBy[r.Source] = t
	}
	if len(testOnlyBy) == 0 {
		return nil, nil
	}
	summary := map[string]int{"non_test": 0, "test_only": 0}
	testOnly := []string{}
	for src, t := range testOnlyBy {
		if t {
			summary["test_only"]++
			testOnly = append(testOnly, src)
		} else {
			summary["non_test"]++
		}
	}
	sort.Strings(testOnly)
	return summary, testOnly
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
func Explain(ctx context.Context, st graphStore, qn string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	els, err := st.FindExact(ctx, qn)
	if err != nil {
		return nil, err
	}
	if len(els) == 0 {
		return nil, ErrUnknownNode
	}
	in, err := st.Incoming(ctx, qn)
	if err != nil {
		return nil, err
	}
	out, err := st.Outgoing(ctx, qn)
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
