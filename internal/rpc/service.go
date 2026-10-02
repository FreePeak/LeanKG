// Package rpc serves the LeanKG core over ConnectRPC: gRPC, gRPC-Web, and
// plain JSON browser clients from ONE set of handlers (rewrite doc §6.7).
// The handlers are thin adapters over internal/core — the envelope resolves
// in core before any gate, same as MCP/REST.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	connect "connectrpc.com/connect"
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
// (`depth`). That is the wire-23 class of defect at its worst: a confident client
// reading a message about its own request that cannot be about its own request.
//
// The descriptor is generated and the repository keeps no .proto source, so
// retyping the map is a schema change with no regeneration story — a product
// decision, not a patch. What is a patch is the LEGIBILITY: a caller must learn
// which argument was refused and that this wire carries strings only. So the
// codec's refusal is translated here, where the service knows what `args` means,
// and the honest remedy is named alongside it.
//
// ponytail: a regexp against the codec's message. It is the only handle on the
// offending argument — the codec reports a byte OFFSET, not a field path — and
// it degrades to passing the original message through untouched, so a future
// protobuf whose wording changes costs clarity and never correctness.
var codecArgRefusal = regexp.MustCompile(`invalid value for string field value: (.+)$`)

// argsWireNote explains the wire's own limitation, in the caller's terms.
const argsWireNote = "this ConnectRPC descriptor types args as map<string,string>, so only STRING action args reach it " +
	"(over MCP the same call takes any JSON value); send it as a string here, or use the MCP or REST surface for typed args"

// explainArgsRefusal rewrites a codec refusal that names the internal map VALUE
// field into one that names the caller's own argument.
func explainArgsRefusal(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	var _ = strings.TrimSpace
	if !strings.Contains(msg, "string field value") {
		return err
	}
	got := codecArgRefusal.FindStringSubmatch(msg)
	if got == nil {
		// The wording changed: pass the original through rather than guess.
		return err
	}
	// The codec quotes the rejected value, e.g. `2` or `["a","b"]`. It does not
	// say which KEY, so name what the caller can act on — the value they sent and
	// the wire's limit — rather than invent a key.
	value := strings.TrimSpace(got[1])
	return fmt.Errorf("args value %s is not a string and this wire cannot carry it: %s (%v)",
		value, argsWireNote, err)
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
		Args:   protoArgs(req.Msg.Args),
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

// protoArgs converts the proto map<string,string> into core's wider
// map[string]any (clients may send typed values over other transports).
func protoArgs(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
