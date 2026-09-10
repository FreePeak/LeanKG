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

	"github.com/FreePeak/LeanKG/go/internal/auth"
	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Server wraps the go-sdk server over one core engine.
type Server struct {
	engine *core.Engine
	srv    *mcp.Server
}

// version is the Go engine's protocol version marker (engine, not the Rust
// crate version).
const version = "0.31.0-go-w1"

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
		return fmt.Errorf("forbidden: tool %q requires contributor or admin role", tool)
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
				"action": {"type": "string", "enum": ["repo", "dir", "docs", "memory", "session", "ontology"], "description": "what to import"},
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
				"summary": {"type": "string", "description": "offload summary (action=session)"}
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
				"action": {"type": "string", "enum": ["search", "exact", "fuzzy", "semantic", "element", "impact", "path", "callers", "callees", "context", "explain", "memory", "session", "ontology"], "description": "empty = ladder router (L0-L3); graph verbs need args.depth (impact) or args.to (path)"},
				"limit": {"type": "integer", "description": "max hits (default 10; impact depth comes from args.depth)"},
				"args": {"type": "object", "additionalProperties": {"type": "string"}, "description": "action params: depth (impact/path), to (path target QN), command/node_id (session), bank (memory)"}
			},
			"required": []
		}`),
	}, s.handleQuery)

	s.srv.AddTool(&mcp.Tool{
		Name:        core.ToolStatus,
		Description: "LeanKG health: inventory, freshness (fresh|possibly_stale|cold), watermark, backend, embeddings state (stamped models, vectors), last embed run.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
	}, s.handleStatus)
}

func (s *Server) handleImport(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := allowTool(ctx, core.ToolImport); err != nil {
		return nil, err
	}
	var in core.ImportRequest
	if err := unmarshalArgs(req, &in); err != nil {
		return nil, err
	}
	out, err := s.engine.Import(ctx, in)
	if err != nil {
		return nil, err
	}
	return textResult(out)
}

func (s *Server) handleQuery(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var in core.QueryRequest
	if err := unmarshalArgs(req, &in); err != nil {
		return nil, err
	}
	out, err := s.engine.Query(ctx, in)
	if err != nil {
		return nil, err
	}
	return textResult(out)
}

func (s *Server) handleStatus(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := s.engine.Status(ctx)
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
