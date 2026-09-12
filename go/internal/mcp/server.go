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

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/budget"
	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/errs"
	"github.com/FreePeak/LeanKG/go/internal/store"
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
// stamp in cmd/leankg/VERSION — the only version source in the Go tree —
// because serverInfo is what clients log; version_test.go fails on drift.
const version = "0.31.0"

// New builds the MCP server with the 3-tool registry.
func New(engine *core.Engine) *Server {
	s := &Server{engine: engine}
	s.srv = mcp.NewServer(&mcp.Implementation{Name: "leankg", Version: version}, nil)
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
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.srv }, nil)
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
			"Legacy tool name 'set' is superseded by this tool.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["repo", "dir", "docs", "prd", "memory", "session", "ontology", "read"], "description": "what to import (prd = index a PRD markdown document; read = compressed file read: args.mode/lines/fresh)"},
				"path": {"type": "string", "description": "repository/directory to index, or memory file path (e.g. MEMORY.md, topics/x.md)"},
				"command": {"type": "string", "enum": ["create", "str_replace", "insert", "delete", "rename", "add", "replace", "remove", "offload", "lesson"], "description": "memory write command (action=memory) or session command (action=session: offload|lesson)"},
				"content": {"type": "string"},
				"old": {"type": "string"},
				"new": {"type": "string"},
				"insert_line": {"type": "integer"},
				"new_path": {"type": "string"},
				"file": {"type": "string", "description": "memory file for add/replace/remove"},
				"text": {"type": "string"},
				"session_id": {"type": "string", "description": "session id (action=session)"},
				"node_id": {"type": "string", "description": "offload node id (action=session)"},
				"payload": {"type": "string", "description": "payload to offload (action=session)"},
				"summary": {"type": "string", "description": "offload summary (action=session)"},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			}
		}`),
	}, s.handleImport)

	s.srv.AddTool(&mcp.Tool{
		Name: core.ToolQuery,
		Description: "Query LeanKG. Empty action routes down the ladder: L1 exact " +
			"identifier → L2 fuzzy keyword → L3 semantic (vectors). Every answer carries " +
			"retrieval{rung,reason} + freshness. action=memory searches agent memory; " +
			"action=exact|fuzzy|semantic pins a rung; graph verbs (impact/path/callers/callees/context/explain) and session/ontology reads are also available. Legacy tool name 'get' is superseded.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "description": "search text, identifier, or memory search text"},
				"action": {"type": "string", "enum": ["search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain", "memory", "session", "ontology", "prd", "incidents", "env_conflicts", "service_context", "pattern", "languages", "lsp", "compress"], "description": "empty = ladder router (L0-L3); graph verbs need args.depth (impact) or args.to (path); org reads take args.service/args.pattern/args.env"},
				"limit": {"type": "integer", "description": "max hits (default 10; impact depth comes from args.depth)"},
				"args": {"type": "object", "description": "action params: depth (impact/path), to (path target QN), command/node_id (session), main (memory), pattern/lang/limit (pattern), lang (lsp), mode/lines/fresh (read), cmd/tool/response (compress)"},
				"project": {"type": "string", "description": "target project (dir path or name); only meaningful when the server serves multiple projects (LEANKG_PROJECT_DIRS)"}
			},
			"required": []
		}`),
	}, s.handleQuery)

	s.srv.AddTool(&mcp.Tool{
		Name:        core.ToolStatus,
		Description: "LeanKG health: inventory, freshness (fresh|possibly_stale|cold), watermark, backend, embeddings state (stamped models, vectors), last embed run.",
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
