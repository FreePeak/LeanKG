// Package core implements the 4-tool surface (import / query / status /
// authenticate) and
// the L0–L3 query ladder for the Go engine. Transports (MCP, REST) are thin
// adapters over this package; the tool envelope resolves HERE before any
// gate — a read-named envelope can never smuggle a write action (the
// security property carried over from the Rust engine).
package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/FreePeak/LeanKG/internal/astgrep"
	"github.com/FreePeak/LeanKG/internal/compress"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/federation"
	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/lsp"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/ontology"
	"github.com/FreePeak/LeanKG/internal/orgknowledge"
	"github.com/FreePeak/LeanKG/internal/pathguard"
	"github.com/FreePeak/LeanKG/internal/session"
	"github.com/FreePeak/LeanKG/internal/store"
)

// The registry is exactly four tools (product contract; pinned here by the
// ResolveEnvelope error below + the mcp registry test). authenticate is the
// server-level login: it takes no project and is exempt from project routing.
const (
	ToolImport       = "import"
	ToolQuery        = "query"
	ToolStatus       = "status"
	ToolAuthenticate = "authenticate"
)

// supersededTools names the removed v4.4 tool names and their replacement.
// They are REJECTED (the aliasing window has closed and the wire test pins
// the refusal); the error says which tool to call instead (RS-22: a declared
// alias map that no call site read promised routing that never existed).
var supersededTools = map[string]string{
	"set": ToolImport,
	"get": ToolQuery,
}

// Engine is the core service over one project's store.
type Engine struct {
	st         store.Backend
	mem        *memory.Memory
	projectDir string
	langsReg   *langs.Registry            // lazy language activation for the opened codebase
	embedder   embed.Provider             // optional; nil ⇒ L3 degrades with reason
	embedProbe embedHealth                // cached reachability of the embedder (status)
	compressor *compress.LeanKGCompressor // context compression (reader/cmd/response paths, Rust parity)

	// lspMu guards the LSP pool and the closed flag. The pool is created on
	// the first lsp query and shut down by Close (CX-45).
	lspMu  sync.Mutex
	lspMgr *lsp.Manager // lazy per-(lang,dir) LSP server pool (query time only)
	closed bool
}

// Options are the engine's collaborators, all fixed at construction (CX-45).
type Options struct {
	// Memory is the project's memory layer; nil makes the memory actions error.
	Memory *memory.Memory
	// Embedder is the query-time embedding provider; nil degrades L3 with a
	// reason. The CLI's one-shot verbs attach one only when an endpoint is set.
	Embedder embed.Provider
	// ProjectDir is the project directory (enables the session actions and
	// the path guard). A relative dir is made absolute now; "" means cwd.
	ProjectDir string
	// Langs is the language registry (lazy activation state) for the opened
	// codebase; nil leaves every language idle.
	Langs *langs.Registry
}

// wireMemoryVectors gives the memory layer the dense recall arm (RS-19): the
// provider, and the calibrated noise floor of its collection.
func (e *Engine) wireMemoryVectors() {
	if e.mem == nil {
		return
	}
	if e.embedder == nil {
		e.mem.SetProvider(nil, nil)
		return
	}
	e.mem.SetProvider(e.embedder, func(ctx context.Context) (float64, bool) {
		return embed.ReadCalibration(ctx, e.st, embed.StampOf(e.embedder))
	})
}

// ProjectDir reports the project directory the engine is bound to (the metric
// ledger records it as each row's project_path).
func (e *Engine) ProjectDir() string { return e.projectDir }

// New builds an Engine over st. It makes no store call: the store is read by
// the actions, and their read errors are returned to the caller (CX-45).
func New(st store.Backend, opts Options) *Engine {
	projectDir := opts.ProjectDir
	if projectDir != "" {
		if abs, err := filepath.Abs(projectDir); err == nil {
			projectDir = abs
		}
	}
	e := &Engine{
		st: st, mem: opts.Memory, embedder: opts.Embedder, langsReg: opts.Langs,
		projectDir: projectDir, compressor: compress.New(),
	}
	if e.embedder != nil {
		e.wireMemoryVectors()
	}
	return e
}

// Close releases what the engine started: the LSP servers and the pool's idle
// janitor. The store and the memory layer belong to the caller, who closes
// them. Close is idempotent; a query after Close answers an error (CX-45).
func (e *Engine) Close() {
	e.lspMu.Lock()
	defer e.lspMu.Unlock()
	if e.closed {
		return
	}
	e.closed = true
	if e.lspMgr != nil {
		e.lspMgr.Shutdown()
	}
}

// lspPool returns the engine's LSP pool, created on first use. It is created
// once, under the lock, so concurrent lsp queries share one pool.
func (e *Engine) lspPool() (*lsp.Manager, error) {
	e.lspMu.Lock()
	defer e.lspMu.Unlock()
	if e.closed {
		return nil, errEngineClosed
	}
	if e.lspMgr == nil {
		e.lspMgr = lsp.NewManager(60 * time.Second)
	}
	return e.lspMgr, nil
}

var errEngineClosed = errors.New("engine is closed")

// DBPath is the store file's path (metadata; no rows).
func (e *Engine) DBPath() string { return e.st.Path() }

// RecordMetric persists one tool-call metric row.
func (e *Engine) RecordMetric(ctx context.Context, m store.Metric) error {
	return e.st.RecordMetric(ctx, m)
}

// OrgKnowledge is the org-knowledge read service over this engine's store.
func (e *Engine) OrgKnowledge() *orgknowledge.Knowledge { return orgknowledge.New(e.st) }

// FederationReceiver serves this project's graph over the federation protocol.
func (e *Engine) FederationReceiver() http.Handler {
	return federation.Receiver(e.st, e.projectDir)
}

// IndexDir indexes dir into this engine's store with the given language registry.
func (e *Engine) IndexDir(ctx context.Context, dir string, reg *langs.Registry) (index.Result, error) {
	return index.IndexDirWith(ctx, e.st, dir, reg)
}

// RefreshInventory recomputes the store inventory after an index run.
func (e *Engine) RefreshInventory(ctx context.Context) error {
	_, err := store.RefreshInventory(ctx, e.st)
	return err
}

// ResolveEnvelope validates a requested tool name against the four-tool
// registry. Unknown names hard-fail naming the valid surface (error-catalog
// posture carried over from FR-ZCP-12); a superseded legacy name also names
// its replacement.
func ResolveEnvelope(tool string) (string, error) {
	switch tool {
	case ToolImport, ToolQuery, ToolStatus, ToolAuthenticate:
		return tool, nil
	}
	cause := fmt.Sprintf("tool %q is not in this server's registry — valid tools: import, query, status, authenticate", tool)
	if repl, ok := supersededTools[tool]; ok {
		cause += fmt.Sprintf(" (%q was superseded by %q)", tool, repl)
	}
	return "", errs.NewError(errs.UnknownTool, cause, "")
}

// freshness derives the status/query freshness label; the derivation itself is
// the store's, shared with `doctor --deep`'s fleet probe.
func (e *Engine) freshness(ctx context.Context, totalElements int) string {
	return store.Freshness(ctx, e.st, totalElements)
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
	NewPath   string         `json:"new_path,omitempty"`
	InsertAt  *int           `json:"insert_line,omitempty"`
	// DocumentID names the bank row a delete_document command removes, and
	// Bank names the bank it lives in. They ride the same flat shape as the
	// other curation fields, so one lookup path serves both call shapes.
	DocumentID string `json:"document_id,omitempty"`
	Bank       string `json:"bank,omitempty"`
	// Tags is the first-class tag list for action=memory command=session_retain
	// (K2). normalizeTags folds it into args.tags, so one lookup path serves
	// both call shapes.
	Tags []string `json:"tags,omitempty"`
}

