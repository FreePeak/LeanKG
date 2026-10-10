package rest

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// Capture (plan v4.15 DS-08) records one telemetry.CallEvent per HTTP request
// served by h: the native REST routes and, on the same mux, the Hindsight
// compat routes. It never changes a response: the handler gets the original
// request (with its body restored byte for byte) and the response is written
// through unchanged. The response is also teed into a bounded buffer so the
// status and JSON body can be classified; that buffer is kept only for the
// classification and, at level Bodies, a redacted copy.
//
// The request context carries telemetry.WithIdentity, so the memory events a
// call causes join to its row. With level Off the wrapper is a pass-through.

// captureBodyCap bounds both the request body read for decoding and the
// response body kept for classification. Larger bodies are still served; they
// are classified by status alone.
const captureBodyCap = 1 << 20

// Capture wraps next. transport is the default transport for the row; paths
// under /v1/default/banks/ are always recorded as telemetry.TransportHS.
func Capture(next http.Handler, rec telemetry.Recorder, transport string) http.Handler {
	if rec == nil {
		rec = telemetry.Nop{}
	}
	return &captureHandler{next: next, rec: rec, transport: transport}
}

type captureHandler struct {
	next      http.Handler
	rec       telemetry.Recorder
	transport string
}

func (c *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if c.rec.Level() == telemetry.Off {
		c.next.ServeHTTP(w, r)
		return
	}
	start := time.Now()
	transport := c.transport
	if isHindsightPath(r.URL.Path) {
		transport = telemetry.TransportHS
	}
	route := describeRoute(r.Method, r.URL.Path)
	id := IdentityFromHeader(r.Header)

	var raw []byte
	if route.decode {
		raw = readRequestBody(r)
	}

	ev := telemetry.CallEvent{
		TS:        start,
		Transport: transport,
		Method:    r.Method + " " + route.pattern,
		Tool:      route.tool,
		Action:    route.action,
		Command:   route.command,
		Project:   r.URL.Query().Get("project"),
		Identity:  id,
	}
	if route.decode && raw != nil {
		var body captureBody
		if json.Unmarshal(raw, &body) == nil {
			ev.Action, ev.Command = route.bodyAction(body, ev.Action), route.bodyCommand(body, ev.Command)
		}
		ev.ArgsHash = telemetry.ArgsHash(raw)
		ev.ArgKeys = telemetry.ArgKeys(raw)
	}
	ev.Correlation = id.Correlation()

	cw := &captureWriter{ResponseWriter: w}
	c.next.ServeHTTP(cw, r.WithContext(telemetry.WithIdentity(r.Context(), id, transport)))

	ev.LatencyMS = time.Since(start).Milliseconds()
	ev.ID = newCallID(start)
	cls := classifyResponse(cw.status, cw.body, cw.truncated)
	ev.Outcome = cls.Outcome
	ev.OutcomeReason = cls.Reason
	ev.ErrorCode = cls.ErrorCode
	ev.Rung = cls.Rung
	ev.Confidence = cls.Confidence
	ev.Freshness = cls.Freshness
	ev.Hits = cls.Hits
	ev.HitFiles = strings.Join(cls.HitFiles, "\n")
	if c.rec.Level() == telemetry.Bodies {
		if raw != nil {
			ev.ArgsRedacted = telemetry.Cap(telemetry.Redact(string(raw)), telemetry.DefaultMaxBodyBytes)
		}
		if len(cw.body) > 0 {
			ev.BodyRedacted = telemetry.Cap(telemetry.Redact(string(cw.body)), telemetry.DefaultMaxBodyBytes)
		}
	}
	c.rec.RecordCall(ev)
}

// captureBody is the subset of a request body the row records.
type captureBody struct {
	Action    string         `json:"action"`
	Command   string         `json:"command"`
	Args      map[string]any `json:"args"`
	Bank      string         `json:"bank"`
	Tags      []string       `json:"tags"`
	TagsMatch string         `json:"tags_match"`
}

// routeInfo is what a request is recorded as, before its body is read.
type routeInfo struct {
	pattern string // normalized: ids replaced by {bank}, {id}, ...
	tool    string
	action  string
	command string
	decode  bool // the body decides action/command
	// Used only when decode is set: which body fields fill action and command.
	hindsightRecall bool
	memorySession   bool
	queryOrImport   bool
	importRoute     bool
}

func (ri routeInfo) bodyAction(b captureBody, fallback string) string {
	switch {
	case ri.queryOrImport:
		if b.Action != "" {
			return b.Action
		}
	case ri.memorySession, ri.hindsightRecall:
		if b.Bank != "" {
			return b.Bank
		}
	}
	return fallback
}

