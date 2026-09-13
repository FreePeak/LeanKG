package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

// seedGraph writes one element with a parent + metadata, one bare element
// (nil parent and metadata), and two relationships — the shape the Rust
// CodeElement/Relationship readers had to render.
func seedGraph(t *testing.T, s *store.Store) {
	t.Helper()
	els := []store.Element{
		{
			QualifiedName: "pkg.Svc.Handle", ElementType: "method", Name: "Handle",
			FilePath: "svc.go", LineStart: 10, LineEnd: 20, Language: "go",
			ParentQualified: "pkg.Svc", Content: "func (s *Svc) Handle()",
			Metadata: map[string]any{"kind": "method"},
		},
		{
			QualifiedName: "pkg.parse", ElementType: "function", Name: "parse",
			FilePath: "util.go", LineStart: 1, LineEnd: 5, Language: "go",
			Content: "func parse()",
		},
	}
	if err := s.UpsertElements(els); err != nil {
		t.Fatalf("upsert elements: %v", err)
	}
	rels := []store.Relationship{
		{Source: "pkg.Svc.Handle", Target: "pkg.parse", RelType: "calls", Confidence: 0.75},
		{Source: "pkg.parse", Target: "pkg.Svc", RelType: "member_of", Confidence: 1},
	}
	if err := s.UpsertRelationships(rels); err != nil {
		t.Fatalf("upsert relationships: %v", err)
	}
}

// recorder captures one request the fake shared server received.
type recorder struct {
	req  *http.Request
	body []byte
}

