// Package mcp wires the core engine into the Model Context Protocol using
// the official go-sdk: stdio (local coding tools) and streamable HTTP
// (shared servers). The registry is EXACTLY three tools — import, query,
// status (legacy set/get were renamed; the PRD deprecation policy applies to
// the action namespace, this is the v0.31 tool-name cutover).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProjectRouter resolves a per-request project selector to an engine
// (multi-project serving: LEANKG_PROJECT_DIRS). Implemented by
// internal/projects; nil keeps the single-engine behavior.
type ProjectRouter interface {
	EngineFor(ctx context.Context, project string) (*core.Engine, error)
	// MultiProject reports whether more than one project is registered. With a
	// single project the default engine is unambiguous, so an omitted
	// `project` argument is not an error — a stdio spawn is one agent session
	// over one project and must keep working.
	MultiProject() bool
}

// Server wraps the go-sdk server over one core engine. When router is set,
// each tool call may name a project (args.project) and is served by that
// project's engine; otherwise the wrapped engine answers.
type Server struct {
	engine *core.Engine
	router ProjectRouter
	// scoped marks a handler that is ALREADY serving one project (the
	// routeByProject builder for a per-request ?project= selector, or the
	// stdio server, whose process was spawned for one repository). Such a
	// handler must not fail closed when a call omits the project argument:
	// the routing layer already answered that question, and asking again made
	// every ?project= URL on the MCP listener unusable (measured live on the
	// 298-project self-host). See TestScopedServerDoesNotReAskForProject.
	scoped bool
	// projectRequired marks the multi-project surface: the tool schemas
	// require the `project` argument, so a client is told which project a
	// call is about BEFORE it makes one.
	projectRequired bool
	srv             *mcp.Server
	authStore       store.Backend // token store for HTTP callers; nil = env tokens only

	// Capture (plan v4.15 DS-04/DS-05). rec defaults to telemetry.Nop; stdio
	// is the identity of the one stdio session, guarded by stdioMu.
	rec             telemetry.Recorder
	stdioMu         sync.Mutex
	stdio           telemetry.Identity
	sessionByClient map[string]string
}

// SetAuthStore makes the HTTP handler honor DB-minted tokens (`leankg auth
// token create`) as well as LEANKG_TOKEN_* env tokens. Without it a token
// minted in the store gated REST and ConnectRPC but left MCP open as Admin
// (RS-03). serve passes the default project's store, the one REST uses.
func (s *Server) SetAuthStore(st store.Backend) { s.authStore = st }

// SetProjectRouter enables per-call project routing. The router resolves a
// directory path or project name and errors on unknown selectors (never
// silently falls back to the default project).
func (s *Server) SetProjectRouter(r ProjectRouter) { s.router = r }

// SetProjectScoped marks this Server as already scoped to one project, so a
// call that omits the project argument is served by the engine it was built
// with instead of being refused (cmd's routeByProject is what supplies that
// engine). The argument, when present, still selects.
func (s *Server) SetProjectScoped(v bool) { s.scoped = v }

// Option configures a Server at construction time.
type Option func(*Server)

// WithProjectRequired marks the surface as multi-project: the tool schemas
// then REQUIRE the `project` argument.
//
// The server refuses a call that omits it (engineFor fails closed — an
// omitted project used to be answered from the serve process's cwd engine,
// so a question about another project got a confident answer built from the
// WRONG graph). That refusal used to reach the caller only as a runtime
// error, AFTER the call: the model had already been told, by the schema,
// that `project` was optional. Measured live on the 298-project self-host,
// an agent's very first `status` call went out with no arguments and came
// back LEANKG_ERROR_MISSING_PARAM.
//
// Requiring the argument in the schema puts the rule where the model reads
// it. The server still fails closed if a call arrives without one.
//
// The same flag also picks the agent protocol this surface advertises
// (instructions): a multi-project server says the project is REQUIRED, a
// server that serves one project says it is fixed. The SDK copies the
// options at construction, so the surface policy has to be known then.
func WithProjectRequired(v bool) Option {
	return func(s *Server) { s.projectRequired = v }
}