// splitCommaList turns "a, b" into ["a","b"]; an empty or blank string
// yields nothing, so an absent tag list stays absent rather than becoming an
// empty slice the tag filters would read as a (wrong) request.
func splitCommaList(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// normalizeTags is the one place the tag input is canonicalised (K2): the
// advertised list field, a list already under args, and the comma-separated
// string an agent sends against a single advertised field all become one
// []string under args.tags, so the retain path reads a single representation.
// An absent tag list stays absent — an empty slice would read as an explicit
// (and wrong) tag request to the filters.
func (r ImportRequest) normalizeTags() ImportRequest {
	// Args is optional on the wire; a bare {action, tags} call arrives with a
	// nil map, and the writes below must not panic on it.
	if r.Args == nil {
		r.Args = map[string]any{}
	}
	switch v := r.Args["tags"].(type) {
	case string:
		if split := splitCommaList(v); len(split) > 0 {
			r.Args["tags"] = split
		} else {
			delete(r.Args, "tags")
		}
	case []string:
		r.Args["tags"] = append([]string(nil), v...)
	case nil:
		if len(r.Tags) > 0 {
			r.Args["tags"] = append([]string(nil), r.Tags...)
		}
	}
	return r
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
	// Only the memory and session actions read their curation keys from args;
	// every other action reads its own typed fields, so the fold does not
	// reach it (its args struct would otherwise refuse the folded keys).
	if r.Action != "memory" && r.Action != "session" {
		return r
	}
	flat := map[string]any{}
	for k, v := range map[string]string{
		"content": r.Content, "old": r.Old, "new": r.New, "text": r.Text,
		"file": r.File, "payload": r.Payload, "summary": r.Summary,
		"session_id": r.SessionID, "node_id": r.NodeID, "new_path": r.NewPath,
		"document_id": r.DocumentID, "bank": r.Bank,
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
		return r.normalizeTags()
	}
	merged := make(map[string]any, len(r.Args)+len(flat))
	for k, v := range flat {
		merged[k] = v
	}
	for k, v := range r.Args {
		merged[k] = v
	}
	r.Args = merged
	return r.normalizeTags()
}

// Import handles the import tool: repo/dir indexing or memory curation writes.
// The flat curation fields are folded into Args first, so a schema-shaped call
// (content at the top level) and an args-shaped call behave identically.
func (e *Engine) Import(ctx context.Context, req ImportRequest) (map[string]any, error) {
	out, err := e.importAction(ctx, req)
	return out, e.scrubPaths(err)
}

func (e *Engine) importAction(ctx context.Context, req ImportRequest) (map[string]any, error) {
	req = req.withFlatArgs()
	// The write path owns the schema. The server no longer migrates a store
	// when it opens it — a read must not rewrite the store it reads — so the
	// actions that write to the STORE migrate it first. Without this, an
	// import into a store whose schema was behind failed with "no such
	// table" and the operator had no way to tell that the store, not the
	// import, was out of date.
	if writesStore(req.Action) {
		if err := e.ensureStoreSchema(ctx); err != nil {
			return nil, err
		}
	}
	h, ok := importActions[req.Action]
	if !ok {
		return nil, fmt.Errorf("unknown import action %q (valid: repo, dir, docs, git, memory, session, ontology, prd)", req.Action)
	}
	return h(e, ctx, req)
}

// writesStore reports whether an import action writes to the STORE (as
// opposed to the markdown memory, which is files, and the compress read,
// which reads). Those actions need the store's schema to be current: the
// server no longer migrates a store when it opens it — a read must not
// rewrite the store it reads — so the write path owns the migration.
func writesStore(action string) bool {
	switch action {
	case "docs", "prd", "repo", "dir", "ontology", "git":
		return true
	}
	return false
}

// ensureStoreSchema applies the pending migrations to the store this engine
// writes to.
//
// It is idempotent — a store that is already current is left alone — and it
// does nothing for a read-only store: there the write that follows reports
// the refusal itself ("attempt to write a readonly database"), which names
// the real cause, while a migration error would name the schema.
func (e *Engine) ensureStoreSchema(ctx context.Context) error {
	if e.st.Mode() == store.RO {
		return nil
	}
	if err := e.st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate store before write: %w", err)
	}
	return nil
}

// refreshInventory recomputes and persists the inventory snapshot. The logic
// lives in the store package (store.RefreshInventory) so the CLI writer verbs
// keep the same bookkeeping — an index that only the Engine refreshed left
// readers reporting possibly_stale forever.
func (e *Engine) refreshInventory(ctx context.Context) (store.Inventory, error) {
	return store.RefreshInventory(ctx, e.st)
}

