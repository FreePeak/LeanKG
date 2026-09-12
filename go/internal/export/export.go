// Package export ports the Rust graph exporters behind `leankg export`
// (src/graph/export_select.rs, src/graph/export_markdown.rs and the
// export_json / export_dot / export_mermaid writers in main.rs), re-expressed
// over store.Backend.
//
// Deliberately NOT ported: src/graph/export.rs HtmlExporter — 460 lines of
// embedded vis/dagre JavaScript producing a standalone .leankg/graph.html.
// The Go engine already serves that interactive view from internal/web, so
// this package covers the json / dot / mermaid / markdown formats only.
package export

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// DefaultMaxNodes is the Rust export_select::DEFAULT_MAX_NODES budget used
// when the caller does not pass --max-nodes.
const DefaultMaxNodes = 5000

// maxRelationships is the engine-wide "read every relationship" cap
// (internal/web/api.go uses the same value: RelationshipsAll treats
// limit <= 0 as 1000, so an explicit large limit is the only way to ask
// for the whole table).
const maxRelationships = 1 << 20

// Cluster is the projection of the Rust graph::clustering::Cluster that the
// community scope and the Markdown exporter need.
type Cluster struct {
	ID                  string
	Label               string
	Members             []string
	RepresentativeFiles []string
}

// Options selects the slice of the graph to export (Rust
// export_select::select_elements_and_relationships arguments).
type Options struct {
	// File scopes to a file's subgraph, walking forward from the file's
	// elements Depth hops (Rust --file / --depth).
	File  string
	Depth uint32
	// Path scopes to a file-path prefix (Rust --path).
	Path string
	// Community scopes to a cluster id or label (Rust --community).
	Community string
	// MaxNodes is the node budget; <= 0 means DefaultMaxNodes.
	MaxNodes int
	// Clusters resolves a community id (or label) to its member qualified
	// names. The Go store persists no cluster column — the Rust engine read
	// the precomputed cluster_id/cluster_label — so the caller injects the
	// detector output (internal/web DetectClusters). Required only for
	// Community scope.
	Clusters func(id string) (members []string, label string, ok bool)
}

// Meta describes the selected slice (Rust export_select::ExportMeta, minus
// banner_html which belonged to the HTML exporter).
type Meta struct {
	SelectedNodes int
	SelectedEdges int
	MaxNodes      int
	Truncated     bool
	ScopeDesc     string
}

// Select returns the element/relationship slice for opts, deduped, edge
// filtered and truncated to the node budget exactly like the Rust selector.
//
// Deviation (documented): a bogus --community is an error here, while Rust
// silently exported an empty graph.
func Select(st store.Backend, opts Options) ([]store.Element, []store.Relationship, Meta, error) {
	budget := opts.MaxNodes
	if budget <= 0 {
		budget = DefaultMaxNodes
	}

	var (
		els   []store.Element
		rels  []store.Relationship
		err   error
		scope string
	)
	switch {
	case opts.File != "":
		els, rels, err = selectFileScoped(st, opts.File, opts.Depth)
		scope = fmt.Sprintf("file:%s (depth %d)", opts.File, opts.Depth)
	case opts.Path != "":
		els, rels, err = selectPathPrefix(st, opts.Path)
		scope = "path:" + opts.Path
	case opts.Community != "":
		els, rels, err = selectCommunity(st, opts)
		scope = "community:" + opts.Community
	default:
		els, rels, err = All(st)
		scope = "full graph"
	}
	if err != nil {
		return nil, nil, Meta{}, err
	}

	truncated := dedupeAndFilter(&els, &rels, budget)
	return els, rels, Meta{
		SelectedNodes: len(els),
		SelectedEdges: len(rels),
		MaxNodes:      budget,
		Truncated:     truncated,
		ScopeDesc:     scope,
	}, nil
}

// dedupeAndFilter ports export_select::dedupe_and_filter: drop duplicate
// qualified names, keep only edges whose endpoints survived, then truncate to
// the budget by descending degree (stable, so ties keep the incoming order).
func dedupeAndFilter(elements *[]store.Element, relationships *[]store.Relationship, budget int) bool {
	seen := make(map[string]bool, len(*elements))
	deduped := make([]store.Element, 0, len(*elements))
	for _, e := range *elements {
		if seen[e.QualifiedName] {
			continue
		}
		seen[e.QualifiedName] = true
		deduped = append(deduped, e)
	}
	*elements = deduped

	kept := make(map[string]bool, len(deduped))
	for _, e := range deduped {
		kept[e.QualifiedName] = true
	}
	*relationships = filterEdges(*relationships, kept)

	truncated := len(*elements) > budget
	if !truncated {
		return false
	}

	degree := make(map[string]int, len(*relationships)*2)
	for _, r := range *relationships {
		degree[r.Source]++
		degree[r.Target]++
	}
	sort.SliceStable(*elements, func(i, j int) bool {
		return degree[(*elements)[i].QualifiedName] > degree[(*elements)[j].QualifiedName]
	})
	*elements = (*elements)[:budget]

	kept = make(map[string]bool, budget)
	for _, e := range *elements {
		kept[e.QualifiedName] = true
	}
	*relationships = filterEdges(*relationships, kept)
	return true
}

func filterEdges(rels []store.Relationship, kept map[string]bool) []store.Relationship {
	out := make([]store.Relationship, 0, len(rels))
	for _, r := range rels {
		if kept[r.Source] && kept[r.Target] {
			out = append(out, r)
		}
	}
	return out
}

