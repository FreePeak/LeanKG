package export

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Caps from the Rust markdown exporter (src/graph/export_markdown.rs).
const (
	maxClusters             = 20
	maxGodNodes             = 10
	maxTreeDepth            = 4
	maxClusterMembersListed = 10
	treeIndent              = "  "
)

// GodNode is one entry of the degree ranking (Rust graph::query::GodNode).
type GodNode struct {
	QualifiedName string
	Name          string
	ElementType   string
	Degree        int
}

// MarkdownDoc is everything the Markdown exporter needs, collected once
// (Rust export_markdown::MarkdownDoc).
type MarkdownDoc struct {
	Project        string
	GeneratedAtUTC string
	Elements       []store.Element
	Relationships  []store.Relationship
	GodNodes       []GodNode
	Clusters       []Cluster
}

// CollectMarkdown gathers the document inputs. generatedAtUTC is the explicit
// determinism seam (Rust collect_with_timestamp): everything below the
// front-matter timestamp line is a pure function of the store.
//
// Clusters come from the deterministic folder grouping. Rust preferred
// DB-precomputed cluster_id rows and fell back to folder clusters when there
// were none; the Go store persists no cluster column, so the fallback is
// always the source — and it is exactly the deterministic path the Rust
// docstring demanded for a byte-stable artifact.
func CollectMarkdown(st store.Backend, project, generatedAtUTC string) (*MarkdownDoc, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		return nil, err
	}
	return &MarkdownDoc{
		Project:        project,
		GeneratedAtUTC: generatedAtUTC,
		Elements:       els,
		Relationships:  rels,
		GodNodes:       godNodes(els, rels, maxGodNodes),
		Clusters:       folderClusters(els),
	}, nil
}

// MarkdownResult reports what a CLI markdown export wrote.
type MarkdownResult struct {
	Path          string
	Elements      int
	Relationships int
	Clusters      int
}

// ExportMarkdown is the CLI entry point (`leankg export --markdown`): collect
// from the open store, render, and write the artifact. An empty out uses
// <projectRoot>/.leankg/graph-docs.md; a relative out resolves against
// projectRoot (the Rust run_export_markdown anchoring rule).
func ExportMarkdown(st store.Backend, projectRoot, out string) (MarkdownResult, error) {
	project := filepath.Base(projectRoot)
	doc, err := CollectMarkdown(st, project, nowRFC3339UTC())
	if err != nil {
		return MarkdownResult{}, err
	}

	dest := out
	if dest == "" {
		dest = filepath.Join(projectRoot, ".leankg", "graph-docs.md")
	} else if !filepath.IsAbs(dest) {
		dest = filepath.Join(projectRoot, dest)
	}
	if parent := filepath.Dir(dest); parent != "" {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return MarkdownResult{}, err
		}
	}
	if err := os.WriteFile(dest, []byte(Markdown(doc)), 0o644); err != nil {
		return MarkdownResult{}, err
	}
	return MarkdownResult{
		Path:          dest,
		Elements:      len(doc.Elements),
		Relationships: len(doc.Relationships),
		Clusters:      len(doc.Clusters),
	}, nil
}

// Markdown renders the deterministic document (Rust
// MarkdownExporter::generate): front matter, overview, top clusters, god
// nodes, architecture tree, cluster details.
func Markdown(doc *MarkdownDoc) string {
	var out strings.Builder
	renderFrontMatter(&out, doc)
	renderOverview(&out, doc)
	renderTopClusters(&out, doc)
	renderGodNodes(&out, doc)
	renderArchitectureTree(&out, doc)
	renderClusterDetails(&out, doc)
	return out.String()
}

func renderFrontMatter(out *strings.Builder, doc *MarkdownDoc) {
	out.WriteString("---\n")
	out.WriteString("title: LeanKG Graph Docs\n")
	fmt.Fprintf(out, "project: %s\n", escapeInline(doc.Project))
	fmt.Fprintf(out, "generated_at: %s\n", doc.GeneratedAtUTC)
	out.WriteString("---\n\n")
	out.WriteString("# LeanKG Graph Docs\n\n")
}

func renderOverview(out *strings.Builder, doc *MarkdownDoc) {
	byType := map[string]int{}
	for _, e := range doc.Elements {
		byType[e.ElementType]++
	}
	relByType := map[string]int{}
	for _, r := range doc.Relationships {
		relByType[r.RelType]++
	}

	out.WriteString("## Overview\n\n")
	fmt.Fprintf(out, "- Project: `%s`\n- Elements: %d\n- Relationships: %d\n- Clusters: %d\n\n",
		escapeInline(doc.Project), len(doc.Elements), len(doc.Relationships), len(doc.Clusters))

	writeCountTable(out, "### Elements by type", byType)
	writeCountTable(out, "### Relationships by type", relByType)
}

