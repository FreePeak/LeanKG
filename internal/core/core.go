// Package core implements the 3-tool surface (import / query / status) and
// the L0–L3 query ladder for the Go engine. Transports (MCP, REST) are thin
// adapters over this package; the tool envelope resolves HERE before any
// gate — a read-named envelope can never smuggle a write action (the
// security property carried over from the Rust engine).
package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/astgrep"
	"github.com/FreePeak/LeanKG/internal/compress"
	"github.com/FreePeak/LeanKG/internal/docindex"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/lsp"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/ontology"
	"github.com/FreePeak/LeanKG/internal/orgknowledge"
	"github.com/FreePeak/LeanKG/internal/prdindex"
	"github.com/FreePeak/LeanKG/internal/session"
	"github.com/FreePeak/LeanKG/internal/store"
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
	// Stamp returns the FULL collection identity this embedder writes with:
	// model, revision, dimensions, distance, provider, chunker version and the
	// query/document prefix pair (issue #279). L3 compares it whole against the
	// stored stamp, so a chunker or prefix drift degrades with the rebuild hint
	// instead of mixing vector spaces.
	Stamp() store.ModelStamp
}

// Engine is the core service over one project's store.
type Engine struct {
	st         store.Backend
	mem        *memory.Memory
	projectDir string
	langsReg   *langs.Registry            // lazy language activation for the opened codebase
	lspManager *lsp.Manager               // lazy per-(lang,dir) LSP server pool (query time only)
	embedder   QueryEmbedder              // optional; nil ⇒ L3 degrades with reason
	compressor *compress.LeanKGCompressor // context compression (reader/cmd/response paths, Rust parity)
}

// SetLangsRegistry attaches the language registry (lazy activation state) to
// the engine. cmd activates it against the opened codebase at startup.
func (e *Engine) SetLangsRegistry(reg *langs.Registry) { e.langsReg = reg }

// SetProjectDir records the project directory (enable
// import{action:"session"} for offloading bulky tool payloads).
func (e *Engine) SetProjectDir(dir string) { e.projectDir = dir }

// SetEmbedder wires the query-time embedder after construction. The CLI's
// one-shot verbs build an engine before they know whether a provider endpoint
// is configured; serve passes it to New directly.
func (e *Engine) SetEmbedder(em QueryEmbedder) { e.embedder = em }

// ProjectDir reports the project directory the engine is bound to (the metric
// ledger records it as each row's project_path).
func (e *Engine) ProjectDir() string { return e.projectDir }

// New builds an Engine. mem may be nil (memory actions then error).
func New(st store.Backend, mem *memory.Memory, embedder QueryEmbedder) *Engine {
	return &Engine{st: st, mem: mem, embedder: embedder, compressor: compress.New()}
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

// Stamp exposes the provider's full collection identity (issue #279).
func (a providerEmbedder) Stamp() store.ModelStamp { return embed.StampOf(a.p) }

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
	return "", errs.NewError(errs.UnknownTool,
		fmt.Sprintf("tool %q is not in this server's registry — valid tools: import, query, status (legacy aliases: set, get)", tool), "")
}

// freshness derives the status/query freshness label; the derivation itself is
// the store's, shared with `doctor --deep`'s fleet probe.
func (e *Engine) freshness(totalElements int) string {
	return store.Freshness(e.st, totalElements)
}

// ImportRequest is the import tool payload. The flat curation fields mirror
// the advertised MCP input schema (content/old/new/text/file/payload/summary/
// session_id/node_id/insert_line): an agent that follows the schema sends them
// at the top level, while the same fields nested under args also work.
type ImportRequest struct {
	Action string `json:"action"`         // repo | dir | memory
	Path   string `json:"path,omitempty"` // repo/dir target
	// Memory write commands ride action="memory".
	Command   string         `json:"command,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Content   string         `json:"content,omitempty"`
	Old       string         `json:"old,omitempty"`
	New       string         `json:"new,omitempty"`
	Text      string         `json:"text,omitempty"`
	File      string         `json:"file,omitempty"`
	Payload   string         `json:"payload,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	SessionID string         `json:"session_id,omitempty"`
	NodeID    string         `json:"node_id,omitempty"`
	InsertAt  *int           `json:"insert_line,omitempty"`
}

// withFlatArgs folds the top-level curation fields into Args so one lookup
// path serves both call shapes. Anything already nested under args wins, so an
// explicit args key is never overridden by the flat one. Before this, a
// schema-shaped call (content at the top level) reported ok:true and wrote an
// EMPTY file — the agent's memory silently lost the write.
//
// The TARGET file is folded under BOTH names, and that is not a convenience.
// The import schema advertises the target twice — `path` for
// create/str_replace/insert/delete/rename, `file` for add/replace/remove — and
// the two command families read opposite fields. So an agent that used the
// other one got `memory: invalid memory path: empty path` for a file it had
// named, which reads like an engine bug rather than a field-name mismatch. One
// lookup, either name: the schema can name the target either way and the engine
// finds it.
func (r ImportRequest) withFlatArgs() ImportRequest {
	flat := map[string]any{}
	for k, v := range map[string]string{
		"content": r.Content, "old": r.Old, "new": r.New, "text": r.Text,
		"file": r.File, "payload": r.Payload, "summary": r.Summary,
		"session_id": r.SessionID, "node_id": r.NodeID,
	} {
		if v != "" {
			flat[k] = v
		}
	}
	// Path fills whichever name the command family reads, without overwriting a
	// field the caller actually set: `file` is the schema's name for the
	// add/replace/remove target, `path` for the create/str_replace family.
	// Filling both when only one was sent is what makes either spelling work.
	if r.Path != "" {
		if _, ok := flat["file"]; !ok {
			flat["file"] = r.Path
		}
		flat["path"] = r.Path
	}
	if r.File != "" {
		if _, ok := flat["path"]; !ok {
			flat["path"] = r.File
		}
	}
	if r.InsertAt != nil {
		flat["insert_line"] = *r.InsertAt
	}
	if len(flat) == 0 {
		return r
	}
	merged := make(map[string]any, len(r.Args)+len(flat))
	for k, v := range flat {
		merged[k] = v
	}
	for k, v := range r.Args {
		merged[k] = v
	}
	r.Args = merged
	return r
}

