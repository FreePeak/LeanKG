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
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/astgrep"
	"github.com/FreePeak/LeanKG/go/internal/docindex"
	"github.com/FreePeak/LeanKG/go/internal/embed"
	"github.com/FreePeak/LeanKG/go/internal/graph"
	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/lsp"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/ontology"
	"github.com/FreePeak/LeanKG/go/internal/session"
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
	st         store.Backend
	mem        *memory.Memory
	projectDir string
	langsReg   *langs.Registry // lazy language activation for the opened codebase
	lspManager *lsp.Manager    // lazy per-(lang,dir) LSP server pool (query time only)
	embedder   QueryEmbedder   // optional; nil ⇒ L3 degrades with reason
}

// SetLangsRegistry attaches the language registry (lazy activation state) to
// the engine. cmd activates it against the opened codebase at startup.
func (e *Engine) SetLangsRegistry(reg *langs.Registry) { e.langsReg = reg }

// SetProjectDir records the project directory (enable
// import{action:"session"} for offloading bulky tool payloads).
func (e *Engine) SetProjectDir(dir string) { e.projectDir = dir }

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
	case "docs":
		if req.Path == "" {
			return nil, fmt.Errorf("import docs requires path")
		}
		res, err := docindex.IndexDocs(ctx, e.st, req.Path)
		if err != nil {
			return nil, fmt.Errorf("docindex %s: %w", req.Path, err)
		}
		if _, err := e.refreshInventory(); err != nil {
			return nil, err
		}
		return map[string]any{
			"indexed": map[string]any{"files": res.Files, "elements": res.Elements, "skipped": res.Skipped},
		}, nil
	case "repo", "dir":
		if req.Path == "" {
			return nil, fmt.Errorf("import %s requires path", req.Action)
		}
		// Index through the language registry: activation is per target, so
		// importing another codebase re-detects its languages first.
		if e.langsReg != nil {
			if _, aerr := e.langsReg.Activate(req.Path); aerr != nil {
				return nil, fmt.Errorf("language detection: %w", aerr)
			}
		}
		res, err := index.IndexDirWith(ctx, e.st, req.Path, e.langsReg)
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
	case "session":
		return e.sessionWrite(req)
	case "ontology":
		if req.Path == "" {
			return nil, fmt.Errorf("import ontology requires path (concept catalog JSON)")
		}
		return e.OntologyMatch(req.Path)
	case "":
		return nil, fmt.Errorf("import requires action (repo, dir, docs, memory, session, ontology)")
	default:
		return nil, fmt.Errorf("unknown import action %q (valid: repo, dir, docs, memory, session, ontology)", req.Action)
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
		"backend":          e.st.Engine(),
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
	if e.langsReg != nil {
		tiers := e.langsReg.Tiers()
		langsOut := []map[string]any{}
		for _, l := range e.langsReg.Active() {
			entry := map[string]any{"language": string(l)}
			if tt, ok := tiers[l]; ok {
				names := make([]string, len(tt))
				for i, x := range tt {
					names[i] = string(x)
				}
				entry["tiers"] = names
			}
			langsOut = append(langsOut, entry)
		}
		out["languages"] = langsOut
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
	Action string         `json:"action,omitempty"` // "" = ladder router
	Query  string         `json:"query"`
	Limit  int            `json:"limit,omitempty"`
	Args   map[string]any `json:"args,omitempty"` // action params (to, depth, command, lang, pattern…)
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
	case "ontology":
		return e.OntologyMatches()
	case "languages":
		return e.LanguagesStatus(), nil
	case "lsp":
		return e.lspQuery(ctx, req, map[string]any{"query": req.Query})
	case "pattern":
		return e.patternQuery(ctx, req, map[string]any{"query": req.Query})
	case "session":
		return e.SessionRead(argStr(req.Args, "command"), req.Query, argStr(req.Args, "node_id"))
	case "", "search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain":
	default:
		return nil, fmt.Errorf("unknown query action %q (valid: search, exact, fuzzy, semantic, element, impact, path, callers, callees, context, explain, memory, session, ontology; empty = ladder router)", req.Action)
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
	case "impact", "path", "callers", "callees", "context", "explain":
		return e.graphAction(ctx, req, resp)
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

// graphAction routes the connection verbs (Rust graph/query.rs parity) to
// internal/graph; the query string is the seed qualified name.
func (e *Engine) graphAction(ctx context.Context, req QueryRequest, resp map[string]any) (map[string]any, error) {
	g := func() map[string]any {
		resp["action"] = req.Action
		return resp
	}
	switch req.Action {
	case "impact":
		// depth comes from args.depth (Rust --depth parity); limit is a
		// result-count concept, not a traversal depth.
		depth := argInt(req.Args, "depth", 2)
		hits, err := graph.Impact(e.st, req.Query, depth)
		if err != nil {
			return nil, err
		}
		resp["hits"] = hits
		return g(), nil
	case "path":
		if argStr(req.Args, "to") == "" {
			return nil, fmt.Errorf("query path requires args.to (target qualified name)")
		}
		// maxDepth comes from args.depth (default 2) — Limit is a result
		// count, not a traversal bound; paths are a single answer anyway.
		maxDepth := argInt(req.Args, "depth", 0) // 0 = graph default
		path, err := graph.ShortestPath(e.st, req.Query, argStr(req.Args, "to"), maxDepth)
		if err != nil {
			return nil, err
		}
		if path == nil {
			resp["path"] = []string{}
			resp["reachable"] = false
		} else {
			resp["path"] = path
			resp["reachable"] = true
		}
		return g(), nil
	case "callers":
		qns, err := graph.Callers(e.st, req.Query)
		if err != nil {
			return nil, err
		}
		resp["callers"] = qns
		return g(), nil
	case "callees":
		qns, err := graph.Callees(e.st, req.Query)
		if err != nil {
			return nil, err
		}
		resp["callees"] = qns
		return g(), nil
	case "context":
		out, err := graph.Context(e.st, req.Query, req.Limit)
		if err != nil {
			return nil, err
		}
		for k, v := range out {
			resp[k] = v
		}
		return g(), nil
	case "explain":
		out, err := graph.Explain(e.st, req.Query)
		if err != nil {
			return nil, err
		}
		for k, v := range out {
			resp[k] = v
		}
		return g(), nil
	}
	return nil, fmt.Errorf("unreachable graph action %q", req.Action)
}

// sessionWrite/read wire internal/session into the 3-tool surface: bulky tool
// payloads are offloaded to .leankg/sessions/<id>/refs/<node>.md and restored
// bit-for-bit by session_recall (Rust session/mod.rs parity).
func (e *Engine) sessionWrite(req ImportRequest) (map[string]any, error) {
	if e.projectDir == "" {
		return nil, fmt.Errorf("session offload requires a project directory")
	}
	s := session.New(e.projectDir)
	get := func(key string) string {
		s2, _ := req.Args[key].(string)
		return s2
	}
	switch req.Command {
	case "offload":
		payload := get("payload")
		ref, err := s.Offload(get("session_id"), get("node_id"), []byte(payload), get("summary"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"offloaded": ref}, nil
	case "lesson":
		deduped, err := s.AddLesson(get("session_id"), get("text"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"deduped": deduped}, nil
	default:
		return nil, fmt.Errorf("unknown session command %q (valid: offload, lesson)", req.Command)
	}
}

// SessionRead restores offloaded payloads / lists a session canvas.
func (e *Engine) SessionRead(command, sessionID, nodeID string) (map[string]any, error) {
	if e.projectDir == "" {
		return nil, fmt.Errorf("session access requires a project directory")
	}
	s := session.New(e.projectDir)
	switch command {
	case "recall":
		payload, err := s.Recall(sessionID, nodeID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "recall", "node_id": nodeID, "payload": string(payload)}, nil
	case "canvas":
		refs, err := s.Canvas(sessionID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "canvas", "session_id": sessionID, "refs": refs}, nil
	default:
		return nil, fmt.Errorf("unknown session read command %q (valid: recall, canvas)", command)
	}
}

// OntologyMatch loads a concept catalog and matches it against indexed
// elements (internal/ontology); matches are persisted in kv for later reads.
func (e *Engine) OntologyMatch(catalogPath string) (map[string]any, error) {
	cat, err := ontology.LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	matches, err := cat.MatchElements(e.st)
	if err != nil {
		return nil, err
	}
	if err := ontology.SaveMatches(e.st, matches); err != nil {
		return nil, err
	}
	return map[string]any{"concepts": len(cat.Concepts), "matches": matches}, nil
}

// OntologyMatches returns the last persisted match set.
func (e *Engine) OntologyMatches() (map[string]any, error) {
	matches, err := ontology.LoadMatches(e.st)
	if err != nil {
		return nil, err
	}
	return map[string]any{"matches": matches}, nil
}

// LanguagesStatus reports the lazy activation state: which languages are
// turned on for the opened codebase and which extraction/lookup tiers are
// live for each (regex always; tree-sitter only under the tstree tag;
// ast-grep only when the CLI exists; lsp only when a server resolves).
func (e *Engine) LanguagesStatus() map[string]any {
	if e.langsReg == nil {
		return map[string]any{"languages": []any{}, "note": "no registry attached — all languages idle"}
	}
	tiers := e.langsReg.Tiers()
	out := []map[string]any{}
	for _, l := range e.langsReg.Active() {
		entry := map[string]any{"language": string(l)}
		if tt, ok := tiers[l]; ok {
			names := make([]string, len(tt))
			for i, x := range tt {
				names[i] = string(x)
			}
			entry["tiers"] = names
		}
		out = append(out, entry)
	}
	return map[string]any{"languages": out, "codebase": e.langsReg.Codebase()}
}

// patternQuery runs structural AST pattern search via the ast-grep CLI when
// installed (lazy: probed per call, never spawned otherwise). Absence DEGRADES
// to L2 keyword search with a reason — same posture as the L3 provider rules.
func (e *Engine) patternQuery(ctx context.Context, req QueryRequest, resp map[string]any) (map[string]any, error) {
	pattern := argStr(req.Args, "pattern")
	if pattern == "" {
		return nil, fmt.Errorf("query pattern requires args.pattern (AST pattern, e.g. \"func $F($A)\")")
	}
	lang := argStr(req.Args, "lang")
	if lang == "" {
		// default to the sole active language when exactly one is on
		if active := e.activeLanguages(); len(active) == 1 {
			lang = string(active[0])
		} else {
			return nil, fmt.Errorf("query pattern requires args.lang when multiple languages are active")
		}
	}
	runner, err := astgrep.New()
	if err != nil {
		resp["retrieval"] = map[string]any{"rung": "L2", "reason": "ast-grep CLI not installed; degraded to keyword search"}
		return e.rungFuzzy(pattern, req.Limit, resp, false)
	}
	root := e.projectDir
	if root == "" {
		root = "."
	}
	matches, err := runner.RunPattern(ctx, lang, pattern, root, req.Limit)
	if err != nil {
		return nil, err
	}
	resp["hits"] = matches
	resp["retrieval"] = map[string]any{"rung": "ast-grep", "reason": fmt.Sprintf("structural pattern match (%s)", lang)}
	return resp, nil
}

// activeLanguages lists the currently active registry languages.
func (e *Engine) activeLanguages() []langs.Language {
	if e.langsReg == nil {
		return nil
	}
	return e.langsReg.Active()
}

// lspQuery consults the language server for the QUERIED directory at query
// time (user requirement: "check the lsp to the query directory when the user
// or agent queries"). Args: lang (required; must be active), command
// ("workspace"=default | "document"), path (file for document symbols),
// query (symbol search text). The server is spawned lazily for
// (lang, dir), pooled, and idle-evicted by the manager.
func (e *Engine) lspQuery(ctx context.Context, req QueryRequest, resp map[string]any) (map[string]any, error) {
	if e.langsReg == nil {
		return nil, fmt.Errorf("no language registry attached")
	}
	langName := argStr(req.Args, "lang")
	prof, ok := e.langsReg.Lookup(langName)
	if !ok {
		return nil, fmt.Errorf("unknown language %q", langName)
	}
	if !e.langsReg.IsActive(prof.Language) {
		return nil, fmt.Errorf("language %q is not active for this codebase (lazy: nothing spawned)", langName)
	}
	spec := e.langsReg.LSPSpec(prof.Language)
	if spec == nil {
		return nil, fmt.Errorf("language %q has no LSP tier", langName)
	}
	if e.lspManager == nil {
		e.lspManager = lsp.NewManager(60 * time.Second)
	}
	dir := e.projectDir
	if dir == "" {
		dir = "."
	}
	client, err := e.lspManager.Get(ctx, prof.Language, spec, dir)
	if err != nil {
		return nil, err
	}
	command := "workspace"
	if c := argStr(req.Args, "command"); c != "" {
		command = c
	}
	switch command {
	case "document":
		path := argStr(req.Args, "path")
		if path == "" {
			return nil, fmt.Errorf("lsp document requires args.path (file to inspect)")
		}
		syms, err := client.DocumentSymbols(ctx, path)
		if err != nil {
			return nil, err
		}
		resp["symbols"] = syms
		resp["server"] = client.Language()
	case "workspace", "":
		// Real servers (gopls) index lazily after initialize — an immediate
		// workspace/symbol returns empty. Poll briefly while ctx allows so
		// the tier actually answers instead of always returning null.
		var syms []lsp.Symbol
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			syms, err = client.WorkspaceSymbols(ctx, req.Query)
			if err != nil {
				return nil, err
			}
			if len(syms) > 0 {
				break
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(700 * time.Millisecond):
			}
		}
		resp["symbols"] = syms
		resp["server"] = client.Language()
	default:
		return nil, fmt.Errorf("unknown lsp command %q (valid: workspace, document)", command)
	}
	resp["retrieval"] = map[string]any{"rung": "lsp", "reason": fmt.Sprintf("language server symbols (%s)", langName)}
	return resp, nil
}

// argStr reads a string arg ("" when absent).
func argStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// argInt reads a numeric arg (fallback when absent/unparseable).
func argInt(m map[string]any, key string, fb int) int {
	if m == nil {
		return fb
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fb
}
