// Graph endpoints of the legacy dashboard API: children, expand-service,
// clusters, service-topology. Ported from the deleted Rust
// src/web/handlers.rs and the src/graph/query.rs data-access helpers
// (get_elements_in_folder, get_children_filtered,
// get_top_level_directories).
package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// stripDotSlash normalizes a possibly "./"-prefixed relative path.
func stripDotSlash(p string) string { return strings.TrimPrefix(p, "./") }

// normalizePath ports the Rust normalize_path helper: "." and "" map to "".
func normalizePath(p string) string {
	if p == "." || p == "" {
		return ""
	}
	return stripDotSlash(p)
}

// folderPage is the shared result of the Rust ChildrenResult queries.
type folderPage struct {
	elements   []store.Element
	totalCount int
	hasMore    bool
}

// relsSourcedBy returns relationships whose source is in the element set
// (the Rust "source_qualified in $qns" query).
func relsSourcedBy(rels []store.Relationship, els []store.Element) []store.Relationship {
	qns := make(map[string]struct{}, len(els))
	for _, e := range els {
		qns[e.QualifiedName] = struct{}{}
	}
	out := make([]store.Relationship, 0, len(rels))
	for _, r := range rels {
		if _, ok := qns[r.Source]; ok {
			out = append(out, r)
		}
	}
	return out
}

func window[T any](xs []T, offset, limit int) []T {
	if offset >= len(xs) {
		return nil
	}
	end := offset + limit
	if end > len(xs) {
		end = len(xs)
	}
	return xs[offset:end]
}

// elementsInFolder ports GraphEngine::get_elements_in_folder.
// folder is project-relative ("" or "." = root); allContent disables the
// direct-child filter. The Go engine stores file paths without the "./"
// prefix, so matching normalizes both sides.
func elementsInFolder(snap *snapshot, rels []store.Relationship, folder string, limit, offset int, allContent bool) folderPage {
	folder = normalizePath(folder)
	if folder == "" {
		if allContent {
			page := window(snap.elements, offset, limit)
			hasMore := len(page) >= limit
			total := offset + len(page)
			if hasMore {
				total++
			}
			return folderPage{elements: page, totalCount: total, hasMore: hasMore}
		}
		// Root without all_content: direct children only (top-level paths).
		var direct []store.Element
		for _, e := range snap.elements {
			if !strings.Contains(stripDotSlash(e.FilePath), "/") {
				direct = append(direct, e)
			}
		}
		total := len(direct)
		hasMore := offset+limit < total
		return folderPage{elements: window(direct, offset, limit), totalCount: total, hasMore: hasMore}
	}

	// Non-empty folder: Rust matched the unanchored regex ".*<folder>/.*"
	// (plus the direct-child filter unless all_content).
	prefix := folder + "/"
	var matched []store.Element
	for _, e := range snap.elements {
		if strings.Contains(stripDotSlash(e.FilePath), prefix) {
			matched = append(matched, e)
		}
	}
	totalCount := len(window(matched, offset, limit))
	var page []store.Element
	for _, e := range window(matched, offset, limit) {
		if allContent {
			page = append(page, e)
			continue
		}
		rem, ok := strings.CutPrefix(stripDotSlash(e.FilePath), prefix)
		if !ok || rem == "" || strings.Contains(rem, "/") {
			continue
		}
		page = append(page, e)
	}
	return folderPage{elements: page, totalCount: totalCount, hasMore: len(page) == limit}
}

// nestedContentTypes are excluded from root-level children (not structural).
var nestedContentTypes = map[string]struct{}{
	"function": {}, "class": {}, "method": {}, "interface": {},
	"property": {}, "struct": {}, "enum": {},
}

