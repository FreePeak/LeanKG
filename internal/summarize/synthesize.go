package summarize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// NodeKind is the curated node type pass 2 emits (graft synthesize.ts:20-31).
type NodeKind string

const (
	NodeSystem  NodeKind = "system"  // groups files that collaborate as one component
	NodeFile    NodeKind = "file"    // a standalone module that deserves its own node
	NodeConcept NodeKind = "concept" // a cross-cutting idea, decision or invariant
)

// NodeTypes is the closed type enum. Graft's JSON schema leaves the string open
// and trusts the provider; the Go port validates, because a typo silently
// creating a new node category is exactly the kind of drift the curated pass
// exists to prevent. A node with an unknown type is dropped, not coerced.
var NodeTypes = map[NodeKind]bool{NodeSystem: true, NodeFile: true, NodeConcept: true}

// RelationVerbs is graft's closed relation enum (synthesize.ts:74-78), each
// verb answering a question a code reviewer asks.
var RelationVerbs = map[string]bool{
	"part_of": true, "uses": true, "depends_on": true, "produces": true,
	"configures": true, "validates": true, "implements": true,
}

// SourceRef is one grounding file with the content hash it had at synthesis
// time — the node's staleness key and the markdown frontmatter's provenance.
type SourceRef struct {
	Path string `json:"path" yaml:"path"`
	Hash string `json:"hash" yaml:"hash"`
}

