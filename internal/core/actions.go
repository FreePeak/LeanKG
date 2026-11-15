package core

// The action registries (CX-43). Each registry maps an action or memory
// command to its handler, one entry per key, next to the handler it names.
// The dispatchers (query, importAction, memoryWrite and MemoryRead in core.go,
// queryMemory below) look the key up and call the handler; none of them switches on an action
// name. A key with no entry is an unknown action and keeps the dispatcher's own
// error text.
//
// Sub-command switches (ontology cmd, lsp command, session command) are not
// registries here; they dispatch on args.command and belong to CX-44.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/internal/docindex"
	"github.com/FreePeak/LeanKG/internal/graph"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/ontology"
	"github.com/FreePeak/LeanKG/internal/orgknowledge"
	"github.com/FreePeak/LeanKG/internal/prdindex"
	"github.com/FreePeak/LeanKG/internal/store"
)

// actionHandler serves one registered action or command. R is the request
// type of the registry it sits in: QueryRequest or ImportRequest.
type actionHandler[R any] func(e *Engine, ctx context.Context, req R) (map[string]any, error)

// memoryReadHandler serves one read-only memory command (view, snapshot, search).
type memoryReadHandler func(e *Engine, path, query string, limit int) (map[string]any, error)

// graphVerb answers one connection verb once the query's seed has resolved.
type graphVerb func(e *Engine, ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error)

// queryActions is the query tool's registry. "" and "search" are the L0-L3
// ladder router; exact, fuzzy and semantic pin one rung. It is filled in init
// rather than declared with a literal: the portfolio verb reaches Query, which
// reaches this map, and a package-level initializer cannot refer to that cycle.
var queryActions map[string]actionHandler[QueryRequest]

func init() {
	queryActions = map[string]actionHandler[QueryRequest]{
		"":                (*Engine).queryLadder,
		"search":          (*Engine).queryLadder,
		"exact":           (*Engine).queryExact,
		"element":         (*Engine).queryExact,
		"fuzzy":           (*Engine).queryFuzzy,
		"semantic":        (*Engine).querySemantic,
		"impact":          graphQuery((*Engine).graphImpact),
		"path":            graphQuery((*Engine).graphPath),
		"callers":         graphQuery((*Engine).graphCallers),
		"callees":         graphQuery((*Engine).graphCallees),
		"context":         graphQuery((*Engine).graphContext),
		"explain":         graphQuery((*Engine).graphExplain),
		"memory":          (*Engine).queryMemory,
		"ontology":        (*Engine).queryOntology,
		"compress":        (*Engine).queryCompress,
		"languages":       (*Engine).queryLanguages,
		"lsp":             (*Engine).queryLSP,
		"pattern":         (*Engine).queryPattern,
		"session":         (*Engine).querySession,
		"prd":             (*Engine).queryPRD,
		"incidents":       (*Engine).queryIncidents,
		"env_conflicts":   (*Engine).queryEnvConflicts,
		"service_context": (*Engine).queryServiceContext,
		"history":         (*Engine).gitHistoryQuery,
		"cochange":        (*Engine).gitHistoryQuery,
		"churn":           (*Engine).gitHistoryQuery,
		"portfolio":       (*Engine).portfolioQuery,
	}
}

// importActions is the import tool's registry. "" is the missing-action refusal.
var importActions = map[string]actionHandler[ImportRequest]{
	"":         importMissingAction,
	"git":      (*Engine).gitImport,
	"docs":     (*Engine).importDocs,
	"prd":      (*Engine).importPRD,
	"repo":     (*Engine).importIndex,
	"dir":      (*Engine).importIndex,
	"memory":   (*Engine).memoryWrite,
	"session":  (*Engine).sessionWrite,
	"read":     importCompressRead,
	"ontology": (*Engine).importOntology,
}

