package core

// The action registries (CX-43) and the typed args (CX-44). Each registry maps
// an action or command to its handler, one entry per key, next to the handler
// it names. The dispatchers (query, importAction, memoryWrite and MemoryRead in
// core.go, queryMemory below) look the key up and call the handler; none of
// them switches on an action name. A key with no entry is an unknown action and
// keeps the dispatcher's own error text.
//
// Each handler gets its args as a typed struct, decoded once at the registry
// seam by queryAction or importArgs (args.go). The struct names every key the
// action reads, so an unknown key or a wrong-typed value is refused before the
// handler runs.

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
	"github.com/FreePeak/LeanKG/internal/session"
	"github.com/FreePeak/LeanKG/internal/store"
)

// actionHandler serves one registered action or command. R is the request
// type of the registry it sits in: QueryRequest or ImportRequest.
type actionHandler[R any] func(e *Engine, ctx context.Context, req R) (map[string]any, error)

// memoryReadHandler serves one read-only memory command (view, snapshot, search).
type memoryReadHandler func(e *Engine, path, query string, limit int) (map[string]any, error)

// graphVerb answers one connection verb once the query's seed has resolved.
type graphVerb func(e *Engine, ctx context.Context, req QueryRequest, a graphArgs, seed string, resp map[string]any) (map[string]any, error)

// deprecatedSubCommandNote is the response note for a caller that still sends
// the old `cmd` key for a sub-command (CX-44: `command` is the canonical key).
const deprecatedSubCommandNote = "args.cmd is deprecated; send args.command"

// queryAction binds a handler that reads typed args: the args decode once,
// here, and the handler receives them. An unknown key or a wrong type is
// refused before the handler runs.
func queryAction[A any](h func(*Engine, context.Context, QueryRequest, A) (map[string]any, error)) actionHandler[QueryRequest] {
	return func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error) {
		a, err := decodeArgs[A](req.Args)
		if err != nil {
			return nil, err
		}
		return h(e, ctx, req, a)
	}
}

// envelopeOnly binds an action that reads no args beyond the envelope; any
// other key is refused.
func envelopeOnly(h func(*Engine, context.Context, QueryRequest) (map[string]any, error)) actionHandler[QueryRequest] {
	return queryAction(func(e *Engine, ctx context.Context, req QueryRequest, _ envelopeArgs) (map[string]any, error) {
		return h(e, ctx, req)
	})
}

// importArgs binds an import handler that reads typed args.
func importArgs[A any](h func(*Engine, context.Context, ImportRequest, A) (map[string]any, error)) actionHandler[ImportRequest] {
	return func(e *Engine, ctx context.Context, req ImportRequest) (map[string]any, error) {
		a, err := decodeArgs[A](req.Args)
		if err != nil {
			return nil, err
		}
		return h(e, ctx, req, a)
	}
}

// importNoArgs binds an import handler that takes no args; any key is refused.
func importNoArgs(h func(*Engine, context.Context, ImportRequest) (map[string]any, error)) actionHandler[ImportRequest] {
	return importArgs(func(e *Engine, ctx context.Context, req ImportRequest, _ noArgs) (map[string]any, error) {
		return h(e, ctx, req)
	})
}

// queryActions is the query tool's registry. "" and "search" are the L0-L3
// ladder router; exact, fuzzy and semantic pin one rung. It is filled in init
// rather than declared with a literal: the portfolio verb reaches Query, which
// reaches this map, and a package-level initializer cannot refer to that cycle.
var queryActions map[string]actionHandler[QueryRequest]

func init() {
	queryActions = map[string]actionHandler[QueryRequest]{
		"":                queryAction((*Engine).queryLadder),
		"search":          queryAction((*Engine).queryLadder),
		"exact":           queryAction((*Engine).queryExact),
		"element":         queryAction((*Engine).queryExact),
		"fuzzy":           queryAction((*Engine).queryFuzzy),
		"semantic":        queryAction((*Engine).querySemantic),
		"impact":          graphQuery((*Engine).graphImpact),
		"path":            graphQuery((*Engine).graphPath),
		"callers":         graphQuery((*Engine).graphCallers),
		"callees":         graphQuery((*Engine).graphCallees),
		"context":         graphQuery((*Engine).graphContext),
		"explain":         graphQuery((*Engine).graphExplain),
		"memory":          queryAction((*Engine).queryMemory),
		"ontology":        queryAction((*Engine).queryOntology),
		"compress":        queryAction((*Engine).queryCompress),
		"languages":       envelopeOnly((*Engine).queryLanguages),
		"lsp":             queryAction((*Engine).queryLSP),
		"pattern":         queryAction((*Engine).queryPattern),
		"session":         queryAction((*Engine).querySession),
		"prd":             envelopeOnly((*Engine).queryPRD),
		"incidents":       queryAction((*Engine).queryIncidents),
		"env_conflicts":   queryAction((*Engine).queryEnvConflicts),
		"service_context": queryAction((*Engine).queryServiceContext),
		"history":         queryAction((*Engine).gitHistoryQuery),
		"cochange":        queryAction((*Engine).gitHistoryQuery),
		"churn":           queryAction((*Engine).gitHistoryQuery),
		"portfolio":       queryAction((*Engine).portfolioQuery),
	}
}