// engineFor picks the engine for one call: args.project when a router is
// configured, else the wrapped engine.
func (s *Server) engineFor(ctx context.Context, req *mcp.CallToolRequest) (*core.Engine, error) {
	if s.router == nil {
		return s.engine, nil
	}
	var args map[string]any
	if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
	}
	// Fail closed: with several projects registered, an omitted `project` used
	// to fall back to the serve process's cwd engine, so a question about
	// another project got a confident answer built from the WRONG graph
	// (measured live on a 298-project host: a term absent from the served
	// project still returned 10 of its hits). The caller's question is
	// unanswerable without knowing which graph to search, so we refuse.
	p, _ := args["project"].(string)
	if p == "" {
		if s.scoped || !s.router.MultiProject() {
			// One project registered: the wrapped engine is the only answer, and
			// an agent that never learned about `project` must keep working.
			return s.engine, nil
		}
		return nil, errs.NewError(errs.MissingParam,
			"this server serves multiple projects (LEANKG_PROJECT_DIRS), so a tool call must name one",
			"add \"project\": \"<repo dir or name>\" to the tool arguments, or from inside a repo run `leankg query …` against that project's own server")
	}
	return s.router.EngineFor(ctx, p)
}

// version is reported as the MCP serverInfo version. It must match the release
// stamp in cmd/leankg/VERSION — release-please bumps both via their
// x-release-please-version markers; version_test.go fails on drift.
const version = "0.35.0" // x-release-please-version

// serverInstructions is returned during MCP initialization. MCP clients may
// add this to the model's system prompt; the shorter toolGuidance below keeps
// the same rules visible to clients that only render tools/list.
//
// The project clause is per SURFACE (projectRuleRequired / projectRuleFixed).
// One text that hedged both — "project= is required only on multi-project
// HTTP servers" — left the model to work out whether THIS server was one,
// which is the inference it cannot make: it sees the tool list, not the
// server's environment. Measured on the 286-project self-host, an agent's
// first call went out with no arguments because the sentence read as a
// conditional that did not apply to it.
const serverInstructionsTemplate = `LeanKG is a code knowledge graph (exactly 3 tools: import, query, status). Agent protocol:
1. For code discovery, call query before bash/grep/read. Leave action empty (or use search) for the L1 exact -> L2 fuzzy -> L3 semantic ladder; pin exact/fuzzy/semantic only when that rung is intentional. Once you have a qualified name, prefer impact/path/callers/callees/context over re-grepping.
2. %s
3. Inspect retrieval{rung,reason}, guidance (when present), and freshness. If a store is cold, import it once with action=repo, path=<absolute repository path>, then query; do not re-import on every turn. On zero hits, follow guidance instead of abandoning LeanKG for bash.
4. Importing indexes elements but does not create vectors. If L3 is required, run ` + "`leankg-embed run`" + ` (or ` + "`leankg-embed full`" + ` on stamp drift); a degraded L3 is not proof that the query ladder is broken.
5. Use status for health, freshness, resolved project_dir, and embedding coverage — not as a substitute for query.
6. Keep context across sessions: at session start call query action=memory with args.command=session_recall (or memories). At session end call import action=memory command=session_retain with session_id + turns[] (args.scope=per-project|global|per-project-tagged, args.cwd optional); durable lessons use import action=session command=lesson with session_id + text.`

// projectRuleRequired is the project clause for a server that serves several
// projects. The tool schema already requires the argument there; the clause
// says how to fill it in — the absolute path of the repository the caller is
// working in, which is what the server can resolve.
const projectRuleRequired = `PROJECT REQUIRED: this server serves several repositories and cannot guess which one a question is about, so EVERY tool call must name one — "project" is the absolute path of the repository you are working in (a path inside a registered project resolves to that project) or the project's name. A call that omits it is refused, and no answer is ever built from another project's store.`

// projectRuleFixed is the project clause for a server that already serves one
// project: a single-project server, a stdio session, or a ?project= handler.
// The argument stays optional there, and passing one selects a DIFFERENT
// project than the one this session serves.
const projectRuleFixed = `PROJECT FIXED: this server already serves one project, so every call may omit "project" — pass it only to deliberately select another registered project.`

// instructions renders the agent protocol for THIS surface. The SDK copies
// the options at construction, so the surface policy has to be known then
// (see WithProjectRequired).
func (s *Server) instructions() string {
	rule := projectRuleFixed
	if s.projectRequired {
		rule = projectRuleRequired
	}
	return strings.Replace(serverInstructionsTemplate, "%s", rule, 1)
}

const toolGuidance = "Agent protocol: query before bash/grep; project= only on multi-project HTTP; import once when cold; inspect retrieval/freshness/guidance; session_recall at start, session_retain (action=memory) and lesson (action=session) at end (memory scope=per-project|global|per-project-tagged); do not pin a rung unless intentional."