// selectFileScoped ports export_select::select_file_scoped: a depth-first walk
// from the seed path over outgoing edges, collecting the relationships seen
// and every element whose file_path was reached. Unreadable nodes are skipped
// (Rust swallowed the error with `if let Ok(rels)`).
func selectFileScoped(st store.Backend, file string, depth uint32) ([]store.Element, []store.Relationship, error) {
	type frame struct {
		name string
		d    uint32
	}
	visited := make(map[string]bool)
	queue := []frame{{name: file}}
	var scoped []store.Relationship

	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if cur.d >= depth || visited[cur.name] {
			continue
		}
		visited[cur.name] = true
		rels := outgoingNormalized(st, cur.name)
		for _, r := range rels {
			queue = append(queue, frame{name: r.Target, d: cur.d + 1})
		}
		scoped = append(scoped, rels...)
	}

	all, err := st.Elements()
	if err != nil {
		return nil, nil, err
	}
	els := make([]store.Element, 0, len(all))
	for _, e := range all {
		if visited[e.FilePath] {
			els = append(els, e)
		}
	}
	// Outgoing has no ORDER BY, so pin a deterministic order for the walk
	// result (the Rust Cozo read had a stable physical order).
	sortRelationships(scoped)
	return els, scoped, nil
}

// outgoingNormalized mirrors GraphEngine::get_relationships: one query matching
// the normalized source plus its "./" variant.
func outgoingNormalized(st store.Backend, source string) []store.Relationship {
	norm := normalizePath(source)
	variants := []string{norm}
	if "./"+norm != norm {
		variants = append(variants, "./"+norm)
	}
	var out []store.Relationship
	for _, v := range variants {
		rels, err := st.Outgoing(v)
		if err != nil {
			continue
		}
		out = append(out, rels...)
	}
	return out
}

// ClusterResolver builds an Options.Clusters resolver from the dashboard's
// clustering output: assignments maps a qualified name to its cluster id and
// labels maps a cluster id to its display label (internal/web
// ClusterAssignments returns exactly these two). A request resolves by cluster
// id first, then by label — the Rust select_community matched cluster_id OR
// cluster_label — and reports ok=false when nothing matches.
func ClusterResolver(assignments map[string]string, labels map[string]string) func(id string) (members []string, label string, ok bool) {
	byCluster := make(map[string][]string, len(labels))
	for qn, cid := range assignments {
		byCluster[cid] = append(byCluster[cid], qn)
	}
	for _, members := range byCluster {
		sort.Strings(members)
	}
	return func(id string) ([]string, string, bool) {
		if label, ok := labels[id]; ok {
			return append([]string(nil), byCluster[id]...), label, true
		}
		var members []string
		label := ""
		for cid, l := range labels {
			if l != id {
				continue
			}
			label = l
			members = append(members, byCluster[cid]...)
		}
		if label == "" {
			return nil, "", false
		}
		sort.Strings(members)
		return members, label, true
	}
}

// normalizePath ports graph::query::normalize_path: "." and "" collapse to "",
// any other path loses one leading "./".

// All returns every element and relationship in the store: the Rust
// GraphEngine::all_elements / all_relationships pair the unscoped exporters
// read. Unlike Select it applies no dedupe, endpoint filter or budget — the
// legacy full-graph dot/mermaid path streamed exactly what the store held.
func All(st store.Backend) ([]store.Element, []store.Relationship, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, nil, err
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return nil, nil, err
	}
	return els, rels, nil
}
func normalizePath(path string) string {
	if path == "." || path == "" {
		return ""
	}
	return strings.TrimPrefix(path, "./")
}

// selectPathPrefix ports export_select::select_path_prefix: elements under the
// prefix, plus the edges with both endpoints in that set (Rust asked the DB for
// source-side edges and then dropped the boundary through dedupe_and_filter —
// the net result is the same set, read in one deterministic pass).
func selectPathPrefix(st store.Backend, prefix string) ([]store.Element, []store.Relationship, error) {
	all, err := st.Elements()
	if err != nil {
		return nil, nil, err
	}
	normPrefix := strings.TrimPrefix(prefix, "./")
	els := make([]store.Element, 0, len(all))
	for _, e := range all {
		if strings.HasPrefix(strings.TrimPrefix(e.FilePath, "./"), normPrefix) {
			els = append(els, e)
		}
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return nil, nil, err
	}
	return els, rels, nil
}

// selectCommunity ports export_select::select_community over the injected
// cluster resolver (the Go store keeps no cluster column).
func selectCommunity(st store.Backend, opts Options) ([]store.Element, []store.Relationship, error) {
	if opts.Clusters == nil {
		return nil, nil, fmt.Errorf("community scope requires a cluster resolver")
	}
	members, _, ok := opts.Clusters(opts.Community)
	if !ok {
		return nil, nil, fmt.Errorf("unknown community %q", opts.Community)
	}
	inCommunity := make(map[string]bool, len(members))
	for _, m := range members {
		inCommunity[m] = true
	}
	all, err := st.Elements()
	if err != nil {
		return nil, nil, err
	}
	els := make([]store.Element, 0, len(members))
	for _, e := range all {
		if inCommunity[e.QualifiedName] {
			els = append(els, e)
		}
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return nil, nil, err
	}
	return els, rels, nil
}

// sortRelationships pins the (source, rel_type, target) order used by the pack
// snapshot and by any selection that reads through unordered source queries.
func sortRelationships(rels []store.Relationship) {
	sort.SliceStable(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.RelType != b.RelType {
			return a.RelType < b.RelType
		}
		return a.Target < b.Target
	})
}