// childrenFiltered ports GraphEngine::get_children_filtered. The Rust
// element_types filter only ever applied the first type (upstream TODO);
// ported as-is. parent is project-relative ("" = root).
func childrenFiltered(snap *snapshot, rels []store.Relationship, parent, elementType string, limit, offset int) folderPage {
	normalized := normalizePath(parent)
	if normalized == "" {
		// Root: structural direct children only; the Rust query pulled
		// :limit rows then filtered, so total_count is the pre-filter page.
		rows := window(snap.elements, offset, limit)
		var page []store.Element
		for _, e := range rows {
			if _, nested := nestedContentTypes[e.ElementType]; nested {
				continue
			}
			if strings.Contains(stripDotSlash(e.FilePath), "/") {
				continue
			}
			page = append(page, e)
		}
		return folderPage{elements: page, totalCount: len(rows), hasMore: len(page) == limit}
	}

	// Non-empty parent: unanchored "contains <parent>/" match, first type
	// filter applied, then direct-child filter.
	prefix := normalized + "/"
	var matched []store.Element
	for _, e := range snap.elements {
		if elementType != "" && e.ElementType != elementType {
			continue
		}
		if strings.Contains(stripDotSlash(e.FilePath), prefix) {
			matched = append(matched, e)
		}
	}
	rows := window(matched, offset, limit)
	var page []store.Element
	for _, e := range rows {
		rem, ok := strings.CutPrefix(stripDotSlash(e.FilePath), prefix)
		if !ok || strings.Contains(rem, "/") {
			continue
		}
		page = append(page, e)
	}
	return folderPage{elements: page, totalCount: len(rows), hasMore: len(page) == limit}
}

