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
	for header, want := range map[string]string{
		HeaderToken:    "lkg_team_token",
		HeaderEnv:      "local",
		HeaderEngineer: "linh.doan",
		"Content-Type": "application/json",
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

// TestPullIsAStatusProbe pins pull_from_remote: GET /api/v2/status with the
// token and env headers, no engineer header, and no local effect — the body is
// never inspected beyond being carried on the result.
func TestPullIsAStatusProbe(t *testing.T) {
	rec := &recorder{}
	envelope := `{"success":true,"data":{"total_elements":2,"total_relationships":2,"service":"demo","version":"4.6.0"}}`
	srv := fakeRemote(t, http.StatusOK, envelope, rec)

	res, err := Pull(context.Background(), Options{Remote: srv.URL + "/", Token: "lkg_team_token", Env: "production"})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if rec.req.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", rec.req.Method)
	}
	if rec.req.URL.Path != StatusPath {
		t.Errorf("path = %s, want %s", rec.req.URL.Path, StatusPath)
	}
	if got := rec.req.Header.Get(HeaderToken); got != "lkg_team_token" {
		t.Errorf("%s = %q", HeaderToken, got)
	}
	if got := rec.req.Header.Get(HeaderEnv); got != "production" {
		t.Errorf("%s = %q", HeaderEnv, got)
	}
	if got := rec.req.Header.Get(HeaderEngineer); got != "" {
		t.Errorf("pull must not send %s (Rust sent it on push only): %q", HeaderEngineer, got)
	}
	if len(rec.body) != 0 {
		t.Errorf("pull body = %q, want empty", rec.body)
	}
	if res.Failed() {
		t.Fatalf("Failed() = true for a 200 status: %+v", res)
	}
	if res.Body != envelope {
		t.Errorf("Body = %q, want the remote document %q", res.Body, envelope)
	}
	want := fmt.Sprintf("Successfully connected to %s (env: production)", srv.URL+"/")
	if got := res.Message(); got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}

	var stdout, stderr bytes.Buffer
	res.Report(&stdout, &stderr)
	if strings.TrimSpace(stdout.String()) != want || stderr.Len() != 0 {
		t.Errorf("Report: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestPullErrors(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := fakeRemote(t, http.StatusUnauthorized, "Missing X-LeanKG-Token header", nil)
		res, err := Pull(context.Background(), Options{Remote: srv.URL, Token: "", Env: "production"})
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
		if _, err := Pull(context.Background(), Options{Remote: remote, Env: "production"}); err == nil {
			t.Fatal("pull from a dead remote must fail")
		} else if !strings.Contains(err.Error(), remote) {
			t.Fatalf("error = %v, want it to name %s", err, remote)
		}
	})
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