func (ri routeInfo) bodyCommand(b captureBody, fallback string) string {
	switch {
	case ri.importRoute:
		if b.Command != "" {
			return b.Command
		}
		if c, _ := b.Args["command"].(string); c != "" {
			return c
		}
	case ri.queryOrImport:
		if c, _ := b.Args["command"].(string); c != "" {
			return c
		}
	case ri.hindsightRecall:
		tm := b.TagsMatch
		if tm == "" {
			tm = "any"
		}
		cmd := "recall tags_match=" + tm
		if len(b.Tags) > 0 {
			cmd = "recall tags=" + strings.Join(b.Tags, ",") + " tags_match=" + tm
		}
		return cmd
	}
	return fallback
}

// literalSegments are the fixed words of the routes LeanKG serves. Any other
// path segment is an identifier and is recorded as {id}.
var literalSegments = map[string]bool{
	"api": true, "v1": true, "v2": true, "default": true,
	"query": true, "import": true, "status": true, "health": true,
	"memory": true, "banks": true, "memories": true, "recall": true,
	"inject": true, "reflect": true, "stats": true, "documents": true,
	"session": true, "retain": true, "read": true, "list": true,
	"auth": true, "mcp": true, "auto-config": true, "ontology": true,
	"match": true, "matches": true, "graph": true, "push": true,
	"incidents": true, "env": true, "diff": true, "service": true,
	"context": true, "canvas": true,
}

func describeRoute(method, path string) routeInfo {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	pattern := normalizeSegments(segs)
	ri := routeInfo{pattern: pattern}

	switch {
	case path == "/api/v1/query":
		ri.tool, ri.decode, ri.queryOrImport = "query", true, true
	case path == "/api/v1/import":
		ri.tool, ri.decode, ri.queryOrImport, ri.importRoute = "import", true, true, true
	case path == "/api/v1/status":
		ri.tool = "status"
	case path == "/health":
		ri.tool = "health"
	case strings.HasPrefix(path, "/api/v1/memory/session/"):
		ri.tool, ri.decode, ri.memorySession = "memory", true, true
		ri.command = segs[len(segs)-1]
	case strings.HasPrefix(path, "/api/v1/memory/banks/"):
		ri.tool = "memory"
		ri.action = segmentAfter(segs, "banks")
		ri.command = strings.Join(afterSegment(segs, "banks", 1), "/")
	case strings.HasPrefix(path, "/v1/default/banks/"):
		ri.tool = "hindsight"
		ri.action = segmentAfter(segs, "banks")
		rest := afterSegment(segs, "banks", 1)
		ri.command = hindsightCommand(method, rest)
		if method == http.MethodPost && len(rest) == 2 && rest[0] == "memories" && rest[1] == "recall" {
			ri.decode, ri.hindsightRecall = true, true
			ri.command = "recall"
		}
	case len(segs) >= 3 && segs[0] == "api":
		ri.tool = "rest"
		ri.action = segs[2]
	default:
		ri.tool = "rest"
	}
	return ri
}

// hindsightCommand names a Hindsight-wire route by its verb.
func hindsightCommand(method string, rest []string) string {
	switch {
	case method == http.MethodPut && len(rest) == 0:
		return "ensure"
	case method == http.MethodPost && len(rest) == 1 && rest[0] == "memories":
		return "retain"
	case method == http.MethodGet && len(rest) == 1 && rest[0] == "memories":
		return "list"
	case method == http.MethodGet && len(rest) == 2 && rest[0] == "memories":
		return "read"
	case method == http.MethodPost && len(rest) == 1 && rest[0] == "reflect":
		return "reflect"
	case method == http.MethodGet && len(rest) == 1 && rest[0] == "stats":
		return "stats"
	case method == http.MethodDelete && len(rest) == 2 && rest[0] == "documents":
		return "delete"
	}
	return strings.Join(rest, "/")
}

// normalizeSegments renders a path as its route pattern: the segment after
// "banks" becomes {bank}, the one after "documents" {document_id}, and any
// segment that is not a fixed route word becomes {id}. The first three
// segments of an /api/ or /v1/ path name the route family and stay literal,
// so an unknown route still shows its family without its identifiers.
func normalizeSegments(segs []string) string {
	versioned := len(segs) > 0 && (segs[0] == "api" || segs[0] == "v1")
	out := make([]string, len(segs))
	for i, s := range segs {
		prev := ""
		if i > 0 {
			prev = segs[i-1]
		}
		switch {
		case prev == "banks":
			out[i] = "{bank}"
		case prev == "documents":
			out[i] = "{document_id}"
		case versioned && i < 3, literalSegments[s]:
			out[i] = s
		default:
			out[i] = "{id}"
		}
	}
	return "/" + strings.Join(out, "/")
}

