package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/store"
)

// isolation is the orgFixture plus tokens minted through the REST token route
// (POST /api/v1/auth/token), so the tests exercise the same issuing code a
// client gets: an admin token, org-A (owns alpha) and org-B (owns beta). Both
// org tokens carry org_id only, no account_id.
type isolation struct {
	orgFixture
	admin string
	orgA  string
	orgB  string
}

func newIsolation(t *testing.T) isolation {
	t.Helper()
	f := newOrgFixture(t)
	admin, _, err := auth.Mint(context.Background(), f.home, auth.MintRequest{Name: "iso-admin", Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	return isolation{
		orgFixture: f,
		admin:      admin,
		orgA:       mintTokenViaRoute(t, f.home, admin, "iso-org-a", "org-1", "contributor"),
		orgB:       mintTokenViaRoute(t, f.home, admin, "iso-org-b", "org-2", "contributor"),
	}
}

// mintTokenViaRoute issues a token through POST /api/v1/auth/token and returns
// its plaintext.
func mintTokenViaRoute(t *testing.T, st auth.Store, admin, name, orgID, role string) string {
	t.Helper()
	body := `{"name":` + jsonString(t, name) + `,"org_id":` + jsonString(t, orgID) + `,"role":` + jsonString(t, role) + `}`
	code, resp := sendJSON(t, auth.Routes(st), http.MethodPost, "/api/v1/auth/token", admin, body)
	if code != http.StatusOK {
		t.Fatalf("token route for %s = %d %s; want 200", name, code, resp)
	}
	var out struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil || out.Data.AccessToken == "" {
		t.Fatalf("token route for %s returned no access_token: %s", name, resp)
	}
	return out.Data.AccessToken
}

// sendJSON issues one request with an optional bearer and returns status and body.
func sendJSON(t *testing.T, h http.Handler, method, target, bearer, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out, _ := io.ReadAll(rec.Body)
	return rec.Code, string(out)
}

// restStatus is GET /api/v1/status with wire as the raw ?project= value, so a
// test controls the exact percent-encoding the client sends. An empty wire
// omits the parameter.
func restStatus(t *testing.T, h http.Handler, wire, bearer string) (int, string) {
	t.Helper()
	target := "/api/v1/status"
	if wire != "" {
		target += "?project=" + wire
	}
	return sendJSON(t, h, http.MethodGet, target, bearer, "")
}

// selectorForm is one way a client can name a project. value is the decoded
// selector the server resolves; named is the part of value that names the
// project (swapped for a nonexistent name to build the control).
type selectorForm struct {
	name  string
	value string
	named string
	rest  string // REST wire form; "" = url.QueryEscape(value)
	mcp   string // MCP argument; "" = value (MCP does not percent-decode)
}

func (s selectorForm) wire() string {
	if s.rest != "" {
		return s.rest
	}
	return url.QueryEscape(s.value)
}

func (s selectorForm) arg() string {
	if s.mcp != "" {
		return s.mcp
	}
	return s.value
}

// nonexistent is the same shape with the project's name replaced by nope.
func (s selectorForm) nonexistent(nope string) selectorForm {
	return selectorForm{name: s.name, value: strings.ReplaceAll(s.value, s.named, nope)}
}

// isolationForms returns beta's selector forms as org-A may send them. Beta is
// the project org-B owns.
func isolationForms(t *testing.T, f isolation) []selectorForm {
	t.Helper()
	base := filepath.Base(f.beta)
	sub := filepath.Join(f.beta, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-to-b")
	if err := os.Symlink(f.beta, link); err != nil {
		t.Fatal(err)
	}
	resolved := realOrSelf(f.beta)
	return []selectorForm{
		{name: "bare name", value: base, named: base},
		{name: "bare name, other letter case", value: strings.ToUpper(base), named: strings.ToUpper(base)},
		{name: "bare name, percent-encoded letter", value: base, named: base,
			rest: fmt.Sprintf("%%%02x%s", base[0], base[1:]), mcp: fmt.Sprintf("%%%02x%s", base[0], base[1:])},
		{name: "absolute path as given", value: f.beta, named: base},
		{name: "absolute path, symlinks resolved", value: resolved, named: base},
		{name: "absolute path, percent-encoded", value: resolved, named: base, mcp: url.QueryEscape(resolved)},
		{name: "subdirectory inside the project", value: sub, named: base},
		{name: "trailing slash", value: f.beta + "/", named: base},
		{name: "dot-dot inside the project", value: f.beta + "/sub/../", named: base},
		{name: "dot-dot into the project from a sibling", value: f.alpha + "/../" + base, named: base},
		{name: "symlink to the project", value: link, named: "link-to-b"},
	}
}

// checkIsolationMatrix asserts, for every form, that bearer's answer for a
// project registered to another org is byte-identical to the answer for the
// same selector once that project does not exist, and that the answer names
// neither the other org's project nor its path.
func checkIsolationMatrix(t *testing.T, forms []selectorForm, bearer string,
	probe func(selectorForm, string) (int, string), refused func(code int, body string) bool,
	echo func(selectorForm) string, hidden []string, unregister func()) {
	t.Helper()
	nope := "nope-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	type answer struct {
		code int
		body string
	}
	before := make([]answer, len(forms))
	for i, s := range forms {
		code, body := probe(s, bearer)
		before[i] = answer{code, body}
		if !refused(code, body) {
			t.Errorf("%s (%q): registered to another org, answered %d %s; want the unknown-project refusal", s.name, echo(s), code, body)
		}
		rest := blankSelector(body, echo(s))
		for _, h := range hidden {
			if strings.Contains(rest, h) {
				t.Errorf("%s (%q): refusal names another org's project %q: %s", s.name, echo(s), h, body)
			}
		}
	}

	unregister()
	for i, s := range forms {
		code, body := probe(s, bearer)
		if code != before[i].code || body != before[i].body {
			t.Errorf("%s (%q): registered-to-other-org answer differs from the nonexistent answer\n  hidden:      %d %s\n  nonexistent: %d %s",
				s.name, echo(s), before[i].code, before[i].body, code, body)
		}
		n := s.nonexistent(nope)
		nCode, nBody := probe(n, bearer)
		if nCode != code || blankSelector(nBody, echo(n)) != blankSelector(body, echo(s)) {
			t.Errorf("%s (%q): differs from a random nonexistent name beyond the echoed selector\n  name:  %d %s\n  probe: %d %s",
				s.name, echo(s), code, body, nCode, nBody)
		}
	}
}

// TestOrgIsolationMatrixOverREST: org-A asks for org-B's project in every
// selector form on the REST ?project= path.
func TestOrgIsolationMatrixOverREST(t *testing.T) {
	f := newIsolation(t)
	h := f.restHandlerOver(f.router)
	if code, body := restStatus(t, h, filepath.Base(f.alpha), f.orgA); code != http.StatusOK {
		t.Fatalf("org-A on its own project = %d %s; want 200", code, body)
	}
	if code, body := restStatus(t, h, filepath.Base(f.beta), f.orgB); code != http.StatusOK {
		t.Fatalf("org-B on its own project = %d %s; want 200", code, body)
	}
	if code, body := restStatus(t, h, filepath.Base(f.beta), f.legacy); code != http.StatusOK {
		t.Fatalf("token without org on beta = %d %s; want 200", code, body)
	}
	forms := isolationForms(t, f)
	probe := func(s selectorForm, bearer string) (int, string) { return restStatus(t, h, s.wire(), bearer) }
	echo := func(s selectorForm) string { return s.value }
	hidden := []string{filepath.Base(f.beta), realOrSelf(f.beta)}
	checkIsolationMatrix(t, forms, f.orgA, probe, func(code int, body string) bool { return code == http.StatusNotFound }, echo, hidden, func() {
		if err := f.router.Remove(f.beta); err != nil {
			t.Fatal(err)
		}
	})
}

// TestOrgIsolationMatrixOverMCP: the same matrix on the MCP status tool's
// `project` argument.
func TestOrgIsolationMatrixOverMCP(t *testing.T) {
	f := newIsolation(t)
	srv := leankgmcp.New(f.defaultEng, leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(f.router)
	srv.SetAuthStore(f.home)
	h := srv.HTTPHandler()
	argsFor := func(arg string) string { return `{"project":` + jsonString(t, arg) + `}` }

	if body := mcpStatus(t, h, argsFor(filepath.Base(f.alpha)), f.orgA); !strings.Contains(body, `\"elements\":1`) {
		t.Fatalf("org-A on its own project over MCP = %s; want alpha's status", body)
	}
	if body := mcpStatus(t, h, argsFor(filepath.Base(f.beta)), f.orgB); strings.Contains(body, "unknown project") {
		t.Fatalf("org-B on its own project over MCP = %s; want an answer", body)
	}
	if body := mcpStatus(t, h, argsFor(filepath.Base(f.beta)), f.legacy); strings.Contains(body, "unknown project") {
		t.Fatalf("token without org over MCP = %s; want an answer", body)
	}
	forms := isolationForms(t, f)
	probe := func(s selectorForm, bearer string) (int, string) {
		// MCP answers over JSON-RPC with HTTP 200; the body carries the verdict.
		return http.StatusOK, mcpStatus(t, h, argsFor(s.arg()), bearer)
	}
	echo := func(s selectorForm) string { return s.arg() }
	hidden := []string{filepath.Base(f.beta), realOrSelf(f.beta)}
	checkIsolationMatrix(t, forms, f.orgA, probe, func(_ int, body string) bool { return strings.Contains(body, "unknown project") }, echo, hidden, func() {
		if err := f.router.Remove(f.beta); err != nil {
			t.Fatal(err)
		}
	})
}

// TestRevokedOrgTokenIsRejected: once revoked, an org token gets the 401 that
// any unknown token gets, on both REST and MCP.
func TestRevokedOrgTokenIsRejected(t *testing.T) {
	f := newIsolation(t)
	const name = "iso-revoked"
	tok := mintTokenViaRoute(t, f.home, f.admin, name, "org-1", "contributor")
	h := f.restHandlerOver(f.router)
	srv := leankgmcp.New(f.defaultEng, leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(f.router)
	srv.SetAuthStore(f.home)
	mcpH := srv.HTTPHandler()
	alpha := filepath.Base(f.alpha)

	if code, body := restStatus(t, h, alpha, tok); code != http.StatusOK {
		t.Fatalf("before revoke, REST = %d %s; want 200", code, body)
	}
	rows, err := f.home.TokenList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, r := range rows {
		if r.Name == name {
			id = r.ID
		}
	}
	if id == "" {
		t.Fatalf("minted token %q not listed", name)
	}
	if code, body := sendJSON(t, auth.Routes(f.home), http.MethodPost, "/api/v1/auth/token/revoke", f.admin,
		`{"token_id":`+jsonString(t, id)+`}`); code != http.StatusOK {
		t.Fatalf("revoke = %d %s; want 200", code, body)
	}
	if code, body := restStatus(t, h, alpha, tok); code != http.StatusUnauthorized {
		t.Fatalf("revoked token over REST = %d %s; want 401", code, body)
	}
	if body := mcpStatus(t, mcpH, `{"project":`+jsonString(t, alpha)+`}`, tok); !strings.Contains(body, "unauthorized") {
		t.Fatalf("revoked token over MCP = %s; want unauthorized", body)
	}
}

// TestHostedRefusesOrgTokenWithoutProject: in hosted mode an org-scoped caller
// that names no project is refused on REST and MCP, not answered from a default.
func TestHostedRefusesOrgTokenWithoutProject(t *testing.T) {
	f := newIsolation(t)
	hosted := projects.NewRouter(f.router.Default(), projects.Config{
		ExtraDirs: []string{f.alpha, f.beta}, Mode: store.RW, EngineName: store.EngineSQLite, Hosted: true,
	})
	t.Cleanup(func() { _ = hosted.Close() })

	code, body := restStatus(t, f.restHandlerOver(hosted), "", f.orgA)
	if code != http.StatusBadRequest || !strings.Contains(body, "must name its project") {
		t.Fatalf("hosted REST without project = %d %s; want 400 naming the project", code, body)
	}
	srv := leankgmcp.New(f.defaultEng, leankgmcp.WithProjectRequired(true))
	srv.SetProjectRouter(hosted)
	srv.SetAuthStore(f.home)
	if body := mcpStatus(t, srv.HTTPHandler(), `{}`, f.orgA); !strings.Contains(body, "LEANKG_ERROR_MISSING_PARAM") {
		t.Fatalf("hosted MCP without project = %s; want LEANKG_ERROR_MISSING_PARAM", body)
	}
}

// TestProjectAdminIsolation: the admin registration route refuses a
// contributor and a viewer, refuses a second org for an owned directory without
// changing its owner, and after DELETE an org-B request answers as an unknown
// project.
func TestProjectAdminIsolation(t *testing.T) {
	f := newIsolation(t)
	ctx := context.Background()
	admin := projectAdminRoute(f.home, f.router, portfolioreg.Options{Engine: store.EngineSQLite})
	extra := filepath.Join(filepath.Dir(f.alpha), "extra-repo")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatal(err)
	}
	register := `{"dir":` + jsonString(t, extra) + `,"org_id":"org-1"}`

	if code, body := sendJSON(t, admin, http.MethodPost, "/api/v1/projects", f.orgA, register); code != http.StatusForbidden {
		t.Fatalf("contributor registration = %d %s; want 403", code, body)
	}
	if code, body := sendJSON(t, admin, http.MethodPost, "/api/v1/projects", f.legacy, register); code != http.StatusForbidden {
		t.Fatalf("viewer registration = %d %s; want 403", code, body)
	}
	if got := f.router.List(); len(got) != 3 {
		t.Fatalf("refused registrations changed the router: %v", got)
	}

	steal := `{"dir":` + jsonString(t, f.alpha) + `,"org_id":"org-2"}`
	if code, body := sendJSON(t, admin, http.MethodPost, "/api/v1/projects", f.admin, steal); code != http.StatusConflict {
		t.Fatalf("second org for alpha = %d %s; want 409", code, body)
	}
	if orgs, err := f.home.ResourceOrgs(ctx, auth.ResourceProject, realOrSelf(f.alpha)); err != nil || len(orgs) != 1 || orgs[0] != "org-1" {
		t.Fatalf("alpha owners after 409 = %v, %v; want [org-1]", orgs, err)
	}

	if code, body := sendJSON(t, admin, http.MethodDelete, "/api/v1/projects", f.admin,
		`{"dir":`+jsonString(t, f.beta)+`}`); code != http.StatusOK {
		t.Fatalf("DELETE beta = %d %s; want 200", code, body)
	}
	h := f.restHandlerOver(f.router)
	code, gone := restStatus(t, h, filepath.Base(f.beta), f.orgB)
	nopeCode, nope := restStatus(t, h, "nope-missing", f.orgB)
	if code != http.StatusNotFound || nopeCode != http.StatusNotFound {
		t.Fatalf("after DELETE: beta = %d, nonexistent = %d; want 404 for both", code, nopeCode)
	}
	if blankSelector(gone, filepath.Base(f.beta)) != blankSelector(nope, "nope-missing") {
		t.Fatalf("after DELETE, org-B's answer for beta is not the unknown-project answer:\n  beta: %s\n  nope: %s", gone, nope)
	}
}