// Import handles the import tool: repo/dir indexing or memory curation writes.
// The flat curation fields are folded into Args first, so a schema-shaped call
// (content at the top level) and an args-shaped call behave identically.
func (e *Engine) Import(ctx context.Context, req ImportRequest) (map[string]any, error) {
	req = req.withFlatArgs()
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
	case "prd":
		if req.Path == "" {
			return nil, fmt.Errorf("import prd requires path (a PRD markdown document)")
		}
		env := argStr(req.Args, "environment")
		if env == "" {
			env = "local"
		}
		res, err := prdindex.IndexDocument(ctx, e.st, e.projectDir, req.Path, env)
		if err != nil {
			return nil, err
		}
		if _, err := e.refreshInventory(); err != nil {
			return nil, err
		}
		return map[string]any{"prd": res}, nil
	case "repo", "dir":
		if req.Path == "" {
			return nil, fmt.Errorf("import %s requires path", req.Action)
		}
		target, terr := e.resolveIndexTarget(req.Path)
		if terr != nil {
			return nil, terr
		}
		// Index through the language registry: activation is per target, so
		// importing another codebase re-detects its languages first.
		if e.langsReg != nil {
			if _, aerr := e.langsReg.Activate(target); aerr != nil {
				return nil, fmt.Errorf("language detection: %w", aerr)
			}
		}
		res, err := index.IndexDirWith(ctx, e.st, target, e.langsReg)
		if err != nil {
			return nil, fmt.Errorf("index %s: %w", target, err)
		}
		if _, err := e.refreshInventory(); err != nil {
			return nil, err
		}
		status, _ := e.Status(ctx)
		indexed := map[string]any{
			"files":         res.Files,
			"elements":      res.Elements,
			"relationships": res.Relationships,
			"skipped":       res.Skipped,
		}
		// Name the reconcile's deletions: on an unchanged tree any nonzero count
		// means the walk root did not cover the store's paths (see DeletedFiles).
		if res.DeletedFiles > 0 {
			indexed["deleted_files"] = res.DeletedFiles
		}
		return map[string]any{"indexed": indexed, "status": status}, nil
	case "memory":
		return e.memoryWrite(req)
	case "session":
		return e.sessionWrite(req)
	case "read":
		return e.compressRead(req)
	case "ontology":
		if req.Path == "" {
			return nil, fmt.Errorf("import ontology requires path (ontology dir or concept catalog JSON)")
		}
		if info, err := os.Stat(req.Path); err == nil && info.IsDir() {
			stats, err := ontology.LoadWorkflows(e.st, req.Path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"ontology": "synced", "stats": stats}, nil
		}
		return e.OntologyMatch(req.Path)
	case "":
		return nil, fmt.Errorf("import requires action (repo, dir, docs, prd, memory, session, ontology, read)")
	default:
		return nil, fmt.Errorf("unknown import action %q (valid: repo, dir, docs, memory, session, ontology, prd)", req.Action)
	}
}