// memoryWriteCommands is the import{action:"memory"} sub-command registry.
var memoryWriteCommands = map[string]actionHandler[ImportRequest]{
	"session_retain": (*Engine).memSessionRetain,
	"create":         (*Engine).memCreate,
	"str_replace": memFileEdit(func(e *Engine, req ImportRequest) error {
		return e.mem.StrReplace(memArg(req, "path"), memArg(req, "old"), memArg(req, "new"))
	}),
	"insert": memFileEdit(func(e *Engine, req ImportRequest) error {
		return e.mem.Insert(memArg(req, "path"), memArg(req, "content"), memInt(req, "insert_line"))
	}),
	"delete":          memFileEdit(func(e *Engine, req ImportRequest) error { return e.mem.Delete(memArg(req, "path")) }),
	"delete_document": (*Engine).memDeleteDocument,
	"rename": memFileEdit(func(e *Engine, req ImportRequest) error {
		return e.mem.Rename(memArg(req, "path"), memArg(req, "new_path"))
	}),
	"add": memFileEdit(func(e *Engine, req ImportRequest) error { return e.mem.Add(memArg(req, "file"), memArg(req, "text")) }),
	"replace": memFileEdit(func(e *Engine, req ImportRequest) error {
		return e.mem.Replace(memArg(req, "file"), memArg(req, "old"), memArg(req, "new"))
	}),
	"remove": memFileEdit(func(e *Engine, req ImportRequest) error {
		return e.mem.Remove(memArg(req, "file"), memArg(req, "text"))
	}),
}

// queryMemoryCommands is the query{action:"memory"} sub-command registry.
var queryMemoryCommands = map[string]actionHandler[QueryRequest]{
	"session_recall": (*Engine).querySessionMemory,
	"memories":       (*Engine).querySessionMemory,
	"view":           (*Engine).queryMemoryView,
	"snapshot":       (*Engine).queryMemorySnapshot,
	"search":         (*Engine).queryMemorySearch,
	"":               (*Engine).queryMemorySearch,
}

// memoryReadCommands serves the read-only memory commands MemoryRead answers.
var memoryReadCommands = map[string]memoryReadHandler{
	"view":     (*Engine).readMemoryView,
	"snapshot": (*Engine).readMemorySnapshot,
	"search":   (*Engine).readMemorySearch,
}

// searchScope is the envelope every search, rung and graph action shares.
type searchScope struct {
	resp  map[string]any
	limit int
	els   int
	keep  store.ElementFilter
}

// searchPrelude validates the query text and builds the shared envelope: the
// limit, the freshness of the element count and the population rule.
func (e *Engine) searchPrelude(req QueryRequest) (searchScope, error) {
	if req.Query == "" {
		return searchScope{}, fmt.Errorf("query requires query text")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	resp := map[string]any{"query": req.Query, "limit": limit}
	els, _ := e.st.ElementCount()
	resp["freshness"] = e.freshness(els)
	// One population rule for every rung: git history rows are hidden unless
	// args.include_git is set (see searchKeep). The semantic rung applies the
	// same rule as a store-side predicate.
	return searchScope{resp: resp, limit: limit, els: els, keep: searchKeep(req.Args)}, nil
}

func (e *Engine) queryExact(ctx context.Context, req QueryRequest) (map[string]any, error) {
	s, err := e.searchPrelude(req)
	if err != nil {
		return nil, err
	}
	return e.rungExact(ctx, req.Query, s.limit, s.resp, true, s.keep)
}

func (e *Engine) queryFuzzy(ctx context.Context, req QueryRequest) (map[string]any, error) {
	s, err := e.searchPrelude(req)
	if err != nil {
		return nil, err
	}
	return e.rungFuzzy(ctx, req.Query, s.limit, s.resp, true, s.keep)
}

func (e *Engine) querySemantic(ctx context.Context, req QueryRequest) (map[string]any, error) {
	s, err := e.searchPrelude(req)
	if err != nil {
		return nil, err
	}
	return e.rungSemantic(ctx, req.Query, s.limit, s.resp, true, req.Args)
}

// queryLadder is the ladder router: L0 cold, then L1 exact, L2 fuzzy, L3
// semantic, with the Noul confidence gate and the prose route.
func (e *Engine) queryLadder(ctx context.Context, req QueryRequest) (map[string]any, error) {
	s, err := e.searchPrelude(req)
	if err != nil {
		return nil, err
	}
	resp, limit, els, keep := s.resp, s.limit, s.els, s.keep
	// One check per rung boundary: the ladder below ignores rung errors on
	// purpose (a miss falls through), so cancellation is checked here instead.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Ladder router: L0 cold → L1 exact → L2 fuzzy → L3 semantic.
	if els == 0 {
		resp["retrieval"] = map[string]any{"rung": "L0", "reason": "no elements indexed"}
		resp["hits"] = []any{}
		resp["guidance"] = "Index a repository first: leankg import <dir> or tool import {action:\"repo\", path:...}"
		return resp, nil
	}
	if _, err := e.rungExact(ctx, req.Query, limit, resp, false, keep); err == nil && len(hitsOf(resp)) > 0 {
		resp["retrieval"] = map[string]any{"rung": "L1", "reason": "exact identifier match"}
		return resp, nil
	}
	// RS-12: a prose question goes to the hybrid L3 rung (keyword + vector
	// fused) whenever a query embedder is wired. The keyword rung ORs every
	// term, so it is never empty for prose and the ladder used to stop there:
	// 16 of 16 natural-language questions were answered by keyword alone.
	// Identifier-shaped queries keep L1 → L2 → L3. The Noul confidence gate
	// still holds L3 back for queries the ontology rates as noise.
	if e.embedder != nil && isProse(req.Query) {
		if conf, _ := ontology.OntologyConfidence(e.st, req.Query); conf == 0 || conf >= ontology.MinConfidence {
			delete(resp, "guidance")
			return withEmptyHint(e.rungSemantic(ctx, req.Query, limit, resp, true, req.Args))
		}
	}
	// pin=true: the rung knows which keyword arm served the hits, so it owns
	// the reason (an L2 miss is overwritten by L3 below, which always sets one).
	if _, err := e.rungFuzzy(ctx, req.Query, limit, resp, true, keep); err == nil && len(hitsOf(resp)) > 0 {
		return resp, nil
	}
	// Noul L3 gate: ontology confidence below MinConfidence
	// (0.3) justifies no embedding call — degrade to L2.
	conf, _ := ontology.OntologyConfidence(e.st, req.Query)
	if conf > 0 && conf < ontology.MinConfidence {
		return withEmptyHint(e.rungFuzzy(ctx, req.Query, limit, resp, true, keep))
	}
	// The empty L2 pass above left "No keyword match…" guidance; L3 sets its
	// own (an outage reads as an outage, not as a phrasing problem — RS-15).
	delete(resp, "guidance")
	return withEmptyHint(e.rungSemantic(ctx, req.Query, limit, resp, true, req.Args))
}

// graphQuery adapts a connection verb to the registry: the shared envelope,
// then the seed resolution every verb needs, then the verb itself.
func graphQuery(verb graphVerb) actionHandler[QueryRequest] {
	return func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error) {
		s, err := e.searchPrelude(req)
		if err != nil {
			return nil, err
		}
		seed, err := e.resolveGraphSeed(ctx, req.Query)
		if err != nil {
			return nil, err
		}
		if seed != req.Query {
			s.resp["resolved_query"] = seed
		}
		return verb(e, ctx, req, seed, s.resp)
	}
}