// importActions is the import tool's registry. "" is the missing-action refusal.
var importActions = map[string]actionHandler[ImportRequest]{
	"":         importMissingAction,
	"git":      importArgs((*Engine).gitImport),
	"docs":     importNoArgs((*Engine).importDocs),
	"prd":      importArgs((*Engine).importPRD),
	"repo":     importNoArgs((*Engine).importIndex),
	"dir":      importNoArgs((*Engine).importIndex),
	"memory":   importArgs((*Engine).memoryWrite),
	"session":  importArgs((*Engine).sessionWrite),
	"read":     importArgs(importCompressRead),
	"ontology": importNoArgs((*Engine).importOntology),
}

// memoryWriteHandler serves one import{action:"memory"} command.
type memoryWriteHandler func(e *Engine, ctx context.Context, req ImportRequest, a memoryArgs) (map[string]any, error)

// memoryWriteCommands is the import{action:"memory"} sub-command registry.
var memoryWriteCommands = map[string]memoryWriteHandler{
	"session_retain": (*Engine).memSessionRetain,
	"create":         (*Engine).memCreate,
	"str_replace": memFileEdit(func(e *Engine, a memoryArgs) error {
		return e.mem.StrReplace(a.Path, a.Old, a.New)
	}),
	"insert": memFileEdit(func(e *Engine, a memoryArgs) error {
		return e.mem.Insert(a.Path, a.Content, a.InsertLine)
	}),
	"delete":          memFileEdit(func(e *Engine, a memoryArgs) error { return e.mem.Delete(a.Path) }),
	"delete_document": (*Engine).memDeleteDocument,
	"rename": memFileEdit(func(e *Engine, a memoryArgs) error {
		return e.mem.Rename(a.Path, a.NewPath)
	}),
	"add": memFileEdit(func(e *Engine, a memoryArgs) error { return e.mem.Add(a.File, a.Text) }),
	"replace": memFileEdit(func(e *Engine, a memoryArgs) error {
		return e.mem.Replace(a.File, a.Old, a.New)
	}),
	"remove": memFileEdit(func(e *Engine, a memoryArgs) error {
		return e.mem.Remove(a.File, a.Text)
	}),
}

// queryMemoryHandler serves one query{action:"memory"} sub-command.
type queryMemoryHandler func(e *Engine, ctx context.Context, req QueryRequest, a queryMemoryArgs) (map[string]any, error)