// refreshInventory recomputes and persists the inventory snapshot. The logic
// lives in the store package (store.RefreshInventory) so the CLI writer verbs
// keep the same bookkeeping — an index that only the Engine refreshed left
// readers reporting possibly_stale forever.
func (e *Engine) refreshInventory() (store.Inventory, error) {
	return store.RefreshInventory(e.st)
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
	// Space is a no-write probe (PRAGMA page_count/freelist_count on SQLite,
	// pg_database_size on Postgres), so it is safe on a read-only handle and
	// costs three rows. Reporting it is what makes disk bloat visible to an
	// agent: docs/mcp-tool-contract.md advertises "watch/vacuum status" on
	// this tool, and without a payload field there is nothing to see.
	if space, serr := e.st.Space(); serr == nil {
		out["space"] = map[string]any{
			"size_bytes":     space.SizeBytes,
			"free_bytes":     space.FreeBytes,
			"live_bytes":     space.LiveBytes,
			"wal_bytes":      space.WALBytes,
			"bloat_fraction": space.BloatFraction(),
		}
	}
	// Surface the resolved project so agents in a worktree/subdir can see
	// which tree the server is actually answering for.
	if e.projectDir != "" {
		out["project_dir"] = e.projectDir
	}
	// FR-HEA-03: mega-graph full-scan advisory banner. Below the
	// LEANKG_MAX_CACHE_ELEMENTS cap (default 50k) the map is empty, so normal
	// graphs keep byte-identical status payloads.
	for k, v := range ontology.MegaGraphBanner(els) {
		out[k] = v
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
// rung. Index-backed answers (ladder rungs + graph verbs) carry
// retrieval{rung,reason} + freshness; memory/ontology/portfolio reads answer
// with their own payload shape (command/banks/count) and do not.
// "memory" additionally dispatches on args.command (session_recall, memories).
func (e *Engine) Query(ctx context.Context, req QueryRequest) (map[string]any, error) {
	switch req.Action {
	case "memory":
		switch cmd := argStr(req.Args, "command"); cmd {
		case "":
			// No command: the documented memory search over the
			// full-markdown memory files (command=search is the same call).
			return e.MemoryRead("search", "", req.Query, req.Limit)
		case "session_recall", "memories":
			return e.SessionMemoryRead(cmd, req.Query, req.Limit, req.Args)
		case "search":
			return e.MemoryRead("search", "", req.Query, req.Limit)
		default:
			// A command that does not exist must say so. Falling through to
			// the search answered a typo ("recal") as a confident empty hit
			// list, which reads to an agent as "the recall found nothing"
			// rather than "that command is not a command".
			return nil, fmt.Errorf("unknown memory command %q (valid: search, session_recall, memories)", cmd)
		}
	case "ontology":
		cmd := argStr(req.Args, "cmd")
		// FR-HEA-03: the four full-table cmds (trace/status/feature_flow/
		// traceability) reach Elements() wholesale; on a mega-graph they are
		// refused with the paginated escape hatch named, exactly as Rust's
		// handler returned Ok(refusal). concept_search/matches stay unguarded
		// (KV read / the paginated path itself).
		if ontology.IsFullScanOntologyCmd(cmd) {
			if refusal, err := ontology.RefuseFullScanIfMega(e.st, "ontology/"+cmd); err != nil {
				return nil, err
			} else if refusal != nil {
				return refusal, nil
			}
		}
		switch cmd {
		case "", "matches":
			return e.OntologyMatches()
		case "trace":
			return ontology.TraceQuery(e.st, req.Query)
		case "status":
			st, err := ontology.OntologyStatus(e.st)
			if err != nil {
				return nil, err
			}
			return map[string]any{"status": st}, nil
		case "concept_search":
			res, err := ontology.ConceptSearch(e.st, req.Query, req.Limit)
			if err != nil {
				return nil, err
			}
			return map[string]any{"result": res}, nil
		case "feature_flow":
			return ontology.FeatureFlow(e.st, req.Query)
		case "traceability":
			m, err := ontology.TraceabilityMatrix(e.st)
			if err != nil {
				return nil, err
			}
			return map[string]any{"matrix": m}, nil
		default:
			return nil, fmt.Errorf("unknown ontology cmd (valid: matches, trace, status, concept_search, feature_flow, traceability)")
		}
	case "compress":
		return e.compressRun(req)
	case "languages":
		return e.LanguagesStatus(), nil
	case "lsp":
		return e.lspQuery(ctx, req, map[string]any{"query": req.Query})
	case "pattern":
		return e.patternQuery(ctx, req, map[string]any{"query": req.Query})
	case "session":
		return e.SessionRead(argStr(req.Args, "command"), req.Query, argStr(req.Args, "node_id"))
	case "prd":
		rows, err := prdindex.Trace(e.st, req.Query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"requirements": rows, "count": len(rows)}, nil
	case "incidents":
		k := orgknowledge.New(e.st)
		incidents, err := k.QueryIncidents(argStr(req.Args, "service"), argStr(req.Args, "pattern"), argStr(req.Args, "env"), req.Limit)
		if err != nil {
			return nil, err
		}
		if incidents == nil {
			incidents = []store.Incident{}
		}
		return map[string]any{"incidents": incidents, "query": map[string]any{
			"service": argStr(req.Args, "service"), "pattern": argStr(req.Args, "pattern"),
			"env": argStr(req.Args, "env"), "limit": req.Limit,
		}}, nil
	case "env_conflicts":
		conflicts, err := orgknowledge.New(e.st).FindEnvConflicts(argStr(req.Args, "service"))
		if err != nil {
			return nil, err
		}
		return map[string]any{"conflicts": conflicts, "service": argStr(req.Args, "service")}, nil
	case "service_context":
		return orgknowledge.New(e.st).ServiceContextJSON(argStr(req.Args, "service"), argStr(req.Args, "env"))
	case "portfolio":
		// Issue #376: the fleet read. The registry, the hot-set cap, the merge
		// and the T0 manifest live in internal/portfolioreg; portfolio.go (this
		// package) runs the REAL ladder inside each hot project.
		return e.portfolioQuery(ctx, req)
	case "", "search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain":
	default:
		return nil, errs.NewError(errs.UnknownAction,
			fmt.Sprintf("query action %q (valid: search, exact, fuzzy, semantic, element, impact, path, callers, callees, context, explain, memory, session, ontology, prd, incidents, env_conflicts, service_context, portfolio; empty = ladder router)", req.Action), "")
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query requires query text")
	}
	// Validate the fusion weight HERE, beside the rest of the argument
	// contract, for two reasons the L3 path alone could not give: a typo must be
	// an error even when the store has no vectors (L3 degrades to L2 without ever
	// reading a weight, so validating inside rungSemantic would silently accept
	// it), and an invalid weight must fail the same way an invalid scope does.
	if _, err := parseFusionWeight(argStr(req.Args, "weight")); err != nil {
		return nil, err
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
		return e.rungSemantic(ctx, req.Query, limit, resp, true, argStr(req.Args, "scope"), argStr(req.Args, "weight"))
	case "impact", "path", "callers", "callees", "context", "explain":
		return e.graphAction(ctx, req, resp)
	}

	// Ladder router: L0 cold → L1 exact → L3 semantic → L2 fuzzy.
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
	// Noul L3 gate: ontology confidence below MinConfidence
	// (0.3) justifies no embedding call — degrade to L2.
	conf, _ := ontology.OntologyConfidence(e.st, req.Query)
	if conf > 0 && conf < ontology.MinConfidence {
		return withEmptyHint(e.rungFuzzy(req.Query, limit, resp, true))
	}
	// The vector rung is tried BEFORE the keyword rung, which is the order
	// D-2026-09-04-4 states (L3 with vectors, L2 without): the whole point of
	// the semantic tier is to outrank keyword noise, and returning as soon as
	// L2 produced any hit made it unreachable — an intent question was answered
	// with whichever elements happened to contain its words. Live on this repo
	// before the fix: "how are search results ranked and fused" returned three
	// archive-report headings; L3 on the same query ranked the function that
	// implements the answer first.
	//
	// No capability probe is needed: rungSemantic degrades to L2 itself when
	// the collection is absent, the stamp drifted, or the provider is
	// unreachable, each with its own reason. pin=true so that reason survives.
	return withEmptyHint(e.rungSemantic(ctx, req.Query, limit, resp, true, argStr(req.Args, "scope"), argStr(req.Args, "weight")))
}

func hitsOf(resp map[string]any) []map[string]any {
	h, _ := resp["hits"].([]map[string]any)
	return h
}

// withEmptyHint attaches a next-step guidance string when a pinned or final
// rung returned zero hits (FR-HEA-02). Existing guidance is left alone.
func withEmptyHint(resp map[string]any, err error) (map[string]any, error) {
	if err != nil || resp == nil {
		return resp, err
	}
	if _, ok := resp["guidance"]; ok {
		return resp, nil
	}
	if len(hitsOf(resp)) > 0 {
		return resp, nil
	}
	rung, reason := "", ""
	if retr, ok := resp["retrieval"].(map[string]any); ok {
		rung, _ = retr["rung"].(string)
		reason, _ = retr["reason"].(string)
	}
	switch rung {
	case "L1":
		resp["guidance"] = "No exact identifier match. Retry with action empty (ladder) or action=fuzzy; prefer a short symbol name over a sentence."
	case "L2":
		if strings.Contains(reason, "degraded from L3") || strings.Contains(reason, "no embedding") || strings.Contains(reason, "no vector") || strings.Contains(reason, "stamp mismatch") {
			resp["guidance"] = "Keyword rung empty after L3 degrade (" + reason + "). Run `leankg-embed run` (or `leankg-embed full` on stamp drift), then retry; or rephrase as a short identifier."
		} else {
			resp["guidance"] = "No keyword match. Rephrase as a short identifier, try action=semantic after `leankg-embed run`, or status to confirm the store is fresh and non-empty."
		}
	case "L3":
		resp["guidance"] = "Semantic rung returned no hits. Try a shorter identifier (action empty or exact/fuzzy), or run `leankg-embed run` if embeddings are stale/missing (see status.embeddings)."
	default:
		resp["guidance"] = "No hits. Try a shorter identifier, leave action empty for the ladder, or call status to check freshness and embedding coverage."
	}
	return resp, nil
}

// graphEmptyGuidance steers agents when a graph verb returns an empty list —
// usually an unresolved seed QN rather than a true leaf.
func graphEmptyGuidance(verb, seed string) string {
	return "No " + verb + " for " + seed + ". Resolve the seed with query (action empty) to a qualified_name, then retry " + verb + " with that exact QN."
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
	if pin {
		return withEmptyHint(resp, nil)
	}
	return resp, nil
}

// rungFuzzy is L2, the keyword rung. sqlite ranks it with FTS5/bm25; the
// PostgreSQL backend ranks its migration-012 tsvector with ts_rank and degrades
// through pg_trgm similarity to ILIKE substring recall (issue #273). The type
// assertion is the only engine branch: *Store does not implement
// store.FTSBackend, so the SQLite call is unchanged in source and behavior.
func (e *Engine) rungFuzzy(q string, limit int, resp map[string]any, pin bool) (map[string]any, error) {
	matches, err := e.st.FindFuzzy(q, limit)
	reason := "FTS5 keyword match"
	if fts, ok := e.st.(store.FTSBackend); ok {
		var arm string
		matches, arm, err = fts.SearchElementsFTS(q, limit)
		switch arm {
		case store.ArmTrigram:
			reason = "trigram keyword match (tsvector degraded)"
		case store.ArmILIKE:
			reason = "substring keyword match (tsvector+trigram degraded)"
		default:
			reason = "tsvector keyword match"
		}
	}
	if err != nil {
		return nil, err
	}
	if pin {
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
	if pin {
		return withEmptyHint(resp, nil)
	}
	return resp, nil
}

// rungSemantic is L3: embed the query via the wired provider and rank the
// collection by FUSING the vector arm with the keyword arm (reciprocal rank
// fusion, the same combination the PostgreSQL path already runs as
// HybridSearch). Provider failure or absent wiring DEGRADES to L2 — never a
// hard error (issue #368 AC: query-time provider failure degrades the ladder
// with retrieval.reason).
//
// Fusing rather than picking one arm is the point. Live on this repository
// before the fix, a question whose answer is CODE ("where is the single flight
// lock taken") was answered by the vector arm with three archived run reports
// and by the keyword arm with cmd/leankg-embed/main.go::lock — the element
// that IS the answer. The two arms are strong on different corpora: vectors
// carry phrasing similarity into prose, keywords carry the identifier out of
// a code symbol. Neither ordering is right, and whichever rung won outright
// lost half the corpus. RRF is scale-free, so both survive with their own
// weights and the disagreement between them becomes visible in the per-arm
// ranks each hit carries.
func (e *Engine) rungSemantic(ctx context.Context, q string, limit int, resp map[string]any, pin bool, argz ...string) (map[string]any, error) {
	// argz carries the caller's optional query args (scope, weight); variadic so
	// the pinned-rung and ladder call sites stay one call each.
	scope, weight := "", ""
	if len(argz) > 0 {
		scope = argz[0]
	}
	if len(argz) > 1 {
		weight = argz[1]
	}
	degrade := func(reason string) (map[string]any, error) {
		if pin {
			resp["retrieval"] = map[string]any{"rung": "L2", "reason": reason}
		}
		// pin=false so rungFuzzy does not overwrite the L3-degrade reason;
		// withEmptyHint still attaches next-step guidance on a zero-hit result.
		return withEmptyHint(e.rungFuzzy(q, limit, resp, false))
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
	if want := e.embedder.Stamp(); *stamp != want {
		// Whole-identity comparison (issue #279): a chunker or prefix change is
		// the same class of drift as a model change — mixed vector spaces must
		// never be served, and the hint names the fix.
		return degrade(fmt.Sprintf("stamp mismatch: collection differs from the live provider (%s); degraded from L3; run `leankg-embed full` to rebuild",
			stampDriftForLog(*stamp, want)))
	}
	qvec, err := e.embedder.EmbedQuery(ctx, q)
	if err != nil {
		return degrade(fmt.Sprintf("embedding provider failed (%v); degraded from L3", err))
	}

	// The scope is the caller's decision about which population to rank, and
	// it applies to BOTH arms: scoping only the vector arm would leave the
	// keyword arm free to reintroduce the test fixture the caller excluded,
	// which is the answer they asked not to see. See scopeFilter for the
	// measured motivation (rank 54 -> 7 on this repository's own store).
	keep, scopeName, err := scopeFilter(e.st, scope)
	if err != nil {
		return nil, err
	}
	fw, err := parseFusionWeight(weight)
	if err != nil {
		return nil, err
	}

	// The keyword arm is FUSED, not consulted afterwards. It comes from
	// FindFuzzy — the store's own L2 rung — rather than the FTSBackend's
	kw, err := e.findFuzzyScoped(q, limit, keep)
	if err != nil {
		return degrade(fmt.Sprintf("keyword arm failed (%v); degraded from L3", err))
	}
	vectors, err := e.st.SearchVectorsScoped(modelID, qvec, limit, keep)
	if err != nil {
		return degrade(fmt.Sprintf("vector search failed (%v); degraded from L3", err))
	}
	// The NAME arm, third and weakest: it reads symbol names only, so it reaches
	// a helper whose body says nothing useful for the question — which is where
	// the other two arms are weakest (measured on this repository's own store:
	// `truncateRunes` ranks 755 and `coverage` 1776 over production code alone,
	// while 18 of the 30 labelled questions contain a token of the answer's own
	// symbol name). Empty for a prose question by construction, so a normal
	// question is unaffected.
	names, err := e.findNameScoped(q, limit, keep)
	if err != nil {
		return degrade(fmt.Sprintf("name arm failed (%v); degraded from L3", err))
	}
	fused, elements, ranks, scores := fuseQueryRanks(vectors, kw, names, fw, limit)
	if len(fused) == 0 {
		return withEmptyHint(e.rungFuzzy(q, limit, resp, true))
	}
	out := make([]map[string]any, 0, len(fused))
	for _, qn := range fused {
		m := shapeElement(elements[qn])
		m["score"] = scores[qn]
		if r, ok := ranks[qn]; ok {
			m["ranks"] = r
		}
		if sim, ok := vectorSim(vectors, qn); ok {
			m["similarity"] = sim
		}
		out = append(out, m)
	}
	resp["hits"] = out
	arms := "vector"
	if fw.keyword > 0 && len(kw) > 0 {
		arms += "+keyword"
	}
	if len(names) > 0 {
		arms += "+name"
	}
	retrieval := map[string]any{"rung": "L3", "reason": "reciprocal-rank fusion " + arms}
	if scopeName != "" {
		// A scoped answer must SAY it is scoped: an agent that asked for
		// production code and got a narrower corpus should be able to see that
		// from the answer alone, not infer it.
		retrieval["scope"] = scopeName
	}
	if fw.raw != "" {
		// A weighted answer must say so, for the same reason scope does: an
		// agent reading the ranking needs to know it was not the default.
		retrieval["weight"] = fw.raw
	}
	resp["retrieval"] = retrieval
	return withEmptyHint(resp, nil)
}

// scopeFilter resolves the query's `args.scope` into an element predicate, and
// the name to report it under. "" (the default) is nil — the full corpus, which
// is what every existing caller and every existing answer sees.
//
// The values are the populations a caller actually wants to separate, measured
// on this repository's own store: 9,306 vectors of which 1,879 are test
// fixtures and 4,148 are documentation, and ranking "reciprocal rank fusion of
// ranked lists" puts internal/store/pg_fts.go::FuseRRF at rank 54 over the whole
// corpus and rank 7 over code. `code` is production source; `prod` is `code`
// plus current (non-archived) docs; `all` is the full corpus, spelled out so an
// agent can undo a narrower scope it inherited.
//
// An unknown scope is an error rather than a silent full corpus: answering over
// everything when the caller asked for a subset looks exactly like the scope
// working, and the difference is invisible in the answer.
func scopeFilter(_ store.Backend, scope string) (func(store.Element) bool, string, error) {
	switch scope {
	case "":
		return nil, "", nil
	case "all":
		return nil, "all", nil
	case "code", "src":
		return func(el store.Element) bool { return isProductionCode(el.FilePath) }, "code", nil
	case "prod", "production":
		return func(el store.Element) bool {
			return isProductionCode(el.FilePath) || isCurrentDoc(el.FilePath)
		}, "prod", nil
	default:
		return nil, "", fmt.Errorf("unknown query scope %q (valid: code, prod, all)", scope)
	}
}

// isProductionCode reports whether a path is source the project ships rather
// than a test or a fixture. A test file is a _test.go sibling or anything under
// a testdata/ directory — the convention every language in this repo's registry
// already follows for its own fixtures.
func isProductionCode(path string) bool {
	if strings.Contains(path, "testdata/") || strings.Contains(path, "/test/") {
		return false
	}
	base := path
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		base = path[i+1:]
	}
	for _, suffix := range []string{
		"_test.go", "_test.py", ".test.ts", ".test.tsx", ".test.js", ".spec.ts",
		"_test.rb", "_spec.rb", "Test.java", "Tests.cs",
	} {
		if strings.HasSuffix(base, suffix) {
			return false
		}
	}
	return true
}

// isCurrentDoc reports whether a documentation path is current rather than
// superseded: docs/archive/ is real content and stays in the corpus, but
// `prod` excludes it because an archive describes a state the project left.
func isCurrentDoc(path string) bool {
	return strings.HasPrefix(path, "docs/") && !strings.HasPrefix(path, "docs/archive/")
}

// fusionWeight is the parsed form of `args.weight`: the two fusion weights the
// caller asked for, plus the raw text so the answer can report it back.
type fusionWeight struct {
	vector, keyword float64
	raw             string
	ok              bool
}

// parseFusionWeight reads `"<vector>,<keyword>"` — the same convention
// `scripts/retrieval-policy-sweep.py` prints, so a number measured offline can
// be pasted in without translation. Unset is exactly today's behaviour, (1,1).
//
// Why an escape hatch and not a better default. Waves 12 and 13 measured the
// trade over four cells (two corpora, two label protocols) and found no single
// weighting that is right for all of them:
//
//	                        corpus1/behaviour  corpus1/doc  corpus2/behaviour  corpus2/doc
//	(1,1) — the default                  16/137       49/84           13/94        92/102
//	(3,1)                               30/137       48/84           21/94        88/102
//	keyword arm dropped (1,0)           59/137       40/84           55/94        84/102
//
// A keyword arm that cannot corroborate contributes nothing measurable to the
// fused order — wave 13's `agree_gate` was byte-identical to vector-only in
// every cell — so on behaviour-shaped questions, which name a concept and
// contain none of the document's words, dropping it is right; on keyword-shaped
// questions it is the arm carrying the answer. Which population a deployment
// serves is a product decision no query reveals, so the lever is the same shape
// as `args.scope`: named, optional, reported back, inert when absent.
//
// Everything but a well-formed non-negative pair is an ERROR. A tuning knob
// that silently falls back on a typo is the class of defect waves 1-11 of this
// loop exist to remove — an agent would believe it asked for (3,1) and be
// served (1,1) with no way to tell.
//
// ponytail: a two-number string, not a nested object. The whole surface is
// "how much do you trust each arm", and a flat "<vector>,<keyword>" is
// unambiguous, trivially reportable, and copy-pasteable out of the bench. It
// grows into an object the day a third arm needs its own knob, which is the day
// a caller has three different questions to ask.
func parseFusionWeight(raw string) (fusionWeight, error) {
	if strings.TrimSpace(raw) == "" {
		// Unset is not an error and not an override: it is today's behaviour.
		return fusionWeight{vector: 1, keyword: 1, ok: true}, nil
	}
	parts := strings.Split(strings.TrimSpace(raw), ",")
	if len(parts) != 2 {
		return fusionWeight{}, fmt.Errorf("query weight %q must be \"<vector>,<keyword>\" (e.g. \"3,1\" or \"1,0\" to drop the keyword arm)", raw)
	}
	nums := make([]float64, 2)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return fusionWeight{}, fmt.Errorf("query weight %q: %q is not a non-negative number", raw, strings.TrimSpace(p))
		}
		nums[i] = v
	}
	if nums[0] == 0 && nums[1] == 0 {
		return fusionWeight{}, fmt.Errorf("query weight %q cannot be \"0,0\": both arms off ranks nothing", raw)
	}
	return fusionWeight{vector: nums[0], keyword: nums[1], raw: strings.TrimSpace(raw), ok: true}, nil
}

// nameArmWeight is the name arm's scale in the L3 fusion.
//
// ponytail: 0.6, chosen because the arm fires on a SUBSTRING of a symbol name,
// which is a much weaker signal than a body that discusses the question or a
// keyword that matches it — and because the failure it must not cause is
// visible: a question mentioning "Fuse" would otherwise pull every
// FuseRRFWeighted / FuseRRFWeights / fuseQueryRanks element to the same rank-1
// score and lose the fusion's ability to break that tie on evidence. One
// number, one place; the sweep in scripts/retrieval-bench.py --sweep is the
// gate if it ever needs revisiting.
const nameArmWeight = 0.6

// findNameScoped is the name arm with the same element filter the other two arms
// got — scoping only the vector arm would let the other arms reintroduce exactly
// the fixture the caller excluded.
func (e *Engine) findNameScoped(q string, limit int, keep func(store.Element) bool) ([]store.FuzzyMatch, error) {
	matches, err := e.st.FindByNameToken(q, limit)
	if err != nil || keep == nil {
		return matches, err
	}
	out := matches[:0]
	for _, m := range matches {
		if keep(m.Element) {
			out = append(out, m)
		}
	}
	return out, nil
}

// findFuzzyScoped is the keyword arm with the same element filter the vector arm
// got. The filter is applied AFTER FindFuzzy, so a scoped caller that filters
// aggressively can see fewer than `limit` keyword hits even when more matched —
// the same ceiling the scoped vector search documents.
func (e *Engine) findFuzzyScoped(q string, limit int, keep func(store.Element) bool) ([]store.FuzzyMatch, error) {
	matches, err := e.st.FindFuzzy(q, limit)
	if err != nil || keep == nil {
		return matches, err
	}
	out := matches[:0]
	for _, m := range matches {
		if keep(m.Element) {
			out = append(out, m)
		}
	}
	return out, nil
}

// vectorSim finds a vector arm's similarity for one qualified name.
func vectorSim(vectors []store.VectorSearchHit, qn string) (float64, bool) {
	for _, v := range vectors {
		if v.Element.QualifiedName == qn {
			return v.Similarity, true
		}
	}
	return 0, false
}

// fuseQueryRanks reciprocal-rank fuses the arms into one key order and returns,
// per key, the element, the per-arm ranks and the fused score. The element maps
// are keyed by qualified name so the caller never re-queries the store; a key
// only one arm returned still appears, carrying that arm's rank.
//
// The name arm is weighted BELOW the other two (nameArmWeight): a query token
// appearing inside a symbol name is weaker evidence than a body that discusses
// the question, and an unweighted arm would let "Fuse" drag in every symbol whose
// name contains it.
func fuseQueryRanks(vectors []store.VectorSearchHit, kw, names []store.FuzzyMatch, fw fusionWeight, limit int) ([]string, map[string]store.Element, map[string]map[string]int, map[string]float64) {
	lists := make([]store.RankList, 0, 3)
	elements := map[string]store.Element{}
	ranks := map[string]map[string]int{}
	scores := map[string]float64{}
	if len(vectors) > 0 && fw.vector > 0 {
		keys := make([]string, 0, len(vectors))
		for _, v := range vectors {
			keys = append(keys, v.Element.QualifiedName)
			elements[v.Element.QualifiedName] = v.Element
		}
		lists = append(lists, store.RankList{Name: store.ArmVector, Keys: keys})
	}
	if fw.vector == 0 {
		for _, v := range vectors {
			elements[v.Element.QualifiedName] = v.Element
		}
	}
	// A keyword weight of ZERO drops the arm. It cannot be expressed as a
	// 0-valued weight: store's armWeight treats a 0 field as "unset" and
	// substitutes 1.0, so "1,0" would silently become "1,1" — the exact
	// silent fallback this hatch exists to prevent. Wave 13 measured that
	// dropping the arm is the whole win (59/137 and 55/94 on behaviour labels),
	// so the intent is honoured where it is known.
	if len(kw) > 0 && fw.keyword > 0 {
		keys := make([]string, 0, len(kw))
		for _, m := range kw {
			keys = append(keys, m.Element.QualifiedName)
			elements[m.Element.QualifiedName] = m.Element
		}
		lists = append(lists, store.RankList{Name: store.ArmTSVector, Keys: keys})
	}
	if fw.keyword == 0 && len(kw) > 0 {
		// A dropped arm's documents are still reportable (an agent asked to
		// rank without the keyword arm may still want to see what it excluded),
		// but they must not be fused or ranked.
		for _, m := range kw {
			elements[m.Element.QualifiedName] = m.Element
		}
	}
	if len(names) > 0 {
		keys := make([]string, 0, len(names))
		for _, m := range names {
			keys = append(keys, m.Element.QualifiedName)
			elements[m.Element.QualifiedName] = m.Element
		}
		lists = append(lists, store.RankList{Name: store.ArmName, Keys: keys})
	}
	fused := store.FuseRRFWeighted(lists, store.FuseRRFWeights{
		Vector:   fw.vector,
		TSVector: fw.keyword,
		Name:     nameArmWeight,
	})
	out := make([]string, 0, len(fused))
	for _, h := range fused {
		out = append(out, h.Key)
		if len(out) >= limit {
			break
		}
		ranks[h.Key] = h.Ranks
		scores[h.Key] = h.Score
	}
	return out, elements, ranks, scores
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
	case "session_retain":
		scope, serr := memory.ParseScope(get("scope"))
		if serr != nil {
			return nil, serr
		}
		cwd := get("cwd")
		if cwd == "" {
			cwd = e.projectDir
		}
		res, serr := e.mem.SessionRetain(scope, cwd, get("bank"), get("session_id"), argStrs(req.Args, "turns"), getInt("retained_through_user_turn"))
		if serr != nil {
			return nil, serr
		}
		return map[string]any{"ok": true, "command": "session_retain", "bank": res.Bank, "written": res.Written, "skipped": res.Skipped, "retained_through_user_turn": res.RetainedThroughUserTurn}, nil
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
		return nil, fmt.Errorf("unknown memory command %q (valid: create, str_replace, insert, delete, rename, add, replace, remove, session_retain)", req.Command)
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

// resolveGraphSeed turns a short name or bare identifier into the qualified
// name graph verbs need. Relationships are keyed by qualified_name only, so a
// bare "ServeProjectDirs" used to look like a leaf (empty callers/callees) even
// when the element and its edges were indexed. FindExact already ranks by
// shortest qualified_name, matching L1. Unknown seeds pass through unchanged so
// graph.ErrUnknownNode still fires for true misses.
func (e *Engine) resolveGraphSeed(seed string) (string, error) {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return seed, nil
	}
	els, err := e.st.FindExact(seed)
	if err != nil {
		return "", err
	}
	if len(els) == 0 {
		return seed, nil
	}
	return els[0].QualifiedName, nil
}

// graphAction routes the connection verbs (Rust graph/query.rs parity) to
// internal/graph. The query string may be a bare name or a qualified name —
// bare names are resolved via FindExact before traversal.
//
// A seed or target that no element resolves to is an ANSWER with recovery
// guidance, never graph.ErrUnknownNode: that error reached the agent as a
// failed tool call reading `graph: unknown node`, naming neither the verb nor
// the seed nor a next step, and it made the same typo look like a broken verb.
func (e *Engine) graphAction(ctx context.Context, req QueryRequest, resp map[string]any) (map[string]any, error) {
	g := func() map[string]any {
		resp["action"] = req.Action
		return resp
	}
	// One recovery sentence for an unresolvable end of the walk: name WHICH
	// end was not found, quote it, and give the one step that fixes it. That
	// is what lets an agent tell a typo from a verb that is misconfigured.
	// The value is the string the lookup was given, not the verb.
	unresolved := func(what, value string) (map[string]any, error) {
		resp["hits"] = []any{}
		resp["reachable"] = false
		resp["guidance"] = fmt.Sprintf(
			"No indexed element matches the %s %q; resolve it with query (action empty) to a qualified_name, then retry %s with that exact QN.",
			what, value, req.Action)
		return g(), nil
	}
	seed, err := e.resolveGraphSeed(req.Query)
	if err != nil {
		return nil, err
	}
	if seed != req.Query {
		resp["resolved_query"] = seed
	}
	switch req.Action {
	case "impact":
		// depth comes from args.depth (Rust --depth parity); limit is a
		// result-count concept, not a traversal depth.
		depth := argInt(req.Args, "depth", 2)
		hits, err := graph.Impact(e.st, seed, depth)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("seed", req.Query)
		}
		if err != nil {
			return nil, err
		}
		// The result carries the VERB's name, like every other graph verb:
		// impact under `hits` was the same key the search ladder uses, and
		// these are different shapes (qualified_name + depth, not elements),
		// so the plausible reading of a 21-entry `hits` was "21 elements
		// matched a text query" when it meant "21 nodes depend on this". The
		// alias stays so an agent that learned `hits` is not broken.
		resp["impact"] = hits
		resp["hits"] = hits
		return g(), nil
	case "path":
		toRaw := argStr(req.Args, "to")
		if toRaw == "" {
			return nil, fmt.Errorf("query path requires args.to (target qualified name)")
		}
		to, err := e.resolveGraphSeed(toRaw)
		if err != nil {
			return nil, err
		}
		if to != toRaw {
			resp["resolved_to"] = to
		}
		// maxDepth comes from args.depth (default 2) — Limit is a result
		// count, not a traversal bound; paths are a single answer anyway.
		maxDepth := argInt(req.Args, "depth", 0) // 0 = graph default
		path, err := graph.ShortestPath(e.st, seed, to, maxDepth)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("target (args.to)", toRaw)
		}
		if err != nil {
			return nil, err
		}
		if path == nil {
			resp["path"] = []string{}
			resp["reachable"] = false
			resp["guidance"] = "No path found. Confirm both ends with query (action empty) and retry path with exact qualified_names (args.to required)."
		} else {
			resp["path"] = path
			resp["reachable"] = true
		}
		return g(), nil
	case "callers":
		qns, err := graph.Callers(e.st, seed)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("seed", req.Query)
		}
		if err != nil {
			return nil, err
		}
		resp["callers"] = qns
		if len(qns) == 0 {
			resp["guidance"] = graphEmptyGuidance("callers", seed)
		}
		return g(), nil
	case "callees":
		qns, err := graph.Callees(e.st, seed)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("seed", req.Query)
		}
		if err != nil {
			return nil, err
		}
		resp["callees"] = qns
		if len(qns) == 0 {
			resp["guidance"] = graphEmptyGuidance("callees", seed)
		}
		return g(), nil
	case "context":
		out, err := graph.Context(e.st, seed, req.Limit)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("seed", req.Query)
		}
		if err != nil {
			return nil, err
		}
		for k, v := range out {
			resp[k] = v
		}
		return g(), nil
	case "explain":
		out, err := graph.Explain(e.st, seed)
		if errors.Is(err, graph.ErrUnknownNode) {
			return unresolved("seed", req.Query)
		}
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
		// The schema names `summary` as the lesson's content field, and only
		// `text` was read, so a schema-shaped call wrote an EMPTY lesson and
		// answered {"deduped":false} — a successful write of nothing, which
		// is the class waves 6, 7 and 11 exist to remove. `text` still works.
		text := get("text")
		if strings.TrimSpace(text) == "" {
			text = get("summary")
		}
		// And an empty lesson is REFUSED rather than stored: a write that
		// cannot report itself empty is a write that cannot be wrong loudly,
		// which is how the field mismatch above hid for this long.
		if strings.TrimSpace(text) == "" {
			return nil, fmt.Errorf("session lesson requires text (or summary) — an empty lesson is unrecallable")
		}
		deduped, err := s.AddLesson(get("session_id"), text)
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
		if command == "" {
			// No command is not a mistake here: a session read carries no
			// query text, so the command IS the request, and the only way to
			// discover what the verb supports is to call it. Answer with the
			// valid set (the same posture action=memory now takes) rather than
			// failing a tool call an agent cannot interpret.
			return map[string]any{"commands": []string{"recall", "canvas"}}, nil
		}
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

