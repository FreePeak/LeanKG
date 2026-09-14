package summarize

// Markdown node persistence — the port of graft's src/context/node-file.ts.
//
// One file per curated node under the project's .leankg/summarize/ directory:
//
//	---
//	name / slug / type / model      ← identity
//	sources: [{path, hash}]         ← provenance + the staleness key
//	sources_digest                  ← sha256 over those path:hash lines
//	links: [{to, relation}]         ← resolved edges (by slug)
//	---
//	<!-- leankg:generated:start -->
//	## Summary … ## Related …       ← regenerated on every run
//	<!-- leankg:generated:end -->
//	## Notes                        ← anything a human writes, kept verbatim
//
// The frontmatter and the fenced block are machine-owned; everything below the
// end marker is not. That split is the whole point of this file: a run must
// never clobber an annotation somebody added by hand, the same way
// projectcfg.MergePreservingExisting refuses to drop user keys from leankg.yaml.
// Where graft returns a fresh notes region for a file whose markers were
// hand-deleted (node-file.ts:231-252) — discarding that file's prose — this
// port treats the whole body as human and keeps it: destroying non-regenerable
// notes to recover from a missing comment is a bad trade.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
	"gopkg.in/yaml.v3"
)

const (
	generatedStart = "<!-- leankg:generated:start -->"
	generatedEnd   = "<!-- leankg:generated:end -->"

	// defaultNotes seeds the human region of a brand-new node.
	defaultNotes = "\n## Notes\n\n_Anything written below the generated block survives every `leankg summarize` run._\n"
)

// nodeFrontmatter is the machine-owned header. Field order is the rendered
// order, so the file is byte-stable across runs.
type nodeFrontmatter struct {
	Name          string      `yaml:"name"`
	Slug          string      `yaml:"slug"`
	Type          string      `yaml:"type"`
	Model         string      `yaml:"model,omitempty"`
	Sources       []SourceRef `yaml:"sources"`
	SourcesDigest string      `yaml:"sources_digest"`
	Links         []Link      `yaml:"links"`
	Generator     string      `yaml:"generator"`
}

// slugPattern collapses every run of non-alphanumerics to one dash (graft
// slugify: normalizeName then [^a-z0-9]+ -> "-").
var slugPattern = regexp.MustCompile(`[^a-z0-9]+`)