// queryMemoryCommands is the query{action:"memory"} sub-command registry.
var queryMemoryCommands = map[string]queryMemoryHandler{
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
func (e *Engine) searchPrelude(ctx context.Context, req QueryRequest, env envelopeArgs) (searchScope, error) {
	if req.Query == "" {
		return searchScope{}, fmt.Errorf("query requires query text")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	resp := map[string]any{"query": req.Query, "limit": limit}
	els, err := e.st.ElementCount(ctx)
	if err != nil {
		// A store that cannot count is an error, not an empty index: the
		// ladder would otherwise answer "no elements indexed" (CX-45).
		return searchScope{}, err
	}
	resp["freshness"] = e.freshness(els)
	// One population rule for every rung: git history rows are hidden unless
	// args.include_git is set (see searchKeep). The semantic rung applies the
	// same rule as a store-side predicate.
	return searchScope{resp: resp, limit: limit, els: els, keep: searchKeep(env.IncludeGit)}, nil
}

func (e *Engine) queryExact(ctx context.Context, req QueryRequest, env envelopeArgs) (map[string]any, error) {
	s, err := e.searchPrelude(ctx, req, env)
	if err != nil {
		return nil, err
	}
	return e.rungExact(ctx, req.Query, s.limit, s.resp, true, s.keep)
}

func (e *Engine) queryFuzzy(ctx context.Context, req QueryRequest, env envelopeArgs) (map[string]any, error) {
	s, err := e.searchPrelude(ctx, req, env)
	if err != nil {
		return nil, err
	}
	return e.rungFuzzy(ctx, req.Query, s.limit, s.resp, true, s.keep)
}

func (e *Engine) querySemantic(ctx context.Context, req QueryRequest, env envelopeArgs) (map[string]any, error) {
	s, err := e.searchPrelude(ctx, req, env)
	if err != nil {
		return nil, err
	}
	return e.rungSemantic(ctx, req.Query, s.limit, s.resp, true, env)
}

// queryLadder is the ladder router: L0 cold, then L1 exact, L2 fuzzy, L3
// semantic, with the Noul confidence gate and the prose route.
func (e *Engine) queryLadder(ctx context.Context, req QueryRequest, env envelopeArgs) (map[string]any, error) {
	s, err := e.searchPrelude(ctx, req, env)
	if err != nil {
		return nil, err
	}
	resp, limit, els, keep := s.resp, s.limit, s.els, s.keep
	// One check per rung boundary: a rung that misses falls through to the
	// next one, so cancellation is checked here instead.
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
	// A store error ends the ladder: it is not a miss to fall through (CX-45).
	if _, err := e.rungExact(ctx, req.Query, limit, resp, false, keep); err != nil {
		return nil, err
	} else if len(hitsOf(resp)) > 0 {
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
		conf, err := ontology.OntologyConfidence(ctx, e.st, req.Query)
		if err != nil {
			return nil, err
		}
		if conf == 0 || conf >= ontology.MinConfidence {
			delete(resp, "guidance")
			return withEmptyHint(e.rungSemantic(ctx, req.Query, limit, resp, true, env))
		}
	}
	// pin=true: the rung knows which keyword arm served the hits, so it owns
	// the reason (an L2 miss is overwritten by L3 below, which always sets one).
	if _, err := e.rungFuzzy(ctx, req.Query, limit, resp, true, keep); err != nil {
		return nil, err
	} else if len(hitsOf(resp)) > 0 {
		return resp, nil
	}
	// Noul L3 gate: ontology confidence below MinConfidence
	// (0.3) justifies no embedding call — degrade to L2.
	conf, err := ontology.OntologyConfidence(ctx, e.st, req.Query)
	if err != nil {
		return nil, err
	}
	if conf > 0 && conf < ontology.MinConfidence {
		return withEmptyHint(e.rungFuzzy(ctx, req.Query, limit, resp, true, keep))
	}
	// The empty L2 pass above left "No keyword match…" guidance; L3 sets its
	// own (an outage reads as an outage, not as a phrasing problem — RS-15).
	delete(resp, "guidance")
	return withEmptyHint(e.rungSemantic(ctx, req.Query, limit, resp, true, env))
}

// graphArgs are the args of the connection verbs: depth (impact and path) and
// to (path). Both are optional; depth stays nil when the caller did not send it,
// because each verb has its own default.
type graphArgs struct {
	envelopeArgs
	Depth *int   `json:"depth"`
	To    string `json:"to"`
}

// graphQuery adapts a connection verb to the registry: the shared envelope,
// then the seed resolution every verb needs, then the verb itself.
func graphQuery(verb graphVerb) actionHandler[QueryRequest] {
	return queryAction(func(e *Engine, ctx context.Context, req QueryRequest, a graphArgs) (map[string]any, error) {
		s, err := e.searchPrelude(ctx, req, a.envelopeArgs)
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
		return verb(e, ctx, req, a, seed, s.resp)
	})
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

func (e *Engine) graphImpact(ctx context.Context, req QueryRequest, a graphArgs, seed string, resp map[string]any) (map[string]any, error) {
	// depth comes from args.depth (Rust --depth parity); limit is a
	// result-count concept, not a traversal depth.
	depth := 2
	if a.Depth != nil {
		depth = *a.Depth
	}
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

func (e *Engine) graphPath(ctx context.Context, req QueryRequest, a graphArgs, seed string, resp map[string]any) (map[string]any, error) {
	toRaw := a.To
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
	maxDepth := 0 // graph default
	if a.Depth != nil {
		maxDepth = *a.Depth
	}
	path, bounds, err := graph.ShortestPath(ctx, e.st, seed, to, maxDepth)
	if errors.Is(err, graph.ErrUnknownNode) {
		// Name the end that is unknown: "graph: unknown node" left the
		// caller guessing which of from/to to fix. And answer, rather
		// than fail: the end that does not resolve is a fact the caller
		// can act on.
		for _, end := range []struct{ label, qn string }{{"from (query)", seed}, {"to (args.to)", to}} {
			els, ferr := e.st.FindExact(ctx, end.qn)
			if ferr != nil {
				return nil, ferr
			}
			if len(els) == 0 {
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

func (e *Engine) graphCallers(ctx context.Context, req QueryRequest, _ graphArgs, seed string, resp map[string]any) (map[string]any, error) {
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

func (e *Engine) graphCallees(ctx context.Context, req QueryRequest, _ graphArgs, seed string, resp map[string]any) (map[string]any, error) {
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

func (e *Engine) graphContext(ctx context.Context, req QueryRequest, _ graphArgs, seed string, resp map[string]any) (map[string]any, error) {
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

func (e *Engine) graphExplain(ctx context.Context, req QueryRequest, _ graphArgs, seed string, resp map[string]any) (map[string]any, error) {
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

// queryMemoryArgs are the args of query{action:"memory"}: the sub-command key
// and the reads that take a path, a cwd or a bank (the envelope carries scope).
type queryMemoryArgs struct {
	envelopeArgs
	Command string `json:"command"`
	Path    string `json:"path"`
	Query   string `json:"query"`
	Cwd     string `json:"cwd"`
	Bank    string `json:"bank"`
}

func (e *Engine) queryMemory(ctx context.Context, req QueryRequest, a queryMemoryArgs) (map[string]any, error) {
	h, ok := queryMemoryCommands[a.Command]
	if !ok {
		return nil, fmt.Errorf("unknown memory read command %q (valid: session_recall, memories, view, snapshot, search)", a.Command)
	}
	return h(e, ctx, req, a)
}

func (e *Engine) querySessionMemory(ctx context.Context, req QueryRequest, a queryMemoryArgs) (map[string]any, error) {
	return e.SessionMemoryRead(ctx, a.Command, req.Query, req.Limit, a.Scope, a.Cwd, a.Bank)
}

// queryMemoryView reads one memory file by path (RS-21: was ConnectRPC-only).
// The path rides args.path; the top-level query is the fallback.
func (e *Engine) queryMemoryView(_ context.Context, req QueryRequest, a queryMemoryArgs) (map[string]any, error) {
	path := a.Path
	if path == "" {
		path = req.Query
	}
	return e.MemoryRead("view", path, "", 0)
}

func (e *Engine) queryMemorySnapshot(_ context.Context, _ QueryRequest, _ queryMemoryArgs) (map[string]any, error) {
	return e.MemoryRead("snapshot", "", "", 0)
}

// queryMemorySearch serves the search command and the bare memory read. The
// search term rides the top-level `query`, not args.query: the tool's own
// schema advertises `query` as the search text, and session_recall takes it
// from the same place.
func (e *Engine) queryMemorySearch(_ context.Context, req QueryRequest, a queryMemoryArgs) (map[string]any, error) {
	term := req.Query
	if term == "" {
		term = a.Query
	}
	return e.MemoryRead("search", "", term, req.Limit)
}

// ontologyArgs are the args of query{action:"ontology"}. command is the
// sub-command key; cmd is its deprecated alias (CX-44).
type ontologyArgs struct {
	envelopeArgs
	Command string `json:"command"`
	Cmd     string `json:"cmd"`
}

// subCommandKey resolves a sub-command key. command is canonical; alias is the
// deprecated `cmd` spelling, read in this one place. It reports whether the
// alias supplied the key, so the caller can say so in the answer.
func subCommandKey(command, alias string) (string, bool) {
	if command != "" {
		return command, false
	}
	return alias, alias != ""
}

// ontologyCommands is the query{action:"ontology"} sub-command registry.
var ontologyCommands = map[string]func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error){
	"":        func(e *Engine, _ context.Context, _ QueryRequest) (map[string]any, error) { return e.OntologyMatches() },
	"matches": func(e *Engine, _ context.Context, _ QueryRequest) (map[string]any, error) { return e.OntologyMatches() },
	"trace": func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error) {
		return ontology.TraceQuery(ctx, e.st, req.Query)
	},
	"status": func(e *Engine, ctx context.Context, _ QueryRequest) (map[string]any, error) {
		st, err := ontology.OntologyStatus(ctx, e.st)
		if err != nil {
			return nil, err
		}
		return map[string]any{"status": st}, nil
	},
	"concept_search": func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error) {
		res, err := ontology.ConceptSearch(ctx, e.st, req.Query, req.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"result": res}, nil
	},
	"feature_flow": func(e *Engine, ctx context.Context, req QueryRequest) (map[string]any, error) {
		return ontology.FeatureFlow(ctx, e.st, req.Query)
	},
	"traceability": func(e *Engine, ctx context.Context, _ QueryRequest) (map[string]any, error) {
		m, err := ontology.TraceabilityMatrix(ctx, e.st)
		if err != nil {
			return nil, err
		}
		return map[string]any{"matrix": m}, nil
	},
}

func (e *Engine) queryOntology(ctx context.Context, req QueryRequest, a ontologyArgs) (map[string]any, error) {
	cmd, alias := subCommandKey(a.Command, a.Cmd)
	// FR-HEA-03: the four full-table cmds (trace/status/feature_flow/
	// traceability) reach Elements() wholesale; on a mega-graph they are
	// refused with the paginated escape hatch named, exactly as Rust's
	// handler returned Ok(refusal). concept_search/matches stay unguarded
	// (KV read / the paginated path itself).
	if ontology.IsFullScanOntologyCmd(cmd) {
		if refusal, err := ontology.RefuseFullScanIfMega(ctx, e.st, "ontology/"+cmd); err != nil {
			return nil, err
		} else if refusal != nil {
			return refusal, nil
		}
	}
	h, ok := ontologyCommands[cmd]
	if !ok {
		return nil, fmt.Errorf("unknown ontology cmd (valid: matches, trace, status, concept_search, feature_flow, traceability)")
	}
	out, err := h(e, ctx, req)
	if err == nil && alias && out != nil {
		out["deprecated"] = deprecatedSubCommandNote
	}
	return out, err
}

func (e *Engine) queryCompress(ctx context.Context, req QueryRequest, a compressArgs) (map[string]any, error) {
	return e.compressRun(req, a)
}

func (e *Engine) queryLanguages(_ context.Context, _ QueryRequest) (map[string]any, error) {
	return e.LanguagesStatus(), nil
}

// lspArgs are the args of query{action:"lsp"}.
type lspArgs struct {
	envelopeArgs
	Lang    string `json:"lang"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

func (e *Engine) queryLSP(ctx context.Context, req QueryRequest, a lspArgs) (map[string]any, error) {
	return e.lspQuery(ctx, req, a, map[string]any{"query": req.Query})
}

// patternArgs are the args of query{action:"pattern"}.
type patternArgs struct {
	envelopeArgs
	Pattern string `json:"pattern"`
	Lang    string `json:"lang"`
}

func (e *Engine) queryPattern(ctx context.Context, req QueryRequest, a patternArgs) (map[string]any, error) {
	return e.patternQuery(ctx, req, a, map[string]any{"query": req.Query})
}

// sessionReadArgs are the args of query{action:"session"}.
type sessionReadArgs struct {
	envelopeArgs
	Command string `json:"command"`
	NodeID  string `json:"node_id"`
}

func (e *Engine) querySession(_ context.Context, req QueryRequest, a sessionReadArgs) (map[string]any, error) {
	return e.SessionRead(a.Command, req.Query, a.NodeID)
}

func (e *Engine) queryPRD(ctx context.Context, req QueryRequest) (map[string]any, error) {
	rows, err := prdindex.Trace(ctx, e.st, req.Query)
	if err != nil {
		return nil, err
	}
	return map[string]any{"requirements": rows, "count": len(rows)}, nil
}

// incidentArgs are the args of query{action:"incidents"}.
type incidentArgs struct {
	envelopeArgs
	Service string `json:"service"`
	Pattern string `json:"pattern"`
	Env     string `json:"env"`
}

func (e *Engine) queryIncidents(_ context.Context, req QueryRequest, a incidentArgs) (map[string]any, error) {
	k := orgknowledge.New(e.st)
	incidents, err := k.QueryIncidents(a.Service, a.Pattern, a.Env, req.Limit)
	if err != nil {
		return nil, err
	}
	if incidents == nil {
		incidents = []store.Incident{}
	}
	return map[string]any{"incidents": incidents, "query": map[string]any{
		"service": a.Service, "pattern": a.Pattern,
		"env": a.Env, "limit": req.Limit,
	}}, nil
}

// serviceArgs are the args of the environment and service reads.
type serviceArgs struct {
	envelopeArgs
	Service string `json:"service"`
}

func (e *Engine) queryEnvConflicts(_ context.Context, _ QueryRequest, a serviceArgs) (map[string]any, error) {
	conflicts, err := orgknowledge.New(e.st).FindEnvConflicts(a.Service)
	if err != nil {
		return nil, err
	}
	return map[string]any{"conflicts": conflicts, "service": a.Service}, nil
}

// serviceContextArgs are the args of query{action:"service_context"}.
type serviceContextArgs struct {
	envelopeArgs
	Service string `json:"service"`
	Env     string `json:"env"`
}

func (e *Engine) queryServiceContext(ctx context.Context, _ QueryRequest, a serviceContextArgs) (map[string]any, error) {
	return orgknowledge.New(e.st).ServiceContextJSON(ctx, a.Service, a.Env)
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

// prdArgs are the args of import{action:"prd"}.
type prdArgs struct {
	Environment string `json:"environment"`
}

func (e *Engine) importPRD(ctx context.Context, req ImportRequest, a prdArgs) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import prd requires path (a PRD markdown document)")
	}
	env := a.Environment
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
		stats, err := ontology.LoadWorkflows(ctx, e.st, opath)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ontology": "synced", "stats": stats}, nil
	}
	return e.OntologyMatch(ctx, opath)
}

// importMissingAction is the refusal for an import with no action.
func importMissingAction(_ *Engine, _ context.Context, _ ImportRequest) (map[string]any, error) {
	return nil, fmt.Errorf("import requires action (repo, dir, docs, prd, git, memory, session, ontology, read)")
}

// importCompressRead serves import{action:"read"}: the path is required here,
// and the reader's args are the typed compressReadArgs.
func importCompressRead(e *Engine, _ context.Context, req ImportRequest, a compressReadArgs) (map[string]any, error) {
	if req.Path == "" {
		return nil, fmt.Errorf("import read requires path")
	}
	return e.compressRead(req.Path, a)
}

// memoryArgs are the args of import{action:"memory"}. Every memory command reads
// its keys from here. withFlatArgs folds the flat curation fields in first, so
// a schema-shaped call (content at the top level) and an args-shaped call read
// the same keys; the target file rides both `path` and `file` for the same reason.
type memoryArgs struct {
	Path                    string   `json:"path"`
	File                    string   `json:"file"`
	Content                 string   `json:"content"`
	Old                     string   `json:"old"`
	New                     string   `json:"new"`
	Text                    string   `json:"text"`
	Payload                 string   `json:"payload"`
	Summary                 string   `json:"summary"`
	SessionID               string   `json:"session_id"`
	NodeID                  string   `json:"node_id"`
	NewPath                 string   `json:"new_path"`
	InsertLine              int      `json:"insert_line"`
	DocumentID              string   `json:"document_id"`
	Bank                    string   `json:"bank"`
	Scope                   string   `json:"scope"`
	Cwd                     string   `json:"cwd"`
	Tags                    []string `json:"tags"`
	Turns                   []string `json:"turns"`
	RetainedThroughUserTurn int      `json:"retained_through_user_turn"`
}

// memoryWrite dispatches import{action:"memory"} on its command.
func (e *Engine) memoryWrite(ctx context.Context, req ImportRequest, a memoryArgs) (map[string]any, error) {
	if e.mem == nil {
		return nil, fmt.Errorf("memory not initialized")
	}
	h, ok := memoryWriteCommands[req.Command]
	if !ok {
		return nil, fmt.Errorf("unknown memory command %q (valid: create, str_replace, insert, delete, delete_document, rename, add, replace, remove, session_retain)", req.Command)
	}
	return h(e, ctx, req, a)
}

// memFileEdit serves the memory commands that edit one file and report only ok.
func memFileEdit(op func(e *Engine, a memoryArgs) error) memoryWriteHandler {
	return func(e *Engine, _ context.Context, req ImportRequest, a memoryArgs) (map[string]any, error) {
		if err := op(e, a); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "command": req.Command}, nil
	}
}

func (e *Engine) memSessionRetain(ctx context.Context, req ImportRequest, a memoryArgs) (map[string]any, error) {
	scope, serr := memory.ParseScope(a.Scope)
	if serr != nil {
		return nil, serr
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = e.projectDir
	}
	// K2: tags are first-class on the retain path, so a tag-scoped recall
	// can match what a conversation wrote. The typed list accepts a
	// comma-separated string too ("a,b"), which is the shape an agent most
	// often sends when the schema names one field.
	res, serr := e.mem.SessionRetain(ctx, scope, cwd, a.Bank, a.SessionID, a.Turns, a.RetainedThroughUserTurn, a.Tags)
	if serr != nil {
		return nil, serr
	}
	return map[string]any{"ok": true, "command": "session_retain", "bank": res.Bank, "written": res.Written, "skipped": res.Skipped, "retained_through_user_turn": res.RetainedThroughUserTurn}, nil
}

// memCreate keeps the memory-tool contract (create or overwrite), but an
// overwrite is reported, never silent.
func (e *Engine) memCreate(_ context.Context, req ImportRequest, a memoryArgs) (map[string]any, error) {
	existed := e.mem.Exists(a.Path)
	if err := e.mem.Create(a.Path, a.Content); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "command": req.Command, "overwrote": existed}, nil
}

// memDeleteDocument is the bank-row delete. The REST mount had it (DELETE
// /v1/default/banks/{bank}/documents/{id}) and the MCP surface did not, so an
// MCP client could never remove a memory row.
func (e *Engine) memDeleteDocument(ctx context.Context, _ ImportRequest, a memoryArgs) (map[string]any, error) {
	docID := strings.TrimSpace(a.DocumentID)
	if docID == "" {
		return nil, fmt.Errorf("memory: delete_document requires a document_id")
	}
	scope, serr := memory.ParseScope(a.Scope)
	if serr != nil {
		return nil, serr
	}
	cwd := a.Cwd
	if cwd == "" {
		cwd = e.projectDir
	}
	bankName := e.mem.WriteBank(scope, cwd, a.Bank)
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

// sessionWriteArgs are the args of import{action:"session"}.
type sessionWriteArgs struct {
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	Payload   string `json:"payload"`
	Summary   string `json:"summary"`
	Text      string `json:"text"`
}

// sessionWriteHandler serves one import{action:"session"} command.
type sessionWriteHandler func(e *Engine, ctx context.Context, s *session.Store, a sessionWriteArgs) (map[string]any, error)

// sessionWriteCommands is the import{action:"session"} sub-command registry.
var sessionWriteCommands = map[string]sessionWriteHandler{
	"offload": (*Engine).sessionOffload,
	"lesson":  (*Engine).sessionLesson,
}

// sessionWrite wires internal/session into the 4-tool surface: bulky tool
// payloads are offloaded to .leankg/sessions/<id>/refs/<node>.md and restored
// bit-for-bit by session_recall (Rust session/mod.rs parity).
func (e *Engine) sessionWrite(ctx context.Context, req ImportRequest, a sessionWriteArgs) (map[string]any, error) {
	if e.projectDir == "" {
		return nil, fmt.Errorf("session offload requires a project directory")
	}
	h, ok := sessionWriteCommands[req.Command]
	if !ok {
		return nil, fmt.Errorf("unknown session command %q (valid: offload, lesson)", req.Command)
	}
	return h(e, ctx, session.New(e.projectDir), a)
}

func (e *Engine) sessionOffload(_ context.Context, s *session.Store, a sessionWriteArgs) (map[string]any, error) {
	ref, err := s.Offload(a.SessionID, a.NodeID, []byte(a.Payload), a.Summary)
	if err != nil {
		return nil, err
	}
	return map[string]any{"offloaded": e.relRef(ref)}, nil
}

// sessionLesson stores one lesson. The import schema advertises `summary` for
// this arm and `text` for the lesson itself; text wins, and an empty lesson is
// refused rather than stored (see the measured incident in the CX-44 evidence
// and the earlier fix this keeps).
func (e *Engine) sessionLesson(ctx context.Context, s *session.Store, a sessionWriteArgs) (map[string]any, error) {
	text := a.Text
	if strings.TrimSpace(text) == "" {
		text = a.Summary
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("session lesson requires text (or summary) — an empty lesson is unrecallable")
	}
	deduped, err := s.AddLessonCtx(ctx, a.SessionID, text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deduped": deduped}, nil
}