// anySlice renders any slice as []any, turning a NIL slice into an EMPTY one.
// Go marshals nil as `null` and empty as `[]`, and for a list answer the second
// is the honest "nothing to report": `null` reads as "this field was never
// populated", which for a corpus-derived answer is a claim about configuration
// wearing the clothes of a result.
//
// It lives here (not only in internal/rest) because the same answer reaches MCP
// and REST from one engine method, and the wire shape must not depend on which
// transport asked.
// AnySlice is exported so internal/rest shares ONE definition of the empty-vs-null
// wire shape rather than re-deriving it per transport.
func AnySlice[T any](in []T) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// OntologyMatches returns the last persisted match set.
func (e *Engine) OntologyMatches() (map[string]any, error) {
	matches, err := ontology.LoadMatches(e.st)
	if err != nil {
		return nil, err
	}
	// A nil match set means no catalog was ever imported (LoadMatches returns
	// nil when the kv row is absent) — a configuration fact, not a result.
	// Without the note it reaches the wire as `{"matches": null}`, which reads
	// as "no concept appears in this corpus" and sends the reader to look
	// harder instead of importing a catalog.
	// LoadMatches returns a nil slice when no catalog was ever imported (the kv
	// row is absent), and Go marshals nil as `null` — which reads as "no concept
	// appears in this corpus" rather than "no catalog exists", and sends the
	// reader to look harder instead of importing one. Normalise to an empty
	// array and say what an empty one means here. `matches` is []ontology.Match,
	// so it goes through the same anySlice shape the REST sites use.
	out := map[string]any{"matches": AnySlice(matches)}
	if len(matches) == 0 {
		out["note"] = "no concept catalog is imported for this project; import one with " +
			"`leankg import` (or the import tool, action=ontology). A NON-empty list means a " +
			"catalog is imported and its concepts matched these elements."
	}
	return out, nil
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

// ---------------------------------------------------------------------------
// Context compression (Rust src/compress parity, reached through the 3-tool
// envelope so the registry stays at import/query/status)
// ---------------------------------------------------------------------------

// compressRead serves import{action:"read"}: reader-mode compression of one
// file (the Rust ctx_read verb). args: mode, lines, fresh.
func (e *Engine) compressRead(req ImportRequest) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import read requires path")
	}
	// Relative paths mean "in this project" — same contract as import repo/dir.
	// Without the anchor, MCP clients sending internal/foo.go open against the
	// server process cwd (often a different repo) and fail with ENOENT.
	path := req.Path
	if !filepath.IsAbs(path) && e.projectDir != "" {
		path = filepath.Join(e.projectDir, path)
	}
	modeName := argStr(req.Args, "mode")
	mode, ok := compress.ParseMode(modeName)
	if modeName != "" && !ok {
		return nil, fmt.Errorf("invalid mode %q (valid: adaptive, full, map, signatures, diff, aggressive, entropy, lines)", modeName)
	}
	res, err := e.compressor.Reduce(compress.Request{
		Path:      path,
		Mode:      mode,
		LinesSpec: argStr(req.Args, "lines"),
		Fresh:     argStr(req.Args, "fresh") == "true",
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":            path,
		"mode":            res.Mode.String(),
		"content":         res.Content,
		"tokens":          res.Tokens,
		"total_tokens":    res.TotalTokens,
		"total_lines":     res.TotalLines,
		"output_lines":    res.OutputLines,
		"savings_percent": res.SavingsPercent,
		"cached":          res.IsCached,
		"lines_included":  res.LinesIncluded,
	}, nil
}

