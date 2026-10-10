package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// fakeAdapter is a sessionlink.Adapter that reads nothing.
type fakeAdapter struct {
	client string
	detect bool
	roots  []string
}

func (f fakeAdapter) Client() string        { return f.client }
func (f fakeAdapter) Detect(string) bool    { return f.detect }
func (f fakeAdapter) Roots(string) []string { return f.roots }
func (f fakeAdapter) Locate(string, sessionlink.CallRef) (sessionlink.TranscriptRef, sessionlink.Confidence, error) {
	return sessionlink.TranscriptRef{}, 0, sessionlink.ErrNotFound
}
func (f fakeAdapter) Window(sessionlink.TranscriptRef, sessionlink.CallRef, int) (sessionlink.Window, error) {
	return sessionlink.Window{}, sessionlink.ErrNotFound
}
func (f fakeAdapter) Transcript(sessionlink.TranscriptRef, int) ([]sessionlink.Turn, error) {
	return nil, nil
}

// fakeStore is a seeded ledger stand-in: it answers every read as an empty
// ledger and reports the seeded stats.
type fakeStore struct {
	emptyStore
	stats telemetry.Stats
}

func (f *fakeStore) Stats(context.Context) (telemetry.Stats, error) { return f.stats, nil }

func testServer(t *testing.T, home string, mutate func(*Options)) *Server {
	t.Helper()
	opts := Options{
		Home:     home,
		UserHome: home,
		Adapters: []sessionlink.Adapter{
			fakeAdapter{client: "claude-code", detect: true, roots: []string{filepath.Join(home, ".claude", "projects")}},
			fakeAdapter{client: "pi"},
		},
		Now: func() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) },
	}
	if mutate != nil {
		mutate(&opts)
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func do(t *testing.T, h http.Handler, method, path string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "127.0.0.1:9701"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// nullLists are the JSON keys that must never be null (RS-09).
var nullLists = map[string]bool{
	"kpis": true, "series": true, "top_failures": true, "by_client": true, "outcome_mix": true,
	"sessions": true, "calls": true, "memory": true, "groups": true, "samples": true, "tools": true,
	"rung_mix": true, "messages": true, "content": true, "banks": true, "recent": true,
	"observational": true, "controlled": true, "clients": true, "roots": true,
	"fallback_tools": true, "failures": true, "tool_calls": true,
}

func assertNoNullLists(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if nullLists[k] && val == nil {
				t.Errorf("%s: list %q is null", path, k)
			}
			assertNoNullLists(t, path+"."+k, val)
		}
	case []any:
		for _, e := range x {
			assertNoNullLists(t, path+"[]", e)
		}
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body is not JSON (%d): %q: %v", rec.Code, rec.Body.String(), err)
	}
	return v
}

var apiGETs = []struct {
	path   string
	status int
}{
	{"/api/dashboard/v1/overview?since=7d", http.StatusOK},
	{"/api/dashboard/v1/overview?since=bogus", http.StatusBadRequest},
	{"/api/dashboard/v1/sessions?client=pi&limit=5&offset=0", http.StatusOK},
	{"/api/dashboard/v1/sessions/claude-code:abc", http.StatusNotFound},
	{"/api/dashboard/v1/sessions/claude-code:abc/transcript", http.StatusNotFound},
	{"/api/dashboard/v1/calls/missing", http.StatusNotFound},
	{"/api/dashboard/v1/failures?group=tool", http.StatusOK},
	{"/api/dashboard/v1/failures?group=nope", http.StatusBadRequest},
	{"/api/dashboard/v1/tools", http.StatusOK},
	{"/api/dashboard/v1/memory?bank=default", http.StatusOK},
	{"/api/dashboard/v1/evidence", http.StatusOK},
	{"/api/dashboard/v1/consent", http.StatusOK},
}

