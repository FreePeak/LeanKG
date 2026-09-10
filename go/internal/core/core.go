// Package core implements the 3-tool surface (import / query / status) and
// the L0–L3 query ladder for the Go engine. Transports (MCP, REST) are thin
// adapters over this package; the tool envelope resolves HERE before any
// gate — a read-named envelope can never smuggle a write action (the
// security property carried over from the Rust engine).
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// The registry is exactly three tools (product contract; CI-pinned on the
// Rust side, pinned here by the ResolveEnvelope error below + the mcp
// registry test).
const (
	ToolImport = "import"
	ToolQuery  = "query"
	ToolStatus = "status"
)

// ToolAliases maps legacy tool names (v4.4 surface) to their Go-engine
// equivalents; one minor release of aliasing per the deprecation policy.
var ToolAliases = map[string]string{
	"set": ToolImport,
	"get": ToolQuery,
}

// QueryEmbedder is the query-time embedding port core needs for the L3 rung.
// The serving binary wires an HTTP provider (sidecar or API) — it performs
// zero inference itself (issue #368: inference lives in leankg-embed).
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	Describe() (modelID, provider string)
	// Revision pins the model revision the query embedder serves; compared
	// against the stored collection stamp on every L3 query (query-side
	// degrade on drift — FR-ZCP-11 part 2 port).
	Revision() string
}

// Engine is the core service over one project's store.
type Engine struct {
	st       store.Backend
	mem      *memory.Memory
	embedder QueryEmbedder // optional; nil ⇒ L3 degrades with reason
}

// New builds an Engine. mem may be nil (memory actions then error).
func New(st store.Backend, mem *memory.Memory, embedder QueryEmbedder) *Engine {
	return &Engine{st: st, mem: mem, embedder: embedder}
}

// Store exposes the underlying backend (transports needing raw reads).
func (e *Engine) Store() store.Backend { return e.st }

// QueryEmbedderFromProvider adapts an embed.Provider to the L3 query
// embedder port (query-time embedding is an HTTP client call — the serving
// binary does zero inference, issue #368).
func QueryEmbedderFromProvider(p embed.Provider) QueryEmbedder {
	return providerEmbedder{p: p}
}

type providerEmbedder struct{ p embed.Provider }

func (a providerEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := a.p.Embed(ctx, embed.Query, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embed: query returned %d vectors, want 1", len(vecs))
	}
	return vecs[0], nil
}

func (a providerEmbedder) Describe() (string, string) {
	return a.p.ModelID(), a.p.Provider()
}

func (a providerEmbedder) Revision() string { return a.p.Revision() }

// ResolveEnvelope maps a requested tool name to the canonical tool, applying
// aliases. Unknown names hard-fail naming the valid surface (error-catalog
// posture carried over from FR-ZCP-12).
func ResolveEnvelope(tool string) (string, error) {
	if tool == ToolImport || tool == ToolQuery || tool == ToolStatus {
		return tool, nil
	}
	if canonical, ok := ToolAliases[tool]; ok {
		return canonical, nil
	}
	return "", fmt.Errorf("unknown tool %q — valid tools: import, query, status (legacy aliases: set, get)", tool)
}

// freshness derives the freshness label by comparing the current watermark
// against the inventory's last-computed seq. cold when nothing is indexed.
// (DB-resident — there is no in-process TTL cache to race.)
func (e *Engine) freshness(totalElements int) string {
	if totalElements == 0 {
		return "cold"
	}
	inv, err := e.st.LoadInventory()
	if err != nil || inv == nil {
		return "possibly_stale"
	}
	seq, _, err := e.st.Watermark()
	if err != nil {
		return "possibly_stale"
	}
	if seq == inv.LastInventorySeq {
		return "fresh"
	}
	return "possibly_stale"
}