// topLevelDirectories ports GraphEngine::get_top_level_directories("").
func topLevelDirectories(snap *snapshot) []string {
	dirs := map[string]struct{}{}
	for _, e := range snap.elements {
		f := stripDotSlash(e.FilePath)
		i := strings.Index(f, "/")
		if i <= 0 {
			continue
		}
		top := f[:i]
		if strings.HasPrefix(top, ".") {
			continue
		}
		dirs[top] = struct{}{}
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// graphElementNode builds a GraphNode from a store element (label = name,
// element type upper-cased like the Rust ui serializers).
func graphElementNode(e store.Element) graphNode {
	label := e.Name
	if label == "" {
		label = e.QualifiedName
	}
	return graphNode{
		ID:    e.QualifiedName,
		Label: label,
		Properties: nodeProperties{
			Name:        e.Name,
			FilePath:    e.FilePath,
			ElementType: upperFirst(e.ElementType),
		},
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// --- GET /api/graph/children ---

func (h *apiH) graphChildren(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	parent := q.Get("parent")
	var elementType string
	if v := strings.TrimSpace(q.Get("element_types")); v != "" {
		// Rust quirk: only the first type was ever applied.
		elementType = strings.TrimSpace(strings.Split(v, ",")[0])
	}
	limit, offset := 200, 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
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

	// Absolute paths under the served project collapse to root (Rust
	// effective_parent rule).
	effective := parent
	if h.projectDir != "" && strings.HasPrefix(parent, "/") && strings.HasPrefix(parent, h.projectDir) {
		effective = ""
	}

	page := childrenFiltered(snap, rels, effective, elementType, limit, offset)

	nodes := make([]graphNode, 0, len(page.elements))
	existingFolders := map[string]struct{}{}
	for _, e := range page.elements {
		nodes = append(nodes, graphElementNode(e))
		if e.ElementType == "Folder" {
			folderName := stripDotSlash(e.FilePath)
			if i := strings.LastIndex(folderName, "/"); i >= 0 {
				folderName = folderName[i+1:]
			}
			existingFolders[folderName] = struct{}{}
		}
	}

	// Synthesize Directory nodes for intermediate path segments that have no
	// indexed Folder row (Rust synthesized_dir_names).
	parentPath := strings.TrimSuffix(normalizePath(effective), "/")
	prefixForDirs := ""
	if parentPath != "" && parentPath != "." {
		prefixForDirs = parentPath + "/"
	}
	synth := map[string]struct{}{}
	for _, e := range page.elements {
		if e.ElementType != "File" && e.ElementType != "Document" {
			continue
		}
		rel := stripDotSlash(e.FilePath)
		if prefixForDirs != "" {
			rest, ok := strings.CutPrefix(rel, prefixForDirs)
			if !ok {
				continue
			}
			rel = rest
		}
		if i := strings.Index(rel, "/"); i > 0 {
			synth[rel[:i]] = struct{}{}
		}
	}
	var dirNodes []graphNode
	for dir := range synth {
		if dir == "." {
			continue
		}
		if _, ok := existingFolders[dir]; ok {
			continue
		}
		folderID := prefixForDirs + "folder:" + dir
		dirNodes = append(dirNodes, graphNode{
			ID:    folderID,
			Label: dir,
			Properties: nodeProperties{
				Name:        dir,
				FilePath:    prefixForDirs + dir + "/",
				ElementType: "Directory",
			},
		})
	}
	sort.Slice(dirNodes, func(i, j int) bool { return dirNodes[i].ID < dirNodes[j].ID })
	nodes = append(nodes, dirNodes...)

	graphRels := make([]graphRelationship, 0)
	for _, rel := range relsSourcedBy(rels, page.elements) {
		graphRels = append(graphRels, relFromStore(rel))
	}

	writeEnvelope(w, okEnvelope(map[string]any{
		"nodes":         nodes,
		"relationships": graphRels,
		"totalCount":    page.totalCount,
		"hasMore":       page.hasMore,
	}))
}

// --- GET /api/graph/expand-service ---

// detectSingleRepo ports the Rust detect_single_repo: exactly one .git
// directory within depth 4 of the root means a single-repo layout.
func detectSingleRepo(root string) bool {
	if root == "" {
		return true
	}
	gitCount := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable subtree: not a nested .git we can see
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr == nil && strings.Count(rel, string(filepath.Separator)) >= 4 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			gitCount++
			if gitCount > 1 {
				return filepath.SkipAll
			}
		}
		return nil
	})
	return gitCount <= 1
}

func (h *apiH) graphExpandService(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	folderPath := ""
	if p := q.Get("path"); p != "" {
		folderPath = p
	} else if s := q.Get("service"); s != "" {
		folderPath = strings.TrimPrefix(s, "service:")
	} else {
		writeEnvelope(w, failEnvelope("Missing 'path' or 'service' query parameter"))
		return
	}

	limit, offset := 500, 0
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	allContent := q.Get("all") == "true" || q.Get("all") == "1"

	// Map absolute project root → "."; keep relative paths as-is.
	relativeFolder := folderPath
	if h.projectDir != "" && strings.HasPrefix(folderPath, h.projectDir) {
		rest := strings.TrimLeft(folderPath[len(h.projectDir):], "/")
		if rest == "" {
			relativeFolder = "."
		} else {
			relativeFolder = "./" + rest
		}
	} else if folderPath == "." || folderPath == "./" || folderPath == "" {
		relativeFolder = "."
	}
	if relativeFolder == "./" {
		relativeFolder = "."
	}

	// FR-MG-03: expanding the project root of a single-repo layout loads the
	// entire graph in one call.
	if normalizePath(relativeFolder) == "" && detectSingleRepo(h.projectDir) {
		allContent = true
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
	page := elementsInFolder(snap, rels, relativeFolder, limit, offset, allContent)

	nodes := make([]graphNode, 0, len(page.elements))
	for _, e := range page.elements {
		nodes = append(nodes, graphElementNode(e))
	}

	// Drop ghost rows whose relative path is missing under the active
	// project root (stale cross-mount pollution; Rust nodes.retain).
	before := len(nodes)
	nodes = retainOnDisk(nodes, h.projectDir)
	dropped := before - len(nodes)

	keep := map[string]struct{}{}
	for _, n := range nodes {
		keep[n.ID] = struct{}{}
	}
	graphRels := make([]graphRelationship, 0, len(page.elements))
	for _, rel := range relsSourcedBy(rels, page.elements) {
		if _, ok := keep[rel.Source]; !ok {
			continue
		}
		if _, ok := keep[rel.Target]; !ok {
			continue
		}
		graphRels = append(graphRels, relFromStore(rel))
	}

	name := folderPath
	if i := strings.LastIndex(folderPath, "/"); i >= 0 {
		name = folderPath[i+1:]
	}
	if name == "" || name == "." {
		name = folderPath
	}
	msg := fmt.Sprintf("Expanded service '%s' with %d elements and %d relationships", name, len(nodes), len(graphRels))
	if dropped > 0 {
		msg += fmt.Sprintf(" (dropped %d missing-on-disk)", dropped)
	}
	writeEnvelope(w, okEnvelope(graphData{
		Nodes:         nodes,
		Relationships: graphRels,
		Filtered:      &graphFilterInfo{TestsFiltered: 0, Message: msg},
		HasMore:       page.hasMore,
	}))
}

// retainOnDisk keeps nodes whose file_path exists under projectDir (Rust
// ghost-row retain in api_graph_expand_service).
func retainOnDisk(nodes []graphNode, projectDir string) []graphNode {
	if projectDir == "" {
		return nodes
	}
	out := nodes[:0]
	for _, n := range nodes {
		fp := strings.TrimSpace(n.Properties.FilePath)
		if fp == "" || fp == "." || fp == "./" {
			out = append(out, n)
			continue
		}
		if _, err := os.Stat(filepath.Join(projectDir, stripDotSlash(fp))); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// --- GET /api/graph/clusters ---

// graphClusters ports the Rust api_graph_clusters: a cluster = the parent
// directory of the element's file_path ("root" when there is none).
func (h *apiH) graphClusters(w http.ResponseWriter, _ *http.Request) {
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

	members := map[string]int{}
	keys := []string{}
	clusterOf := func(path string) string {
		if i := strings.LastIndex(path, "/"); i >= 0 {
			return path[:i]
		}
		return "root"
	}
	for _, e := range snap.elements {
		k := clusterOf(e.FilePath)
		if _, ok := members[k]; !ok {
			keys = append(keys, k)
		}
		members[k]++
	}
	sort.Strings(keys)

	nodes := make([]graphNode, 0, len(keys))
	for _, k := range keys {
		label := k
		if i := strings.LastIndex(k, "/"); i >= 0 {
			label = k[i+1:]
		}
		nodes = append(nodes, graphNode{
			ID:    "cluster:" + k,
			Label: fmt.Sprintf("%s (%d)", label, members[k]),
			Properties: nodeProperties{
				Name:        k,
				FilePath:    k,
				ElementType: fmt.Sprintf("Cluster[%d files]", members[k]),
			},
		})
	}

	// Inter-cluster edges (Rust derived both sides from the relationship's
	// qualified names' parent directories).
	seen := map[[3]string]struct{}{}
	edges := []graphRelationship{}
	for _, rel := range rels {
		src := clusterOf(rel.Source)
		tgt := clusterOf(rel.Target)
		if src == tgt {
			continue
		}
		key := [3]string{src, tgt, rel.RelType}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		edges = append(edges, newGraphRelationship("cluster:"+src, "cluster:"+tgt, rel.RelType, "INFERRED"))
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })

	writeEnvelope(w, okEnvelope(graphData{
		Nodes:         nodes,
		Relationships: edges,
		Filtered: &graphFilterInfo{
			Message: fmt.Sprintf("Cluster overview: %d clusters, %d inter-cluster edges", len(nodes), len(edges)),
		},
	}))
}

// --- GET /api/graph/service-topology ---

type serviceNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Properties nodeProperties `json:"properties"`
}

type serviceRelationship struct {
	ID       string `json:"id"`
	SourceID string `json:"sourceId"`
	TargetID string `json:"targetId"`
	RelType  string `json:"rel_type"`
}

type serviceTopology struct {
	Nodes         []serviceNode         `json:"nodes"`
	Relationships []serviceRelationship `json:"relationships"`
	ProjectType   string                `json:"projectType"`
}

func (h *apiH) serviceTopology(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	showOrphans := q.Get("show_orphans") == "true"
	depth := 1
	if v := q.Get("depth"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			depth = n
		}
	}

	topo, err := extractServiceTopology(h.projectDir, showOrphans)
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	if len(topo.Nodes) == 0 {
		topo.ProjectType = "single_repo"
		projectName := "project"
		if h.projectDir != "" {
			if base := filepath.Base(h.projectDir); base != "" && base != "." {
				projectName = base
			}
		}
		topo.Nodes = append(topo.Nodes, serviceNode{
			ID:    "service:" + projectName,
			Label: projectName,
			Properties: nodeProperties{
				Name:        projectName,
				FilePath:    h.projectDir,
				ElementType: "Service",
			},
		})
		snap, serr := loadSnapshot(h.engine.Store())
		if serr != nil {
			writeEnvelope(w, failEnvelope(serr.Error()))
			return
		}
		for _, dir := range topLevelDirectories(snap) {
			folderID := "folder:" + dir
			topo.Nodes = append(topo.Nodes, serviceNode{
				ID:    folderID,
				Label: dir,
				Properties: nodeProperties{
					Name:        dir,
					FilePath:    dir + "/",
					ElementType: "Folder",
				},
			})
			if depth >= 1 {
				topo.Relationships = append(topo.Relationships, serviceRelationship{
					ID:       projectName + "_CONTAINS_" + dir,
					SourceID: "service:" + projectName,
					TargetID: folderID,
					RelType:  "CONTAINS",
				})
			}
		}
	} else {
		topo.ProjectType = "multi_repo"
	}
	writeEnvelope(w, okEnvelope(topo))
}

// extractServiceTopology ports the Rust extract_service_topology: discover
// sibling git repos (top-level and one level deep under non-hidden dirs),
// then scan go/yaml/yml/json/tmpl sources for dns:/// service references.
func extractServiceTopology(projectPath string, showOrphans bool) (serviceTopology, error) {
	services := map[string]string{}
	if projectPath == "" {
		return serviceTopology{}, nil
	}

	entries, err := os.ReadDir(projectPath)
	if err != nil {
		return serviceTopology{}, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(projectPath, entry.Name())
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			services[entry.Name()] = path
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// Scan platform/*/ subdirectories for git repos.
		subEntries, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			subPath := filepath.Join(path, sub.Name())
			if _, err := os.Stat(filepath.Join(subPath, ".git")); err == nil {
				services[entry.Name()+"_"+sub.Name()] = subPath
			}
		}
	}

	// Scan config/code files for dns:///SERVICE references.
	callerToCallee := map[[2]string]struct{}{}
	_ = filepath.WalkDir(projectPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.TrimPrefix(filepath.Ext(path), ".")
		switch ext {
		case "go", "yaml", "yml", "json", "tmpl":
		default:
			return nil
		}
		caller := ""
		for name, folder := range services {
			if strings.HasPrefix(path, folder+string(filepath.Separator)) {
				caller = name
				break
			}
		}
		if caller == "" {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for _, callee := range dnsServiceRefs(string(content)) {
			qualified := ""
			for key := range services {
				if strings.HasSuffix(key, callee) || key == callee || strings.HasSuffix(callee, key) {
					qualified = key
					break
				}
			}
			if qualified == "" || caller == qualified {
				continue
			}
			callerToCallee[[2]string{caller, qualified}] = struct{}{}
		}
		return nil
	})

	// Build the relationship list; with no call data, fall back to a
	// complete graph so the visualization has edges.
	rels := []serviceRelationship{}
	if len(callerToCallee) == 0 {
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		sort.Strings(names)
		for i, caller := range names {
			for _, callee := range names[i+1:] {
				rels = append(rels,
					serviceRelationship{
						ID:       caller + "_SERVICE_CALLS_" + callee,
						SourceID: "service:" + caller,
						TargetID: "service:" + callee,
						RelType:  "SERVICE_CALLS",
					},
					serviceRelationship{
						ID:       callee + "_SERVICE_CALLS_" + caller,
						SourceID: "service:" + callee,
						TargetID: "service:" + caller,
						RelType:  "SERVICE_CALLS",
					})
			}
		}
	} else {
		keys := make([][2]string, 0, len(callerToCallee))
		for k := range callerToCallee {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if keys[i][0] != keys[j][0] {
				return keys[i][0] < keys[j][0]
			}
			return keys[i][1] < keys[j][1]
		})
		for _, k := range keys {
			rels = append(rels, serviceRelationship{
				ID:       k[0] + "_SERVICE_CALLS_" + k[1],
				SourceID: "service:" + k[0],
				TargetID: "service:" + k[1],
				RelType:  "SERVICE_CALLS",
			})
		}
	}

	connected := map[string]struct{}{}
	for _, rel := range rels {
		connected[strings.TrimPrefix(rel.SourceID, "service:")] = struct{}{}
		connected[strings.TrimPrefix(rel.TargetID, "service:")] = struct{}{}
	}

	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	nodes := []serviceNode{}
	for _, name := range names {
		if !showOrphans {
			if _, ok := connected[name]; !ok {
				continue
			}
		}
		nodes = append(nodes, serviceNode{
			ID:    "service:" + name,
			Label: name,
			Properties: nodeProperties{
				Name:        name,
				FilePath:    services[name],
				ElementType: "Service",
			},
		})
	}
	return serviceTopology{Nodes: nodes, Relationships: rels}, nil
}

// dnsServiceRefs extracts service names from dns:/// URIs (Rust pattern
// dns://[/]{0,2}([a-zA-Z0-9][-a-zA-Z0-9_]*)).
func dnsServiceRefs(content string) []string {
	var out []string
	seen := map[string]struct{}{}
	i := 0
	for i < len(content) {
		j := strings.Index(content[i:], "dns://")
		if j < 0 {
			break
		}
		i += j + len("dns://")
		// [/]{0,2} — skip up to two additional slashes.
		skipped := 0
		for skipped < 2 && i < len(content) && content[i] == '/' {
			i++
			skipped++
		}
		start := i
		for i < len(content) && isDNSNameChar(content[i]) {
			i++
		}
		name := content[start:i]
		if name == "" || !isDNSNameStart(name[0]) {
			continue
		}
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	return out
}

func isDNSNameChar(c byte) bool {
	return c == '-' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

func isDNSNameStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