// graphDone stamps the verb name on a finished graph answer.
func graphDone(resp map[string]any, req QueryRequest) (map[string]any, error) {
	resp["action"] = req.Action
	return resp, nil
}

// graphUnresolved is the answer for an unresolvable end of the walk: name WHICH
// end was not found, quote it, and give the one step that fixes it. That is
// what lets an agent tell a typo from a verb that is misconfigured. The value
// is the string the lookup was given, not the verb.
//
// An unresolvable end is an ANSWER, never graph.ErrUnknownNode: that error
// reached the agent as a failed tool call reading `graph: unknown node`,
// naming neither the verb nor the seed nor a next step, and it made the same
// typo look like a broken verb.
func graphUnresolved(resp map[string]any, req QueryRequest, what, value string) (map[string]any, error) {
	resp["hits"] = []any{}
	resp["reachable"] = false
	resp["guidance"] = fmt.Sprintf(
		"No indexed element matches the %s %q; resolve it with query (action empty) to a qualified_name, then retry %s with that exact QN.",
		what, value, req.Action)
	return graphDone(resp, req)
}

func (e *Engine) graphImpact(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	// depth comes from args.depth (Rust --depth parity); limit is a
	// result-count concept, not a traversal depth.
	depth := argInt(req.Args, "depth", 2)
	hits, bounds, err := graph.Impact(ctx, e.st, seed, depth)
	if errors.Is(err, graph.ErrUnknownNode) {
		return graphUnresolved(resp, req, "seed", req.Query)
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
	markBounds(resp, bounds)
	return graphDone(resp, req)
}

func (e *Engine) graphPath(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	toRaw := argStr(req.Args, "to")
	if toRaw == "" {
		return nil, fmt.Errorf("query path requires args.to (target qualified name)")
	}
	to, err := e.resolveGraphSeed(ctx, toRaw)
	if err != nil {
		return nil, err
	}
	if to != toRaw {
		resp["resolved_to"] = to
	}
	// maxDepth comes from args.depth (default 2) — Limit is a result
	// count, not a traversal bound; paths are a single answer anyway.
	maxDepth := argInt(req.Args, "depth", 0) // 0 = graph default
	path, bounds, err := graph.ShortestPath(ctx, e.st, seed, to, maxDepth)
	if errors.Is(err, graph.ErrUnknownNode) {
		// Name the end that is unknown: "graph: unknown node" left the
		// caller guessing which of from/to to fix. And answer, rather
		// than fail: the end that does not resolve is a fact the caller
		// can act on.
		for _, end := range []struct{ label, qn string }{{"from (query)", seed}, {"to (args.to)", to}} {
			if els, ferr := e.st.FindExact(end.qn); ferr == nil && len(els) == 0 {
				return graphUnresolved(resp, req, end.label, end.qn)
			}
		}
	}
	if err != nil {
		return nil, err
	}
	markBounds(resp, bounds)
	switch {
	case path != nil:
		resp["path"] = path
		resp["reachable"] = true
	case bounds.Truncated:
		// The walk stopped at the node budget: the answer is unknown, so
		// reachable is left out rather than reported as false.
		resp["path"] = []string{}
		resp["guidance"] = "Search stopped at the node budget before reaching the target; the answer is unknown, not unreachable. Narrow the question or lower args.depth."
	default:
		resp["path"] = []string{}
		resp["reachable"] = false
		resp["guidance"] = "No path found. Confirm both ends with query (action empty) and retry path with exact qualified_names (args.to required)."
	}
	return graphDone(resp, req)
}

func (e *Engine) graphCallers(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	qns, err := graph.Callers(ctx, e.st, seed)
	if errors.Is(err, graph.ErrUnknownNode) {
		return graphUnresolved(resp, req, "seed", req.Query)
	}
	if err != nil {
		return nil, err
	}
	resp["callers"] = qns
	if len(qns) == 0 {
		resp["guidance"] = graphEmptyGuidance("callers", seed)
	}
	return graphDone(resp, req)
}

func (e *Engine) graphCallees(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	qns, err := graph.Callees(ctx, e.st, seed)
	if errors.Is(err, graph.ErrUnknownNode) {
		return graphUnresolved(resp, req, "seed", req.Query)
	}
	if err != nil {
		return nil, err
	}
	resp["callees"] = qns
	if len(qns) == 0 {
		resp["guidance"] = graphEmptyGuidance("callees", seed)
	}
	return graphDone(resp, req)
}

func (e *Engine) graphContext(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	out, err := graph.Context(ctx, e.st, seed, req.Limit)
	if errors.Is(err, graph.ErrUnknownNode) {
		return graphUnresolved(resp, req, "seed", req.Query)
	}
	if err != nil {
		return nil, err
	}
	for k, v := range out {
		resp[k] = v
	}
	return graphDone(resp, req)
}

func (e *Engine) graphExplain(ctx context.Context, req QueryRequest, seed string, resp map[string]any) (map[string]any, error) {
	out, err := graph.Explain(ctx, e.st, seed)
	if errors.Is(err, graph.ErrUnknownNode) {
		return graphUnresolved(resp, req, "seed", req.Query)
	}
	if err != nil {
		return nil, err
	}
	for k, v := range out {
		resp[k] = v
	}
	return graphDone(resp, req)
}

func (e *Engine) queryMemory(ctx context.Context, req QueryRequest) (map[string]any, error) {
	cmd := argStr(req.Args, "command")
	h, ok := queryMemoryCommands[cmd]
	if !ok {
		return nil, fmt.Errorf("unknown memory read command %q (valid: session_recall, memories, view, snapshot, search)", cmd)
	}
	return h(e, ctx, req)
}

func (e *Engine) querySessionMemory(ctx context.Context, req QueryRequest) (map[string]any, error) {
	return e.SessionMemoryRead(ctx, argStr(req.Args, "command"), req.Query, req.Limit, req.Args)
}

// queryMemoryView reads one memory file by path (RS-21: was ConnectRPC-only).
// The path rides args.path; the top-level query is the fallback.
func (e *Engine) queryMemoryView(_ context.Context, req QueryRequest) (map[string]any, error) {
	path := argStr(req.Args, "path")
	if path == "" {
		path = req.Query
	}
	return e.MemoryRead("view", path, "", 0)
}

func (e *Engine) queryMemorySnapshot(_ context.Context, _ QueryRequest) (map[string]any, error) {
	return e.MemoryRead("snapshot", "", "", 0)
}

// queryMemorySearch serves the search command and the bare memory read. The
// search term rides the top-level `query`, not args.query: the tool's own
// schema advertises `query` as the search text, and session_recall takes it
// from the same place.
func (e *Engine) queryMemorySearch(_ context.Context, req QueryRequest) (map[string]any, error) {
	term := req.Query
	if term == "" {
		term = argStr(req.Args, "query")
	}
	return e.MemoryRead("search", "", term, req.Limit)
}

func (e *Engine) queryOntology(ctx context.Context, req QueryRequest) (map[string]any, error) {
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
}

func (e *Engine) queryCompress(_ context.Context, req QueryRequest) (map[string]any, error) {
	return e.compressRun(req)
}

func (e *Engine) queryLanguages(_ context.Context, _ QueryRequest) (map[string]any, error) {
	return e.LanguagesStatus(), nil
}

func (e *Engine) queryLSP(ctx context.Context, req QueryRequest) (map[string]any, error) {
	return e.lspQuery(ctx, req, map[string]any{"query": req.Query})
}

func (e *Engine) queryPattern(ctx context.Context, req QueryRequest) (map[string]any, error) {
	return e.patternQuery(ctx, req, map[string]any{"query": req.Query})
}

func (e *Engine) querySession(_ context.Context, req QueryRequest) (map[string]any, error) {
	return e.SessionRead(argStr(req.Args, "command"), req.Query, argStr(req.Args, "node_id"))
}

func (e *Engine) queryPRD(_ context.Context, req QueryRequest) (map[string]any, error) {
	rows, err := prdindex.Trace(e.st, req.Query)
	if err != nil {
		return nil, err
	}
	return map[string]any{"requirements": rows, "count": len(rows)}, nil
}

func (e *Engine) queryIncidents(_ context.Context, req QueryRequest) (map[string]any, error) {
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
}

func (e *Engine) queryEnvConflicts(_ context.Context, req QueryRequest) (map[string]any, error) {
	conflicts, err := orgknowledge.New(e.st).FindEnvConflicts(argStr(req.Args, "service"))
	if err != nil {
		return nil, err
	}
	return map[string]any{"conflicts": conflicts, "service": argStr(req.Args, "service")}, nil
}

func (e *Engine) queryServiceContext(_ context.Context, req QueryRequest) (map[string]any, error) {
	return orgknowledge.New(e.st).ServiceContextJSON(argStr(req.Args, "service"), argStr(req.Args, "env"))
}

func (e *Engine) importDocs(ctx context.Context, req ImportRequest) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import docs requires path")
	}
	dir, _, err := e.projectPath(req.Path)
	if err != nil {
		return nil, err
	}
	res, err := docindex.IndexDocsUnder(ctx, e.st, e.projectRoot(), dir)
	if err != nil {
		return nil, fmt.Errorf("docindex %s: %w", req.Path, err)
	}
	if _, err := e.refreshInventory(); err != nil {
		return nil, err
	}
	return map[string]any{
		"indexed": map[string]any{"files": res.Files, "elements": res.Elements, "skipped": res.Skipped},
	}, nil
}