// New builds the MCP server with the 3-tool registry.
func New(engine *core.Engine, opts ...Option) *Server {
	s := &Server{engine: engine, rec: telemetry.Nop{}}
	for _, opt := range opts {
		opt(s)
	}
	s.initStdioIdentity(os.Getenv)
	s.srv = mcp.NewServer(&mcp.Implementation{
		Name: "leankg", Version: version,
		Description: "LeanKG code knowledge graph (3 tools: import/query/status): query for code discovery and memory recall; import for indexing and session_retain.",
	}, &mcp.ServerOptions{Instructions: s.instructions()})
	s.registerTools()
	// capture stays outermost: it sees name-resolution failures (DS-04).
	s.srv.AddReceivingMiddleware(s.capture, resolveToolNames)
	return s
}

// resolveToolNames routes every tools/call through core.ResolveEnvelope
// (RS-22): the legacy names `get`/`set` reach query/import, and an unknown
// name answers the catalog error that lists the valid tools instead of the
// SDK's bare "unknown tool". The alias map was declared but no call site
// read it, so the aliasing its comment promised never existed.
func resolveToolNames(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
			canonical, err := core.ResolveEnvelope(call.Params.Name)
			if err != nil {
				return nil, err
			}
			call.Params.Name = canonical
		}
		return next(ctx, method, req)
	}
}

// RunStdio serves MCP over stdin/stdout (blocks until the client disconnects).
func (s *Server) RunStdio(ctx context.Context) error {
	return s.srv.Run(ctx, &mcp.StdioTransport{})
}

// HTTPHandler returns the streamable-HTTP MCP handler (mount under /mcp)
// wrapped in RBAC: the role is resolved from the Authorization header and
// enforced per tool call inside the handlers (MCP carries capability in the
// JSON-RPC body, so path middleware cannot see it).
func (s *Server) HTTPHandler() http.Handler {
	// Stateless: true is required, not an optimization. go-sdk refuses protocol
	// >= 2026-07-28 on any stateful HTTP server, and that version arrives in the
	// Mcp-Protocol-Version HEADER — so a compliant client (DSH) gets HTTP 400
	// without this. It also drops per-session state, which suits a read-mostly
	// query server holding no session-bound resources. Pinned by
	// TestMCPNewProtocolVersionOverHTTP.
	inner := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.srv },
		// PropagateRequestCancellation (RS-02): a client that gives up on a
		// long import cancels the handler instead of leaving the walk running
		// (go-sdk applies it to the >= 2026-07-28 protocol only; older
		// clients rely on the index-root guard to bound an import).
		&mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, err := auth.RoleForRequestWithStore(s.authStore, r)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		ctx := withRole(r.Context(), role)
		if s.recorder().Level() != telemetry.Off {
			ctx = telemetry.WithIdentity(ctx, identityFromHeaders(r), telemetry.TransportHTTP)
		}
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxKey int

const roleKey ctxKey = 0

func withRole(ctx context.Context, role auth.Role) context.Context {
	return context.WithValue(ctx, roleKey, role)
}

// roleOf returns the caller role (Admin when ungated, which is the local
// default; stdio transport is inherently local).
func roleOf(ctx context.Context) auth.Role {
	if r, ok := ctx.Value(roleKey).(auth.Role); ok {
		return r
	}
	return auth.Admin
}

// allowTool enforces RBAC per tool call (MCP body capability).
func allowTool(ctx context.Context, tool string) error {
	if !auth.AllowedTool(roleOf(ctx), tool) {
		return errs.NewError(errs.PermissionDenied,
			fmt.Sprintf("forbidden: tool %q requires contributor or admin role", tool), "")
	}
	return nil
}

// textResult marshals v as the tool's JSON text content.
func textResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
}

// projectPropertyOptional is the `project` property as a single-project
// server advertises it: present, but not required.
//
// projectPropertyRequired is the multi-project form. The server refuses a
// call that omits the project (engineFor fails closed), so on that surface
// the property is REQUIRED and the description says what to pass — the
// model reads the schema BEFORE it makes the call, which is the only place
// the rule can still be followed.
const (
	projectPropertyOptional = `"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}`
	projectPropertyRequired = `"project": {"type": "string", "description": "REQUIRED on a multi-project server: the repository this call is about — a directory path or a project name (e.g. the current repository). The server refuses to guess which project a question is about, and naming one is what keeps the answer out of another project's graph."}`
)