// ImportRequest is the import tool payload.
type ImportRequest struct {
	Action string `json:"action"`         // repo | dir | memory
	Path   string `json:"path,omitempty"` // repo/dir target
	// Memory write commands ride action="memory".
	Command string         `json:"command,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
}

// Import handles the import tool: repo/dir indexing or memory curation writes.
func (e *Engine) Import(ctx context.Context, req ImportRequest) (map[string]any, error) {
	switch req.Action {
	case "repo", "dir":
		if req.Path == "" {
			return nil, fmt.Errorf("import %s requires path", req.Action)
		}
		res, err := index.IndexDir(ctx, e.st, req.Path)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", req.Path, err)
		}
		if _, err := e.refreshInventory(); err != nil {
			return nil, err
		}
		status, _ := e.Status(ctx)
		return map[string]any{
			"indexed": map[string]any{
				"files":         res.Files,
				"elements":      res.Elements,
				"relationships": res.Relationships,
				"skipped":       res.Skipped,
			},
			"status": status,
		}, nil
	case "memory":
		return e.memoryWrite(req)
	case "":
		return nil, fmt.Errorf("import requires action (repo, dir, memory)")
	default:
		return nil, fmt.Errorf("unknown import action %q (valid: repo, dir, memory)", req.Action)
	}
}

// refreshInventory recomputes and persists the inventory snapshot.
func (e *Engine) refreshInventory() (store.Inventory, error) {
	els, err := e.st.ElementCount()
	if err != nil {
		return store.Inventory{}, err
	}
	rels, err := e.st.RelationshipCount()
	if err != nil {
		return store.Inventory{}, err
	}
	files, err := e.st.FileCount()
	if err != nil {
		return store.Inventory{}, err
	}
	byType, err := e.st.ElementsByType()
	if err != nil {
		return store.Inventory{}, err
	}
	vectors := 0
	if stamps, err := e.st.Stamps(); err == nil {
		for _, st := range stamps {
			if n, err := e.st.VectorCount(st.ModelID); err == nil {
				vectors += n
			}
		}
	}
	inv := store.Inventory{
		TotalElements:      els,
		TotalFiles:         files,
		TotalRelationships: rels,
		TotalVectors:       vectors,
		ElementsByType:     byType,
	}
	if err := e.st.SaveInventory(inv); err != nil {
		return store.Inventory{}, err
	}
	return inv, nil
}

// Status is the status tool: health, inventory, freshness, backend, and the
// embed-pipeline state read from DB tables (no in-process coupling with
// leankg-embed — issue #368 AC). Provider disclosure is part of the egress
// contract: status always names the active embedding provider.
func (e *Engine) Status(_ context.Context) (map[string]any, error) {
	els, err := e.st.ElementCount()
	if err != nil {
		return nil, err
	}
	rels, err := e.st.RelationshipCount()
	if err != nil {
		return nil, err
	}
	files, err := e.st.FileCount()
	if err != nil {
		return nil, err
	}
	byType, err := e.st.ElementsByType()
	if err != nil {
		return nil, err
	}
	seq, at, err := e.st.Watermark()
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"backend":          "sqlite",
		"healthy":          true,
		"store":            e.st.Path(),
		"mode":             e.st.Engine(),
		"elements":         els,
		"files":            files,
		"relationships":    rels,
		"elements_by_type": byType,
		"watermark":        map[string]any{"seq": seq, "at": at},
		"freshness":        e.freshness(els),
		"tools":            []string{ToolImport, ToolQuery, ToolStatus},
		"embeddings":       e.embeddingsState(),
	}
	if e.embedder != nil {
		modelID, provider := e.embedder.Describe()
		out["query_embedder"] = map[string]any{"model_id": modelID, "provider": provider}
	}
	if run, err := e.st.LastEmbedRunAny(); err == nil && run != nil {
		out["last_embed_run"] = run
	}
	if e.mem != nil {
		out["memory_root"] = e.mem.Root()
	}
	return out, nil
}

func (e *Engine) embeddingsState() []map[string]any {
	stamps, err := e.st.Stamps()
	if err != nil {
		return []map[string]any{}
	}
	states := make([]map[string]any, 0, len(stamps))
	for _, st := range stamps {
		vecs, _ := e.st.VectorCount(st.ModelID)
		states = append(states, map[string]any{
			"model_id":   st.ModelID,
			"revision":   st.Revision,
			"dimensions": st.Dimensions,
			"distance":   st.Distance,
			"provider":   st.Provider,
			"vectors":    vecs,
		})
	}
	return states
}

// QueryRequest is the query tool payload.
type QueryRequest struct {
	Action string `json:"action,omitempty"` // "" = ladder router
	Query  string `json:"query"`
	Limit  int    `json:"limit,omitempty"`
}

// Query handles the query tool. action "" routes down the ladder
// L1 exact → L2 fuzzy → L3 semantic (provider wired). Explicit actions pin a
// rung; "memory" routes to memory reads. Every answer carries
// retrieval{rung,reason} + freshness.
func (e *Engine) Query(ctx context.Context, req QueryRequest) (map[string]any, error) {
	switch req.Action {
	case "memory":
		// Query-tool memory reads carry the command in Query.
		return e.MemoryRead("search", "", req.Query, req.Limit)
	case "", "search", "exact", "fuzzy", "semantic", "element":
	default:
		return nil, fmt.Errorf("unknown query action %q (valid: search, exact, fuzzy, semantic, element, memory; empty = ladder router)", req.Action)
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query requires query text")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	resp := map[string]any{"query": req.Query, "limit": limit}

	els, _ := e.st.ElementCount()
	resp["freshness"] = e.freshness(els)

	switch req.Action {
	case "exact", "element":
		return e.rungExact(req.Query, limit, resp, true)
	case "fuzzy":
		return e.rungFuzzy(req.Query, limit, resp, true)
	case "semantic":
		return e.rungSemantic(ctx, req.Query, limit, resp, true)
	}

	// Ladder router: L0 cold → L1 exact → L2 fuzzy → L3 semantic.
	if els == 0 {
		resp["retrieval"] = map[string]any{"rung": "L0", "reason": "no elements indexed"}
		resp["hits"] = []any{}
		resp["guidance"] = "Index a repository first: leankg import <dir> or tool import {action:\"repo\", path:...}"
		return resp, nil
	}
	if _, err := e.rungExact(req.Query, limit, resp, false); err == nil && len(hitsOf(resp)) > 0 {
		resp["retrieval"] = map[string]any{"rung": "L1", "reason": "exact identifier match"}
		return resp, nil
	}
	if _, err := e.rungFuzzy(req.Query, limit, resp, false); err == nil && len(hitsOf(resp)) > 0 {
		resp["retrieval"] = map[string]any{"rung": "L2", "reason": "FTS5 keyword match"}
		return resp, nil
	}
	return e.rungSemantic(ctx, req.Query, limit, resp, true)
}

func hitsOf(resp map[string]any) []map[string]any {
	h, _ := resp["hits"].([]map[string]any)
	return h
}

// rungExact is L1: case-insensitive exact match on name / qualified name.
func (e *Engine) rungExact(q string, limit int, resp map[string]any, pin bool) (map[string]any, error) {
	if pin {
		resp["retrieval"] = map[string]any{"rung": "L1", "reason": "exact identifier match"}
	}
	els, err := e.st.FindExact(q)
	if err != nil {
		return nil, err
	}
	resp["hits"] = shapeElements(els, limit)
	return resp, nil
}

// rungFuzzy is L2: FTS5 keyword match (sqlite's fuzzy rung; trigram is the
// other engine's PG story).
func (e *Engine) rungFuzzy(q string, limit int, resp map[string]any, pin bool) (map[string]any, error) {
	matches, err := e.st.FindFuzzy(q, limit)
	if err != nil {
		return nil, err
	}
	if pin {
		reason := "FTS5 keyword match"
		if len(matches) == 0 {
			reason = "no keyword match"
		}
		resp["retrieval"] = map[string]any{"rung": "L2", "reason": reason}
	}
	hits := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		h := shapeElement(m.Element)
		h["score"] = m.Score
		hits = append(hits, h)
	}
	resp["hits"] = hits
	return resp, nil
}

// rungSemantic is L3: embed the query via the wired provider, cosine top-k
// over the first stamped collection. Provider failure or absent wiring
// DEGRADES to L2 — never a hard error (issue #368 AC: query-time provider
// failure degrades the ladder with retrieval.reason).
func (e *Engine) rungSemantic(ctx context.Context, q string, limit int, resp map[string]any, pin bool) (map[string]any, error) {
	degrade := func(reason string) (map[string]any, error) {
		if pin {
			resp["retrieval"] = map[string]any{"rung": "L2", "reason": reason}
		}
		return e.rungFuzzy(q, limit, resp, false)
	}
	if e.embedder == nil {
		return degrade("no embedding provider wired; degraded from L3")
	}
	// Select the collection BY the embedder's model (Stamps() has no ORDER
	// BY — stamps[0] was arbitrary with >1 model) and enforce the query-side
	// stamp contract: drift DEGRADES to L2, never silently wrong answers.
	modelID, _ := e.embedder.Describe()
	stamp, err := e.st.Stamp(modelID)
	if err != nil || stamp == nil {
		return degrade(fmt.Sprintf("no vector collection for model %s; degraded from L3", modelID))
	}
	if stamp.Revision != e.embedder.Revision() {
		return degrade(fmt.Sprintf("stamp mismatch: collection %q vs provider %q; degraded from L3", stamp.Revision, e.embedder.Revision()))
	}
	qvec, err := e.embedder.EmbedQuery(ctx, q)
	if err != nil {
		return degrade(fmt.Sprintf("embedding provider failed (%v); degraded from L3", err))
	}
	hits, err := e.st.SearchVectors(modelID, qvec, limit)
	if err != nil {
		return degrade(fmt.Sprintf("vector search failed (%v); degraded from L3", err))
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		m := shapeElement(h.Element)
		m["similarity"] = h.Similarity
		out = append(out, m)
	}
	resp["hits"] = out
	resp["retrieval"] = map[string]any{"rung": "L3", "reason": "vector similarity (cosine)"}
	return resp, nil
}

// --- memory actions (ride the 3-tool surface; #369) ---

func (e *Engine) memoryWrite(req ImportRequest) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	get := func(key string) string {
		if s, ok := req.Args[key].(string); ok && s != "" {
			return s
		}
		// MCP callers pass the memory file at the top-level path field.
		if key == "path" {
			return req.Path
		}
		return ""
	}
	getInt := func(key string) int {
		f, _ := req.Args[key].(float64)
		return int(f)
	}
	var err error
	switch req.Command {
	case "create":
		err = e.mem.Create(get("path"), get("content"))
	case "str_replace":
		err = e.mem.StrReplace(get("path"), get("old"), get("new"))
	case "insert":
		err = e.mem.Insert(get("path"), get("content"), getInt("insert_line"))
	case "delete":
		err = e.mem.Delete(get("path"))
	case "rename":
		err = e.mem.Rename(get("path"), get("new_path"))
	case "add":
		err = e.mem.Add(get("file"), get("text"))
	case "replace":
		err = e.mem.Replace(get("file"), get("old"), get("new"))
	case "remove":
		err = e.mem.Remove(get("file"), get("text"))
	default:
		return nil, fmt.Errorf("unknown memory command %q (valid: create, str_replace, insert, delete, rename, add, replace, remove)", req.Command)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "command": req.Command}, nil
}

// MemoryRead handles read-only memory commands: view, snapshot, search.
func (e *Engine) MemoryRead(command, path, query string, limit int) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	switch command {
	case "view":
		text, err := e.mem.View(path, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "view", "path": path, "content": text}, nil
	case "snapshot":
		text, err := e.mem.Snapshot()
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "snapshot", "content": text}, nil
	case "search":
		if limit <= 0 {
			limit = 10
		}
		hits, err := e.mem.Search(query, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "search", "hits": hits}, nil
	default:
		return nil, fmt.Errorf("unknown memory read command %q (valid: view, snapshot, search)", command)
	}
}

// --- shaping ---

func shapeElements(els []store.Element, limit int) []map[string]any {
	out := make([]map[string]any, 0, len(els))
	for _, el := range els {
		out = append(out, shapeElement(el))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func shapeElement(el store.Element) map[string]any {
	m := map[string]any{
		"qualified_name": el.QualifiedName,
		"element_type":   el.ElementType,
		"name":           el.Name,
		"file_path":      el.FilePath,
		"line_start":     el.LineStart,
		"line_end":       el.LineEnd,
		"language":       el.Language,
	}
	if el.ParentQualified != "" {
		m["parent_qualified"] = el.ParentQualified
	}
	content := el.Content
	if len(content) > 400 {
		content = content[:400] + "…"
	}
	m["content"] = content
	return m
}

// NLRoute extracts the best ladder query string from a natural-language
// question: the longest identifier-looking token beats prose for L1.
func NLRoute(q string) string {
	fields := strings.Fields(q)
	var idents []string
	for _, f := range fields {
		trimmed := strings.Trim(f, ".,;:()[]{}\"'`")
		if looksLikeIdentifier(trimmed) {
			idents = append(idents, trimmed)
		}
	}
	if len(idents) > 0 {
		sort.Slice(idents, func(i, j int) bool { return len(idents[i]) > len(idents[j]) })
		return idents[0]
	}
	return q
}

func looksLikeIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' {
			continue
		}
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r > 127
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}