func (e *Engine) importPRD(ctx context.Context, req ImportRequest) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import prd requires path (a PRD markdown document)")
	}
	env := argStr(req.Args, "environment")
	if env == "" {
		env = "local"
	}
	_, relDoc, err := e.projectPath(req.Path)
	if err != nil {
		return nil, err
	}
	res, err := prdindex.IndexDocument(ctx, e.st, e.projectRoot(), relDoc, env)
	if err != nil {
		return nil, err
	}
	if _, err := e.refreshInventory(); err != nil {
		return nil, err
	}
	return map[string]any{"prd": res}, nil
}

// importIndex serves import{action:"repo"} and import{action:"dir"}.
func (e *Engine) importIndex(ctx context.Context, req ImportRequest) (map[string]any, error) {
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
		if _, aerr := e.langsReg.Activate(ctx, target); aerr != nil {
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
}

func (e *Engine) importOntology(ctx context.Context, req ImportRequest) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import ontology requires path (ontology dir or concept catalog JSON)")
	}
	opath, _, err := e.projectPath(req.Path)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(opath); err == nil && info.IsDir() {
		stats, err := ontology.LoadWorkflows(e.st, opath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ontology": "synced", "stats": stats}, nil
	}
	return e.OntologyMatch(opath)
}

// importMissingAction is the refusal for an import with no action.
func importMissingAction(_ *Engine, _ context.Context, _ ImportRequest) (map[string]any, error) {
	return nil, fmt.Errorf("import requires action (repo, dir, docs, prd, git, memory, session, ontology, read)")
}