func fakeRemote(t *testing.T, status int, body string, rec *recorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rec != nil {
			rec.req = r
			rec.body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPushSendsRustPayload pins the wire contract of push_to_remote: POST to
// /api/v2/graph/push with the three headers, and the serde_json envelope
// {"env","service","elements","relationships"} whose rows serialize as the
// Rust CodeElement/Relationship structs.
func TestPushSendsRustPayload(t *testing.T) {
	st := openStore(t)
	seedGraph(t, st)

	rec := &recorder{}
	// Trailing slashes prove the Rust trim_end_matches('/') normalization.
	srv := fakeRemote(t, http.StatusOK, `{"success":true,"data":{"received":4}}`, rec)

	t.Setenv("USER", "linh.doan")
	res, err := Push(context.Background(), st, Options{
		Remote:     srv.URL + "///",
		Token:      "lkg_team_token",
		Env:        "local",
		ProjectDir: "/tmp/work/demo-project",
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}

	if rec.req.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", rec.req.Method)
	}
	if rec.req.URL.Path != PushPath {
		t.Errorf("path = %s, want %s", rec.req.URL.Path, PushPath)
	}
	// The Go auth middleware reads only the bearer; Rust's X-LeanKG-Token stays
	// for shared servers that gate on it alone.
	for header, want := range map[string]string{
		HeaderToken:     "lkg_team_token",
		HeaderEnv:       "local",
		HeaderEngineer:  "linh.doan",
		"Authorization": "Bearer lkg_team_token",
		"Content-Type":  "application/json",
	} {
		if got := rec.req.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	var doc struct {
		Env           string           `json:"env"`
		Service       string           `json:"service"`
		Elements      []map[string]any `json:"elements"`
		Relationships []map[string]any `json:"relationships"`
	}
	if err := json.Unmarshal(rec.body, &doc); err != nil {
		t.Fatalf("unmarshal payload: %v\n%s", err, rec.body)
	}
	if doc.Env != "local" {
		t.Errorf("payload env = %q, want local", doc.Env)
	}
	if doc.Service != "demo-project" {
		t.Errorf("payload service = %q, want demo-project", doc.Service)
	}
	if len(doc.Elements) != 2 || len(doc.Relationships) != 2 {
		t.Fatalf("payload counts = %d elements / %d relationships, want 2/2", len(doc.Elements), len(doc.Relationships))
	}

	els := map[string]map[string]any{}
	for _, e := range doc.Elements {
		els[e["qualified_name"].(string)] = e
	}
	handle := els["pkg.Svc.Handle"]
	if handle == nil || els["pkg.parse"] == nil {
		t.Fatalf("elements = %v", doc.Elements)
	}
	for _, key := range []string{
		"qualified_name", "element_type", "name", "file_path", "line_start", "line_end",
		"language", "parent_qualified", "cluster_id", "cluster_label", "metadata", "env",
	} {
		if _, ok := handle[key]; !ok {
			t.Errorf("element missing Rust key %q: %v", key, handle)
		}
	}
	// Go's Element carries content the Rust wire format has no slot for.
	if _, ok := handle["content"]; ok {
		t.Errorf("element must not publish content: %v", handle)
	}
	if handle["parent_qualified"] != "pkg.Svc" || handle["env"] != localEnv {
		t.Errorf("element = %v", handle)
	}
	// The Go store has no cluster column, so the Rust cluster fields are null.
	if handle["cluster_id"] != nil || handle["cluster_label"] != nil {
		t.Errorf("cluster fields = %v / %v, want null", handle["cluster_id"], handle["cluster_label"])
	}
	if !strings.Contains(string(rec.body), `"metadata":{}`) {
		t.Errorf("empty metadata must serialize as {} (Rust row-read fallback), body:\n%s", rec.body)
	}
	if meta, ok := els["pkg.parse"]["metadata"].(map[string]any); !ok || len(meta) != 0 {
		t.Errorf("bare element metadata = %#v, want empty object (never null)", els["pkg.parse"]["metadata"])
	}

	rel := doc.Relationships[0]
	for _, key := range []string{"source_qualified", "target_qualified", "rel_type", "confidence", "metadata", "env"} {
		if _, ok := rel[key]; !ok {
			t.Errorf("relationship missing Rust key %q: %v", key, rel)
		}
	}
	for _, absent := range []string{"id", "source", "target"} {
		if _, ok := rel[absent]; ok {
			t.Errorf("relationship must not carry %q: %v", absent, rel)
		}
	}

	if res.Failed() {
		t.Errorf("Failed() = true for a 200 response: %+v", res)
	}
	if res.Elements != 2 || res.Relationships != 2 || res.Service != "demo-project" {
		t.Errorf("result = %+v", res)
	}
	// Rust prints the remote it was given, trailing slashes included.
	want := fmt.Sprintf("Pushed 2 elements and 2 relationships to %s (env: local)", srv.URL+"///")
	if got := res.Message(); got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}
	if strings.Contains(res.Message(), "received") {
		t.Errorf("success message must ignore the response body: %q", res.Message())
	}
}

// TestPushFailureIsReportedNotReturned pins the Rust exit-code semantics: a
// rejected request is printed to stderr and still exits 0 (no error).
func TestPushFailureIsReportedNotReturned(t *testing.T) {
	st := openStore(t)
	seedGraph(t, st)
	srv := fakeRemote(t, http.StatusUnauthorized, "Invalid team token", nil)

	res, err := Push(context.Background(), st, Options{
		Remote: srv.URL, Token: "bad", Env: "local", ProjectDir: "/tmp/demo",
	})
	if err != nil {
		t.Fatalf("push returned an error for a 401 (Rust exited 0): %v", err)
	}
	if !res.Failed() || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("result = %+v, want Failed 401", res)
	}
	want := "Push failed (401 Unauthorized): Invalid team token"
	if got := res.Message(); got != want {
		t.Fatalf("Message() = %q, want %q", got, want)
	}

	var stdout, stderr bytes.Buffer
	res.Report(&stdout, &stderr)
	if stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != want {
		t.Fatalf("Report: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

// TestPushResponseBodyIsNeverParsed: the Rust client never decoded the push
// response, so a malformed body cannot fail a push — it is only echoed on the
// failure line. A malformed response is the only "malformed payload" a push
// can meet: the request payload is built from the local store.
func TestPushResponseBodyIsNeverParsed(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		failed bool
		// message is rendered from the remote the push was given.
		message func(remote string) string
	}{
		"garbage on 200 is a success": {
			status: http.StatusOK,
			message: func(remote string) string {
				return fmt.Sprintf("Pushed 2 elements and 2 relationships to %s (env: local)", remote)
			},
		},
		"garbage on 500 is echoed": {
			status: http.StatusInternalServerError,
			failed: true,
			message: func(string) string {
				return "Push failed (500 Internal Server Error): <<<not json>>>"
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			st := openStore(t)
			seedGraph(t, st)
			srv := fakeRemote(t, tc.status, "<<<not json>>>", nil)

			res, err := Push(context.Background(), st, Options{
				Remote: srv.URL, Token: "t", Env: "local", ProjectDir: "/tmp/demo",
			})
			if err != nil {
				t.Fatalf("push: %v", err)
			}
			if res.Failed() != tc.failed {
				t.Fatalf("Failed() = %v, want %v (%+v)", res.Failed(), tc.failed, res)
			}
			if got := res.Message(); got != tc.message(srv.URL) {
				t.Fatalf("Message() = %q", got)
			}
		})
	}
}

// TestPushLocalReadErrors: a read failure is an error (Rust's `?` → exit 1).
func TestPushLocalReadErrors(t *testing.T) {
	st := openStore(t)
	seedGraph(t, st)
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	rec := &recorder{}
	srv := fakeRemote(t, http.StatusOK, "", rec)

	if _, err := Push(context.Background(), st, Options{Remote: srv.URL, ProjectDir: "/tmp/demo"}); err == nil {
		t.Fatal("push of a closed store must fail before any request")
	} else if !strings.Contains(err.Error(), "read local elements") {
		t.Fatalf("error = %v", err)
	}
	if rec.req != nil {
		t.Fatal("no request may be sent when the local read fails")
	}
}

// TestPushTruncationGuard: the Rust read was unbounded, the Go read is paged.
// Reaching the page boundary with more rows behind it is an error, never a
// silent partial publish.
func TestPushTruncationGuard(t *testing.T) {
	st := openStore(t)
	seedGraph(t, st)
	extra := []store.Relationship{
		{Source: "pkg.Svc", Target: "pkg.parse", RelType: "references", Confidence: 1},
		{Source: "pkg.parse", Target: "pkg.Svc.Handle", RelType: "calls", Confidence: 1},
	}
	if err := st.UpsertRelationships(extra); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	restore := maxRelationships
	maxRelationships = 2
	t.Cleanup(func() { maxRelationships = restore })

	if _, err := readRelationships(st); err == nil {
		t.Fatal("a truncated relationship read must fail loudly")
	} else if !strings.Contains(err.Error(), "above the 2 publish cap") {
		t.Fatalf("error = %v", err)
	}

	// Exactly at the cap with nothing behind it is still published.
	maxRelationships = 4
	if rels, err := readRelationships(st); err != nil || len(rels) != 4 {
		t.Fatalf("read at cap = %d rels, err %v", len(rels), err)
	}
}

// TestPushUnreachableRemote: a transport failure propagates (Rust's `?`).
func TestPushUnreachableRemote(t *testing.T) {
	st := openStore(t)
	seedGraph(t, st)
	remote := deadRemote(t)

	if _, err := Push(context.Background(), st, Options{
		Remote: remote, Token: "t", Env: "local", ProjectDir: "/tmp/demo",
	}); err == nil {
		t.Fatal("push to a dead remote must fail")
	} else if !strings.Contains(err.Error(), remote) {
		t.Fatalf("error = %v, want it to name %s", err, remote)
	}
}

// servedRequest records what a fake shared server received.
type servedRequest struct {
	method string
	path   string
	header http.Header
}

// remoteRoute is one path a fake remote answers; status 0 means 200.
type remoteRoute struct {
	status int
	body   string
}

// remoteRoutes serves a remote that only implements the paths listed: anything
// else answers 404, exactly like a router with no such route registered.
func remoteRoutes(t *testing.T, routes map[string]remoteRoute, seen *[]servedRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = append(*seen, servedRequest{method: r.Method, path: r.URL.Path, header: r.Header.Clone()})
		}
		route, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if route.status != 0 {
			w.WriteHeader(route.status)
		}
		_, _ = io.WriteString(w, route.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// graphBody renders the envelope a Go receiver publishes for st, so the client
// tests speak the real wire format instead of hand-written JSON.
func graphBody(t *testing.T, st *store.Store, env, service string) string {
	t.Helper()
	els, err := st.Elements()
	if err != nil {
		t.Fatalf("elements: %v", err)
	}
	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	body, err := json.Marshal(pushPayload{
		Env: env, Service: service,
		Elements: wireElements(els), Relationships: wireRelationships(rels),
	})
	if err != nil {
		t.Fatalf("encode graph: %v", err)
	}
	return string(body)
}

// TestPullAppliesRemoteGraph is the #372 contract: the fetched graph goes
// through the same policy a push uses — rows upserted by key, rows the document
// never mentions left alone, provenance recorded in metadata, and local element
// content preserved because the wire format carries none.
func TestPullAppliesRemoteGraph(t *testing.T) {
	source := openStore(t)
	seedGraph(t, source)

	local := openStore(t)
	if err := local.UpsertElements([]store.Element{
		// Same qualified name the remote also publishes.
		{QualifiedName: "pkg.Svc.Handle", ElementType: "method", Name: "Stale", FilePath: "stale.go", Language: "go", Content: "LOCAL CONTENT"},
		// A row the remote never mentions: must survive untouched.
		{QualifiedName: "pkg.KeepMe", ElementType: "function", Name: "KeepMe", FilePath: "keep.go", Language: "go", Content: "KEEP"},
	}); err != nil {
		t.Fatalf("seed local: %v", err)
	}

	srv := remoteRoutes(t, map[string]remoteRoute{
		GraphPath: {body: graphBody(t, source, "production", "shared-service")},
	}, nil)
	res, err := Pull(context.Background(), local, Options{Remote: srv.URL + "/", Token: "lkg_team_token", Env: "production"})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if res.Failed() || !res.Applied {
		t.Fatalf("result = %+v, want an applied 2xx", res)
	}
	if res.Elements != 2 || res.Relationships != 2 {
		t.Errorf("applied = %d elements / %d relationships, want 2/2", res.Elements, res.Relationships)
	}
	if res.Body != "" {
		t.Errorf("Body = %q, want the graph left out of a successful result", res.Body)
	}
	want := fmt.Sprintf("Pulled 2 elements and 2 relationships from %s (env: production)", srv.URL+"/")
	if got := res.Message(); got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}

	got := elementsByQN(t, local)
	if _, ok := got["pkg.KeepMe"]; !ok {
		t.Error("a row the document never mentioned was deleted")
	}
	handle, ok := got["pkg.Svc.Handle"]
	if !ok {
		t.Fatal("pkg.Svc.Handle missing after apply")
	}
	// Last-write-wins on the wire fields…
	if handle.Name != "Handle" || handle.FilePath != "svc.go" || handle.LineStart != 10 {
		t.Errorf("conflict not resolved to the incoming row: %+v", handle)
	}
	// …and content preserved: the envelope has no content slot.
	if handle.Content != "LOCAL CONTENT" {
		t.Errorf("content = %q, want the local text carried forward", handle.Content)
	}
	fresh, ok := got["pkg.parse"]
	if !ok {
		t.Fatal("pkg.parse missing after apply")
	}
	if fresh.Content != "" {
		t.Errorf("a newly synced element must not invent content, got %q", fresh.Content)
	}
	for qn, e := range got {
		if qn == "pkg.KeepMe" {
			if _, stamped := e.Metadata[MetaSource]; stamped {
				t.Error("an unmentioned row was stamped with provenance")
			}
			continue
		}
		assertProvenance(t, e.Metadata, "shared-service", "production")
	}

	rels, err := local.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("local relationships = %d, want 2", len(rels))
	}
	for _, r := range rels {
		assertProvenance(t, r.Metadata, "shared-service", "production")
	}
}

// TestPullFallsBackToTheRustProbe keeps `leankg pull` honest against a server
// that has no graph route: it degrades to Rust's /api/v2/status connectivity
// probe, prints the exact Rust line, and applies nothing.
func TestPullFallsBackToTheRustProbe(t *testing.T) {
	statusEnvelope := `{"success":true,"data":{"total_elements":0,"service":"demo","version":"4.6.0"}}`

	for name, tc := range map[string]struct {
		routes map[string]remoteRoute
	}{
		// A Rust shared server, or any pre-#372 build: the route is absent.
		"no such route": {routes: map[string]remoteRoute{StatusPath: {body: statusEnvelope}}},
		// The graph path exists but refuses a read — the other way a remote
		// says it has no graph to give.
		"read not allowed": {routes: map[string]remoteRoute{
			GraphPath:  {status: http.StatusMethodNotAllowed},
			StatusPath: {body: statusEnvelope},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []servedRequest
			srv := remoteRoutes(t, tc.routes, &seen)

			st := openStore(t)
			seedGraph(t, st)
			before := elementCount(t, st)

			res, err := Pull(context.Background(), st, Options{
				Remote: srv.URL + "/", Token: "lkg_team_token", Env: "production",
			})
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if res.Failed() || res.Applied {
				t.Fatalf("result = %+v, want a successful probe", res)
			}
			if res.Body != statusEnvelope {
				t.Errorf("Body = %q, want the status document", res.Body)
			}
			want := fmt.Sprintf("Successfully connected to %s (env: production)", srv.URL+"/")
			if got := res.Message(); got != want {
				t.Errorf("Message() = %q, want the Rust connectivity line %q", got, want)
			}
			var stdout, stderr bytes.Buffer
			res.Report(&stdout, &stderr)
			if strings.TrimSpace(stdout.String()) != want || stderr.Len() != 0 {
				t.Errorf("Report: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}

			// The fallback is a probe, not a rewrite: nothing applied locally.
			if after := elementCount(t, st); after != before {
				t.Errorf("element count changed from %d to %d", before, after)
			}

			// Graph first, then the legacy probe; neither sends the engineer
			// header (Rust sent it on push only).
			if len(seen) != 2 {
				t.Fatalf("requests = %v, want the graph fetch then the probe", seen)
			}
			for i, path := range []string{GraphPath, StatusPath} {
				if seen[i].method != http.MethodGet || seen[i].path != path {
					t.Errorf("request %d = %s %s, want GET %s", i, seen[i].method, seen[i].path, path)
				}
				if got := seen[i].header.Get(HeaderToken); got != "lkg_team_token" {
					t.Errorf("%s on request %d = %q", HeaderToken, i, got)
				}
				if got := seen[i].header.Get("Authorization"); got != "Bearer lkg_team_token" {
					t.Errorf("bearer on request %d = %q", i, got)
				}
				if got := seen[i].header.Get(HeaderEnv); got != "production" {
					t.Errorf("%s on request %d = %q", HeaderEnv, i, got)
				}
				if got := seen[i].header.Get(HeaderEngineer); got != "" {
					t.Errorf("request %d must not send %s: %q", i, HeaderEngineer, got)
				}
			}
		})
	}
}

func TestPullErrors(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := fakeRemote(t, http.StatusUnauthorized, "Missing X-LeanKG-Token header", nil)
		res, err := Pull(context.Background(), openStore(t), Options{Remote: srv.URL, Token: "", Env: "production"})
		if err != nil {
			t.Fatalf("pull returned an error for a 401 (Rust exited 0): %v", err)
		}
		if !res.Failed() {
			t.Fatalf("result = %+v, want Failed", res)
		}
		want := "Pull failed (401 Unauthorized): Missing X-LeanKG-Token header"
		if got := res.Message(); got != want {
			t.Fatalf("Message() = %q, want %q", got, want)
		}
		var stdout, stderr bytes.Buffer
		res.Report(&stdout, &stderr)
		if stdout.Len() != 0 || strings.TrimSpace(stderr.String()) != want {
			t.Fatalf("Report: stdout=%q stderr=%q", stdout.String(), stderr.String())
		}
	})

	t.Run("unreachable", func(t *testing.T) {
		remote := deadRemote(t)
		if _, err := Pull(context.Background(), openStore(t), Options{Remote: remote, Env: "production"}); err == nil {
			t.Fatal("pull from a dead remote must fail")
		} else if !strings.Contains(err.Error(), remote) {
			t.Fatalf("error = %v, want it to name %s", err, remote)
		}
	})

	t.Run("no store", func(t *testing.T) {
		// Pull now writes: without a store there is nothing to apply into, and
		// silently dropping the graph would report a successful pull.
		if _, err := Pull(context.Background(), nil, Options{Remote: "http://127.0.0.1:1"}); err == nil {
			t.Fatal("pull without a store must fail")
		}
	})

	// A 2xx that is not a usable graph document is a hard failure, never a
	// silent "Successfully connected": an older server 404s this path, so a 200
	// here is a broken remote, and a broken remote must say so.
	for name, body := range map[string]string{
		"not a graph":       `{"success":true,"data":{"service":"demo"}}`,
		"undecodable row":   `{"elements":[{"qualified_name":"a","line_start":"not-a-number"}],"relationships":[]}`,
		"identity-less row": `{"elements":[{"element_type":"function"}],"relationships":[]}`,
		"truncated json":    `{"elements":[{"qualified_name":"a"`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := remoteRoutes(t, map[string]remoteRoute{GraphPath: {body: body}}, nil)
			st := openStore(t)
			seedGraph(t, st)
			before := elementCount(t, st)

			if _, err := Pull(context.Background(), st, Options{Remote: srv.URL, Token: "t", Env: "local"}); err == nil {
				t.Fatalf("pull of a %s body must fail, not report success", name)
			}
			if after := elementCount(t, st); after != before {
				t.Errorf("element count changed from %d to %d on a rejected document", before, after)
			}
		})
	}
}

