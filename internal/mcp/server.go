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
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ProjectRouter resolves a per-request project selector to an engine
// (multi-project serving: LEANKG_PROJECT_DIRS). Implemented by
// internal/projects; nil keeps the single-engine behavior.
type ProjectRouter interface {
	EngineFor(ctx context.Context, project string) (*core.Engine, error)
}

// Server wraps the go-sdk server over one core engine. When router is set,
// each tool call may name a project (args.project) and is served by that
// project's engine; otherwise the wrapped engine answers.
type Server struct {
	engine *core.Engine
	router ProjectRouter
	srv    *mcp.Server
}

// SetProjectRouter enables per-call project routing. The router resolves a
// directory path or project name and errors on unknown selectors (never
// silently falls back to the default project).
func (s *Server) SetProjectRouter(r ProjectRouter) { s.router = r }

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
	p, _ := args["project"].(string)
	if p == "" {
		return s.engine, nil
	}
	return s.router.EngineFor(ctx, p)
}

// version is reported as the MCP serverInfo version. It must match the release
// stamp in cmd/leankg/VERSION — release-please bumps both via their
// x-release-please-version markers; version_test.go fails on drift.
const version = "0.34.0" // x-release-please-version

// serverInstructions is returned during MCP initialization. MCP clients may
// add this to the model's system prompt; the shorter toolGuidance below keeps
// the same rules visible to clients that only render tools/list.
const serverInstructions = `LeanKG is a code knowledge graph (exactly 3 tools: import, query, status). Agent protocol:
1. For code discovery, call query before bash/grep/read. Leave action empty (or use search) for the L1 exact -> L2 fuzzy -> L3 semantic ladder; pin exact/fuzzy/semantic only when that rung is intentional. Once you have a qualified name, prefer impact/path/callers/callees/context over re-grepping.
2. project= is required only on multi-project HTTP servers (LEANKG_PROJECT_DIRS / ?project=). Stdio and single-project serve resolve from the server process cwd — omit project there. Pass project=<repo basename or absolute path> when the server hosts more than one repo; never assume another project's store.
3. Inspect retrieval{rung,reason}, guidance (when present), and freshness. If a store is cold, import it once with action=repo, path=<absolute repository path>, then query; do not re-import on every turn. On zero hits, follow guidance instead of abandoning LeanKG for bash.
4. Importing indexes elements but does not create vectors. If L3 is required, run ` + "`leankg-embed run`" + ` (or ` + "`leankg-embed full`" + ` on stamp drift); a degraded L3 is not proof that the query ladder is broken.
5. Use status for health, freshness, resolved project_dir, and embedding coverage — not as a substitute for query.
6. Keep context across sessions: at session start call query action=memory with args.command=session_recall (or memories). At session end call import action=memory command=session_retain with session_id + turns[] (args.scope=per-project|global|per-project-tagged, args.cwd optional); durable lessons use command=lesson.`

const toolGuidance = "Agent protocol: query before bash/grep; project= only on multi-project HTTP; import once when cold; inspect retrieval/freshness/guidance; session_recall at start and session_retain/lesson at end (memory scope=per-project|global|per-project-tagged); do not pin a rung unless intentional."