func importCompressRead(e *Engine, _ context.Context, req ImportRequest) (map[string]any, error) {
	return e.compressRead(req)
}

// memArg reads a memory command's string argument. MCP callers pass the memory
// file at the top-level path field, so "path" falls back to req.Path.
func memArg(req ImportRequest, key string) string {
	if s, ok := req.Args[key].(string); ok && s != "" {
		return s
	}
	if key == "path" {
		return req.Path
	}
	return ""
}

func memInt(req ImportRequest, key string) int {
	f, _ := req.Args[key].(float64)
	return int(f)
}

// memFileEdit serves the memory commands that edit one file and report only ok.
func memFileEdit(op func(e *Engine, req ImportRequest) error) actionHandler[ImportRequest] {
	return func(e *Engine, _ context.Context, req ImportRequest) (map[string]any, error) {
		if err := op(e, req); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "command": req.Command}, nil
	}
}

func (e *Engine) memSessionRetain(ctx context.Context, req ImportRequest) (map[string]any, error) {
	scope, serr := memory.ParseScope(memArg(req, "scope"))
	if serr != nil {
		return nil, serr
	}
	cwd := memArg(req, "cwd")
	if cwd == "" {
		cwd = e.projectDir
	}
	// K2: tags are first-class on the retain path, so a tag-scoped recall
	// can match what a conversation wrote. argStrs also accepts a
	// comma-separated string ("a,b"), which is the shape an agent most
	// often sends when the schema names one field.
	tags := argStrs(req.Args, "tags")
	res, serr := e.mem.SessionRetain(ctx, scope, cwd, memArg(req, "bank"), memArg(req, "session_id"), argStrs(req.Args, "turns"), memInt(req, "retained_through_user_turn"), tags)
	if serr != nil {
		return nil, serr
	}
	return map[string]any{"ok": true, "command": "session_retain", "bank": res.Bank, "written": res.Written, "skipped": res.Skipped, "retained_through_user_turn": res.RetainedThroughUserTurn}, nil
}