// Status is the status tool: health, inventory, freshness, backend, and the
// embed-pipeline state read from DB tables (no in-process coupling with
// leankg-embed — issue #368 AC). Provider disclosure is part of the egress
// contract: status always names the active embedding provider.
//
// healthy is DERIVED, never asserted. The rule: healthy is true iff every
// STORE read that FEEDS A PUBLISHED FIELD succeeded. The inventory counts
// (elements/relationships/files/watermark) fail the whole call when they
// cannot be read, so they never reach a payload. The embeddings layer was
// the one read left that could still degrade a delivered payload: a store
// whose emb_stamp table cannot be listed used to publish an EMPTY embeddings
// list — indistinguishable from "no embeddings configured" — next to a
// hardcoded healthy:true, while the store actually held thousands of
// vectors.
//
// So the embeddings read decides healthy, and its failure is NAMED in the
// payload (embeddings_error) instead of being swallowed into an empty list.
// Two facts are deliberately NOT folded into healthy:
//
//   - Provider reachability (query_embedder.reachable): an unreachable query
//     provider degrades the L3 rung, it does not make the store unreadable.
//   - Reads whose failure is NAMED in their own error field (space,
//     last_embed_run): they publish no substitute value, so they falsify nothing.
func (e *Engine) Status(ctx context.Context) (map[string]any, error) {
	els, err := e.st.ElementCount(ctx)
	if err != nil {
		return nil, err
	}
	rels, err := e.st.RelationshipCount(ctx)
	if err != nil {
		return nil, err
	}
	files, err := e.st.FileCount(ctx)
	if err != nil {
		return nil, err
	}
	byType, err := e.st.ElementsByType(ctx)
	if err != nil {
		return nil, err
	}
	seq, at, err := e.st.Watermark(ctx)
	if err != nil {
		return nil, err
	}
	// The embeddings state is read BEFORE the payload is built: it is the
	// one read whose failure used to be swallowed, so it is the one read
	// that decides healthy.
	// A Go package element carries no vector (embed.readElements skips it), so
	// coverage is taken over the embeddable elements.
	emb, embErr := e.embeddingsState(ctx, els-byType["package"])
	out := map[string]any{
		"backend":          e.st.Engine(),
		"healthy":          embErr == nil,
		"store":            e.st.Path(),
		"mode":             e.st.Engine(),
		"elements":         els,
		"files":            files,
		"relationships":    rels,
		"elements_by_type": byType,
		"watermark":        map[string]any{"seq": seq, "at": at},
		"freshness":        e.freshness(ctx, els),
		// head_sync says whether the store still mirrors the tree it was
		// indexed from. It is the signal freshness cannot give: freshness
		// compares two counters inside the same database, so a store 5.3
		// days and 59 % behind HEAD still reported "fresh". "unknown" means
		// the store was not git-indexed or HEAD could not be read — never
		// a freshness claim that was not proven.
		"head_sync":  store.HeadSync(ctx, e.st),
		"tools":      []string{ToolImport, ToolQuery, ToolStatus, ToolAuthenticate},
		"embeddings": emb,
	}
	if embErr != nil {
		// The embeddings state could not be read. Say so in the payload
		// instead of letting an empty list pass for "nothing is embedded":
		// healthy is already false, and this names the read that failed.
		// The list itself is nil (JSON null), not an empty array, so a
		// consumer can tell "the read failed" from "there are no
		// embeddings configured" (an empty array) without reading the
		// error field.
		out["embeddings_error"] = embErr.Error()
	}
	// Space is a no-write probe (PRAGMA page_count/freelist_count on SQLite,
	// pg_database_size on Postgres), so it is safe on a read-only handle and
	// costs three rows. Reporting it is what makes disk bloat visible to an
	// agent: docs/mcp-tool-contract.md advertises "watch/vacuum status" on
	// this tool, and without a payload field there is nothing to see.
	space, serr := e.st.Space(ctx)
	if serr != nil {
		// Named, not dropped: a null payload field says "the read failed", an
		// absent one would read as "nothing to report" (CX-45).
		out["space_error"] = serr.Error()
	} else {
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
		// The health check embeds a probe text, which also resolves the
		// provider's identity (its revision); read the identity after it.
		reachable, perr := e.embedProbe.check(ctx, e.embedder)
		id := e.embedder.Identity()
		qe := map[string]any{"model_id": id.Model, "provider": id.Provider,
			"revision": id.Revision, "reachable": reachable}
		if id.Provider == embed.ProviderDeterministic {
			// D10: the hash-derived provider is selectable only by name, and
			// its vectors carry no meaning. Say so where the operator looks.
			qe["deterministic"] = true
			qe["warning"] = "deterministic provider: hash-derived vectors, not a semantic embedder"
		}
		if perr != "" {
			// status used to report healthy with the provider down while every
			// L3 query degraded (RS-15); say so where the agent looks.
			qe["error"] = perr
		}
		out["query_embedder"] = qe
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
	run, rerr := e.st.LastEmbedRunAny(ctx)
	if rerr != nil {
		out["last_embed_run_error"] = rerr.Error()
	} else if run != nil {
		out["last_embed_run"] = run
	}
	if e.mem != nil {
		out["memory_root"] = e.mem.Root()
	}
	return out, nil
}

// embeddingsState reads the embed-pipeline state from the store: one entry
// per stamped model, with its vector count and the L3 coverage it implies.
//
// The layer read can FAIL — a store whose emb_stamp table cannot be listed
// (a schema the read does not understand) fails the query outright — and
// that failure is RETURNED, not swallowed. Swallowing it turned "the
// embeddings layer is unreadable" into "there are no embeddings", which is
// a different fact, and Status published healthy:true next to it.
//
// Two failure shapes, and the caller tells them apart by the LIST:
//
//   - the stamp listing failed: no list at all (nil) plus the error. The
//     caller publishes a null list, never an empty array, so "the read
//     failed" cannot be mistaken for "nothing is embedded".
//   - one model's vector count failed: the models that could be counted
//     are still published, the failed one is named in the error.
//
// embeddable is the count of elements that take a vector, which Status already
// read (it fails the whole call when that read fails), so the coverage math does
// not query it twice.
func (e *Engine) embeddingsState(ctx context.Context, embeddable int) ([]map[string]any, error) {
	stamps, err := e.st.Stamps(ctx)
	if err != nil {
		return nil, fmt.Errorf("read embedding stamps: %w", err)
	}
	states := make([]map[string]any, 0, len(stamps))
	var errs []error
	for _, st := range stamps {
		vecs, err := e.st.VectorCount(ctx, st.ModelID)
		if err != nil {
			// A count that could not be read is not a count of zero: the
			// model is left out of the list and named in the error instead.
			errs = append(errs, fmt.Errorf("count vectors for model %q: %w", st.ModelID, err))
			continue
		}
		// RS-25: freshness tracks the index only, so a store with un-embedded
		// elements still read "fresh"; coverage says how much L3 can see.
		coverage := 0.0
		if embeddable > 0 {
			coverage = float64(min(vecs, embeddable)) / float64(embeddable)
		}
		states = append(states, map[string]any{
			"coverage":   coverage,
			"model_id":   st.ModelID,
			"revision":   st.Revision,
			"dimensions": st.Dimensions,
			"distance":   st.Distance,
			"provider":   st.Provider,
			"vectors":    vecs,
		})
	}
	return states, errors.Join(errs...)
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
	// Resolve the scope ONCE, before the ladder, so an unknown value is refused
	// even when the ladder answers at L1/L2 — otherwise a caller asking for
	// `code` over a corpus with no vectors would silently get the full corpus
	// from the keyword rung — and so the answer can always name the population
	// it ranked, whichever rung served it.
	env, err := decodeKnownArgs[envelopeArgs](req.Args)
	if err != nil {
		return nil, err
	}
	_, scopeName, serr := scopePredicate(env.Scope)
	if serr != nil {
		return nil, serr
	}
	// The fusion weight is validated HERE, beside the scope contract and before
	// the ladder, for the same reason: L3 degrades to L2 without ever reading a
	// weight, so validating inside the semantic rung would silently accept a
	// typo on exactly the stores where it is hardest to notice. Measured live on
	// the 298-project self-host 2026-10-11 — every shape was accepted and
	// ignored: `banana`, `3`, `0,0`, and even a well-formed `3,1` left
	// retrieval.weight absent and the ranking identical. A caller who sets a
	// weight believes it set one.
	fw, werr := parseFusionWeight(env.Weight)
	if werr != nil {
		return nil, werr
	}
	demoteTest := demotesTests(req.Action) && !mentionsTests(req.Query)
	demoteDoc := demotesTests(req.Action) && !mentionsDocs(req.Query)
	demote := demoteTest || demoteDoc
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if demote {
		req.Limit = limit * 2 // a wider window so implementations below the cut can move up
	}
	out, err := e.query(ctx, req)
	if err == nil && out != nil {
		if r, ok := out["retrieval"].(map[string]any); ok && r != nil {
			r["scope"] = scopeName
			// A weighted answer says so, for the same reason scope does — but
			// ONLY when the rung that applies a weight actually ran. Measured on
			// this branch: an L0 answer ("no elements indexed") claimed
			// weight=3,1, and an L2 degrade would have too, while only L3 fuses
			// two arms to scale. Claiming a setting the answer did not apply is
			// the same defect as #491's false advice: the caller believes it.
			if fw.raw != "" && r["rung"] == "L3" {
				r["weight"] = fw.raw
			}
		}
	}
	if demote && out != nil {
		if hits := hitsOf(out); len(hits) > 0 {
			out["hits"] = demoteTestHits(hits, limit, demoteTest, demoteDoc)
		}
		out["limit"] = limit
	}
	if out != nil {
		out, _ = emptyLists(out).(map[string]any)
	}
	return out, e.scrubPaths(err)
}

// scrubPaths rewrites the project's absolute path out of a client-facing
// error (RS-23): an OS error such as `remove /path/to/project/topics/x.md: no such
// file` told any caller where the server's checkout lives. Catalog errors
// keep their type and code; only the cause text changes.
func (e *Engine) scrubPaths(err error) error {
	if err == nil || e.projectDir == "" {
		return err
	}
	scrub := func(msg string) string {
		for _, root := range []string{e.projectRoot(), filepath.Clean(e.projectDir)} {
			msg = strings.ReplaceAll(msg, root+string(filepath.Separator), "")
			msg = strings.ReplaceAll(msg, root, ".")
		}
		return msg
	}
	var ce *errs.Error
	if errors.As(err, &ce) {
		cp := *ce
		cp.Cause = scrub(cp.Cause)
		return &cp
	}
	if msg := scrub(err.Error()); msg != err.Error() {
		return errors.New(msg)
	}
	return err
}

// relRef reports a session ref with a project-relative path (RS-23).
func (e *Engine) relRef(r session.Ref) session.Ref {
	if rel, err := filepath.Rel(e.projectDir, r.Path); err == nil && !strings.HasPrefix(rel, "..") {
		r.Path = filepath.ToSlash(rel)
	}
	return r
}

func (e *Engine) query(ctx context.Context, req QueryRequest) (map[string]any, error) {
	h, ok := queryActions[req.Action]
	if !ok {
		return nil, errs.NewError(errs.UnknownAction,
			fmt.Sprintf("query action %q (valid: search, exact, fuzzy, semantic, element, impact, path, callers, callees, context, explain, memory, session, ontology, prd, incidents, env_conflicts, service_context, portfolio, history, cochange, churn; empty = ladder router)", req.Action), "")
	}
	return h(e, ctx, req)
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
func (e *Engine) rungExact(ctx context.Context, q string, limit int, resp map[string]any, pin bool, keep store.ElementFilter) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if pin {
		resp["retrieval"] = map[string]any{"rung": "L1", "reason": "exact identifier match"}
	}
	els, err := e.st.FindExact(ctx, q)
	if err != nil {
		return nil, err
	}
	els = admit(els, identityElement, keep, limit)
	resp["hits"] = shapeElements(els, limit)
	if pin {
		return withEmptyHint(resp, nil)
	}
	return resp, nil
}

// rungFuzzy is L2, the keyword rung. sqlite ranks it with FTS5/bm25; the
// PostgreSQL backend ranks its migration-012 tsvector with ts_rank and degrades
// through pg_trgm similarity to ILIKE substring recall (issue #273). FindFuzzy
// reports the arm that served the hits, so the rung has no engine branch.
func (e *Engine) rungFuzzy(ctx context.Context, q string, limit int, resp map[string]any, pin bool, keep store.ElementFilter) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	window := fetchWindow(limit, keep)
	matches, arm, err := e.st.FindFuzzy(ctx, q, window)
	if err != nil {
		return nil, err
	}
	reason := keywordReason(arm)
	matches = admit(matches, matchElement, keep, limit)
	if pin {
		if len(matches) == 0 {
			reason = "no keyword match"
		}
		resp["retrieval"] = map[string]any{"rung": "L2", "reason": reason}
	}
	hits := make([]map[string]any, 0, len(matches))
	for _, m := range matches {
		h := shapeElement(m.Element, limit)
		h["score"] = m.Score
		hits = append(hits, h)
	}
	resp["hits"] = hits
	if pin {
		return withEmptyHint(resp, nil)
	}
	return resp, nil
}