func TestRoutesWithoutLedgerServeEmptyShapes(t *testing.T) {
	home := t.TempDir()
	var opened int
	srv := testServer(t, home, func(o *Options) {
		o.openStore = func(string, bool) (telemetry.Store, error) { opened++; return nil, os.ErrNotExist }
	})
	h := srv.Handler()
	for _, c := range apiGETs {
		rec := do(t, h, http.MethodGet, c.path, nil, "")
		if rec.Code != c.status {
			t.Errorf("GET %s: status %d, want %d; body %s", c.path, rec.Code, c.status, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("GET %s: content-type %q", c.path, ct)
		}
		assertNoNullLists(t, c.path, decodeJSON(t, rec))
	}
	if opened != 0 {
		t.Errorf("store opened %d times without a ledger file", opened)
	}
	if _, err := os.Stat(telemetry.DBPath(home)); !os.IsNotExist(err) {
		t.Errorf("dashboard created %s (err=%v)", telemetry.DBPath(home), err)
	}
}

func TestSeededLedgerIsOpenedAndConsulted(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(telemetry.DBPath(home), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{stats: telemetry.Stats{Path: telemetry.DBPath(home), SizeBytes: 4096, DroppedEvents: 7}}
	srv := testServer(t, home, func(o *Options) {
		o.openStore = func(string, bool) (telemetry.Store, error) { return fs, nil }
	})
	h := srv.Handler()
	for _, c := range apiGETs {
		rec := do(t, h, http.MethodGet, c.path, nil, "")
		if rec.Code >= 500 {
			t.Errorf("GET %s: status %d", c.path, rec.Code)
		}
		assertNoNullLists(t, c.path, decodeJSON(t, rec))
	}
	rec := do(t, h, http.MethodGet, "/api/dashboard/v1/consent", nil, "")
	var s report.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.DroppedEvents != 7 || s.DBSizeBytes != 4096 {
		t.Errorf("settings did not read the seeded store: dropped=%d size=%d", s.DroppedEvents, s.DBSizeBytes)
	}
}

func TestUnknownAPIRoutesAreJSON404(t *testing.T) {
	srv := testServer(t, t.TempDir(), nil)
	h := srv.Handler()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/dashboard/v1/nope"},
		{http.MethodGet, "/api/unknown"},
		{http.MethodPost, "/api/dashboard/v1/overview"},
	} {
		rec := do(t, h, c.method, c.path, nil, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d", c.method, c.path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: content-type %q", c.method, c.path, ct)
		}
		if v, ok := decodeJSON(t, rec).(map[string]any); !ok || v["error"] == nil {
			t.Errorf("%s %s: body is not an error envelope", c.method, c.path)
		}
	}
}

func TestSPAFallbackServesIndexForClientRoutes(t *testing.T) {
	srv := testServer(t, t.TempDir(), nil)
	h := srv.Handler()
	index, err := fs.ReadFile(assets, "embed/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/sessions", "/sessions/claude-code:abc", "/settings"} {
		rec := do(t, h, http.MethodGet, p, nil, "")
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: content-type %q", p, ct)
		}
		if rec.Body.String() != string(index) {
			t.Errorf("GET %s: not the embedded index.html", p)
		}
	}
	if rec := do(t, h, http.MethodGet, "/assets/missing.js", nil, ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing asset: status %d, want 404", rec.Code)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv := testServer(t, t.TempDir(), nil)
	h := srv.Handler()
	rec := do(t, h, http.MethodGet, "/api/dashboard/v1/overview", nil, "")
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("API Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing nosniff")
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", csp)
	}
	page := do(t, h, http.MethodGet, "/", nil, "")
	if page.Header().Get("Content-Security-Policy") == "" {
		t.Errorf("SPA response has no CSP")
	}
}