// slugify turns a display name into a stable, filesystem- and link-safe slug.
// Long names are bounded: the suffix keeps distinct inputs distinct, and
// keeping only [a-z0-9-] means a model-invented name can never escape the node
// directory through a path separator or dot-dot.
func slugify(name string) string {
	s := strings.Trim(slugPattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-"), "-")
	if s == "" {
		s = "node"
	}
	if len(s) > 80 {
		sum := sha256.Sum256([]byte(s))
		s = s[:60] + "-" + hex.EncodeToString(sum[:])[:8]
	}
	return s
}

// renderNode serializes a node file. human is the preserved region; an empty
// one seeds the default notes block.
func renderNode(n Node, model, human string) ([]byte, error) {
	fm := nodeFrontmatter{
		Name: n.Name, Slug: n.Slug, Type: string(n.Kind), Model: model,
		Sources: n.Sources, SourcesDigest: n.SourcesDigest, Links: n.Links,
		Generator: "leankg summarize",
	}
	if fm.Sources == nil {
		fm.Sources = []SourceRef{}
	}
	if fm.Links == nil {
		fm.Links = []Link{}
	}
	head, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("summarize: frontmatter for %s: %w", n.Slug, err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(head)
	b.WriteString("---\n")
	b.WriteString(generatedStart + "\n")
	b.WriteString("## Summary\n\n")
	b.WriteString(strings.TrimSpace(n.Summary) + "\n\n")
	if len(n.Links) > 0 {
		b.WriteString("## Related\n\n")
		for _, l := range n.Links {
			b.WriteString("- " + strings.ReplaceAll(l.Relation, "_", " ") + " [[" + l.To + "]]")
			if l.Description != "" {
				b.WriteString(" — " + l.Description)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(generatedEnd + "\n")
	if human == "" {
		human = defaultNotes
	}
	b.WriteString(human)
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// humanRegion returns the preserved region of an existing node file:
// everything after the generated-end marker. found reports whether the file
// exists at all; a file with no markers has its whole body treated as human.
func humanRegion(path string) (human string, found bool, err error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("summarize: read node file %s: %w", filepath.Base(path), err)
	}
	s := string(b)
	if i := strings.Index(s, generatedEnd); i >= 0 {
		return strings.TrimPrefix(s[i+len(generatedEnd):], "\n"), true, nil
	}
	// No markers: either a hand-written file or one whose fence was deleted.
	// Below the frontmatter, everything is treated as human and kept.
	if strings.HasPrefix(s, "---\n") {
		if rest, ok := trimFrontmatter(s); ok {
			return strings.TrimPrefix(rest, "\n"), true, nil
		}
	}
	return s, true, nil
}

// isDefaultRegion reports whether a preserved region holds nothing a human
// wrote — the seed notes block, or blank. That is the only case in which a
// pruned node's file may be deleted.
func isDefaultRegion(human string) bool {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(human), "## Notes"))
	return t == "" || t == strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(defaultNotes), "## Notes"))
}

// trimFrontmatter drops a leading YAML frontmatter block.
func trimFrontmatter(s string) (string, bool) {
	i := strings.Index(s[3:], "\n---")
	if i < 0 {
		return "", false
	}
	rest := s[3+i+4:]
	return strings.TrimPrefix(rest, "\n"), true
}

// existingSlugs lists the node files on disk as slug -> path.
func existingSlugs(dir string) (map[string]string, error) {
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("summarize: read node dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		out[slug] = filepath.Join(dir, e.Name())
	}
	return out, nil
}

// persist writes the node set: the graph projection first (so query verbs see
// it), then the markdown files (so humans and agents can annotate them).
//
// Every node is cleared before anything is written, exactly like internal/convo
// does for mined nodes: store.DeleteByFile cascades to the relationships
// SOURCED at the cleared elements, so interleaving clears with writes would wipe
// the edges of nodes written earlier in the same pass.
func (p *pipeline) persist(nodes []Node) error {
	live := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		live[n.Slug] = n
	}
	existing, err := existingSlugs(p.nodesDir)
	if err != nil {
		return err
	}

	// Dead nodes leave the graph (graft build.ts:336-339). Their file goes too,
	// unless a human wrote in it: discarding non-regenerable notes is the one
	// thing this pipeline never does. Such a file keeps its provenance
	// frontmatter, so a later run that re-invents the node finds the notes.
	for slug, path := range existing {
		if _, ok := live[slug]; ok {
			continue
		}
		human, _, herr := humanRegion(path)
		if herr != nil {
			return herr
		}
		if err := p.st.DeleteByFile("summarize://" + slug); err != nil {
			return fmt.Errorf("summarize: prune node %s: %w", slug, err)
		}
		p.mu.Lock()
		p.deadNodes++
		p.mu.Unlock()
		if isDefaultRegion(human) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("summarize: remove node file %s: %w", slug, err)
			}
		}
	}

	elements := make([]store.Element, 0, len(nodes))
	var relationships []store.Relationship
	for _, n := range nodes {
		if err := p.st.DeleteByFile(n.FilePath()); err != nil {
			return fmt.Errorf("summarize: clear node %s: %w", n.Slug, err)
		}
		elements = append(elements, n.element(p.model()))
		relationships = append(relationships, n.relationships()...)
	}
	if len(elements) > 0 {
		if err := p.st.UpsertElements(elements); err != nil {
			return fmt.Errorf("summarize: write %d meaning nodes: %w", len(elements), err)
		}
	}
	if len(relationships) > 0 {
		if err := p.st.UpsertRelationships(relationships); err != nil {
			return fmt.Errorf("summarize: write %d meaning edges: %w", len(relationships), err)
		}
	}

	if err := os.MkdirAll(p.nodesDir, 0o755); err != nil {
		return fmt.Errorf("summarize: create node dir: %w", err)
	}
	for _, n := range nodes {
		path := filepath.Join(p.nodesDir, n.Slug+".md")
		human, _, err := humanRegion(path)
		if err != nil {
			return err
		}
		body, err := renderNode(n, p.model(), human)
		if err != nil {
			return err
		}
		// Atomic replace: a crash mid-write must not leave a half-written node
		// file whose frontmatter no parser accepts.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return fmt.Errorf("summarize: write node %s: %w", n.Slug, err)
		}
		if err := os.Rename(tmp, path); err != nil {
			return fmt.Errorf("summarize: replace node %s: %w", n.Slug, err)
		}
		p.mu.Lock()
		p.nodeFiles++
		p.mu.Unlock()
	}
	return nil
}
