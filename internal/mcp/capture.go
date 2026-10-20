package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/FreePeak/LeanKG/internal/budget"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Capture is the telemetry hook of plan v4.15 (DS-04 middleware, DS-05
// identity, DS-06 classifier, DS-07 tokens). It only observes: it never
// changes a request or a response and never returns an error. With the
// recorder at Off the middleware is a pass-through.

const (
	methodInitialize = telemetry.MethodInitialize
	// methodDiscover is the 2026-07-28 sessionless handshake. The SDK client
	// sends it first, so it is a handshake method too (DS-05).
	methodDiscover  = telemetry.MethodDiscover
	methodToolsCall = telemetry.MethodToolsCall
	unknownClient   = "unknown"
)

// Identity headers read by the streamable-HTTP wrapper (DS-05). Stateless
// HTTP has no session, so each request carries who it is.
const (
	headerClient  = "X-LeanKG-Client"
	headerSession = "X-LeanKG-Session"
	headerCwd     = "X-LeanKG-Cwd"
)

// knownClients are the coding agents the dashboard groups by. A name matches
// when it equals the key or starts with key followed by "-" or a space.
var knownClients = []string{"claude-code", "xdev", "omp", "pi", "opencode", "grok", "codex", "gemini"}

// sessionEnv maps each agent's session env var to its client name (DS-05).
// The variables are read once, at server start.
var sessionEnv = []struct{ env, client string }{
	{"CLAUDE_CODE_SESSION_ID", "claude-code"},
	{"PI_SESSION_ID", "pi"},
	{"XDEV_SESSION_ID", "xdev"},
	{"OPENCODE_SESSION_ID", "opencode"},
}

// SetRecorder attaches the capture sink (nil means Nop). Call it before the
// server starts serving; it is not safe to swap while calls are in flight.
func (s *Server) SetRecorder(rec telemetry.Recorder) {
	if rec == nil {
		rec = telemetry.Nop{}
	}
	s.rec = rec
}

func (s *Server) recorder() telemetry.Recorder {
	if s.rec == nil {
		return telemetry.Nop{}
	}
	return s.rec
}

// initStdioIdentity reads the session env once and sets the stdio identity
// fallback. The client name is refined from clientInfo at initialize.
func (s *Server) initStdioIdentity(getenv func(string) string) {
	s.sessionByClient = map[string]string{}
	for _, e := range sessionEnv {
		if v := strings.TrimSpace(getenv(e.env)); v != "" {
			s.sessionByClient[e.client] = v
			if s.stdio.ClientSessionID == "" {
				s.stdio.ClientName = e.client
				s.stdio.ClientSessionID = v
			}
		}
	}
	if s.stdio.ClientName == "" {
		s.stdio.ClientName = unknownClient
	}
	if cwd, err := os.Getwd(); err == nil {
		s.stdio.Cwd = cwd
	}
}

// noteStdioClient sets the stdio identity from clientInfo. The session id
// comes only from the env var of that same client, so a Claude Code id is
// never attached to a pi session.
func (s *Server) noteStdioClient(name, version string) {
	if name == "" {
		return
	}
	s.stdioMu.Lock()
	defer s.stdioMu.Unlock()
	cn := normalizeClient(name)
	s.stdio.ClientName = cn
	s.stdio.ClientVersion = version
	s.stdio.ClientSessionID = s.sessionByClient[cn]
}

func (s *Server) identityFor(ctx context.Context) (telemetry.Identity, string) {
	if id, transport, ok := telemetry.IdentityFrom(ctx); ok {
		return id, transport
	}
	s.stdioMu.Lock()
	defer s.stdioMu.Unlock()
	return s.stdio, telemetry.TransportStdio
}

// capture is the receiving middleware installed before resolveToolNames, so
// it also sees unknown-tool and argument failures.
func (s *Server) capture(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		rec := s.recorder()
		level := rec.Level()
		if level == telemetry.Off {
			return next(ctx, method, req)
		}
		switch method {
		case methodInitialize, methodDiscover:
			return s.captureHandshake(ctx, rec, next, method, req)
		case methodToolsCall:
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				return s.captureCall(ctx, level, rec, next, method, req, call)
			}
		}
		return next(ctx, method, req)
	}
}

// captureHandshake records the handshake row and the stdio identity. The
// client info comes from the request params (initialize) or from the
// session, which go-sdk fills from each request's _meta (discover).
func (s *Server) captureHandshake(ctx context.Context, rec telemetry.Recorder, next mcp.MethodHandler, method string, req mcp.Request) (mcp.Result, error) {
	started := time.Now()
	res, err := next(ctx, method, req)
	latency := time.Since(started)
	var info *mcp.Implementation
	if p, ok := req.GetParams().(*mcp.InitializeParams); ok && p != nil {
		info = p.ClientInfo
	}
	if info == nil {
		info = clientInfoOf(req)
	}
	s.noteClient(ctx, info)
	id, transport := s.identityFor(ctx)
	cls := telemetry.Classify(nil, err)
	ev := telemetry.CallEvent{
		ID:            newEventID(started),
		TS:            started,
		LatencyMS:     latency.Milliseconds(),
		Transport:     transport,
		Method:        method,
		Identity:      id,
		Correlation:   id.Correlation(),
		Outcome:       cls.Outcome,
		OutcomeReason: cls.Reason,
		ErrorCode:     cls.ErrorCode,
	}
	emitCall(rec, ev)
	return res, err
}