// compressRun serves query{action:"compress"} — one dispatcher for the three
// Rust compression paths: file reader (args.mode/lines), command output
// (args.cmd + query text as the output body), and response shaping
// (args.tool + args.response, the maybe_compress parity).
func (e *Engine) compressRun(req QueryRequest) (map[string]any, error) {
	if tool := argStr(req.Args, "tool"); tool != "" {
		resp, _ := req.Args["response"].(map[string]any)
		if resp == nil {
			return nil, fmt.Errorf("query compress with args.tool requires args.response (object)")
		}
		res, err := e.compressor.Reduce(compress.Request{Tool: tool, Response: resp})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"tool":              tool,
			"response":          res.Response,
			"original_tokens":   res.Stats.OriginalTokens,
			"compressed_tokens": res.Stats.CompressedTokens,
			"savings_percent":   res.Stats.SavingsPercent,
		}, nil
	}
	if cmd := argStr(req.Args, "cmd"); cmd != "" {
		// The captured output rides `query` (the shell-tool convention) but
		// clients naturally reach for args.response, which the schema names for
		// the tool form; accept either, and never answer a no-input call with a
		// silent zero-count envelope.
		output := req.Query
		if output == "" {
			output = argStr(req.Args, "response")
		}
		if output == "" {
			return nil, fmt.Errorf("query compress with args.cmd requires the captured output in query (or args.response as a string)")
		}
		res, err := e.compressor.Reduce(compress.Request{Cmd: cmd, Output: output})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"cmd":             cmd,
			"content":         res.Content,
			"tokens":          res.Tokens,
			"total_tokens":    res.TotalTokens,
			"total_lines":     res.TotalLines,
			"output_lines":    res.OutputLines,
			"savings_percent": res.SavingsPercent,
		}, nil
	}
	if req.Query != "" {
		// Treat the query text as the file path for reader-mode compression.
		return e.compressRead(ImportRequest{Path: req.Query, Args: req.Args})
	}
	return nil, fmt.Errorf("query compress requires one of: args.tool+args.response, args.cmd (+ output in query), or a file path in query")
}

