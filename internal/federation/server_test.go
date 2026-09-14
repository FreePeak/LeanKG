package federation

// Receiver tests: the server half of #372 over real SQLite stores. They pin the
// apply policy (upsert by key, never delete, provenance in metadata, content
// preserved), the auth ladder the handler enforces for itself, the published
// envelope, and the push-then-pull round trip both halves exist for.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/store"
)

// clearTokenEnv keeps the token gate hermetic: the env fallback is process-wide,
// and a developer machine with LEANKG_TOKEN_* set would otherwise change which
// requests the ladder admits.
func clearTokenEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LEANKG_TOKEN_ADMIN", "LEANKG_TOKEN_CONTRIBUTOR", "LEANKG_TOKEN_VIEWER"} {
		t.Setenv(k, "")
	}
}

// mint mints a DB-backed token of the given role. Minting is what turns the
// gate on: with no token anywhere the store is open (auth's local default).
func mint(t *testing.T, st store.Backend, role string) string {
	t.Helper()
	secret, _, err := auth.Mint(st, auth.MintRequest{Name: "federation-" + role, Role: role})
	if err != nil {
		t.Fatalf("mint %s token: %v", role, err)
	}
	return secret
}

// serve runs one request against h and returns the response.
func serve(t *testing.T, h http.Handler, method, target, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, r)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeDoc(t *testing.T, rec *httptest.ResponseRecorder) graphDocument {
	t.Helper()
	var doc graphDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	return doc
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v\n%s", err, rec.Body.String())
	}
	return body.Error
}

func elementsByQN(t *testing.T, st store.Backend) map[string]store.Element {
	t.Helper()
	els, err := st.Elements()
	if err != nil {
		t.Fatalf("read elements: %v", err)
	}
	out := make(map[string]store.Element, len(els))
	for _, e := range els {
		out[e.QualifiedName] = e
	}
	return out
}

func elementCount(t *testing.T, st store.Backend) int {
	t.Helper()
	return len(elementsByQN(t, st))
}

// assertProvenance checks the three metadata stamps a sync leaves on a row.
func assertProvenance(t *testing.T, meta map[string]any, source, env string) {
	t.Helper()
	if got := meta[MetaSource]; got != source {
		t.Errorf("%s = %v, want %q", MetaSource, got, source)
	}
	if got := meta[MetaEnv]; got != env {
		t.Errorf("%s = %v, want %q", MetaEnv, got, env)
	}
	raw, ok := meta[MetaPushedAt].(string)
	if !ok {
		t.Fatalf("%s = %v, want an RFC3339 string", MetaPushedAt, meta[MetaPushedAt])
	}
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		t.Errorf("%s = %q, not RFC3339: %v", MetaPushedAt, raw, err)
	}
}

// pushDoc builds the envelope a client sends for the given rows.
func pushDoc(t *testing.T, env, service string, elements []wireElement, relationships []wireRelationship) string {
	t.Helper()
	body, err := json.Marshal(pushPayload{Env: env, Service: service, Elements: elements, Relationships: relationships})
	if err != nil {
		t.Fatalf("encode push payload: %v", err)
	}
	return string(body)
}