// schema applies the server's project policy to one tool's advertised input
// schema: on a multi-project server the `project` property becomes required.
//
// The alternative — leaving it optional and letting a call fail at runtime —
// is what this replaces. Measured live on the 298-project self-host: an
// agent's first `status` call carried no arguments, the schema said
// `project` was optional, and the call came back
// LEANKG_ERROR_MISSING_PARAM. The rule was in the error and in the server
// instructions; it was NOT in the contract the model reads before calling.
func (s *Server) schema(raw string) json.RawMessage {
	prop := projectPropertyOptional
	if s.projectRequired {
		prop = projectPropertyRequired
	}
	out := strings.ReplaceAll(raw, projectPropertyOptional, prop)
	if s.projectRequired {
		out = strings.ReplaceAll(out, `"required": []`, `"required": ["project"]`)
	}
	return json.RawMessage(out)
}

func (s *Server) registerTools() {
	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolImport,
		Description: "Import content into LeanKG: index a repository or directory " +
			"of repositories (action=repo|dir, path), or curate agent memory " +
			"(action=memory, command=create|str_replace|insert|delete|rename|add|replace|remove; create overwrites an existing file). " +
			"Legacy tool name 'set' is superseded by this tool. " +
			"Use action=dir with path=\".\" to import the current directory as a scoped index target (FR-P2). " +
			"The store is <project>/.leankg/leankg.db unless LEANKG_DB_PATH or leankg.yaml db.standalone_db_path " +
			"names another file — in that case EVERY verb (import, query, status, doctor, leankg-embed) reads THAT " +
			"store and no store appears under the project, so the path you import does not decide where the data lives; " +
			"status reports it in the store field. " +
			"Import only for first-time indexing or deliberate updates; indexing does not create vectors. " + toolGuidance,
		InputSchema: s.schema(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["repo", "dir", "docs", "prd", "memory", "session", "ontology", "read"], "description": "what to import (prd = index a PRD markdown document; read = compressed file read: args.mode/lines/fresh)"},
				"path": {"type": "string", "description": "repository/directory to index, or memory file path (e.g. MEMORY.md, topics/x.md)"},
				"command": {"type": "string", "enum": ["create", "str_replace", "insert", "delete", "rename", "add", "replace", "remove", "session_retain", "offload", "lesson"], "description": "memory write command (action=memory; session_retain carries transcript turns in args), or session command (action=session: offload|lesson)"},
				"content": {"type": "string"},
				"old": {"type": "string"},
				"new": {"type": "string"},
				"insert_line": {"type": "integer"},
				"new_path": {"type": "string"},
				"file": {"type": "string", "description": "memory file for add/replace/remove"},
				"text": {"type": "string"},
				"session_id": {"type": "string", "description": "session id (action=session; also action=memory command=session_retain)"},
				"node_id": {"type": "string", "description": "offload node id (action=session)"},
				"payload": {"type": "string", "description": "payload to offload (action=session)"},
				"summary": {"type": "string", "description": "offload summary (action=session)"},
				"tags": {"type": "array", "items": {"type": "string"}, "description": "tags for action=memory command=session_retain (also accepted inside args.tags, or as one comma-separated string); persisted on the row so a later tag-scoped recall can match it"},
				"args": {"type": "object", "description": "action params: turns[]/session_id/retained_through_user_turn/tags[]/scope/cwd/bank (memory session_retain); content/old/new (memory curation); mode/lines/fresh (read). For query: scope=all (default; the whole corpus, test fixtures and archived docs included) | code (production source only) | prod (code plus current docs) picks the population to RANK; an unknown value is an error; every answer reports retrieval.scope"},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			},
			"required": []
		}`),
	}, s.handleImport)

	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolQuery,
		Description: "Query LeanKG. Empty action routes down the ladder: L1 exact " +
			"identifier → L2 fuzzy keyword → L3 semantic (vectors). Every answer carries " +
			"retrieval{rung,reason} + freshness. action=memory searches agent memory (args.command=view with args.path reads one file, =snapshot the MEMORY.md/USER.md core); " +
			"action=exact|fuzzy|semantic pins a rung; graph verbs (impact/path/callers/callees/context/explain) and session/ontology reads are also available. " +
			"Leave action empty/search for semantic questions; legacy tool name 'get' is superseded. " + toolGuidance,
		InputSchema: s.schema(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "search text, identifier, or memory search text"},
				"action": {"type": "string", "enum": ["search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain", "memory", "session", "ontology", "prd", "incidents", "env_conflicts", "service_context", "portfolio", "pattern", "languages", "lsp", "compress"], "description": "empty = ladder router (L0-L3). Actions that NEED another argument (they error without it): path → args.to (target qualified name); pattern → args.pattern (AST pattern, e.g. \"func $F\"); lsp → args.lang (server language); service_context and env_conflicts → args.service; portfolio → args.cmd=summary for the T0 manifest, args.action pins each child's action. impact reads its traversal DEPTH from args.depth. ontology reads args.cmd (matches|trace|status|concept_search|feature_flow|traceability); org reads take args.pattern/args.env for filtering."},
				"limit": {"type": "integer", "description": "max hits for search and the graph verbs (default 10; impact DEPTH comes from args.depth). This is the field those actions read; args.limit applies only to the full-scan tools pattern/lsp, and when both are sent this one wins."},
				"args": {"type": "object", "description": "action params. scope: search — code|prod|all, the corpus to rank (default is everything, including test fixtures and archived docs; retrieval.scope reports it). weight: search — \"<vector>,<keyword>\" fusion weights, e.g. \"3,1\" trusts the vector arm more, \"1,0\" drops the keyword arm; unset is the engine's default (vector:keyword = 3:1) and retrieval.weight reports it. cmd: ontology — one of matches|trace|status|concept_search|feature_flow|traceability. command: session — recall|canvas|lessons (node_id for recall). command: memory — session_recall|memories (plus scope/cwd/bank). main: memory — the query to search. depth: impact (traversal depth), path (max hops). to: path (target qualified name). pattern: pattern (AST pattern, e.g. \"func $F\"). lang: lsp (server language). mode/lines/fresh: read. cmd: portfolio (summary selects the T0 manifest); action: portfolio (pins each child's action); tool/response: compress. limit: pattern/lsp — these full-scan tools read args.limit, while search and the graph verbs read the TOP-LEVEL limit; sending both gives the top-level one precedence."},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			},
			"required": []
		}`),
	}, s.handleQuery)

	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolStatus,
		Description: "LeanKG health: inventory, freshness (fresh|possibly_stale|cold), watermark, backend, embeddings state (stamped models, vectors), last embed run. " +
			"Use status for health and coverage, not as a substitute for query. " + toolGuidance,
		InputSchema: s.schema(`{"type": "object", "properties": {"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}}, "required": []}`),
	}, s.handleStatus)
}