// Link is one curated edge, addressed by the TARGET NODE NAME until resolution
// turns it into a slug (graft build.ts:237-244).
type Link struct {
	To          string `json:"to" yaml:"to"`
	Relation    string `json:"relation" yaml:"relation"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Node is one curated synthesis node after validation and merge.
type Node struct {
	Name          string
	Slug          string
	Kind          NodeKind
	Summary       string
	Sources       []SourceRef
	SourcesDigest string
	Links         []Link
}

// QualifiedName is the node's key in code_elements. The scheme prefix keeps a
// curated node from ever colliding with an indexed element.
func (n Node) QualifiedName() string { return "summarize:" + n.Slug }

// FilePath marks the meaning tier in code_elements.file_path, the same marker
// convention the ontology layer uses ("ontology://<gid>"). DeleteByFile on this
// path removes the node and its outgoing edges, which is how a re-run clears a
// node before rewriting it.
func (n Node) FilePath() string { return "summarize://" + n.Slug }

// ElementType is the node's element type. It is deliberately distinct from the
// index layer's types: "file" there means a real source file (docgen counts
// them, ontology traversal seeds on them), so a curated node must not wear it.
func (n Node) ElementType() string {
	switch n.Kind {
	case NodeSystem:
		return "summary_system"
	case NodeFile:
		return "summary_file"
	default:
		return "summary_concept"
	}
}

// element projects a node into the store, so graph verbs traverse the tier.
func (n Node) element(model string) store.Element {
	return store.Element{
		QualifiedName: n.QualifiedName(),
		ElementType:   n.ElementType(),
		Name:          n.Name,
		FilePath:      n.FilePath(),
		LineStart:     1,
		LineEnd:       1,
		Language:      "summarize",
		Content:       n.Summary,
		Metadata: map[string]any{
			"slug":           n.Slug,
			"kind":           string(n.Kind),
			"model":          model,
			"sources":        n.Sources,
			"sources_digest": n.SourcesDigest,
			"links":          n.Links,
		},
	}
}

// relationships projects the node's curated edges. Links are already resolved
// to defined nodes (see mergeNodes), so no edge can dangle.
func (n Node) relationships() []store.Relationship {
	out := make([]store.Relationship, 0, len(n.Links))
	for _, l := range n.Links {
		out = append(out, store.Relationship{
			Source:     n.QualifiedName(),
			Target:     "summarize:" + l.To,
			RelType:    l.Relation,
			Confidence: 0.7, // LLM-curated: INFERRED, never EXTRACTED
			Metadata: map[string]any{
				"confidence_label": "INFERRED",
				"description":      l.Description,
				"node":             n.Slug,
			},
		})
	}
	return out
}

// fileSummary is one labeled summary fed into a synthesis call.
type fileSummary struct {
	Path    string
	Summary string
}

// cleanedNode is the validated wire shape, and the shape the batch cache
// stores: links are still addressed by node NAME, because resolution depends on
// which other nodes the whole run defines.
type cleanedNode struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Summary string   `json:"summary"`
	Sources []string `json:"sources"`
	Links   []Link   `json:"links"`
}

// batchSummaries greedily packs summaries into batches under a char budget,
// every batch holding at least one file (graft build.ts:381-395). Summaries
// arrive sorted by path, so the split is deterministic.
func batchSummaries(files []fileSummary, budget int) [][]fileSummary {
	var batches [][]fileSummary
	var cur []fileSummary
	size := 0
	for _, f := range files {
		// graft: path + summary + 8 (the "## \n\n" framing).
		l := len(f.Path) + len(f.Summary) + 8
		if len(cur) > 0 && size+l > budget {
			batches = append(batches, cur)
			cur, size = nil, 0
		}
		cur = append(cur, f)
		size += l
	}
	if len(cur) > 0 {
		batches = append(batches, cur)
	}
	return batches
}

// batchKey is the content key of one batch: sha256 over the sorted
// path:content-hash lines (graft build.ts:396-401). An edited file therefore
// invalidates only its own summary and the batches that contain it.
func batchKey(batch []fileSummary, hashByPath map[string]string) string {
	lines := make([]string, 0, len(batch))
	for _, f := range batch {
		lines = append(lines, f.Path+":"+hashByPath[f.Path])
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// batchContent renders the user payload of one synthesis call (graft
// synthesize.ts:92-97).
func batchContent(batch []fileSummary) string {
	var b strings.Builder
	for i, f := range batch {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("## " + f.Path + "\n\n" + f.Summary)
	}
	return b.String()
}

// extractJSON recovers the JSON object from a completion. Providers that honor
// response_format answer with bare JSON; the rest wrap it in prose or a code
// fence (graft #129 hit exactly this through a gateway), so the outermost
// braces are located rather than assumed.
func extractJSON(text string) ([]byte, bool) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	return []byte(text[start : end+1]), true
}

// parseNodes validates one synthesis reply. It is strict about the enum and
// tolerant about junk rows: a malformed node is dropped and counted, never
// allowed to abort a batch that produced good ones. An unusable reply reports
// an error so the caller can book it as a content-quality miss.
func parseNodes(text string) ([]cleanedNode, error) {
	raw, ok := extractJSON(text)
	if !ok {
		return nil, fmt.Errorf("no JSON object in the reply")
	}
	var payload struct {
		Nodes []cleanedNode `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	out := make([]cleanedNode, 0, len(payload.Nodes))
	for _, n := range payload.Nodes {
		name := strings.TrimSpace(n.Name)
		kind := NodeKind(strings.ToLower(strings.TrimSpace(n.Type)))
		if name == "" || !NodeTypes[kind] {
			continue
		}
		clean := cleanedNode{
			Name:    name,
			Type:    string(kind),
			Summary: strings.TrimSpace(n.Summary),
		}
		for _, s := range n.Sources {
			if s = strings.TrimSpace(s); s != "" {
				clean.Sources = append(clean.Sources, s)
			}
		}
		for _, l := range n.Links {
			to := strings.TrimSpace(l.To)
			rel := strings.ToLower(strings.TrimSpace(l.Relation))
			if to == "" || !RelationVerbs[rel] {
				continue
			}
			clean.Links = append(clean.Links, Link{To: to, Relation: rel, Description: strings.TrimSpace(l.Description)})
		}
		out = append(out, clean)
	}
	return out, nil
}