// rungSemantic is L3: embed the query via the wired provider, cosine top-k
// over the first stamped collection. Provider failure or absent wiring
// DEGRADES to L2 — never a hard error (issue #368 AC: query-time provider
// failure degrades the ladder with retrieval.reason).
func (e *Engine) rungSemantic(ctx context.Context, q string, limit int, resp map[string]any, pin bool, keep store.ElementFilter, env envelopeArgs) (map[string]any, error) {
	degrade := func(reason string) (map[string]any, error) {
		// A cancelled caller is not a provider failure: it ends the rung with
		// ctx.Err() instead of an L2 answer that would be cached as a result.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pin {
			resp["retrieval"] = map[string]any{"rung": "L2", "reason": reason}
		}
		// pin=false so rungFuzzy does not overwrite the L3-degrade reason;
		// withEmptyHint still attaches next-step guidance on a zero-hit result.
		return withEmptyHint(e.rungFuzzy(ctx, q, limit, resp, false, keep))
	}
	if e.embedder == nil {
		return degrade("no embedding provider wired; degraded from L3")
	}
	// Select the collection BY the embedder's model (Stamps() has no ORDER
	// BY — stamps[0] was arbitrary with >1 model) and enforce the query-side
	// stamp contract: drift DEGRADES to L2, never silently wrong answers.
	if err := e.embedder.Probe(ctx); err != nil {
		return nil, err
	}
	modelID := e.embedder.Identity().Model
	stamp, err := e.st.Stamp(ctx, modelID)
	if err != nil {
		// A READ ERROR is not a missing collection: the collection may well
		// exist (measured: 2314 vectors in the store) while the stamp row
		// could not be read. Reporting "no vector collection" here named a
		// false cause and sent the operator to rebuild a collection that was
		// there all along. The read error is surfaced verbatim instead.
		return degrade(fmt.Sprintf("vector collection stamp for model %s could not be read (%v); degraded from L3", modelID, err))
	}
	if stamp == nil {
		return degrade(fmt.Sprintf("no vector collection for model %s; degraded from L3", modelID))
	}
	if want := embed.StampOf(e.embedder); !stamp.Matches(want) {
		// Whole-identity comparison (issue #279): a chunker or prefix change is
		// the same class of drift as a model change — mixed vector spaces must
		// never be served, and the hint names the fix.
		return degrade(fmt.Sprintf("stamp mismatch: collection differs from the live provider (%s); degraded from L3; run `leankg-embed full` to rebuild",
			stampDriftForLog(*stamp, want)))
	}
	qvec, err := embed.EmbedQuery(ctx, e.embedder, q)
	if err != nil {
		return degrade(fmt.Sprintf("embedding provider failed (%v); degraded from L3", err))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// keep is the caller's population (scope and history rule, see
	// searchPrelude). Both arms of the hybrid search receive it, so the keyword
	// arm cannot reintroduce rows the caller excluded.
	// Query validated this already; parsed again here because the rung is also
	// reachable as a pinned action. An unset weight is the default.
	fw, _ := parseFusionWeight(env.Weight)
	weights := store.DefaultFuseWeights()
	if fw.raw != "" {
		weights = store.FuseRRFWeights{Vector: fw.vector, Keyword: fw.keyword, Explicit: true}
	}
	// Issue #273 / RS-12: L3 fuses the cosine ranking with the keyword
	// rankings by reciprocal rank fusion, because the scores live on unrelated
	// scales (cosine, bm25/ts_rank, trigram similarity) and only their ranks
	// are comparable — PostgreSQL over tsvector+trigram, SQLite over FTS5. An
	// empty fusion falls through to the plain cosine rung below.
	// The fusion weight is the caller's when set, else the store default
	// (store.DefaultFuseWeights, the same value both engines read). The weight
	// was already validated in Query, so a malformed value never reaches here.
	fused, _, ferr := e.st.Hybrid(ctx, store.HybridQuery{ModelID: modelID, Text: q, Vec: qvec,
		Limit: limit, Filter: keep, Weights: weights})
	if ferr != nil {
		return degrade(fmt.Sprintf("hybrid fusion failed (%v); degraded from L3", ferr))
	}
	if len(fused) > 0 {
		out := make([]map[string]any, 0, len(fused))
		for _, h := range fused {
			m := shapeElement(h.Element, limit)
			m["similarity"] = h.Similarity
			m["score"] = h.Score // the fused RRF score, not a single-arm score
			m["ranks"] = h.Ranks
			out = append(out, m)
		}
		resp["hits"] = out
		resp["retrieval"] = map[string]any{"rung": "L3", "reason": "reciprocal-rank fusion " + fusionArms(fused)}
		best, seen := 0.0, false
		for _, h := range fused {
			if _, ok := h.Ranks[store.ArmVector]; ok && (!seen || h.Similarity > best) {
				best, seen = h.Similarity, true
			}
		}
		e.annotateConfidence(ctx, resp, *stamp, best, seen)
		return withEmptyHint(resp, nil)
	}
	hits, err := e.st.SearchVectors(ctx, store.VectorQuery{ModelID: modelID, Vec: qvec, K: limit, Filter: keep})
	if err != nil {
		return degrade(fmt.Sprintf("vector search failed (%v); degraded from L3", err))
	}
	out := make([]map[string]any, 0, len(hits))
	for _, h := range hits {
		m := shapeElement(h.Element, limit)
		m["similarity"] = h.Similarity
		out = append(out, m)
	}
	resp["hits"] = out
	resp["retrieval"] = map[string]any{"rung": "L3", "reason": "vector similarity (cosine)"}
	if len(hits) > 0 {
		e.annotateConfidence(ctx, resp, *stamp, hits[0].Similarity, true)
	}
	return withEmptyHint(resp, nil)
}

// fusionArms names the arms that contributed to the fused hits, in fusion order,
// for the L3 reason: "rrf(vector+fts5)".
func fusionArms(hits []store.HybridHit) string {
	order := []store.Arm{store.ArmVector, store.ArmFTS5, store.ArmTSVector, store.ArmTrigram, store.ArmILIKE, store.ArmName}
	var names []string
	for _, arm := range order {
		for _, h := range hits {
			if _, ok := h.Ranks[arm]; ok {
				names = append(names, string(arm))
				break
			}
		}
	}
	return "rrf(" + strings.Join(names, "+") + ")"
}