// clientInfoOf reads the client info of the request's session. Nil when the
// request has no session or the session has not seen a client yet.
func clientInfoOf(req mcp.Request) *mcp.Implementation {
	ss, ok := req.GetSession().(*mcp.ServerSession)
	if !ok || ss == nil {
		return nil
	}
	if p := ss.InitializeParams(); p != nil {
		return p.ClientInfo
	}
	return nil
}

// noteClient applies client info to the stdio identity. HTTP identity comes
// from headers only, so it is left alone.
func (s *Server) noteClient(ctx context.Context, info *mcp.Implementation) {
	if info == nil {
		return
	}
	if _, transport, ok := telemetry.IdentityFrom(ctx); !ok || transport == telemetry.TransportStdio {
		s.noteStdioClient(info.Name, info.Version)
	}
}

// callScratch carries what the handler learned about one tools/call back to
// the middleware. Handlers fill it only when a context value is present, so
// with capture off nothing is allocated for it.
type callScratch struct {
	set            bool
	projectDir     string
	cls            telemetry.Classification
	outPre         int64
	outPost        int64
	trimmed        int64
	baseline       int64
	baselineMethod string
	saved          int64
}

type scratchKey struct{}

func scratchFrom(ctx context.Context) *callScratch {
	sc, _ := ctx.Value(scratchKey{}).(*callScratch)
	return sc
}

func (s *Server) captureCall(ctx context.Context, level telemetry.Level, rec telemetry.Recorder, next mcp.MethodHandler, method string, req mcp.Request, call *mcp.CallToolRequest) (mcp.Result, error) {
	started := time.Now()
	sc := &callScratch{}
	res, err := next(context.WithValue(ctx, scratchKey{}, sc), method, req)
	latency := time.Since(started)

	// resolveToolNames rewrote the name to its canonical form when it resolved.
	s.noteClient(ctx, clientInfoOf(req))
	raw := []byte(call.Params.Arguments)
	id, transport := s.identityFor(ctx)
	top := readCallFields(raw)
	ev := telemetry.CallEvent{
		ID:          newEventID(started),
		TS:          started,
		LatencyMS:   latency.Milliseconds(),
		Transport:   transport,
		Method:      method,
		Tool:        call.Params.Name,
		Action:      top.action,
		Command:     top.command,
		Project:     top.project,
		Identity:    id,
		Correlation: id.Correlation(),
		ArgsHash:    telemetry.ArgsHash(raw),
		ArgKeys:     telemetry.ArgKeys(raw),
	}

	var body string
	if res != nil {
		body = resultText(res)
	}
	if sc.set {
		ev.Project = firstNonEmptyStr(sc.projectDir, top.project)
		ev.OutTokensPre = sc.outPre
		ev.OutTokensPost = sc.outPost
		ev.BudgetTrimmedTokens = sc.trimmed
		ev.BaselineTokens = sc.baseline
		ev.BaselineMethod = sc.baselineMethod
		ev.TokensSaved = sc.saved
		applyClassification(&ev, sc.cls)
	} else {
		cls := telemetry.Classify(nil, err)
		applyClassification(&ev, cls)
		ev.OutTokensPre = int64(len(body) / 4)
		ev.OutTokensPost = ev.OutTokensPre
		ev.BaselineMethod = telemetry.BaselineNone
	}
	if level == telemetry.Bodies {
		ev.ArgsRedacted = string(raw)
		if res != nil {
			ev.BodyRedacted = body
		} else if err != nil {
			ev.BodyRedacted = err.Error()
		}
	}
	emitCall(rec, ev)
	return res, err
}

func applyClassification(ev *telemetry.CallEvent, c telemetry.Classification) {
	ev.Outcome = c.Outcome
	ev.OutcomeReason = c.Reason
	ev.ErrorCode = c.ErrorCode
	ev.Rung = c.Rung
	ev.Confidence = c.Confidence
	ev.Freshness = c.Freshness
	ev.Hits = c.Hits
	ev.HitFiles = strings.Join(c.HitFiles, "\n")
}