// segmentAfter returns the path segment following key, or "".
func segmentAfter(segs []string, key string) string {
	for i, s := range segs {
		if s == key && i+1 < len(segs) {
			return segs[i+1]
		}
	}
	return ""
}

// afterSegment returns the segments after key plus skip more, i.e. the route
// family below .../banks/{bank}/.
func afterSegment(segs []string, key string, skip int) []string {
	for i, s := range segs {
		if s == key {
			if i+1+skip <= len(segs) {
				return segs[i+1+skip:]
			}
			return nil
		}
	}
	return nil
}

func isHindsightPath(path string) bool {
	return strings.HasPrefix(path, "/v1/default/banks/")
}

// readRequestBody reads a request body for decoding and returns it, up to
// captureBodyCap. The request's Body is replaced with a reader that yields
// exactly the bytes that were there, so the handler sees the original body.
// A body over the cap is restored and not decoded (nil).
func readRequestBody(r *http.Request) []byte {
	if r.Body == nil || r.Body == http.NoBody {
		return nil
	}
	orig := r.Body
	head, _ := io.ReadAll(io.LimitReader(orig, captureBodyCap+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), orig), orig}
	if len(head) > captureBodyCap {
		return nil
	}
	return head
}

// captureWriter passes writes through and keeps a bounded copy of the body
// and the status code for classification.
type captureWriter struct {
	http.ResponseWriter
	status    int
	body      []byte
	truncated bool
}

func (w *captureWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *captureWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if room := captureBodyCap - len(w.body); room > 0 {
		if len(p) <= room {
			w.body = append(w.body, p...)
		} else {
			w.body = append(w.body, p[:room]...)
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return w.ResponseWriter.Write(p)
}

// Flush forwards to the underlying writer when it can flush.
func (w *captureWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *captureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// classifyResponse labels a finished call. A JSON body goes to the shared
// classifier; any 4xx/5xx that the classifier did not already mark as an
// error becomes error:HTTP_<status>, so a status-level failure is never
// recorded as success. A non-JSON body is classified by status alone.
func classifyResponse(status int, body []byte, truncated bool) telemetry.Classification {
	if status == 0 {
		status = http.StatusOK
	}
	cls := telemetry.Classification{Outcome: telemetry.OutcomeOK}
	if len(body) > 0 && !truncated {
		var decoded any
		if json.Unmarshal(body, &decoded) == nil {
			cls = telemetry.Classify(decoded, nil)
		}
	}
	if status >= http.StatusBadRequest && !strings.HasPrefix(cls.Outcome, telemetry.OutcomeErrorPrefix) {
		code := "HTTP_" + strconv.Itoa(status)
		cls.Outcome = telemetry.OutcomeErrorPrefix + code
		cls.ErrorCode = code
	}
	return cls
}

// IdentityFromHeader reads the caller identity from the request headers
// (DS-05 rules, shared with the ConnectRPC interceptor). X-LeanKG-Client names
// the agent, X-LeanKG-Session its session, X-LeanKG-Cwd its working dir. When
// no client name is sent, the User-Agent product token is used. Values are
// clipped so a hostile header cannot bloat the ledger.
func IdentityFromHeader(h http.Header) telemetry.Identity {
	id := telemetry.Identity{
		ClientName:      clip(h.Get("X-LeanKG-Client")),
		ClientSessionID: clip(h.Get("X-LeanKG-Session")),
		Cwd:             clip(h.Get("X-LeanKG-Cwd")),
	}
	uaName, uaVersion := parseUserAgent(h.Get("User-Agent"))
	if id.ClientName == "" {
		id.ClientName = uaName
	}
	if id.ClientName == "" {
		id.ClientName = "unknown"
	}
	id.ClientVersion = uaVersion
	return id
}

// parseUserAgent splits the first product token of a User-Agent into name and
// version ("omp/1.4.2 (darwin)" -> "omp", "1.4.2").
func parseUserAgent(ua string) (name, version string) {
	fields := strings.Fields(ua)
	if len(fields) == 0 {
		return "", ""
	}
	name, version, _ = strings.Cut(fields[0], "/")
	return clip(name), clip(version)
}

const maxIdentityLen = 128

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxIdentityLen {
		return s[:maxIdentityLen]
	}
	return s
}

// newCallID is a time-sortable id: a nanosecond prefix, then random bytes.
func newCallID(at time.Time) string {
	var tail [8]byte
	_, _ = rand.Read(tail[:])
	return fmt.Sprintf("%016x%x", at.UnixNano(), tail)
}