// New builds the MCP server with the 3-tool registry.
func New(engine *core.Engine) *Server {
	s := &Server{engine: engine}
	s.srv = mcp.NewServer(&mcp.Implementation{
		Name: "leankg", Version: version,
		Description: "LeanKG code knowledge graph (3 tools: import/query/status): query for code discovery and memory recall; import for indexing and session_retain.",
	}, &mcp.ServerOptions{Instructions: serverInstructions})
	s.registerTools()
	return s
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
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, err := auth.RoleForRequest(r)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		inner.ServeHTTP(w, r.WithContext(withRole(r.Context(), role)))
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

func (s *Server) registerTools() {
	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolImport,
		Description: "Import content into LeanKG: index a repository or directory " +
			"of repositories (action=repo|dir, path), or curate agent memory " +
			"(action=memory, command=create|str_replace|insert|delete|rename|add|replace|remove). " +
			"Legacy tool name 'set' is superseded by this tool. " +
			"Use action=dir with path=\".\" to import the current directory as a scoped index target (FR-P2). " +
			"The store is <project>/.leankg/leankg.db unless LEANKG_DB_PATH or leankg.yaml db.standalone_db_path " +
			"names another file — in that case EVERY verb (import, query, status, doctor, leankg-embed) reads THAT " +
			"store and no store appears under the project, so the path you import does not decide where the data lives; " +
			"status reports it in the store field. " +
			"Import only for first-time indexing or deliberate updates; indexing does not create vectors. " + toolGuidance,
		InputSchema: json.RawMessage(`{
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
				"args": {"type": "object", "description": "action params: turns[]/session_id/retained_through_user_turn/scope/cwd/bank (memory session_retain); content/old/new (memory curation); mode/lines/fresh (read)"},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			}
		}`),
	}, s.handleImport)

	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolQuery,
		Description: "Query LeanKG. Empty action routes down the ladder: L1 exact " +
			"identifier → L2 fuzzy keyword → L3 semantic (vectors). Every answer carries " +
			"retrieval{rung,reason} + freshness. action=memory searches agent memory; " +
			"action=exact|fuzzy|semantic pins a rung; graph verbs (impact/path/callers/callees/context/explain) and session/ontology reads are also available. " +
			"Leave action empty/search for semantic questions; legacy tool name 'get' is superseded. " + toolGuidance,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "search text, identifier, or memory search text"},
				"action": {"type": "string", "enum": ["search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain", "memory", "session", "ontology", "prd", "incidents", "env_conflicts", "service_context", "portfolio", "pattern", "languages", "lsp", "compress"], "description": "empty = ladder router (L0-L3). Actions that NEED another argument (they error without it): path → args.to (target qualified name); pattern → args.pattern (AST pattern, e.g. \"func $F\"); lsp → args.lang (server language); service_context and env_conflicts → args.service; portfolio → args.cmd=summary for the T0 manifest, args.action pins each child's action. impact reads its traversal DEPTH from args.depth. ontology reads args.cmd (matches|trace|status|concept_search|feature_flow|traceability); org reads take args.pattern/args.env for filtering."},
				"limit": {"type": "integer", "description": "max hits for search and the graph verbs (default 10; impact DEPTH comes from args.depth). This is the field those actions read; args.limit applies only to the full-scan tools pattern/lsp, and when both are sent this one wins."},
				"args": {"type": "object", "description": "action params. scope: search — code|prod|all, the corpus to rank (default is everything, including test fixtures and archived docs; retrieval.scope reports it). weight: search — \"<vector>,<keyword>\" fusion weights, e.g. \"3,1\" trusts the vector arm more, \"1,0\" drops the keyword arm; unset is 1,1 and retrieval.weight reports it. cmd: ontology — one of matches|trace|status|concept_search|feature_flow|traceability. command: session — recall|canvas (node_id for recall). command: memory — session_recall|memories (plus scope/cwd/bank). main: memory — the query to search. depth: impact (traversal depth), path (max hops). to: path (target qualified name). pattern: pattern (AST pattern, e.g. \"func $F\"). lang: lsp (server language). mode/lines/fresh: read. cmd: portfolio (summary selects the T0 manifest); action: portfolio (pins each child's action); tool/response: compress. limit: pattern/lsp — these full-scan tools read args.limit, while search and the graph verbs read the TOP-LEVEL limit; sending both gives the top-level one precedence."},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			},
			"required": []
		}`),
	}, s.handleQuery)

	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolStatus,
		Description: "LeanKG health: inventory, freshness (fresh|possibly_stale|cold), watermark, backend, embeddings state (stamped models, vectors), last embed run. " +
			"Use status for health and coverage, not as a substitute for query. " + toolGuidance,
		InputSchema: json.RawMessage(`{"type": "object", "properties": {"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}}}`),
	}, s.handleStatus)
}

// recordMetric persists one context_metrics row per served tool call (Rust
// mcp/handler.rs records the same fields at the end of its tool dispatch).
// Best-effort by contract: a ledger write failure must never fail the call
// (Rust logged and returned the tool result anyway).
func (s *Server) recordMetric(eng *core.Engine, req *mcp.CallToolRequest, started time.Time, out any, err error) {
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
	}
	if err == nil {
		if raw, merr := json.Marshal(out); merr == nil {
			m.OutputTokens = int64(len(raw) / 4)
		}
		m.OutputElements = int64(countResponseElements(out))
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
	key := action
	if key == "" {
		key = "query"
	}
	res := any(v)
	if m, ok := v.(map[string]any); ok {
		res, _ = budget.TokenBudget{}.Apply(m, key)
		return res
	}
	if raw, err := json.Marshal(v); err == nil {
		var round map[string]any
		if json.Unmarshal(raw, &round) == nil {
			res, _ = budget.TokenBudget{}.Apply(round, key)
			return res
		}
	}
	res, _ = budget.TokenBudget{}.Apply(v, key)
	return res
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
	s.recordMetric(eng, req, started, out, err)
	if err != nil {
		return nil, err
	}
	res := enforceBudget(out, in.Action)
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
	s.recordMetric(eng, req, started, out, err)
	if err != nil {
		return nil, err
	}
	res := enforceBudget(out, in.Action)
	return textResult(res)
}

func (s *Server) handleStatus(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	started := time.Now()
	eng, err := s.engineFor(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := eng.Status(ctx)
	s.recordMetric(eng, req, started, out, err)
	if err != nil {
		return nil, err
	}
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