func writeCountTable(out *strings.Builder, heading string, counts map[string]int) {
	out.WriteString(heading + "\n\n")
	out.WriteString("| Type | Count |\n|---|---|\n")
	for _, t := range sortedKeys(counts) {
		fmt.Fprintf(out, "| %s | %d |\n", t, counts[t])
	}
	out.WriteString("\n")
}

func renderTopClusters(out *strings.Builder, doc *MarkdownDoc) {
	ranked := rankedClusters(doc)
	out.WriteString("## Top Clusters\n\n")
	out.WriteString("| ID | Label | Members |\n|---|---|---|\n")
	for i, c := range ranked {
		if i >= maxClusters {
			break
		}
		fmt.Fprintf(out, "| `%s` | %s | %d |\n", c.ID, escapeInline(c.Label), len(c.Members))
	}
	if len(ranked) > maxClusters {
		fmt.Fprintf(out, "\n… and %d more clusters.\n", len(ranked)-maxClusters)
	}
	out.WriteString("\n")
}

func renderGodNodes(out *strings.Builder, doc *MarkdownDoc) {
	byQN := make(map[string]store.Element, len(doc.Elements))
	for _, e := range doc.Elements {
		byQN[e.QualifiedName] = e
	}
	nodes := append([]GodNode(nil), doc.GodNodes...)
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Degree != nodes[j].Degree {
			return nodes[i].Degree > nodes[j].Degree
		}
		return nodes[i].QualifiedName < nodes[j].QualifiedName
	})
	if len(nodes) > maxGodNodes {
		nodes = nodes[:maxGodNodes]
	}

	fmt.Fprintf(out, "## God Nodes (top %d by degree)\n\n", maxGodNodes)
	out.WriteString("| Degree | Qualified name | Type |\n|---|---|---|\n")
	for _, n := range nodes {
		symbol := fmt.Sprintf("`%s`", n.QualifiedName)
		if e, ok := byQN[n.QualifiedName]; ok {
			anchor := fmt.Sprintf("%s#L%d", strings.ReplaceAll(e.FilePath, " ", "%20"), e.LineStart)
			symbol = fmt.Sprintf("[`%s`](%s)", n.QualifiedName, anchor)
		}
		fmt.Fprintf(out, "| %d | %s | %s |\n", n.Degree, symbol, n.ElementType)
	}
	out.WriteString("\n")
}