// metricEval is the per-call token accounting finishCall derives once and
// shares with the context_metrics row and the capture scratch.
type metricEval struct {
	baseline, saved, postTokens int64
}

// recordMetric persists one context_metrics row per served tool call (Rust
// mcp/handler.rs records the same fields at the end of its tool dispatch).
// post is the delivered (post-budget) output, so OutputTokens and the
// elements count match what the client received (plan DS-07). Best-effort by
// contract: a ledger write failure must never fail the call.
func (s *Server) recordMetric(eng *core.Engine, req *mcp.CallToolRequest, started time.Time, post any, err error, ev metricEval) {
	if eng == nil || req == nil {
		return
	}
	args := req.Params.Arguments // raw JSON; Rust sized the same bytes: len/4
	m := store.Metric{
		ToolName:        req.Params.Name,
		Timestamp:       time.Now().Unix(),
		ProjectPath:     eng.ProjectDir(),
		InputTokens:     int64(len(args) / 4),
		ExecutionTimeMs: time.Since(started).Milliseconds(),
		Success:         err == nil,
		BaselineTokens:  ev.baseline,
		TokensSaved:     ev.saved,
	}
	if ev.baseline > 0 {
		m.SavingsPercent = float64(ev.saved) * 100 / float64(ev.baseline)
	}
	if err == nil {
		m.OutputTokens = ev.postTokens
		m.OutputElements = int64(countResponseElements(post))
	}
	m.QueryPattern, m.QueryFile, m.QueryDepth = metricQueryArgs(args)
	if rerr := eng.Store().RecordMetric(m); rerr != nil {
		log.Printf("mcp: record metric %s: %v", req.Params.Name, rerr)
	}
}

// countResponseElements ports Rust's count_response_elements: an array counts
// its items, an object counts the leaves of its values, anything else is one.
func countResponseElements(v any) int {
	switch t := v.(type) {
	case []any:
		return len(t)
	case map[string]any:
		n := 0
		for _, item := range t {
			n += countResponseElements(item)
		}
		return n
	default:
		return 1
	}
}