// TestReceiverAppliesPushPolicy is the heart of #372: what a received graph may
// and may not do to the receiving store.
func TestReceiverAppliesPushPolicy(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	token := mint(t, st, "contributor")
	h := Receiver(st, "/srv/shared-project")

	doc := pushDoc(t, "production", "pusher-service", []wireElement{
		// Same identity as a local row: the incoming fields win, but the local
		// content survives (the wire format has no content slot).
		{QualifiedName: "pkg.Svc.Handle", ElementType: "function", Name: "Handle", FilePath: "renamed.go",
			LineStart: 30, LineEnd: 44, Language: "go", ParentQualified: optional("pkg.Svc"),
			Metadata: map[string]any{"kind": "handler", "owner": "team-a"}},
		// A qualified name the receiver has never seen.
		{QualifiedName: "pkg.New", ElementType: "function", Name: "New", FilePath: "new.go",
			LineStart: 1, LineEnd: 3, Language: "go", Metadata: nil},
	}, []wireRelationship{
		// Conflicting edge: same identity, new confidence.
		{SourceQualified: "pkg.Svc.Handle", TargetQualified: "pkg.parse", RelType: "calls", Confidence: 0.25},
		{SourceQualified: "pkg.New", TargetQualified: "pkg.Svc.Handle", RelType: "calls", Confidence: 1},
	})

	rec := serve(t, h, http.MethodPost, PushPath, doc, map[string]string{"Authorization": "Bearer " + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	ack := decodeDoc(t, rec)
	if ack.AppliedElements != 2 || ack.AppliedRelationships != 2 {
		t.Errorf("ack = %d elements / %d relationships, want 2/2", ack.AppliedElements, ack.AppliedRelationships)
	}
	if ack.Service != "pusher-service" || ack.Env != "production" {
		t.Errorf("ack envelope = %+v, want the pusher's env/service echoed", ack.pushPayload)
	}
	// The response is the same envelope shape the client sends, so one decoder
	// handles a push answer and a pull body alike.
	if ack.Elements == nil || ack.Relationships == nil {
		t.Errorf("ack envelope arrays must be present (possibly empty), got %#v", ack.pushPayload)
	}

	// Two local rows (one overwritten in place) plus the one the document
	// introduces: upsert by identity, never append.
	els := elementsByQN(t, st)
	if len(els) != 3 {
		t.Fatalf("elements = %d, want 3", len(els))
	}
	handle := els["pkg.Svc.Handle"]
	if handle.Name != "Handle" || handle.FilePath != "renamed.go" || handle.LineStart != 30 || handle.ElementType != "function" {
		t.Errorf("incoming row did not win the conflict: %+v", handle)
	}
	if handle.Content != "func (s *Svc) Handle()" {
		t.Errorf("content = %q, want the local text preserved (wire has no content)", handle.Content)
	}
	if got := handle.Metadata["owner"]; got != "team-a" {
		t.Errorf("row metadata lost: %v", handle.Metadata)
	}
	assertProvenance(t, handle.Metadata, "pusher-service", "production")

	if got := els["pkg.New"]; got.Content != "" {
		t.Errorf("a name the receiver had never seen must land contentless, got %q", got.Content)
	}
	assertProvenance(t, els["pkg.New"].Metadata, "pusher-service", "production")

	rels, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("relationships = %d, want 3 (two local + one new, one overwritten)", len(rels))
	}
	byKey := map[string]store.Relationship{}
	for _, r := range rels {
		byKey[r.Source+"->"+r.Target+":"+r.RelType] = r
	}
	// The conflicting edge took the incoming confidence; the local-only edge is
	// untouched, provenance and all.
	if got := byKey["pkg.Svc.Handle->pkg.parse:calls"]; got.Confidence != 0.25 {
		t.Errorf("conflicting edge confidence = %v, want the incoming 0.25", got.Confidence)
	}
	if got := byKey["pkg.parse->pkg.Svc:member_of"]; got.Confidence != 1 {
		t.Errorf("unmentioned edge changed: %+v", got)
	}
	if _, stamped := byKey["pkg.parse->pkg.Svc:member_of"].Metadata[MetaSource]; stamped {
		t.Error("an edge the document never mentioned was stamped with provenance")
	}
	assertProvenance(t, byKey["pkg.Svc.Handle->pkg.parse:calls"].Metadata, "pusher-service", "production")
}

// TestReceiverNeverDeletesUnmentionedRows: a partial push is an update, not a
// replacement. Losing this would make every sync a graph wipe.
func TestReceiverNeverDeletesUnmentionedRows(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	token := mint(t, st, "contributor")

	before := elementCount(t, st)
	relsBefore, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}

	doc := pushDoc(t, "local", "tiny", []wireElement{
		{QualifiedName: "pkg.Only", ElementType: "function", Name: "Only", FilePath: "only.go", Language: "go"},
	}, nil)
	rec := serve(t, Receiver(st, "/srv/p"), http.MethodPost, PushPath, doc, map[string]string{"Authorization": "Bearer " + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	after := elementsByQN(t, store.Backend(st))
	if len(after) != before+1 {
		t.Fatalf("elements = %d, want %d (one added, none removed)", len(after), before+1)
	}
	for _, qn := range []string{"pkg.Svc.Handle", "pkg.parse"} {
		if _, ok := after[qn]; !ok {
			t.Errorf("%s was deleted by a push that never mentioned it", qn)
		}
	}
	relsAfter, err := st.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(relsAfter) != len(relsBefore) {
		t.Errorf("relationships = %d, want %d untouched", len(relsAfter), len(relsBefore))
	}
}

// TestReceiverAuthLadder pins the handler's own gate: the process-wide
// middleware does not classify these paths as writes, so the receiver must.
func TestReceiverAuthLadder(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	viewer := mint(t, st, "viewer")
	contributor := mint(t, st, "contributor")
	admin := mint(t, st, "admin")
	h := Receiver(st, "/srv/shared-project")
	body := pushDoc(t, "local", "svc", nil, nil)

	for name, tc := range map[string]struct {
		method, path string
		body         string
		header       map[string]string
		want         int
	}{
		"push without a token":       {http.MethodPost, PushPath, body, nil, http.StatusUnauthorized},
		"push with a bad token":      {http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer lkg_nope"}, http.StatusUnauthorized},
		"push as viewer":             {http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer " + viewer}, http.StatusForbidden},
		"push as contributor":        {http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer " + contributor}, http.StatusOK},
		"push as admin":              {http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer " + admin}, http.StatusOK},
		"push with legacy header":    {http.MethodPost, PushPath, body, map[string]string{HeaderToken: contributor}, http.StatusOK},
		"push with legacy bad token": {http.MethodPost, PushPath, body, map[string]string{HeaderToken: "lkg_nope"}, http.StatusUnauthorized},
		"read without a token":       {http.MethodGet, GraphPath, "", nil, http.StatusUnauthorized},
		"read as viewer":             {http.MethodGet, GraphPath, "", map[string]string{"Authorization": "Bearer " + viewer}, http.StatusOK},
		"read with legacy header":    {http.MethodGet, GraphPath, "", map[string]string{HeaderToken: viewer}, http.StatusOK},
		"unknown route":              {http.MethodGet, "/api/v2/other", "", map[string]string{"Authorization": "Bearer " + admin}, http.StatusMethodNotAllowed},
		"wrong method":               {http.MethodPut, PushPath, body, map[string]string{"Authorization": "Bearer " + admin}, http.StatusMethodNotAllowed},
	} {
		t.Run(name, func(t *testing.T) {
			rec := serve(t, h, tc.method, tc.path, tc.body, tc.header)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want != http.StatusOK && decodeError(t, rec) == "" {
				t.Errorf("error response carried no message: %s", rec.Body.String())
			}
		})
	}
}

// TestReceiverOpenWhenNoTokensConfigured keeps the local default intact: with
// no token anywhere the routes are open, exactly like every other endpoint.
func TestReceiverOpenWhenNoTokensConfigured(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	h := Receiver(st, "/srv/local")

	rec := serve(t, h, http.MethodPost, PushPath, pushDoc(t, "local", "svc", nil, nil), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("open posture push = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, h, http.MethodGet, GraphPath, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("open posture read = %d, want 200", rec.Code)
	}
}

// TestReceiverRejectsMalformedGraph: the trust boundary. A document the store
// could not address afterwards is refused, and refused before anything lands —
// a push must not be half-applied.
func TestReceiverRejectsMalformedGraph(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	token := mint(t, st, "contributor")
	h := Receiver(st, "/srv/p")

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"blank qualified_name": {
			body: pushDoc(t, "local", "svc", []wireElement{{QualifiedName: "  ", Name: "x"}}, nil),
			want: "no qualified_name",
		},
		"relationship without a target": {
			body: pushDoc(t, "local", "svc", []wireElement{{QualifiedName: "pkg.Fine", Name: "Fine"}},
				[]wireRelationship{{SourceQualified: "pkg.Fine", RelType: "calls"}}),
			want: "missing source, target or type",
		},
		"relationship without a type": {
			body: pushDoc(t, "local", "svc", []wireElement{{QualifiedName: "pkg.Fine", Name: "Fine"}},
				[]wireRelationship{{SourceQualified: "pkg.Fine", TargetQualified: "pkg.Other"}}),
			want: "missing source, target or type",
		},
		"not json": {body: `{"elements":`, want: "decode push body"},
	} {
		t.Run(name, func(t *testing.T) {
			before := elementCount(t, st)
			rec := serve(t, h, http.MethodPost, PushPath, tc.body, map[string]string{"Authorization": "Bearer " + token})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if msg := decodeError(t, rec); !strings.Contains(msg, tc.want) {
				t.Errorf("error = %q, want it to mention %q", msg, tc.want)
			}
			if after := elementCount(t, st); after != before {
				t.Errorf("element count changed from %d to %d on a rejected document", before, after)
			}
		})
	}
}

// TestReceiverPublishesGraph pins the read envelope a pull consumes: the same
// keys a push sends, never null arrays, and the serving project's identity.
func TestReceiverPublishesGraph(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	h := Receiver(st, "/srv/shared-project/")

	rec := serve(t, h, http.MethodGet, GraphPath, "", map[string]string{HeaderEnv: "staging"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	doc := decodeDoc(t, rec)
	if doc.Service != "shared-project" {
		t.Errorf("service = %q, want the project basename", doc.Service)
	}
	if doc.Env != "staging" {
		t.Errorf("env = %q, want the requested environment echoed", doc.Env)
	}
	if len(doc.Elements) != 2 || len(doc.Relationships) != 2 {
		t.Fatalf("published %d elements / %d relationships, want 2/2", len(doc.Elements), len(doc.Relationships))
	}
	if doc.AppliedElements != 2 || doc.AppliedRelationships != 2 {
		t.Errorf("counts = %d/%d, want 2/2", doc.AppliedElements, doc.AppliedRelationships)
	}
	// The published rows are the Rust wire shape: no content, no cluster
	// columns, env always the local default (the Go store has no env column).
	body := rec.Body.String()
	if strings.Contains(body, `"content"`) {
		t.Errorf("published graph must not carry content:\n%s", body)
	}
	for _, want := range []string{`"cluster_id":null`, `"cluster_label":null`, `"env":"local"`} {
		if !strings.Contains(body, want) {
			t.Errorf("published graph missing %s:\n%s", want, body)
		}
	}
	// An empty store answers empty arrays, never null: a decoder must not need
	// a nil check to tell "nothing here" from "no answer".
	empty := Receiver(openStore(t), "/srv/empty")
	rec = serve(t, empty, http.MethodGet, GraphPath, "", nil)
	if !strings.Contains(rec.Body.String(), `"elements":[]`) || !strings.Contains(rec.Body.String(), `"relationships":[]`) {
		t.Errorf("empty graph must serialize as [] not null: %s", rec.Body.String())
	}
}

// TestRegisterMountsBothRoutes pins the mount Main splices into rest.Handler:
// the receiver's own dispatch and a mux's method patterns must agree.
func TestRegisterMountsBothRoutes(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)

	mux := http.NewServeMux()
	Register(mux, st, "/srv/mounted")

	if rec := serve(t, mux, http.MethodGet, GraphPath, "", nil); rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", GraphPath, rec.Code)
	}
	rec := serve(t, mux, http.MethodGet, PushPath, "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET %s = %d, want 405 (the mux rejects the method)", PushPath, rec.Code)
	}
	doc := pushDoc(t, "local", "svc", []wireElement{
		{QualifiedName: "pkg.Mounted", ElementType: "function", Name: "Mounted", FilePath: "m.go", Language: "go"},
	}, nil)
	if rec := serve(t, mux, http.MethodPost, PushPath, doc, nil); rec.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200: %s", PushPath, rec.Code, rec.Body.String())
	}
	if _, ok := elementsByQN(t, st)["pkg.Mounted"]; !ok {
		t.Error("mounted receiver did not apply the pushed element")
	}
	// A path the receiver does not serve must not be shadowed by the mount.
	if rec := serve(t, mux, http.MethodGet, "/api/v2/graph/extra", "", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unrelated path = %d, want 404", rec.Code)
	}
}

// TestReceiverBehindAuthMiddleware composes the receiver the way the server
// mounts it (auth.MiddlewareWithStore wrapping the router), so the receiver's
// self-authorization is proven not to depend on the mount.
func TestReceiverBehindAuthMiddleware(t *testing.T) {
	clearTokenEnv(t)
	st := openStore(t)
	seedGraph(t, st)
	viewer := mint(t, st, "viewer")
	contributor := mint(t, st, "contributor")

	mux := http.NewServeMux()
	Register(mux, st, "/srv/gated")
	h := auth.MiddlewareWithStore(st, mux)
	body := pushDoc(t, "local", "svc", nil, nil)

	if rec := serve(t, h, http.MethodPost, PushPath, body, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated push = %d, want 401 from the middleware", rec.Code)
	}
	// The middleware admits a viewer (these paths are not in its write prefix
	// list); the receiver's own gate is what refuses the write.
	if rec := serve(t, h, http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer " + viewer}); rec.Code != http.StatusForbidden {
		t.Errorf("viewer push = %d, want 403 from the receiver", rec.Code)
	}
	if rec := serve(t, h, http.MethodPost, PushPath, body, map[string]string{"Authorization": "Bearer " + contributor}); rec.Code != http.StatusOK {
		t.Errorf("contributor push = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := serve(t, h, http.MethodGet, GraphPath, "", map[string]string{"Authorization": "Bearer " + viewer}); rec.Code != http.StatusOK {
		t.Errorf("viewer read = %d, want 200", rec.Code)
	}
}

// TestFederationPushPullRoundTrip is the #372 acceptance path end to end: one
// project pushes to another over HTTP, and a third pulls the result back out —
// both halves the issue asks for, wired through the real client and receiver.
func TestFederationPushPullRoundTrip(t *testing.T) {
	clearTokenEnv(t)

	source := openStore(t)
	seedGraph(t, source)

	receiver := openStore(t)
	token := mint(t, receiver, "contributor")
	mux := http.NewServeMux()
	Register(mux, receiver, "/srv/receiver")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	res, err := Push(context.Background(), source, Options{
		Remote: srv.URL, Token: token, Env: "production", ProjectDir: "/tmp/work/alpha",
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if res.Failed() {
		t.Fatalf("push failed: %s", res.Message())
	}

	// The receiver holds the source graph, stamped with where it came from.
	landed := elementsByQN(t, receiver)
	if len(landed) != 2 {
		t.Fatalf("receiver elements = %d, want 2", len(landed))
	}
	assertProvenance(t, landed["pkg.parse"].Metadata, "alpha", "production")

	// A third project pulls that graph back out through the read route.
	third := openStore(t)
	pulled, err := Pull(context.Background(), third, Options{
		Remote: srv.URL, Token: token, Env: "production",
	})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if !pulled.Applied || pulled.Elements != 2 || pulled.Relationships != 2 {
		t.Fatalf("pulled = %+v, want 2/2 applied", pulled)
	}
	final := elementsByQN(t, third)
	for _, qn := range []string{"pkg.Svc.Handle", "pkg.parse"} {
		if _, ok := final[qn]; !ok {
			t.Errorf("%s did not survive the round trip", qn)
		}
	}
	rels, err := third.RelationshipsAll(maxRelationships)
	if err != nil {
		t.Fatalf("relationships: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("third relationships = %d, want 2", len(rels))
	}
	// Provenance survives the hop: the receiving server re-stamps with the
	// service that pushed into it, which is what makes the chain traceable.
	assertProvenance(t, final["pkg.parse"].Metadata, "receiver", "production")
	want := fmt.Sprintf("Pulled 2 elements and 2 relationships from %s (env: production)", srv.URL)
	if got := pulled.Message(); got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}
}