func TestConsentGETReportsAdaptersAndCSRF(t *testing.T) {
	home := t.TempDir()
	srv := testServer(t, home, nil)
	rec := do(t, srv.Handler(), http.MethodGet, "/api/dashboard/v1/consent", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var s report.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.CSRFToken == "" || s.CSRFToken != srv.csrf {
		t.Errorf("csrf token missing or not the server's: %q", s.CSRFToken)
	}
	if s.Capture != "off" || s.ConsentCurrent {
		t.Errorf("fresh home should be capture off without consent: %+v", s)
	}
	if len(s.Clients) != 2 || !s.Clients[0].Found || s.Clients[0].Client != "claude-code" || s.Clients[1].Found {
		t.Errorf("clients = %+v", s.Clients)
	}
	if s.Clients[0].Roots[0] != filepath.Join(home, ".claude", "projects") {
		t.Errorf("roots = %v", s.Clients[0].Roots)
	}
}

func consentHeaders(srv *Server) map[string]string {
	return map[string]string{
		"Origin":       "http://127.0.0.1:9701",
		"X-CSRF-Token": srv.csrf,
		"Content-Type": "application/json",
	}
}

const grantBody = `{"capture":"metadata","sessions":["claude-code"],"purge":false}`

func TestConsentPOSTRefusesMissingCSRFOrForeignOrigin(t *testing.T) {
	home := t.TempDir()
	srv := testServer(t, home, nil)
	h := srv.Handler()
	cases := map[string]map[string]string{
		"no csrf":        {"Origin": "http://127.0.0.1:9701"},
		"wrong csrf":     {"Origin": "http://127.0.0.1:9701", "X-CSRF-Token": "nope"},
		"foreign origin": {"Origin": "https://evil.example", "X-CSRF-Token": srv.csrf},
		"no origin":      {"X-CSRF-Token": srv.csrf},
	}
	for name, hdr := range cases {
		rec := do(t, h, http.MethodPost, "/api/dashboard/v1/consent", hdr, grantBody)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", name, rec.Code)
		}
	}
	if _, err := os.Stat(telemetry.ConfigPath(home)); !os.IsNotExist(err) {
		t.Errorf("refused POST wrote the config (err=%v)", err)
	}
}

func TestConsentPOSTWritesConfig(t *testing.T) {
	home := t.TempDir()
	srv := testServer(t, home, nil)
	rec := do(t, srv.Handler(), http.MethodPost, "/api/dashboard/v1/consent", consentHeaders(srv), grantBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capture != telemetry.Metadata {
		t.Errorf("capture = %q", cfg.Capture)
	}
	if !telemetry.SessionsGranted(cfg, "claude-code") || telemetry.SessionsGranted(cfg, "pi") {
		t.Errorf("sessions grant = %+v", cfg.Sessions)
	}
	if cfg.Consent.GrantedBy != "dashboard" || cfg.Consent.Version != telemetry.ConsentVersion || cfg.Consent.GrantedAt.IsZero() {
		t.Errorf("consent = %+v", cfg.Consent)
	}
	raw, err := os.ReadFile(telemetry.ConfigPath(home))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	for _, want := range []string{"capture: metadata", "granted_by: dashboard"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("telemetry.yaml lacks %q:\n%s", want, raw)
		}
	}
}

func TestConsentPOSTRejectsBadInput(t *testing.T) {
	srv := testServer(t, t.TempDir(), nil)
	h := srv.Handler()
	for _, body := range []string{
		`{"capture":"everything"}`,
		`{"capture":"metadata","sessions":["not-a-client"]}`,
		`{"capture":"metadata","purge":true}`,
		`not json`,
	} {
		rec := do(t, h, http.MethodPost, "/api/dashboard/v1/consent", consentHeaders(srv), body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400", body, rec.Code)
		}
	}
}

