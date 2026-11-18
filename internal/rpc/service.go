// Package rpc serves the LeanKG core over ConnectRPC: gRPC, gRPC-Web, and
// plain JSON browser clients from ONE set of handlers (rewrite doc §6.7).
// The handlers are thin adapters over internal/core — the envelope resolves
// in core before any gate, same as MCP/REST.
package rpc

import (
	"context"
	"encoding/json"
	"net/http"

	connect "connectrpc.com/connect"
	"github.com/FreePeak/LeanKG/internal/httpx"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"

	"github.com/FreePeak/LeanKG/internal/core"
)

// LeanKGService implements the generated connect service over a core engine.
type LeanKGService struct {
	engine *core.Engine
}

// NewLeanKGService builds the service.
func NewLeanKGService(engine *core.Engine) *LeanKGService {
	return &LeanKGService{engine: engine}
}

// The generated descriptor types `args` as map<string,string>: a protobuf
// comment on the field even says clients "may send typed values over other
// transports", which is true and is also the whole problem. Over this wire every
// NON-STRING action argument is unreachable — `limit`, `depth`, `insert_line`,
// `retained_through_user_turn`, and the array-valued `turns` — and the refusal
// the caller gets is the codec's:
//
//	unmarshal message: … proto: (line 1:48): invalid value for string field value: 2
//
// It names a field the caller never sent (`value`), not the argument they did

// jsonOf marshals core's map[string]any payloads into the proto's string
// field — the same JSON MCP text content and REST bodies carry (one wire
// shape, three transports).
func jsonOf(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *LeanKGService) Import(ctx context.Context, req *connect.Request[leankgv1.ImportRequest]) (*connect.Response[leankgv1.ImportResponse], error) {
	args := map[string]any{}
	for k, v := range req.Msg.Args {
		args[k] = v
	}
	out, err := s.engine.Import(ctx, core.ImportRequest{
		Action:  req.Msg.Action,
		Path:    req.Msg.Path,
		Command: req.Msg.Command,
		Args:    args,
	})
	if err != nil {
		return nil, connect.NewError(connectCode(err), err)
	}
	capped, _ := core.EnforceBudget(out, req.Msg.Action, 0)
	js, err := jsonOf(capped)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.ImportResponse{Json: js}), nil
}

func (s *LeanKGService) Query(ctx context.Context, req *connect.Request[leankgv1.QueryRequest]) (*connect.Response[leankgv1.QueryResponse], error) {
	query, err := core.DecodeQuery(map[string]any{
		"action": req.Msg.Action,
		"query":  req.Msg.Query,
		"limit":  req.Msg.Limit,
		"args":   req.Msg.Args,
	})
	if err != nil {
		return nil, connect.NewError(connectCode(err), err)
	}
	ans, err := s.engine.QueryAnswer(ctx, query)
	if err != nil {
		return nil, connect.NewError(connectCode(err), err)
	}
	js, err := jsonOf(ans.Body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.QueryResponse{Json: js}), nil
}

func (s *LeanKGService) Status(ctx context.Context, req *connect.Request[leankgv1.StatusRequest]) (*connect.Response[leankgv1.StatusResponse], error) {
	out, err := s.engine.Status(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	js, err := jsonOf(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.StatusResponse{Json: js}), nil
}

func (s *LeanKGService) MemoryRead(ctx context.Context, req *connect.Request[leankgv1.MemoryReadRequest]) (*connect.Response[leankgv1.MemoryReadResponse], error) {
	// Route through the same dispatcher the MCP tool and the CLI use
	// (query action=memory), so all three surfaces answer the SAME command
	// vocabulary. Measured live before this: the RPC refused `session_recall`
	// with "valid: view, snapshot, search" while MCP accepted "session_recall,
	// memories, view, snapshot, search" — the agent protocol's own first
	// instruction ("at session start call session_recall") was the one command
	// a ConnectRPC client could not run.
	query, err := core.DecodeQuery(map[string]any{
		"action": "memory",
		"query":  req.Msg.Query,
		"limit":  req.Msg.Limit,
		"args":   map[string]string{"command": req.Msg.Command, "path": req.Msg.Path, "cwd": ""},
	})
	if err != nil {
		return nil, connect.NewError(connectCode(err), err)
	}
	ans, err := s.engine.QueryAnswer(ctx, query)
	if err != nil {
		return nil, connect.NewError(connectCode(err), err)
	}
	js, err := jsonOf(ans.Body)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.MemoryReadResponse{Json: js}), nil
}

// connectCode maps a failed call to the ConnectRPC code of the HTTP status the
// shared error table (internal/httpx) gives it, so the RPC wire refuses a
// request with the same class REST does.
func connectCode(err error) connect.Code {
	switch httpx.StatusFor(err) {
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeNotFound
	case http.StatusConflict:
		return connect.CodeAborted
	case httpx.StatusClientClosedRequest:
		return connect.CodeCanceled
	case http.StatusInternalServerError:
		return connect.CodeInternal
	case http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	}
	return connect.CodeInvalidArgument
}