func renderArchitectureTree(out *strings.Builder, doc *MarkdownDoc) {
	out.WriteString("## Architecture Tree\n\n")
	if len(doc.Elements) == 0 {
		out.WriteString("_No indexed elements._\n\n")
		return
	}

	byFile := map[string][]store.Element{}
	for _, e := range doc.Elements {
		byFile[e.FilePath] = append(byFile[e.FilePath], e)
	}
	paths := make([]string, 0, len(byFile))
	for p := range byFile {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	root := newTrieNode()
	for _, path := range paths {
		els := append([]store.Element(nil), byFile[path]...)
		sort.SliceStable(els, func(i, j int) bool {
			return els[i].QualifiedName < els[j].QualifiedName
		})
		parts := strings.Split(path, "/")
		node := root
		for _, seg := range parts[:len(parts)-1] {
			child, ok := node.dirs[seg]
			if !ok {
				child = newTrieNode()
				node.dirs[seg] = child
			}
			node = child
		}
		node.files = append(node.files, fileEntry{name: parts[len(parts)-1], elements: els})
	}
	root.sort()

	lines := make([]string, 0)
	renderTrie(root, 0, &lines)
	for _, l := range lines {
		out.WriteString(l)
		out.WriteString("\n")
	}
	out.WriteString("\n")
}

func renderClusterDetails(out *strings.Builder, doc *MarkdownDoc) {
	ranked := rankedClusters(doc)
	if len(ranked) == 0 {
		return
	}
	out.WriteString("## Cluster Details\n\n")
	byQN := make(map[string]store.Element, len(doc.Elements))
	for _, e := range doc.Elements {
		byQN[e.QualifiedName] = e
	}

	for i, c := range ranked {
		if i >= maxClusters {
			break
		}
		fmt.Fprintf(out, "### %s (`%s`)\n\n", escapeInline(c.Label), c.ID)
		fmt.Fprintf(out, "%d members across %d files.\n\n", len(c.Members), len(c.RepresentativeFiles))
		if len(c.RepresentativeFiles) > 0 {
			out.WriteString("Member files:\n\n")
			for _, f := range c.RepresentativeFiles {
				fmt.Fprintf(out, "- `%s`\n", f)
			}
			out.WriteString("\n")
		}
		out.WriteString("Key symbols:\n\n")
		for j, qn := range c.Members {
			if j >= maxClusterMembersListed {
				break
			}
			if e, ok := byQN[qn]; ok {
				fmt.Fprintf(out, "- [`%s`](%s#L%d)\n", qn, e.FilePath, e.LineStart)
			} else {
				fmt.Fprintf(out, "- `%s`\n", qn)
			}
		}
		out.WriteString("\n")
	}
}

// fileEntry is one file row of the architecture trie.
type fileEntry struct {
	name     string
	elements []store.Element
}

// trieNode is a directory node keyed by path segment; children keep sorted
// order so the rendering is stable.
type trieNode struct {
	dirs  map[string]*trieNode
	files []fileEntry
}

func newTrieNode() *trieNode {
	return &trieNode{dirs: map[string]*trieNode{}}
}

func (n *trieNode) sort() {
	sort.SliceStable(n.files, func(i, j int) bool { return n.files[i].name < n.files[j].name })
	for _, d := range n.dirs {
		d.sort()
	}
}

// renderTrie renders depth-first. level is the indentation level of this
// node's children (the root's children print unindented); at maxTreeDepth the
// remaining subtrees collapse into one deterministic count line.
func renderTrie(node *trieNode, level int, lines *[]string) {
	pad := strings.Repeat(treeIndent, level)
	if level >= maxTreeDepth {
		hidden := len(node.dirs) + len(node.files)
		if hidden > 0 {
			*lines = append(*lines, fmt.Sprintf("%s- … (%d more)", pad, hidden))
		}
		return
	}
	for _, name := range sortedKeys(node.dirs) {
		*lines = append(*lines, fmt.Sprintf("%s- %s/", pad, name))
		renderTrie(node.dirs[name], level+1, lines)
	}
	for _, f := range node.files {
		*lines = append(*lines, fmt.Sprintf("%s- %s", pad, f.name))
		elPad := strings.Repeat(treeIndent, level+1)
		for _, e := range f.elements {
			*lines = append(*lines, fmt.Sprintf("%s- %s (%s)", elPad, e.Name, e.ElementType))
		}
	}
}

// rankedClusters is the canonical cluster ordering: size desc, then label asc,
// then id asc (Rust ranked_clusters). Members and representative files are
// sorted on a deep copy so rendering cannot mutate the document.
func rankedClusters(doc *MarkdownDoc) []Cluster {
	out := make([]Cluster, 0, len(doc.Clusters))
	for _, c := range doc.Clusters {
		members := append([]string(nil), c.Members...)
		sort.Strings(members)
		files := append([]string(nil), c.RepresentativeFiles...)
		sort.Strings(files)
		files = dedupeSorted(files)
		out = append(out, Cluster{ID: c.ID, Label: c.Label, Members: members, RepresentativeFiles: files})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Members) != len(out[j].Members) {
			return len(out[i].Members) > len(out[j].Members)
		}
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// folderClusters is the deterministic folder grouping (Rust folder_clusters):
// ids derive from the folder path, so they are stable forever.
func folderClusters(elements []store.Element) []Cluster {
	byFolder := map[string][]string{}
	for _, e := range elements {
		folder := "root"
		if i := strings.LastIndex(e.FilePath, "/"); i >= 0 {
			folder = e.FilePath[:i]
		}
		byFolder[folder] = append(byFolder[folder], e.QualifiedName)
	}
	out := make([]Cluster, 0, len(byFolder))
	for _, folder := range sortedKeys(byFolder) {
		members := append([]string(nil), byFolder[folder]...)
		sort.Strings(members)
		label := folder
		if i := strings.LastIndex(folder, "/"); i >= 0 {
			label = folder[i+1:]
		}
		out = append(out, Cluster{
			ID:                  "dir:" + folder,
			Label:               label,
			Members:             members,
			RepresentativeFiles: []string{folder},
		})
	}
	return out
}

// godNodes ports GraphEngine::get_god_nodes(limit, None): degree is the sum of
// the in and out edge counts (a self loop therefore counts twice, as the two
// Rust aggregate passes did), ordered by degree desc then qualified name asc.
func godNodes(els []store.Element, rels []store.Relationship, limit int) []GodNode {
	degree := map[string]int{}
	for _, r := range rels {
		degree[r.Source]++
		degree[r.Target]++
	}
	byQN := make(map[string]store.Element, len(els))
	for _, e := range els {
		byQN[e.QualifiedName] = e
	}
	qns := sortedKeys(degree)
	sort.SliceStable(qns, func(i, j int) bool {
		if degree[qns[i]] != degree[qns[j]] {
			return degree[qns[i]] > degree[qns[j]]
		}
		return qns[i] < qns[j]
	})
	if len(qns) > limit {
		qns = qns[:limit]
	}
	out := make([]GodNode, 0, len(qns))
	for _, qn := range qns {
		e := byQN[qn]
		out = append(out, GodNode{
			QualifiedName: qn,
			Name:          e.Name,
			ElementType:   e.ElementType,
			Degree:        degree[qn],
		})
	}
	return out
}

// escapeInline escapes characters that would break Markdown tables.
func escapeInline(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

// nowRFC3339UTC is the single timestamp in a markdown export (Rust
// now_rfc3339_utc, which hand-rolled the civil-from-days conversion).
func nowRFC3339UTC() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dedupeSorted(sorted []string) []string {
	if len(sorted) == 0 {
		return sorted
	}
	out := sorted[:1]
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}