// SessionMemoryRead serves the FR-ZCP-07 harness reads on the query tool:
// session_recall (merged ranked recall across the scope's read banks) and
// memories (the <memories> first-turn injection text). args: scope, cwd
// (default the project dir), bank (bank-name mode override).
func (e *Engine) SessionMemoryRead(command, query string, limit int, args map[string]any) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	scope, err := memory.ParseScope(argStr(args, "scope"))
	if err != nil {
		return nil, err
	}
	cwd := argStr(args, "cwd")
	if cwd == "" {
		cwd = e.projectDir
	}
	switch command {
	case "session_recall":
		banks, entries, err := e.mem.SessionRecall(scope, cwd, argStr(args, "bank"), query, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "session_recall", "banks": banks, "count": len(entries), "memories": memory.RankEntries(entries)}, nil
	case "memories":
		text, entries, err := e.mem.FirstTurnMemories(scope, cwd, argStr(args, "bank"), query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "memories", "count": len(entries), "text": text}, nil
	default:
		return nil, fmt.Errorf("unknown memory session command %q (valid: session_recall, memories)", command)
	}
}

// argStrs reads a string-array arg (nil-safe). MCP turns arrive JSON-decoded
// as []any of strings.
func argStrs(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	list, _ := m[key].([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		if sv, ok := v.(string); ok {
			out = append(out, sv)
		}
	}
	return out
}

// stampDriftForLog names the differing stamp components (revision plus any
// #279 identity field) so a degraded L3 query tells you WHAT drifted — the
// model, the chunker, or the prefix pair.
func stampDriftForLog(stored, want store.ModelStamp) string {
	var diffs []string
	if stored.Revision != want.Revision {
		diffs = append(diffs, fmt.Sprintf("revision %q", stored.Revision))
	}
	if stored.ModelID != want.ModelID {
		diffs = append(diffs, fmt.Sprintf("model %q", stored.ModelID))
	}
	if stored.Dimensions != want.Dimensions {
		diffs = append(diffs, fmt.Sprintf("dimensions %d", stored.Dimensions))
	}
	if stored.Distance != want.Distance {
		diffs = append(diffs, fmt.Sprintf("distance %q", stored.Distance))
	}
	if stored.Provider != want.Provider {
		diffs = append(diffs, fmt.Sprintf("provider %q", stored.Provider))
	}
	if stored.ChunkerVersion != want.ChunkerVersion {
		diffs = append(diffs, fmt.Sprintf("chunker_version %d", stored.ChunkerVersion))
	}
	if stored.QueryPrefix != want.QueryPrefix || stored.DocumentPrefix != want.DocumentPrefix {
		diffs = append(diffs, "prefixes")
	}
	if len(diffs) == 0 {
		return "identity differs"
	}
	if want.Revision != "" {
		diffs = append(diffs, fmt.Sprintf("live revision %s", want.Revision))
	}
	return strings.Join(diffs, ", ")
}

// resolveIndexTarget turns an import path into the directory to index.
//
// Two hazards, both from the #332 class the doctor tripwire guards:
//
//  1. A RELATIVE path used to resolve against the server process's working
//     directory, so an MCP client sending "." indexed whatever the daemon
//     happened to be started in. A client means "this project" — anchor at it.
//  2. index.IndexDir reconciles the store against the walk, and the store's
//     paths are relative to the walk root. Pointing that at a SUBTREE of the
//     project therefore reads every file outside the subtree as deleted and
//     sweeps it. Dogfooding this engine cost 4,525 elements that way before the
//     guard existed, so a subtree root is refused instead of attempted.
func (e *Engine) resolveIndexTarget(path string) (string, error) {
	abs := path
	if !filepath.IsAbs(abs) {
		base := e.projectDir
		if base == "" {
			base = "."
		}
		abs = filepath.Join(base, abs)
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	if e.projectDir != "" {
		root := e.projectDir
		if r, err := filepath.EvalSymlinks(root); err == nil {
			root = r
		}
		if abs != root && strings.HasPrefix(abs, root+string(filepath.Separator)) {
			return "", fmt.Errorf("import %s: %q is a subdirectory of the project %q; indexing a subtree would delete every element outside it (the store reconciles against the walk root) — run `leankg index %s` as its own project, or index the project root",
				"repo|dir", path, root, abs)
		}
	}
	return abs, nil
}