func TestConsentPurgeRemovesLedger(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"telemetry.db", "telemetry.db-wal", "telemetry.db-shm"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	srv := testServer(t, home, func(o *Options) {
		o.openStore = func(string, bool) (telemetry.Store, error) { return &fakeStore{}, nil }
	})
	body := `{"capture":"off","sessions":[],"purge":true}`
	rec := do(t, srv.Handler(), http.MethodPost, "/api/dashboard/v1/consent", consentHeaders(srv), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"telemetry.db", "telemetry.db-wal", "telemetry.db-shm"} {
		if _, err := os.Stat(filepath.Join(home, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived purge (err=%v)", name, err)
		}
	}
}

func TestRemoteModeRequiresBearer(t *testing.T) {
	srv := testServer(t, t.TempDir(), func(o *Options) {
		o.AllowRemote = true
		o.Token = "s3cret-token"
	})
	h := srv.Handler()
	for _, hdr := range []map[string]string{
		nil,
		{"Authorization": "Bearer wrong"},
		{"Authorization": "s3cret-token"},
	} {
		if rec := do(t, h, http.MethodGet, "/api/dashboard/v1/overview", hdr, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("hdr %v: status %d, want 401", hdr, rec.Code)
		}
	}
	ok := do(t, h, http.MethodGet, "/api/dashboard/v1/overview", map[string]string{"Authorization": "Bearer s3cret-token"}, "")
	if ok.Code != http.StatusOK {
		t.Errorf("valid bearer: status %d", ok.Code)
	}
	if rec := do(t, h, http.MethodGet, "/", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("SPA without bearer: status %d, want 401", rec.Code)
	}
}

func TestLoopbackModeNeedsNoBearer(t *testing.T) {
	srv := testServer(t, t.TempDir(), func(o *Options) { o.Token = "ignored-in-loopback" })
	if rec := do(t, srv.Handler(), http.MethodGet, "/api/dashboard/v1/overview", nil, ""); rec.Code != http.StatusOK {
		t.Errorf("loopback without bearer: status %d", rec.Code)
	}
}

func TestNewRejectsRemoteWithoutToken(t *testing.T) {
	if _, err := New(Options{Home: t.TempDir(), AllowRemote: true}); err == nil {
		t.Fatal("allow-remote without a token must be refused")
	}
}

func TestCheckListen(t *testing.T) {
	cases := []struct {
		addr   string
		remote bool
		token  string
		ok     bool
	}{
		{"127.0.0.1:9701", false, "", true},
		{"localhost:9701", false, "", true},
		{"[::1]:9701", false, "", true},
		{"0.0.0.0:9701", false, "", false},
		{"192.168.1.5:9701", false, "tok", false},
		{"0.0.0.0:9701", true, "tok", true},
		{"127.0.0.1:9701", true, "tok", true},
		{"0.0.0.0:9701", true, "", false},
		{"not-an-address", false, "", false},
	}
	for _, c := range cases {
		err := CheckListen(c.addr, c.remote, c.token)
		if (err == nil) != c.ok {
			t.Errorf("CheckListen(%q, remote=%v, token=%q) err=%v, want ok=%v", c.addr, c.remote, c.token, err, c.ok)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"":                     {},
		"24h":                  now.Add(-24 * time.Hour),
		"7d":                   now.AddDate(0, 0, -7),
		"2w":                   now.AddDate(0, 0, -14),
		"2026-10-01T00:00:00Z": time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, err := ParseSince(in, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("ParseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"7", "0d", "-3d", "7m", "x"} {
		if _, err := ParseSince(bad, now); err == nil {
			t.Errorf("ParseSince(%q) accepted", bad)
		}
	}
}

func TestNormalizeListsEmitsEmptyArrays(t *testing.T) {
	type inner struct {
		Items []int `json:"items"`
	}
	type outer struct {
		Kpis  []string `json:"kpis"`
		Child *inner   `json:"child,omitempty"`
		Kids  []inner  `json:"kids"`
	}
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, outer{Child: &inner{}, Kids: []inner{{}}})
	want := `{"kpis":[],"child":{"items":[]},"kids":[{"items":[]}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s\nwant %s", got, want)
	}
}

// The embed holds either the placeholder (explains how to build) or a real
// `make go-ui-dashboard` build; either way every asset index.html references
// must be embedded, so a partial or stale copy fails here, not in a browser.
func TestEmbedIndexIsCompleteOrExplainsBuild(t *testing.T) {
	sub, err := fs.Sub(assets, "embed")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="/([^"]+\.(?:js|css|svg))"`).FindAllStringSubmatch(string(raw), -1)
	if len(refs) == 0 {
		if !strings.Contains(string(raw), "make go-ui-dashboard") {
			t.Fatal("index.html is neither a built SPA nor the placeholder that explains the build")
		}
		return
	}
	for _, m := range refs {
		if _, err := fs.Stat(sub, m[1]); err != nil {
			t.Errorf("index.html references %s, which is not embedded", m[1])
		}
	}
}
