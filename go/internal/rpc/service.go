// Package rpc serves the LeanKG core over ConnectRPC: gRPC, gRPC-Web, and
// plain JSON browser clients from ONE set of handlers (rewrite doc §6.7).
// The handlers are thin adapters over internal/core — the envelope resolves
// in core before any gate, same as MCP/REST.
package rpc

import (
	"context"
	"encoding/json"

	connect "connectrpc.com/connect"
	leankgv1 "github.com/FreePeak/LeanKG/go/internal/rpc/leankg/v1"

	"github.com/FreePeak/LeanKG/go/internal/core"
)

// LeanKGService implements the generated connect service over a core engine.
type LeanKGService struct {
	engine *core.Engine
}

// NewLeanKGService builds the service.
func NewLeanKGService(engine *core.Engine) *LeanKGService {
	return &LeanKGService{engine: engine}
}

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
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	js, err := jsonOf(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.ImportResponse{Json: js}), nil
}

func (s *LeanKGService) Query(ctx context.Context, req *connect.Request[leankgv1.QueryRequest]) (*connect.Response[leankgv1.QueryResponse], error) {
	out, err := s.engine.Query(ctx, core.QueryRequest{
		Action: req.Msg.Action,
		Query:  req.Msg.Query,
		Limit:  int(req.Msg.Limit),
		Args:   req.Msg.Args,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	js, err := jsonOf(out)
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
	out, err := s.engine.MemoryRead(req.Msg.Command, req.Msg.Path, req.Msg.Query, int(req.Msg.Limit))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	js, err := jsonOf(out)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&leankgv1.MemoryReadResponse{Json: js}), nil
}