// memCreate keeps the memory-tool contract (create or overwrite), but an
// overwrite is reported, never silent.
func (e *Engine) memCreate(_ context.Context, req ImportRequest) (map[string]any, error) {
	existed := e.mem.Exists(memArg(req, "path"))
	if err := e.mem.Create(memArg(req, "path"), memArg(req, "content")); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "command": req.Command, "overwrote": existed}, nil
}

// memDeleteDocument is the bank-row delete. The REST mount had it (DELETE
// /v1/default/banks/{bank}/documents/{id}) and the MCP surface did not, so an
// MCP client could never remove a memory row.
func (e *Engine) memDeleteDocument(ctx context.Context, req ImportRequest) (map[string]any, error) {
	docID := strings.TrimSpace(memArg(req, "document_id"))
	if docID == "" {
		return nil, fmt.Errorf("memory: delete_document requires a document_id")
	}
	scope, serr := memory.ParseScope(memArg(req, "scope"))
	if serr != nil {
		return nil, serr
	}
	cwd := memArg(req, "cwd")
	if cwd == "" {
		cwd = e.projectDir
	}
	bankName := e.mem.WriteBank(scope, cwd, memArg(req, "bank"))
	n, derr := e.mem.DeleteDocument(ctx, bankName, docID)
	if derr != nil {
		return nil, derr
	}
	return map[string]any{
		"ok": true, "command": "delete_document", "bank": bankName,
		"document_id": docID, "memory_units_deleted": n,
	}, nil
}

func (e *Engine) readMemoryView(path, _ string, _ int) (map[string]any, error) {
	text, err := e.mem.View(path, 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": "view", "path": path, "content": text}, nil
}

func (e *Engine) readMemorySnapshot(_, _ string, _ int) (map[string]any, error) {
	text, err := e.mem.Snapshot()
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": "snapshot", "content": text}, nil
}

func (e *Engine) readMemorySearch(_, query string, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 10
	}
	hits, err := e.mem.Search(query, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"command": "search", "hits": hits}, nil
}