// finishCall is the single report point for a served tools/call. It writes
// the context_metrics row (recordMetric) on post-budget output, and it hands
// the classification and token accounting to the capture middleware. The
// classifier reads the pre-budget engine output out; the tokens are measured
// on the delivered payload post.
func (s *Server) finishCall(ctx context.Context, eng *core.Engine, req *mcp.CallToolRequest, started time.Time, action string, out, post any, st budget.Stats, err error) {
	cls := telemetry.Classify(out, err)
	hitFiles := cls.HitFiles
	if action == "memory" {
		hitFiles = nil
	}
	projectDir, tool := "", ""
	if eng != nil {
		projectDir = eng.ProjectDir()
	}
	if req != nil && req.Params != nil {
		tool = req.Params.Name
	}
	base, method := telemetry.Baseline(projectDir, tool, cls.Outcome, hitFiles)
	var postTokens int64
	if err == nil && post != nil {
		postTokens = tokenEstimate(post)
	}
	saved := telemetry.Saved(base, postTokens)
	pre := postTokens
	if st.PreTruncationToken > 0 {
		pre = int64(st.PreTruncationToken)
	}
	s.recordMetric(eng, req, started, post, err, metricEval{
		baseline: base, saved: saved, postTokens: postTokens,
	})
	if sc := scratchFrom(ctx); sc != nil {
		*sc = callScratch{
			set:            true,
			projectDir:     projectDir,
			cls:            cls,
			outPre:         pre,
			outPost:        postTokens,
			trimmed:        int64(st.SavedTokens()),
			baseline:       base,
			baselineMethod: method,
			saved:          saved,
		}
	}
}

// identityFromHeaders reads the identity headers of a stateless HTTP request.
// Missing headers give client "unknown"; nothing here can fail a request.
func identityFromHeaders(r *http.Request) telemetry.Identity {
	name, ver := splitClient(r.Header.Get(headerClient))
	if name == "" {
		name, ver = splitClient(r.Header.Get("User-Agent"))
	}
	return telemetry.Identity{
		ClientName:      normalizeClient(name),
		ClientVersion:   ver,
		ClientSessionID: strings.TrimSpace(r.Header.Get(headerSession)),
		Cwd:             strings.TrimSpace(r.Header.Get(headerCwd)),
	}
}

// splitClient reads "name/version (details)" or "name". A user agent such as
// "claude-cli/2.0.1 (external, cli)" yields name "claude-cli", version "2.0.1".
func splitClient(v string) (name, version string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ""
	}
	if i := strings.IndexByte(v, '/'); i >= 0 {
		name, version = v[:i], v[i+1:]
		if j := strings.IndexByte(version, ' '); j >= 0 {
			version = version[:j]
		}
		return strings.TrimSpace(name), strings.TrimSpace(version)
	}
	if j := strings.IndexByte(v, ' '); j >= 0 {
		return v[:j], ""
	}
	return v, ""
}

// normalizeClient maps a client name onto the dashboard's names (DS-05):
// claude-code, xdev, omp, pi, opencode, grok, codex, gemini. Anything else
// is its lowercase name, and an empty name is "unknown".
func normalizeClient(raw string) string {
	n := strings.ToLower(strings.TrimSpace(raw))
	if n == "" {
		return unknownClient
	}
	if n == "claude" || strings.HasPrefix(n, "claude-") || strings.HasPrefix(n, "claude ") {
		return "claude-code"
	}
	for _, k := range knownClients {
		if n == k || strings.HasPrefix(n, k+"-") || strings.HasPrefix(n, k+" ") {
			return k
		}
	}
	return n
}

// callFields are the routing fields read from one tools/call argument object.
// action and project are top-level; command may sit top-level or under args.
type callFields struct {
	action, command, project string
}

func readCallFields(raw []byte) callFields {
	var top struct {
		Action  string          `json:"action"`
		Command string          `json:"command"`
		Project string          `json:"project"`
		Args    json.RawMessage `json:"args"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &top) != nil {
		return callFields{}
	}
	f := callFields{action: top.Action, command: top.Command, project: top.Project}
	if f.command == "" && len(top.Args) > 0 {
		var nested struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(top.Args, &nested) == nil {
			f.command = nested.Command
		}
	}
	return f
}

// resultText is the concatenated text content of a tool result (the JSON the
// client received).
func resultText(res mcp.Result) string {
	r, ok := res.(*mcp.CallToolResult)
	if !ok || r == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// tokenEstimate is len(json)/4, the budget package's estimate.
func tokenEstimate(v any) int64 {
	raw, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return int64(len(raw) / 4)
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// emitCall hands one event to the recorder. A recorder fault must never
// reach the caller, so a panic is logged and dropped.
func emitCall(rec telemetry.Recorder, ev telemetry.CallEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("mcp: capture: %v", r)
		}
	}()
	rec.RecordCall(ev)
}

var eventSeq atomic.Uint64

// newEventID is time-sortable: 16 hex digits of the start time, then a
// process-local sequence.
func newEventID(ts time.Time) string {
	return fmt.Sprintf("%016x%08x", ts.UnixNano(), eventSeq.Add(1)&0xffffffff)
}