// metricQueryArgs reads the query/file/depth fields Rust recorded. The Go
// envelope carries depth inside the nested "args" object (QueryRequest.Args), so
// the nested form is honoured after the top-level one.
func metricQueryArgs(raw json.RawMessage) (pattern, file string, depth int64) {
	var top map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &top) != nil {
		return "", "", 0
	}
	read := func(key string) json.RawMessage { return top[key] }
	if nested, ok := top["args"]; ok {
		var inner map[string]json.RawMessage
		if json.Unmarshal(nested, &inner) == nil {
			read = func(key string) json.RawMessage {
				if v, ok := inner[key]; ok {
					return v
				}
				return top[key]
			}
		}
	}
	_ = json.Unmarshal(read("query"), &pattern)
	_ = json.Unmarshal(read("file"), &file)
	_ = json.Unmarshal(read("depth"), &depth)
	return pattern, file, depth
}

// enforceBudget applies the FR-GF tool token budget to a tool response before
// it is returned. The envelope tool names (query/import) are uncapped in the
// budget table on purpose — the cap belongs to the caller's chosen action
// (QueryRequest.Action, the legacy Rust tool-name space), so an envelope hit
// must never re-truncate an already-compliant action response.
// budget.Apply attaches the `_token_budget` marker to map[string]any payloads;
// typed engine payloads are round-tripped through JSON to keep the marker.
func enforceBudget(v any, action string) any {
	res, _ := enforceBudgetStats(v, action)
	return res
}

// enforceBudgetStats is enforceBudget that also returns the trim statistics
// (pre-truncation size, delivered size) the capture layer records (DS-07).
func enforceBudgetStats(v any, action string) (any, budget.Stats) {
	key := action
	if key == "" {
		// The ladder router answers search hits (L1/L2/L3), so it carries the
		// search cap; the uncapped `query` envelope row let a ladder answer
		// grow without bound while the same hits pinned to semantic were cut.
		key = "search"
	}
	if m, ok := v.(map[string]any); ok {
		return budget.TokenBudget{}.Apply(m, key)
	}
	if raw, err := json.Marshal(v); err == nil {
		var round map[string]any
		if json.Unmarshal(raw, &round) == nil {
			return budget.TokenBudget{}.Apply(round, key)
		}
	}
	return budget.TokenBudget{}.Apply(v, key)
}

func (s *Server) handleImport(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	started := time.Now()
	if err := allowTool(ctx, core.ToolImport); err != nil {
		return nil, err
	}
	var in core.ImportRequest
	if err := unmarshalArgs(req, &in); err != nil {
		return nil, err
	}
	eng, err := s.engineFor(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := eng.Import(ctx, in)
	if err != nil {
		s.finishCall(ctx, eng, req, started, in.Action, out, nil, budget.Stats{}, err)
		return nil, err
	}
	res, st := enforceBudgetStats(out, in.Action)
	s.finishCall(ctx, eng, req, started, in.Action, out, res, st, nil)
	return textResult(res)
}

func (s *Server) handleQuery(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	started := time.Now()
	var in core.QueryRequest
	if err := unmarshalArgs(req, &in); err != nil {
		return nil, err
	}
	eng, err := s.engineFor(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := eng.Query(ctx, in)
	if err != nil {
		s.finishCall(ctx, eng, req, started, in.Action, out, nil, budget.Stats{}, err)
		return nil, err
	}
	res, st := enforceBudgetStats(out, in.Action)
	s.finishCall(ctx, eng, req, started, in.Action, out, res, st, nil)
	return textResult(res)
}

func (s *Server) handleStatus(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	started := time.Now()
	eng, err := s.engineFor(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := eng.Status(ctx)
	if err != nil {
		s.finishCall(ctx, eng, req, started, "", out, nil, budget.Stats{}, err)
		return nil, err
	}
	s.finishCall(ctx, eng, req, started, "", out, out, budget.Stats{}, nil)
	return textResult(out)
}

// unmarshalArgs decodes tool arguments from the raw JSON params.
func unmarshalArgs(req *mcp.CallToolRequest, into any) error {
	raw, err := json.Marshal(req.Params.Arguments)
	if err != nil {
		return fmt.Errorf("mcp: encode args: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("mcp: decode args: %w", err)
	}
	return nil
}

var _ = log.Printf
