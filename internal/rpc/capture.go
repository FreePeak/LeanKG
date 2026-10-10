package rpc

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	connect "connectrpc.com/connect"

	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/rest"
	leankgv1 "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1"
	leankgv1connect "github.com/FreePeak/LeanKG/internal/rpc/leankg/v1/leankgv1connect"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Handler mounts the LeanKG ConnectRPC service over engine with the capture
// interceptor for rec. It returns the same path and handler that
// leankgv1connect.NewLeanKGHandler does.
func Handler(engine *core.Engine, rec telemetry.Recorder) (string, http.Handler) {
	return leankgv1connect.NewLeanKGHandler(NewLeanKGService(engine), connect.WithInterceptors(CaptureInterceptor(rec)))
}

// CaptureInterceptor records each unary ConnectRPC call as one
// telemetry.CallEvent on transport rpc (plan v4.15 DS-08). It never changes a
// request or a response, and it is a pass-through when the sink is Off.
// Streaming calls are not used by this service and pass through unrecorded.
// Identity comes from the same headers as the REST capture
// (rest.IdentityFromHeader), and the call context carries
// telemetry.WithIdentity so memory events join the row.
func CaptureInterceptor(rec telemetry.Recorder) connect.Interceptor {
	return captureInterceptor{rec: rec}
}

type captureInterceptor struct {
	rec telemetry.Recorder
}

func (c captureInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if c.rec == nil || c.rec.Level() == telemetry.Off {
			return next(ctx, req)
		}
		start := time.Now()
		id := rest.IdentityFromHeader(req.Header())
		procedure := req.Spec().Procedure
		tool := snakeCase(procedure[strings.LastIndex(procedure, "/")+1:])
		action, command := requestAction(req.Any())

		res, err := next(telemetry.WithIdentity(ctx, id, telemetry.TransportRPC), req)

		ev := telemetry.CallEvent{
			TS:          start,
			Transport:   telemetry.TransportRPC,
			Method:      procedure,
			Tool:        tool,
			Action:      action,
			Command:     command,
			Identity:    id,
			Correlation: id.Correlation(),
			ID:          newRPCCallID(start),
		}
		raw := requestJSON(req.Any())
		ev.ArgsHash = telemetry.ArgsHash(raw)
		ev.ArgKeys = telemetry.ArgKeys(raw)

		body := ""
		if err != nil {
			code := connect.CodeOf(err).String()
			ev.Outcome = telemetry.OutcomeErrorPrefix + strings.ToUpper(code)
			ev.ErrorCode = strings.ToUpper(code)
			ev.OutcomeReason = err.Error()
		} else {
			body = responseJSON(res.Any())
			cls := classify(body)
			ev.Outcome = cls.Outcome
			ev.OutcomeReason = cls.Reason
			ev.ErrorCode = cls.ErrorCode
			ev.Rung = cls.Rung
			ev.Confidence = cls.Confidence
			ev.Freshness = cls.Freshness
			ev.Hits = cls.Hits
			ev.HitFiles = strings.Join(cls.HitFiles, "\n")
		}
		ev.LatencyMS = time.Since(start).Milliseconds()
		if c.rec.Level() == telemetry.Bodies {
			ev.ArgsRedacted = telemetry.Cap(telemetry.Redact(string(raw)), telemetry.DefaultMaxBodyBytes)
			ev.BodyRedacted = telemetry.Cap(telemetry.Redact(body), telemetry.DefaultMaxBodyBytes)
		}
		c.rec.RecordCall(ev)
		return res, err
	}
}

// WrapStreamingClient and WrapStreamingHandler pass through: LeanKG's
// ConnectRPC surface is unary only.
func (captureInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (captureInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

// requestAction reads the action and command out of a typed request.
func requestAction(msg any) (action, command string) {
	switch m := msg.(type) {
	case *leankgv1.QueryRequest:
		return m.Action, m.Args["command"]
	case *leankgv1.ImportRequest:
		command = m.Command
		if command == "" {
			command = m.Args["command"]
		}
		return m.Action, command
	case *leankgv1.MemoryReadRequest:
		return "", m.Command
	}
	return "", ""
}

// requestJSON renders the request fields as the JSON the call's args hash is
// computed over, the same shape REST and MCP would carry.
func requestJSON(msg any) []byte {
	var v any
	switch m := msg.(type) {
	case *leankgv1.QueryRequest:
		v = map[string]any{"action": m.Action, "query": m.Query, "limit": m.Limit, "args": m.Args}
	case *leankgv1.ImportRequest:
		v = map[string]any{"action": m.Action, "path": m.Path, "command": m.Command, "args": m.Args}
	case *leankgv1.MemoryReadRequest:
		v = map[string]any{"command": m.Command, "path": m.Path, "query": m.Query, "limit": m.Limit}
	default:
		return nil
	}
	raw, _ := json.Marshal(v)
	return raw
}

// responseJSON returns the JSON payload carried by a typed response.
func responseJSON(msg any) string {
	switch m := msg.(type) {
	case *leankgv1.QueryResponse:
		return m.Json
	case *leankgv1.ImportResponse:
		return m.Json
	case *leankgv1.StatusResponse:
		return m.Json
	case *leankgv1.MemoryReadResponse:
		return m.Json
	}
	return ""
}

// classify runs the shared classifier on a JSON payload; a non-JSON or empty
// payload is a plain success.
func classify(body string) telemetry.Classification {
	if body == "" {
		return telemetry.Classification{Outcome: telemetry.OutcomeOK}
	}
	var decoded any
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return telemetry.Classification{Outcome: telemetry.OutcomeOK}
	}
	return telemetry.Classify(decoded, nil)
}

// snakeCase turns a procedure name such as "MemoryRead" into "memory_read".
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// newRPCCallID is a time-sortable id: a nanosecond prefix, then random bytes.
func newRPCCallID(at time.Time) string {
	var tail [8]byte
	_, _ = rand.Read(tail[:])
	return fmt.Sprintf("%016x%x", at.UnixNano(), tail)
}