// annotateConfidence marks an L3 answer whose best vector hit does not beat
// the collection's calibrated noise floor (embed.Calibrate): the question is
// probably not about this codebase, so the hits are the nearest unrelated
// text. The hits stay — the flag and guidance tell the agent not to trust
// them. Collections never calibrated (an embed run before this change) get
// no flag at all rather than a guessed one.
func (e *Engine) annotateConfidence(ctx context.Context, resp map[string]any, stamp store.ModelStamp, best float64, seen bool) {
	floor, ok := embed.ReadCalibration(ctx, e.st, stamp)
	if !ok {
		return
	}
	r, _ := resp["retrieval"].(map[string]any)
	if r == nil {
		return
	}
	if !seen || best <= floor {
		r["confidence"] = "low"
		resp["guidance"] = fmt.Sprintf("No strong semantic match: the best hit (similarity %.3f) scores no higher than text unrelated to this codebase (noise floor %.3f). The question may be outside this project; rephrase with its terms, or try a keyword/identifier query.", best, floor)
		return
	}
	r["confidence"] = "normal"
}

// --- memory actions (ride the 4-tool surface; #369) ---

// MemoryRead handles read-only memory commands: view, snapshot, search.
func (e *Engine) MemoryRead(ctx context.Context, command, path, query string, limit int) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	h, ok := memoryReadCommands[command]
	if !ok {
		return nil, fmt.Errorf("unknown memory read command %q (valid: view, snapshot, search)", command)
	}
	return h(e, ctx, path, query, limit)
}

// --- shaping ---

// compactHitLimit is the window size above which a search hit drops language
// and line_end. A wide window is a list to scan, so each hit carries only what
// locates it; a narrow one is read closely and keeps the full shape.
const compactHitLimit = 5

// hitSnippetBytes caps the content snippet of a search hit.
const hitSnippetBytes = 200

func shapeElements(els []store.Element, limit int) []map[string]any {
	out := make([]map[string]any, 0, len(els))
	for _, el := range els {
		out = append(out, shapeElement(el, limit))
		if len(out) >= limit {
			break
		}
	}
	return out
}