// mergeNodes folds every batch's nodes into the final set: merge by slug,
// resolve links only to defined nodes, give an unattributed concept the
// provenance of what it links to, and finalize the digests (graft build.ts
// phases 3-5).
func mergeNodes(raw []cleanedNode, hashByPath map[string]string) []Node {
	type draft struct {
		name    string
		slug    string
		kind    NodeKind
		summary string
		sources map[string]string
		links   map[string]Link
	}
	drafts := map[string]*draft{}
	nameToSlug := map[string]string{}
	var order []string

	register := func(name, slug string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key != "" {
			if _, ok := nameToSlug[key]; !ok {
				nameToSlug[key] = slug
			}
		}
	}
	for _, n := range raw {
		slug := slugify(n.Name)
		d, ok := drafts[slug]
		if !ok {
			d = &draft{name: n.Name, slug: slug, kind: NodeKind(n.Type), sources: map[string]string{}, links: map[string]Link{}}
			drafts[slug] = d
			order = append(order, slug)
		}
		if len(n.Summary) > len(d.summary) {
			d.summary = n.Summary
		}
		// graft: an explicit type overrides the concept default.
		if n.Type != "" && d.kind == NodeConcept {
			d.kind = NodeKind(n.Type)
		}
		for _, src := range n.Sources {
			if h, ok := hashByPath[src]; ok {
				d.sources[src] = h
			}
		}
		register(n.Name, slug)
	}

	// Resolve links: both endpoints must be defined nodes; self-links drop.
	for _, n := range raw {
		from := drafts[slugify(n.Name)]
		if from == nil {
			continue
		}
		for _, l := range n.Links {
			to, ok := nameToSlug[strings.ToLower(strings.TrimSpace(l.To))]
			if !ok || to == from.slug {
				continue
			}
			key := to + "|" + l.Relation
			if _, ok := from.links[key]; !ok {
				from.links[key] = Link{To: to, Relation: l.Relation, Description: l.Description}
			}
		}
	}
	// A concept the model grounded in no file inherits the provenance of the
	// nodes it links to, so it still goes stale when its subject changes.
	for _, slug := range order {
		d := drafts[slug]
		if len(d.sources) > 0 {
			continue
		}
		for _, l := range d.links {
			if target := drafts[l.To]; target != nil {
				for p, h := range target.sources {
					d.sources[p] = h
				}
			}
		}
	}

	out := make([]Node, 0, len(order))
	for _, slug := range order {
		d := drafts[slug]
		sources := make([]SourceRef, 0, len(d.sources))
		for p, h := range d.sources {
			sources = append(sources, SourceRef{Path: p, Hash: h})
		}
		sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
		links := make([]Link, 0, len(d.links))
		for _, l := range d.links {
			links = append(links, l)
		}
		sort.Slice(links, func(i, j int) bool { return links[i].To < links[j].To })
		out = append(out, Node{
			Name: d.name, Slug: d.slug, Kind: d.kind, Summary: d.summary,
			Sources: sources, SourcesDigest: digestSources(sources), Links: links,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out
}

// countNodes tallies a node set by kind.
func countNodes(nodes []Node) (total, systems, files, concepts, links int) {
	for _, n := range nodes {
		total++
		links += len(n.Links)
		switch n.Kind {
		case NodeSystem:
			systems++
		case NodeFile:
			files++
		default:
			concepts++
		}
	}
	return total, systems, files, concepts, links
}

// digestSources is sha256 over the sorted path:hash lines of a source set
// (graft node-file.ts digestSources) — the node's staleness key.
func digestSources(sources []SourceRef) string {
	lines := make([]string, 0, len(sources))
	for _, s := range sources {
		lines = append(lines, s.Path+":"+s.Hash)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// synthCacheNamespace is the store KV namespace the batch cache lives under.
// The ontology layer persists its catalogs the same way, so this is the
// established shape for "a generated catalog that is not graph content".
const synthCacheNamespace = "summarize"

// synthCacheKey is the one key holding the whole batch cache: a map of batch
// content key -> validated nodes. One row keeps the cache prunable, which
// per-batch rows could not be without a delete API.
const synthCacheKey = "synth-cache"

// loadSynthCache reads the batch cache. A malformed or missing cache is an
// empty one: the cache is an optimization, never a source of truth.
func (p *pipeline) loadSynthCache() map[string][]cleanedNode {
	raw, ok, err := p.st.KVGet(synthCacheNamespace, synthCacheKey)
	if err != nil || !ok || raw == "" {
		return map[string][]cleanedNode{}
	}
	var cache map[string][]cleanedNode
	if err := json.Unmarshal([]byte(raw), &cache); err != nil {
		return map[string][]cleanedNode{}
	}
	return cache
}

// saveSynthCache rewrites the cache with the live batches only, so it cannot
// grow forever and a batch that produced nothing is retried next run rather
// than frozen as an empty success (graft build.ts:240-254).
func (p *pipeline) saveSynthCache(live map[string][]cleanedNode) {
	blob, err := json.Marshal(live)
	if err != nil {
		return
	}
	_ = p.st.KVSet(synthCacheNamespace, synthCacheKey, string(blob))
}

// pass2 batches the summaries and asks for a curated node set per batch.
func (p *pipeline) pass2(ctx context.Context, ws []work) []Node {
	items := make([]fileSummary, 0, len(ws))
	hashByPath := make(map[string]string, len(ws))
	for _, w := range ws {
		if w.hash == "" {
			continue
		}
		hashByPath[w.path] = w.hash
		if w.summary != "" {
			items = append(items, fileSummary{Path: w.path, Summary: w.summary})
		}
	}
	batches := batchSummaries(items, BatchCharBudget)
	p.mu.Lock()
	p.batches = len(batches)
	p.mu.Unlock()
	if len(batches) == 0 {
		return nil
	}

	cache := p.loadSynthCache()
	live := map[string][]cleanedNode{}
	var all []cleanedNode
	for i, batch := range batches {
		if ctx.Err() != nil {
			break
		}
		key := batchKey(batch, hashByPath)
		// An empty entry is a miss, not a hit (graft #177): a cached [] made a
		// silent empty synthesis permanent. --force ignores the cache outright,
		// because the key is file CONTENT: a prompt change leaves every hash
		// identical, and forcing a regeneration must not hand back old nodes.
		if !p.opts.Force {
			if got, ok := cache[key]; ok && len(got) > 0 {
				live[key] = got
				all = append(all, got...)
				p.mu.Lock()
				p.batchHit++
				p.mu.Unlock()
				continue
			}
		}
		if p.opts.DryRun {
			continue // would spend one call; the plan already counts the batch
		}
		text, err := p.opts.Chat.Complete(ctx, Request{
			System: synthesisSystemPrompt,
			User:   batchContent(batch),
			JSON:   true,
		})
		if err != nil {
			p.gate.record(err.Error())
			p.addError(fmt.Sprintf("synthesis batch %d/%d: %v", i+1, len(batches), err))
			continue
		}
		nodes, perr := parseNodes(text)
		if perr != nil {
			p.gate.recordQuality(fmt.Sprintf("synthesis batch %d/%d: %v", i+1, len(batches), perr))
			p.addError(fmt.Sprintf("synthesis batch %d/%d: %v", i+1, len(batches), perr))
			continue
		}
		if len(nodes) == 0 {
			p.gate.recordQuality(fmt.Sprintf("synthesis batch %d/%d: the model defined no usable node", i+1, len(batches)))
			p.addError(fmt.Sprintf("synthesis batch %d/%d: no usable nodes in the reply", i+1, len(batches)))
			continue
		}
		live[key] = nodes
		all = append(all, nodes...)
		p.gate.succeeded()
	}
	p.saveSynthCache(live)
	if len(all) == 0 {
		return nil
	}

	nodes := mergeNodes(all, hashByPath)
	total, systems, files, concepts, links := countNodes(nodes)
	p.mu.Lock()
	p.nodes, p.systems, p.fileNodes, p.concepts, p.links = total, systems, files, concepts, links
	if links == 0 && len(batches) > 1 {
		// graft's degrading-model warning (build.ts:348-352): real nodes, no
		// edges, across many batches is a model that stopped relating things.
		p.warning = fmt.Sprintf("synthesis produced 0 links across %d batches — the model may be degrading", len(batches))
	}
	p.mu.Unlock()
	return nodes
}