// deadRemote returns a URL nothing is listening on.
func deadRemote(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return "http://" + addr
}

// TestServiceName pins Rust project_path.file_name(): the basename, or
// "unknown" for paths whose file_name() is None.
func TestServiceName(t *testing.T) {
	for dir, want := range map[string]string{
		"/tmp/work/demo-project":  "demo-project",
		"/tmp/work/demo-project/": "demo-project",
		"relative/dir":            "dir",
		"dir":                     "dir",
		"":                        "unknown",
		".":                       "unknown",
		"/":                       "unknown",
		"//":                      "unknown",
		"..":                      "unknown",
		"/tmp/..":                 "unknown",
	} {
		if got := serviceName(dir); got != want {
			t.Errorf("serviceName(%q) = %q, want %q", dir, got, want)
		}
	}
}

// TestEngineerHeader pins Rust's X-LeanKG-Engineer default: an explicit value
// wins, else $USER, else "unknown" — and a set-but-empty $USER sends an empty
// header (env::var returns Ok("")).
func TestEngineerHeader(t *testing.T) {
	old, had := os.LookupEnv("USER")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("USER", old)
		} else {
			_ = os.Unsetenv("USER")
		}
	})

	if err := os.Unsetenv("USER"); err != nil {
		t.Fatal(err)
	}
	if got := engineer(""); got != "unknown" {
		t.Errorf("unset USER = %q, want unknown", got)
	}
	if got := engineer("ops"); got != "ops" {
		t.Errorf("override = %q, want ops", got)
	}
	t.Setenv("USER", "linh")
	if got := engineer(""); got != "linh" {
		t.Errorf("USER = %q, want linh", got)
	}
	t.Setenv("USER", "")
	if got := engineer(""); got != "" {
		t.Errorf("empty USER = %q, want empty", got)
	}
}