func shapeElement(el store.Element, limit int) map[string]any {
	m := map[string]any{
		"qualified_name": el.QualifiedName,
		"element_type":   el.ElementType,
		"name":           el.Name,
		"file_path":      el.FilePath,
		"line_start":     el.LineStart,
	}
	if limit <= compactHitLimit {
		m["line_end"] = el.LineEnd
		m["language"] = el.Language
	}
	if el.ParentQualified != "" {
		m["parent_qualified"] = el.ParentQualified
	}
	// A spec is a declaration, not an implementer, which changes what the
	// element means, so the flag is kept on compact hits too.
	if graph.IsInterfaceSpec(el) {
		m["interface_spec"] = true
	}
	content := el.Content
	if len(content) > hitSnippetBytes {
		content = store.ClipUTF8(content, hitSnippetBytes) + "…"
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

// isProse reports whether q reads as a natural-language question rather than
// a symbol lookup: at least two words, none of them code-shaped (camelCase,
// snake_case, dotted/qualified paths, call syntax).
func isProse(q string) bool {
	fields := strings.Fields(q)
	if len(fields) < 2 {
		return false
	}
	for _, f := range fields {
		if codeShaped(strings.Trim(f, ".,;:?!\"'`")) {
			return false
		}
	}
	return true
}

func codeShaped(w string) bool {
	if strings.ContainsAny(w, "_.:/()[]{}<>=") {
		return true
	}
	// camelCase / PascalCase: an interior capital in a word that also has
	// lowercase letters. All-caps words are acronyms (JSON, RPC, XML), not
	// code — they used to keep prose questions off the L3 rung.
	hasLower := strings.IndexFunc(w, unicode.IsLower) >= 0
	for i, r := range w {
		if i > 0 && unicode.IsUpper(r) && hasLower {
			return true
		}
	}
	return false
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

// resolveGraphSeeds turns a short name or bare identifier into the qualified
// names graph verbs walk. Relationships are keyed by qualified_name only, so a
// bare "ServeProjectDirs" used to look like a leaf (empty callers/callees) even
// when the element and its edges were indexed. A qualified name resolves to
// exactly itself. A bare name shared by callable elements (Store.X and
// PGStore.X) resolves to all of them, shortest qualified name first, so one
// verb call answers for every same-named function or method. Any other bare
// name resolves to the shortest match, as FindExact ranks it. Unknown seeds
// pass through unchanged so graph.ErrUnknownNode still fires for true misses.
// When neither the exact nor the bare-name lookup finds anything, the package
// and path-suffix forms of seedAliases are tried; aliased reports that they
// resolved the seed. The result is never empty; index 0 is the seed the
// single-seed verbs use.
func (e *Engine) resolveGraphSeeds(ctx context.Context, seed string) (seeds []string, aliased bool, err error) {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		return []string{seed}, false, nil
	}
	els, err := e.st.FindExact(ctx, seed)
	if err != nil {
		return nil, false, err
	}
	var callables []string
	for _, el := range els {
		if el.QualifiedName == seed {
			return []string{el.QualifiedName}, false, nil
		}
		if strings.EqualFold(el.Name, seed) && (el.ElementType == "function" || el.ElementType == "method") {
			callables = append(callables, el.QualifiedName)
		}
	}
	switch {
	case len(callables) > 0:
		sort.Slice(callables, func(i, j int) bool {
			if len(callables[i]) != len(callables[j]) {
				return len(callables[i]) < len(callables[j])
			}
			return callables[i] < callables[j]
		})
		return callables, false, nil
	case len(els) > 0:
		return []string{els[0].QualifiedName}, false, nil
	}
	alias, err := e.seedAliases(ctx, seed)
	if err != nil {
		return nil, false, err
	}
	if len(alias) > 0 {
		return alias, true, nil
	}
	return []string{seed}, false, nil
}

// aliasElementTypes are the element kinds a package or path seed may name: the
// callables, types, constants and variables an agent writes as seeds.
var aliasElementTypes = map[string]bool{
	"function": true, "method": true, "constructor": true, "type": true,
	"class": true, "constant": true, "variable": true,
}

// seedAliases resolves a seed written without its exact qualified name to the
// elements it names, sorted by qualified name. Two forms are accepted:
//   - pkg.Name or pkg.Type.Method: the element's file directory is named pkg
//     (its last path element), and it is named Name, or its qualified name ends
//     in Type.Method;
//   - a path suffix such as dir/file.go::Name or file.go::Name: the element's
//     qualified name ends in the seed at a path-segment boundary.
//
// Both look the element up by its last name segment (FindExact), so no new
// store lookup is needed. It returns nil when no element matches.
func (e *Engine) seedAliases(ctx context.Context, seed string) ([]string, error) {
	name := seedName(seed)
	if name == "" {
		return nil, nil
	}
	els, err := e.st.FindExact(ctx, name)
	if err != nil {
		return nil, err
	}
	pkg, tail, dotted := strings.Cut(seed, ".")
	pathSuffix := strings.Contains(seed, "::")
	var out []string
	for _, el := range els {
		if !aliasElementTypes[el.ElementType] {
			continue
		}
		ok := false
		switch {
		case pathSuffix:
			ok = el.QualifiedName == seed || strings.HasSuffix(el.QualifiedName, "/"+seed)
		case dotted:
			ok = filepath.Base(filepath.Dir(el.FilePath)) == pkg &&
				(el.Name == tail || strings.HasSuffix(el.QualifiedName, "::"+tail))
		}
		if ok {
			out = append(out, el.QualifiedName)
		}
	}
	sort.Strings(out)
	return out, nil
}

// didYouMeanLimit caps the candidate names an unknown seed is offered.
const didYouMeanLimit = 5

// didYouMean offers qualified names for a seed that did not resolve. Exact
// name matches come first (shortest qualified name first, as FindExact ranks
// them), then names within a small edit distance of the seed's last segment,
// nearest first. The list holds at most didYouMeanLimit names.
func (e *Engine) didYouMean(ctx context.Context, seed string) ([]string, error) {
	out := []string{}
	name := seedName(seed)
	if name == "" {
		return out, nil
	}
	els, err := e.st.Elements(ctx)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		qn    string
		exact bool
		dist  int
	}
	var cands []candidate
	for _, el := range els {
		if strings.EqualFold(el.Name, name) {
			cands = append(cands, candidate{qn: el.QualifiedName, exact: true})
		} else if d, ok := nearName(name, el.Name); ok {
			cands = append(cands, candidate{qn: el.QualifiedName, dist: d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.exact != b.exact {
			return a.exact
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		if len(a.qn) != len(b.qn) {
			return len(a.qn) < len(b.qn)
		}
		return a.qn < b.qn
	})
	for _, c := range cands {
		if len(out) == didYouMeanLimit {
			break
		}
		out = append(out, c.qn)
	}
	return out, nil
}

// nearName reports whether an indexed name is a near miss for the seed's name:
// within max(2, len/4) edits, case-insensitively. Names shorter than four
// characters are never near misses, so a short name cannot pull in noise.
func nearName(seed, name string) (int, bool) {
	a, b := strings.ToLower(seed), strings.ToLower(name)
	if len(a) < 4 || len(b) < 4 {
		return 0, false
	}
	d := editDistance(a, b)
	return d, d <= max(2, len(a)/4)
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// seedName is the last segment of a seed: the part after the "::" that ends a
// file path, then the part after the last "." (Type.Method) or "/" (a path).
func seedName(seed string) string {
	if i := strings.LastIndex(seed, "::"); i >= 0 {
		seed = seed[i+2:]
	}
	if i := strings.LastIndexAny(seed, "./"); i >= 0 {
		seed = seed[i+1:]
	}
	return seed
}

// markBounds reports the effective depth of a traversal and, only when the node
// budget stopped it, truncated: true (decision D9). A complete walk keeps its
// payload unchanged apart from the depth.
func markBounds(resp map[string]any, b graph.Bounds) {
	resp["depth"] = b.Depth
	if b.Truncated {
		resp["truncated"] = true
	}
}

// sessionReadHandler serves one query{action:"session"} sub-command.
type sessionReadHandler func(e *Engine, s *session.Store, sessionID, nodeID string) (map[string]any, error)

// sessionReadCommands is the query{action:"session"} sub-command registry. ""
// answers the list of commands, since a session read carries no query text and
// the command is the request.
var sessionReadCommands = map[string]sessionReadHandler{
	"recall":  (*Engine).readSessionRecall,
	"canvas":  (*Engine).readSessionCanvas,
	"lessons": (*Engine).readSessionLessons,
	"":        (*Engine).readSessionCommands,
}

// SessionRead restores offloaded payloads / lists a session canvas.
func (e *Engine) SessionRead(command, sessionID, nodeID string) (map[string]any, error) {
	if e.projectDir == "" {
		return nil, fmt.Errorf("session access requires a project directory")
	}
	h, ok := sessionReadCommands[command]
	if !ok {
		return nil, fmt.Errorf("unknown session read command %q (valid: recall, canvas, lessons)", command)
	}
	return h(e, session.New(e.projectDir), sessionID, nodeID)
}

func (e *Engine) readSessionRecall(s *session.Store, sessionID, nodeID string) (map[string]any, error) {
	payload, err := s.Recall(sessionID, nodeID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": "recall", "node_id": nodeID, "payload": string(payload)}, nil
}

func (e *Engine) readSessionCanvas(s *session.Store, sessionID, _ string) (map[string]any, error) {
	refs, err := s.Canvas(sessionID)
	if err != nil {
		return nil, err
	}
	for i := range refs {
		refs[i] = e.relRef(refs[i])
	}
	return map[string]any{"command": "canvas", "session_id": sessionID, "refs": refs}, nil
}

// readSessionLessons lists a session's lessons. A lesson was a black hole before
// this: `command=lesson` wrote one and nothing could read it back. A lesson list
// is per-session and small, so the caller filters.
func (e *Engine) readSessionLessons(s *session.Store, sessionID, _ string) (map[string]any, error) {
	lessons, err := s.Lessons(sessionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": "lessons", "session_id": sessionID, "lessons": lessons}, nil
}

func (e *Engine) readSessionCommands(_ *session.Store, _, _ string) (map[string]any, error) {
	return map[string]any{"commands": []string{"recall", "canvas", "lessons"}}, nil
}

// OntologyMatch loads a concept catalog and matches it against indexed
// elements (internal/ontology); matches are persisted in kv for later reads.
func (e *Engine) OntologyMatch(ctx context.Context, catalogPath string) (map[string]any, error) {
	// The catalog is a project file like any other import input (RS-01): the
	// REST /api/v1/ontology/match route reaches this method directly.
	catalogPath, _, err := e.projectPath(catalogPath)
	if err != nil {
		return nil, err
	}
	cat, err := ontology.LoadCatalog(catalogPath)
	if err != nil {
		return nil, err
	}
	matches, err := cat.MatchElements(ctx, e.st)
	if err != nil {
		return nil, err
	}
	if err := ontology.SaveMatches(ctx, e.st, matches); err != nil {
		return nil, err
	}
	return map[string]any{"concepts": len(cat.Concepts), "matches": matches}, nil
}

// OntologyMatches returns the last persisted match set.
//
// An EMPTY set has two meanings and they want opposite next steps: no catalog
// was ever imported (import one — the answer will not change until you do), or
// a catalog is imported and nothing matched (look harder, or the corpus is what
// it is). Measured live on the 298-project self-host 2026-10-11: a store with
// no catalog answered `{"matches":[]}` — an empty array with nothing said about
// it, so a reader looks harder at a corpus that has no catalog to match
// against. The note is added only for the empty case, so it still
// distinguishes something.
//
// The SHAPE half of this (nil -> `[]`, never `null`) is owned by emptyLists
// (RS-09) and applies to every answer.
func (e *Engine) OntologyMatches(ctx context.Context) (map[string]any, error) {
	matches, err := ontology.LoadMatches(ctx, e.st)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"matches": NonNil(matches)}
	if len(matches) == 0 {
		out["note"] = "no concept catalog is imported for this project, so nothing can match; " +
			"import one with `leankg import` (action=ontology). A NON-empty list means a catalog " +
			"is imported and its concepts matched these elements."
	}
	return out, nil
}

// NonNil returns list as a non-nil slice.
//
// Go marshals a nil slice as `null` and an empty slice as `[]`, and the two
// are DIFFERENT answers — "never configured" and "configured and found
// nothing" — so a list answer must not collapse them into one shape.
func NonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
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
func (e *Engine) patternQuery(ctx context.Context, req QueryRequest, a patternArgs, resp map[string]any) (map[string]any, error) {
	pattern := a.Pattern
	if pattern == "" {
		return nil, fmt.Errorf("query pattern requires args.pattern (AST pattern, e.g. \"func $F($A)\")")
	}
	lang := a.Lang
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
		return e.rungFuzzy(ctx, pattern, req.Limit, resp, false, searchKeep(a.IncludeGit))
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

// lspCommandHandler answers one query{action:"lsp"} sub-command with symbols.
type lspCommandHandler func(e *Engine, ctx context.Context, client *lsp.Client, req QueryRequest, a lspArgs) ([]lsp.Symbol, error)

// lspCommands is the query{action:"lsp"} sub-command registry (args.command).
var lspCommands = map[string]lspCommandHandler{
	"":          (*Engine).lspWorkspaceSymbols,
	"workspace": (*Engine).lspWorkspaceSymbols,
	"document":  (*Engine).lspDocumentSymbols,
}

// lspQuery consults the language server for the QUERIED directory at query
// time (user requirement: "check the lsp to the query directory when the user
// or agent queries"). The server is spawned lazily for (lang, dir), pooled, and
// idle-evicted by the pool; Close shuts the pool down.
func (e *Engine) lspQuery(ctx context.Context, req QueryRequest, a lspArgs, resp map[string]any) (map[string]any, error) {
	if e.langsReg == nil {
		return nil, fmt.Errorf("no language registry attached")
	}
	prof, ok := e.langsReg.Lookup(a.Lang)
	if !ok {
		return nil, fmt.Errorf("unknown language %q", a.Lang)
	}
	if !e.langsReg.IsActive(prof.Language) {
		return nil, fmt.Errorf("language %q is not active for this codebase (lazy: nothing spawned)", a.Lang)
	}
	spec := e.langsReg.LSPSpec(prof.Language)
	if spec == nil {
		return nil, fmt.Errorf("language %q has no LSP tier", a.Lang)
	}
	pool, err := e.lspPool()
	if err != nil {
		return nil, err
	}
	dir := e.projectDir
	if dir == "" {
		dir = "."
	}
	client, err := pool.Get(ctx, prof.Language, spec, dir)
	if err != nil {
		return nil, err
	}
	command := a.Command
	if command == "" {
		command = "workspace"
	}
	h, ok := lspCommands[command]
	if !ok {
		return nil, fmt.Errorf("unknown lsp command %q (valid: workspace, document)", command)
	}
	syms, err := h(e, ctx, client, req, a)
	if err != nil {
		return nil, err
	}
	resp["symbols"] = capSymbols(syms, req.Limit)
	resp["server"] = client.Language()
	resp["retrieval"] = map[string]any{"rung": "lsp", "reason": fmt.Sprintf("language server symbols (%s)", a.Lang)}
	return resp, nil
}

// capSymbols keeps the first limit symbols; a limit of 0 keeps them all.
func capSymbols(syms []lsp.Symbol, limit int) []lsp.Symbol {
	if limit > 0 && len(syms) > limit {
		return syms[:limit]
	}
	return syms
}

func (e *Engine) lspDocumentSymbols(ctx context.Context, client *lsp.Client, _ QueryRequest, a lspArgs) ([]lsp.Symbol, error) {
	if a.Path == "" {
		return nil, fmt.Errorf("lsp document requires args.path (file to inspect)")
	}
	path, _, err := e.projectPath(a.Path)
	if err != nil {
		return nil, err
	}
	return client.DocumentSymbols(ctx, path)
}

// lspWorkspaceSymbols polls briefly: real servers (gopls) index lazily after
// initialize, so an immediate workspace/symbol returns empty. The poll runs
// while ctx allows so the tier actually answers instead of always returning null.
func (e *Engine) lspWorkspaceSymbols(ctx context.Context, client *lsp.Client, req QueryRequest, _ lspArgs) ([]lsp.Symbol, error) {
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
	return syms, nil
}

// ---------------------------------------------------------------------------
// Context compression (Rust src/compress parity, reached through the 4-tool
// envelope so the registry stays at import/query/status)
// ---------------------------------------------------------------------------

// compressReadArgs are the args of the file reader, shared by
// query{action:"compress"} with a path and import{action:"read"}.
type compressReadArgs struct {
	Mode  string `json:"mode"`
	Lines string `json:"lines"`
	Fresh bool   `json:"fresh"`
}

// compressArgs are the args of query{action:"compress"}: the reader's keys plus
// the tool form (tool, response) and the command form (cmd).
type compressArgs struct {
	envelopeArgs
	compressReadArgs
	Tool     string `json:"tool"`
	Response any    `json:"response"`
	Cmd      string `json:"cmd"`
}

// compressRead serves import{action:"read"} and the query path form: reader-mode
// compression of one file (the Rust ctx_read verb).
func (e *Engine) compressRead(path string, a compressReadArgs) (map[string]any, error) {
	// Relative paths mean "in this project" — same contract as import repo/dir
	// — and every path must STAY in the project (RS-01): an absolute path, a
	// `..` climb or an escaping symlink is refused, never read.
	abs, rel, err := e.projectPath(path)
	if err != nil {
		return nil, err
	}
	mode, ok := compress.ParseMode(a.Mode)
	if a.Mode != "" && !ok {
		return nil, fmt.Errorf("invalid mode %q (valid: adaptive, full, map, signatures, diff, aggressive, entropy, lines)", a.Mode)
	}
	res, err := e.compressor.Reduce(compress.Request{
		Path:      abs,
		Mode:      mode,
		LinesSpec: a.Lines,
		Fresh:     a.Fresh,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":            rel,
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
func (e *Engine) compressRun(req QueryRequest, a compressArgs) (map[string]any, error) {
	if a.Tool != "" {
		resp, _ := a.Response.(map[string]any)
		if resp == nil {
			return nil, fmt.Errorf("query compress with args.tool requires args.response (object)")
		}
		res, err := e.compressor.Reduce(compress.Request{Tool: a.Tool, Response: resp})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"tool":              a.Tool,
			"response":          res.Response,
			"original_tokens":   res.Stats.OriginalTokens,
			"compressed_tokens": res.Stats.CompressedTokens,
			"savings_percent":   res.Stats.SavingsPercent,
		}, nil
	}
	if a.Cmd != "" {
		// The captured output rides `query` (the shell-tool convention) but
		// clients naturally reach for args.response, which the schema names for
		// the tool form; accept either, and never answer a no-input call with a
		// silent zero-count envelope.
		output := req.Query
		if output == "" {
			output, _ = a.Response.(string)
		}
		if output == "" {
			return nil, fmt.Errorf("query compress with args.cmd requires the captured output in query (or args.response as a string)")
		}
		res, err := e.compressor.Reduce(compress.Request{Cmd: a.Cmd, Output: output})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"cmd":             a.Cmd,
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
		return e.compressRead(req.Query, a.compressReadArgs)
	}
	return nil, fmt.Errorf("query compress requires one of: args.tool+args.response, args.cmd (+ output in query), or a file path in query")
}

// SessionMemoryRead serves the FR-ZCP-07 harness reads on the query tool:
// session_recall (merged ranked recall across the scope's read banks) and
// memories (the <memories> first-turn injection text). args: scope, cwd
// (default the project dir), bank (bank-name mode override). The caller ctx
// travels with the recall, so recall events carry the caller identity (DS-09).
func (e *Engine) SessionMemoryRead(ctx context.Context, command, query string, limit int, scopeName, cwd, bank string) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	scope, err := memory.ParseScope(scopeName)
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		cwd = e.projectDir
	}
	switch command {
	case "session_recall":
		banks, entries, err := e.mem.SessionRecall(ctx, scope, cwd, bank, query, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "session_recall", "banks": banks, "count": len(entries), "memories": memory.RankEntries(entries)}, nil
	case "memories":
		text, entries, err := e.mem.FirstTurnMemories(ctx, scope, cwd, bank, query)
		if err != nil {
			return nil, err
		}
		return map[string]any{"command": "memories", "count": len(entries), "text": text}, nil
	default:
		return nil, fmt.Errorf("unknown memory session command %q (valid: session_recall, memories)", command)
	}
}

// ScopeAll / ScopeCode / ScopeProd name the retrieval populations a caller can
// ask for. The default is the whole corpus, byte for byte.
const (
	ScopeAll  = "all"
	ScopeCode = "code"
	ScopeProd = "prod"
)

// scopePredicate maps an args.scope value to the population it selects. An
// empty value means the whole corpus (the default); an unknown value is an
// ERROR, never a silent full corpus — answering over everything when the
// caller asked for a subset looks identical to the scope working.
//
// The populations are measured, not intuited: on this repository's own store
// 22% of the elements are test fixtures and 37% are archived docs, so 60% of
// the vector population is a document a working engineer is not asking about.
//
//	code  production source only
//	prod  code plus current (non-archived) documentation
//	all   everything, the default, spelled out so a caller can undo a narrower
//	      scope it inherited
//
// fusionWeight is the caller's per-arm scaling for the L3 fusion, parsed from
// `args.weight` as "<vector>,<keyword>" (e.g. "3,1", or "1,0" to drop the
// keyword arm). The zero value (raw == "") means the store default applies.
type fusionWeight struct {
	vector  float64
	keyword float64
	raw     string
}

// parseFusionWeight validates the caller's weight. An UNSET value is not an
// error and not an override: it leaves the store default in force. Everything else must be
// two non-negative numbers, because a weight the caller cannot read is worse
// than no weight at all: `0,0` ranks nothing, and a typo must fail the same way
// an unknown scope does rather than quietly ranking the default.
func parseFusionWeight(raw string) (fusionWeight, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fusionWeight{}, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) != 2 {
		return fusionWeight{}, fmt.Errorf("query weight %q must be \"<vector>,<keyword>\" (e.g. \"3,1\", or \"1,0\" to drop the keyword arm)", raw)
	}
	nums := make([]float64, 2)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return fusionWeight{}, fmt.Errorf("query weight %q: %q is not a non-negative number", raw, strings.TrimSpace(p))
		}
		nums[i] = v
	}
	if nums[0] == 0 && nums[1] == 0 {
		return fusionWeight{}, fmt.Errorf("query weight %q cannot be \"0,0\": both arms off ranks nothing", raw)
	}
	return fusionWeight{vector: nums[0], keyword: nums[1], raw: raw}, nil
}

func scopePredicate(scope string) (store.ElementFilter, string, error) {
	switch scope {
	case "", ScopeAll:
		return store.ElementFilter{}, ScopeAll, nil
	case ScopeCode:
		return fixtureFilter.Merge(archivedFilter).Merge(markdownFilter), ScopeCode, nil
	case ScopeProd:
		return fixtureFilter, ScopeProd, nil
	}
	return store.ElementFilter{}, "", fmt.Errorf("unknown scope %q (valid: all, code, prod)", scope)
}

// graphScope is the file rule a graph verb's args.scope applies to the files its
// answer names (a caller's file, an impact node's file). prod keeps production
// files (drops test files, reusing isTestPath); code drops markdown documents
// (markdownFilter); all keeps everything. It returns the scope name the answer
// echoes. An unknown value is the error scopePredicate gives.
func graphScope(scope string) (keep func(file string) bool, name string, err error) {
	_, name, err = scopePredicate(scope)
	if err != nil {
		return nil, "", err
	}
	switch name {
	case ScopeProd:
		return func(file string) bool { return !isTestPath(file) }, name, nil
	case ScopeCode:
		return func(file string) bool { return markdownFilter.Admits(store.Element{FilePath: file}) }, name, nil
	}
	return func(string) bool { return true }, name, nil
}

// keepSiteFiles drops the sites of every seed whose file keep rejects.
func keepSiteFiles(bySeed map[string][]graph.Site, keep func(file string) bool) map[string][]graph.Site {
	out := make(map[string][]graph.Site, len(bySeed))
	for seed, sites := range bySeed {
		kept := make([]graph.Site, 0, len(sites))
		for _, s := range sites {
			if keep(s.File) {
				kept = append(kept, s)
			}
		}
		out[seed] = kept
	}
	return out
}

// fixtureFilter excludes test fixtures; archivedFilter excludes archived docs;
// markdownFilter excludes markdown documents (root CLAUDE.md and AGENTS.md
// included, which a prefix rule would miss). The scope names below compose them.
var (
	fixtureFilter = store.ElementFilter{
		ExcludePathContains: []string{"testdata", "/fixtures/"},
		ExcludePathSuffixes: []string{"_test.go", "fixtures.js"},
	}
	archivedFilter = store.ElementFilter{ExcludePathPrefixes: []string{"docs/archive/"}}
	markdownFilter = store.ElementFilter{ExcludePathSuffixes: []string{".md", ".mdx"}}
)

// searchKeep is the default search population for every rung. Git history rows
// (commit, author and git:file nodes, see docs/git-history.md) are answered by
// history, cochange and churn; a search returns them only when the caller sets
// args.include_git. A nil result admits every element. Code qualified names
// never start with "git:", so the prefix is the whole test.
func searchKeep(includeGit bool) store.ElementFilter {
	if includeGit {
		return store.ElementFilter{}
	}
	return store.ElementFilter{ExcludeQNPrefixes: []string{"git:"}}
}

// commitRefRe is a git commit reference: 7 to 40 hex characters.
var commitRefRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// commitLookup reports whether q is a commit reference. If it is, the lookup
// text is its 7-character prefix, the token githistory.commitElement puts in
// the commit row's name; otherwise the lookup text is q itself.
func commitLookup(q string) (string, bool) {
	if !commitRefRe.MatchString(q) {
		return q, false
	}
	return strings.ToLower(q[:7]), true
}

// admit keeps the items whose element f admits, at most limit of them. The zero
// filter admits all, so it only trims to limit. Every keyword rung applies it to
// the store's rows here, once, instead of re-deriving the population.
func admit[T any](items []T, elementOf func(T) store.Element, f store.ElementFilter, limit int) []T {
	out := make([]T, 0, min(len(items), limit))
	for _, it := range items {
		if !f.Admits(elementOf(it)) {
			continue
		}
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// keywordReason is the L2 reason for the keyword arm that served the hits. The
// wording is part of the query response; TestKeywordReasonPerArm pins it.
func keywordReason(arm store.Arm) string {
	switch arm {
	case store.ArmFTS5:
		return "FTS5 keyword match"
	case store.ArmTrigram:
		return "trigram keyword match (tsvector degraded)"
	case store.ArmILIKE:
		return "substring keyword match (tsvector+trigram degraded)"
	default:
		return "tsvector keyword match"
	}
}

// fetchWindow is how many keyword matches a rung reads before admit trims them
// to limit. An unfiltered read is exactly limit; a filtered one reads a wider
// window (3x, at least 50, the store's hybrid window) so a run of excluded rows
// does not starve the answer. A window cannot guarantee a full answer when the
// excluded rows outnumber it.
func fetchWindow(limit int, f store.ElementFilter) int {
	if f.IsZero() {
		return limit
	}
	return max(3*limit, 50)
}

func identityElement(el store.Element) store.Element { return el }

func matchElement(m store.FuzzyMatch) store.Element { return m.Element }

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
	// Provider is a transport label, not part of identity (ModelStamp.Matches).
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

// projectPath confines a client-supplied file argument to the project root
// (RS-01). Relative paths are taken from the root; absolute paths, `..`
// climbs and symlinks leaving the root are refused with
// LEANKG_ERROR_PATH_OUTSIDE_PROJECT. rel is root-relative, for responses
// that must not leak the server's filesystem layout.
func (e *Engine) projectPath(p string) (abs, rel string, err error) {
	return pathguard.Resolve(e.projectDir, p)
}

// projectRoot is the symlink-resolved project directory (cwd when the engine
// has none), the base every stored file path is relative to.
func (e *Engine) projectRoot() string {
	root := e.projectDir
	if root == "" {
		root, _ = os.Getwd()
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		return r
	}
	return root
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
		// RS-02: the walk root must BE the project root. A subtree reads every
		// file outside it as deleted; a sibling, ancestor or unrelated root
		// reads the whole project as deleted (validation: `import repo
		// <sibling>` reported deleted_files 48 and left one element; `import
		// repo /path/to/unrelated-dir` grew the store to 2.2 GB). Only "." / the root
		// itself is an index target for this engine's store.
		if abs != root {
			kind := "is not the project root"
			if strings.HasPrefix(abs, root+string(filepath.Separator)) {
				kind = "is a subdirectory of the project"
			}
			return "", errs.NewError(errs.IndexRootMismatch,
				fmt.Sprintf("import repo|dir: %q %s; indexing it into this project's store would delete every element outside it (the store reconciles against the walk root)", path, kind),
				`index the project root (path "."), run `+"`leankg index <dir>`"+` to make the other directory its own project, or select it with project= on a multi-project server`)
		}
	}
	return abs, nil
}
