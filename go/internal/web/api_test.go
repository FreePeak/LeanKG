package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// seedFixture builds a temp project with a populated store:
//
//	src/auth.rs   -> authenticate, login (both exist on disk)
//	src/db.rs     -> query_db (exists on disk)
//	src/ghost.rs  -> ghost_fn (NOT on disk — ghost-row case)
func seedFixture(t *testing.T) (dir string, eng *core.Engine) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/auth.rs", "src/db.rs"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("fn stub() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	els := []store.Element{
		{QualifiedName: "src/auth.rs::authenticate", ElementType: "function", Name: "authenticate", FilePath: "src/auth.rs", LineStart: 1, LineEnd: 10, Language: "rust"},
		{QualifiedName: "src/auth.rs::login", ElementType: "function", Name: "login", FilePath: "src/auth.rs", LineStart: 11, LineEnd: 20, Language: "rust"},
		{QualifiedName: "src/db.rs::query_db", ElementType: "function", Name: "query_db", FilePath: "src/db.rs", LineStart: 1, LineEnd: 5, Language: "rust"},
		{QualifiedName: "src/ghost.rs::ghost_fn", ElementType: "function", Name: "ghost_fn", FilePath: "src/ghost.rs", LineStart: 1, LineEnd: 5, Language: "rust"},
	}
	if err := st.UpsertElements(els); err != nil {
		t.Fatal(err)
	}
	rels := []store.Relationship{
		{Source: "src/auth.rs::authenticate", Target: "src/auth.rs::login", RelType: "calls", Confidence: 0.9, Metadata: map[string]any{"resolution_method": "name"}},
		{Source: "src/auth.rs::login", Target: "src/db.rs::query_db", RelType: "calls", Confidence: 0.95, Metadata: map[string]any{"resolution_method": "typed"}},
		{Source: "src/auth.rs::authenticate", Target: "src/ghost.rs::ghost_fn", RelType: "imports", Confidence: 0.2, Metadata: map[string]any{"resolution_method": "unresolved"}},
	}
	if err := st.UpsertRelationships(rels); err != nil {
		t.Fatal(err)
	}
	eng = core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	return dir, eng
}

func newServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir, eng := seedFixture(t)
	srv := httptest.NewServer(APIHandler(eng, nil))
	t.Cleanup(srv.Close)
	return srv, dir
}

func getJSON(t *testing.T, url string, out any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s: content-type %q, want application/json", url, ct)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("%s: decode: %v", url, err)
	}
	return res.StatusCode
}

func postJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	res, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("%s: content-type %q, want application/json", url, ct)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		t.Fatalf("%s: decode: %v", url, err)
	}
	return res.StatusCode
}

// envelope is the ApiEnvelope shape every endpoint must return.
type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   *string         `json:"error"`
}

func TestIndexStatus(t *testing.T) {
	srv, dir := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/index/status", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success || env.Error != nil {
		t.Fatalf("env: %+v", env)
	}
	var data struct {
		IsIndexing        bool    `json:"is_indexing"`
		ProgressPercent   int     `json:"progress_percent"`
		ElementCount      *int    `json:"element_count"`
		RelationshipCount *int    `json:"relationship_count"`
		ProjectPath       *string `json:"project_path"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.IsIndexing {
		t.Error("is_indexing should be false")
	}
	if data.ElementCount == nil || *data.ElementCount != 4 {
		t.Errorf("element_count = %v, want 4", data.ElementCount)
	}
	if data.RelationshipCount == nil || *data.RelationshipCount != 3 {
		t.Errorf("relationship_count = %v, want 3", data.RelationshipCount)
	}
	if data.ProjectPath == nil || *data.ProjectPath != dir {
		t.Errorf("project_path = %v, want %s", data.ProjectPath, dir)
	}
}

func TestQuery(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := postJSON(t, srv.URL+"/api/query", `{"query":"authenticate"}`, &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("query failed: %v", *env.Error)
	}
	var data struct {
		Result struct {
			Hits      []map[string]any `json:"hits"`
			Retrieval struct {
				Rung string `json:"rung"`
			} `json:"retrieval"`
		} `json:"result"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Result.Hits) == 0 {
		t.Fatal("expected hits for exact match authenticate")
	}
	if data.Result.Retrieval.Rung != "L1" {
		t.Errorf("rung = %q, want L1", data.Result.Retrieval.Rung)
	}
}

func TestSearch(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/search?q=auth", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("search failed: %v", *env.Error)
	}
	var hits []store.Element
	if err := json.Unmarshal(env.Data, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Errorf("q=auth matched %d elements, want 2 (authenticate, login)", len(hits))
	}

	// element_type filter narrows to zero here (no class elements).
	if code := getJSON(t, srv.URL+"/api/search?q=auth&element_type=class", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if err := json.Unmarshal(env.Data, &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Errorf("element_type=class matched %d, want 0", len(hits))
	}
}

func TestFile(t *testing.T) {
	srv, dir := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/file?path=src/auth.rs", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("file failed: %v", *env.Error)
	}
	var data struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Content != "fn stub() {}\n" {
		t.Errorf("content = %q", data.Content)
	}

	// Absolute path under the project resolves relative.
	abs := srv.URL + "/api/file?path=" + filepath.Join(dir, "src", "db.rs")
	if code := getJSON(t, abs, &env); code != 200 {
		t.Fatalf("abs status %d", code)
	}

	// Missing file: 404 + the Rust not-found message.
	if code := getJSON(t, srv.URL+"/api/file?path=src/nope.rs", &env); code != 404 {
		t.Fatalf("missing status %d, want 404", code)
	}
	if env.Error == nil || !strings.Contains(*env.Error, "File not found 'src/nope.rs'") {
		t.Errorf("error = %v", env.Error)
	}

	// Directory: 400 + the Rust directory hint.
	if code := getJSON(t, srv.URL+"/api/file?path=src", &env); code != 400 {
		t.Fatalf("dir status %d, want 400", code)
	}
	if env.Error == nil || !strings.Contains(*env.Error, "is a directory") {
		t.Errorf("error = %v", env.Error)
	}

	// Absolute path outside the project: 403 (Rust OutsideProject).
	if code := getJSON(t, srv.URL+"/api/file?path=/etc/passwd", &env); code != 403 {
		t.Fatalf("outside-project status %d, want 403", code)
	}
	if env.Error == nil || !strings.Contains(*env.Error, "outside project") {
		t.Errorf("outside error = %v", env.Error)
	}

	// A relative ".." walk that canonicalizes to a missing path is a 404,
	// not a 403 — the Rust resolver canonicalized first and fell through to
	// NotFound when the target did not exist.
	if code := getJSON(t, srv.URL+"/api/file?path=../../etc/passwd", &env); code != 404 {
		t.Fatalf("relative traversal status %d, want 404", code)
	}
}

func TestGraphChildren(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/graph/children?parent=", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("children failed: %v", *env.Error)
	}
	var data struct {
		Nodes []struct {
			ID         string `json:"id"`
			Properties struct {
				ElementType string `json:"elementType"`
				FilePath    string `json:"filePath"`
			} `json:"properties"`
		} `json:"nodes"`
		Relationships []map[string]any `json:"relationships"`
		TotalCount    int              `json:"totalCount"`
		HasMore       bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	// Root children: src is a synthesized Directory node (elements live at
	// src/x depth; nothing sits directly at root).
	var dirs []string
	for _, n := range data.Nodes {
		if n.Properties.ElementType == "Directory" {
			dirs = append(dirs, n.ID)
		}
	}
	if len(dirs) == 1 && dirs[0] != "folder:src" {
		t.Errorf("synthesized dirs = %v, want [folder:src]", dirs)
	}
	// The two authenticate->login/query_db edges are NOT root children
	// sources (their sources are functions in src/), so root has no edges.
	if len(data.Relationships) != 0 {
		t.Errorf("root children edges = %d, want 0", len(data.Relationships))
	}
}

func TestGraphExpandService(t *testing.T) {
	srv, dir := newServer(t)
	var env envelope
	// Single-repo layout: expanding "." loads all content (FR-MG-03), and
	// the ghost row src/ghost.rs is dropped because it's not on disk.
	url := srv.URL + "/api/graph/expand-service?path=" + dir + "&all=true&limit=500&offset=0"
	if code := getJSON(t, url, &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("expand failed: %v", *env.Error)
	}
	var data struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Relationships []struct {
			SourceID string `json:"sourceId"`
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
		} `json:"relationships"`
		Filtered struct {
			Message string `json:"message"`
		} `json:"filtered"`
		HasMore bool `json:"hasMore"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range data.Nodes {
		ids[n.ID] = true
	}
	if len(data.Nodes) != 3 {
		t.Errorf("nodes = %d, want 3 (ghost dropped): %v", len(data.Nodes), ids)
	}
	if ids["src/ghost.rs::ghost_fn"] {
		t.Error("ghost element should be dropped (file missing on disk)")
	}
	// authenticate->login and login->query_db survive (both endpoints kept);
	// authenticate->ghost_fn is dropped with the ghost target.
	if len(data.Relationships) != 2 {
		t.Errorf("relationships = %d (%+v), want 2", len(data.Relationships), data.Relationships)
	}
	if !strings.Contains(data.Filtered.Message, "dropped 1 missing-on-disk") {
		t.Errorf("message = %q, want dropped-1 hint", data.Filtered.Message)
	}

	// Missing path param → error envelope.
	if code := getJSON(t, srv.URL+"/api/graph/expand-service", &env); code != 200 {
		t.Fatalf("missing-param status %d", code)
	}
	if env.Success || env.Error == nil || !strings.Contains(*env.Error, "Missing 'path'") {
		t.Errorf("missing-param env = %+v", env)
	}
}

func TestGraphClusters(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/graph/clusters", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("clusters failed: %v", *env.Error)
	}
	var data struct {
		Nodes []struct {
			ID         string `json:"id"`
			Label      string `json:"label"`
			Properties struct {
				Name        string `json:"name"`
				ElementType string `json:"elementType"`
			} `json:"properties"`
		} `json:"nodes"`
		Relationships []map[string]any `json:"relationships"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Nodes) != 1 {
		t.Fatalf("clusters = %d, want 1 (all elements under src/)", len(data.Nodes))
	}
	n := data.Nodes[0]
	if n.ID != "cluster:src" || n.Label != "src (4)" {
		t.Errorf("cluster node = %+v, want cluster:src / src (4)", n)
	}
	if n.Properties.ElementType != "Cluster[4 files]" {
		t.Errorf("elementType = %q", n.Properties.ElementType)
	}
	if len(data.Relationships) != 0 {
		t.Errorf("inter-cluster edges = %d, want 0 (single cluster)", len(data.Relationships))
	}
}

func TestServiceTopologySingleRepo(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/graph/service-topology", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("topology failed: %v", *env.Error)
	}
	var data struct {
		Nodes []struct {
			ID         string `json:"id"`
			Properties struct {
				ElementType string `json:"elementType"`
				FilePath    string `json:"filePath"`
			} `json:"properties"`
		} `json:"nodes"`
		Relationships []struct {
			RelType string `json:"rel_type"`
		} `json:"relationships"`
		ProjectType string `json:"projectType"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ProjectType != "single_repo" {
		t.Errorf("projectType = %q, want single_repo", data.ProjectType)
	}
	sawService, sawFolder, sawContains := false, false, 0
	for _, n := range data.Nodes {
		if n.Properties.ElementType == "Service" {
			sawService = true
		}
		if n.Properties.ElementType == "Folder" && n.ID == "folder:src" {
			sawFolder = true
		}
	}
	for _, r := range data.Relationships {
		if r.RelType == "CONTAINS" {
			sawContains++
		}
	}
	if !sawService {
		t.Error("expected a Service node")
	}
	if !sawFolder {
		t.Error("expected a Folder node for src/")
	}
	if sawContains != 1 {
		t.Errorf("CONTAINS edges = %d, want 1", sawContains)
	}
}

func TestServiceTopologyMultiRepo(t *testing.T) {
	dir := t.TempDir()
	// Two sibling git repos; svc-a calls svc-b via a dns:/// reference.
	for _, svc := range []string{"svc-a", "svc-b"} {
		if err := os.MkdirAll(filepath.Join(dir, svc, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := []byte("dial: dns:///" + "svc-b" + ":9090\n")
	if err := os.WriteFile(filepath.Join(dir, "svc-a", "config.yaml"), cfg, 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	srv := httptest.NewServer(APIHandler(eng, nil))
	t.Cleanup(srv.Close)

	var env envelope
	if code := getJSON(t, srv.URL+"/api/graph/service-topology", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	var data struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
		Relationships []struct {
			SourceID string `json:"sourceId"`
			TargetID string `json:"targetId"`
		} `json:"relationships"`
		ProjectType string `json:"projectType"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.ProjectType != "multi_repo" {
		t.Fatalf("projectType = %q, want multi_repo", data.ProjectType)
	}
	var sawEdge bool
	for _, r := range data.Relationships {
		if r.SourceID == "service:svc-a" && r.TargetID == "service:svc-b" {
			sawEdge = true
		}
	}
	if !sawEdge {
		t.Errorf("expected svc-a -> svc-b SERVICE_CALLS edge, got %+v", data.Relationships)
	}
}

func TestGraphReport(t *testing.T) {
	srv, dir := newServer(t)
	var env envelope
	if code := getJSON(t, srv.URL+"/api/graph/report", &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("report failed: %v", *env.Error)
	}
	var data struct {
		Markdown string `json:"markdown"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Graph Report: ",
		"- Total elements: 4",
		"- Total relationships: 3",
		"## Confidence Distribution",
		"## Top God Nodes",
		"## Suggested Questions",
	} {
		if !strings.Contains(data.Markdown, want) {
			t.Errorf("markdown missing %q:\n%s", want, data.Markdown)
		}
	}
	// The .leankg/GRAPH_REPORT.md side effect (Rust wrote it too).
	if _, err := os.Stat(filepath.Join(dir, ".leankg", "GRAPH_REPORT.md")); err != nil {
		t.Errorf("GRAPH_REPORT.md not written: %v", err)
	}
}

func TestQueryGraph(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := postJSON(t, srv.URL+"/api/query-graph",
		`{"question":"what connects authenticate to query_db?","token_budget":4000,"max_depth":3}`, &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	if !env.Success {
		t.Fatalf("query-graph failed: %v", *env.Error)
	}
	var data struct {
		Question string   `json:"question"`
		Seeds    []string `json:"seeds"`
		Nodes    []struct {
			QualifiedName string `json:"qualified_name"`
			IsSeed        bool   `json:"is_seed"`
		} `json:"nodes"`
		Edges []struct {
			From            string `json:"from"`
			To              string `json:"to"`
			ConfidenceLabel string `json:"confidence_label"`
		} `json:"edges"`
		Hops           int  `json:"hops"`
		Truncated      bool `json:"truncated"`
		TokenBudget    int  `json:"token_budget"`
		TokensEstimate int  `json:"tokens_estimate"`
		Path           *struct {
			Hops int `json:"hops"`
		} `json:"path"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Question != "what connects authenticate to query_db?" {
		t.Errorf("question = %q", data.Question)
	}
	if data.TokenBudget != 4000 {
		t.Errorf("token_budget = %d", data.TokenBudget)
	}
	if len(data.Seeds) == 0 {
		t.Fatal("expected seeds")
	}
	var sawExtracted bool
	for _, e := range data.Edges {
		if e.ConfidenceLabel == "EXTRACTED" {
			sawExtracted = true
		}
	}
	if !sawExtracted {
		t.Errorf("expected an EXTRACTED edge, got %+v", data.Edges)
	}
	if data.Path != nil && data.Path.Hops != 2 {
		t.Errorf("path hops = %d, want 2 (authenticate->login->query_db)", data.Path.Hops)
	}
	if data.TokensEstimate <= 0 {
		t.Error("tokens_estimate should be positive")
	}

	// Blank question → error envelope.
	if code := postJSON(t, srv.URL+"/api/query-graph", `{"question":"   "}`, &env); code != 200 {
		t.Fatalf("blank status %d", code)
	}
	if env.Success || env.Error == nil || !strings.Contains(*env.Error, "empty") {
		t.Errorf("blank env = %+v", env)
	}
}

func TestQueryGraphTokenBudget(t *testing.T) {
	srv, _ := newServer(t)
	var env envelope
	if code := postJSON(t, srv.URL+"/api/query-graph",
		`{"question":"what connects authenticate to query_db?","token_budget":200,"max_depth":4}`, &env); code != 200 {
		t.Fatalf("status %d", code)
	}
	var data struct {
		Truncated      bool `json:"truncated"`
		TokenBudget    int  `json:"token_budget"`
		TokensEstimate int  `json:"tokens_estimate"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.TokensEstimate > data.TokenBudget {
		t.Errorf("tokens %d > budget %d", data.TokensEstimate, data.TokenBudget)
	}
	if !data.Truncated {
		t.Errorf("expected truncated=true for a 200-token budget")
	}
}

func TestProjectSwitch(t *testing.T) {
	srv, dir := newServer(t)
	var env envelope

	// Switch to the served project: 200 + current project payload.
	if code := postJSON(t, srv.URL+"/api/project/switch", fmt.Sprintf(`{"path":%q}`, dir), &env); code != 200 {
		t.Fatalf("same-project status %d", code)
	}
	if !env.Success {
		t.Fatalf("switch failed: %v", *env.Error)
	}
	var data struct {
		IsDirectory   bool   `json:"is_directory"`
		HasDatabase   bool   `json:"has_database"`
		NeedsIndexing bool   `json:"needs_indexing"`
		IsGithub      bool   `json:"is_github"`
		ProjectPath   string `json:"project_path"`
		ElementCount  *int   `json:"element_count"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatal(err)
	}
	if !data.IsDirectory || data.ProjectPath != dir {
		t.Errorf("data = %+v", data)
	}
	if data.ElementCount == nil || *data.ElementCount != 4 {
		t.Errorf("element_count = %v, want 4", data.ElementCount)
	}

	// Root is rejected with the Rust message.
	if code := postJSON(t, srv.URL+"/api/project/switch", `{"path":"/"}`, &env); code != 200 {
		t.Fatalf("root status %d", code)
	}
	if env.Error == nil || !strings.Contains(*env.Error, "filesystem root") {
		t.Errorf("root error = %v", env.Error)
	}

	// A different directory: the single-project engine refuses.
	other := t.TempDir()
	if code := postJSON(t, srv.URL+"/api/project/switch", fmt.Sprintf(`{"path":%q}`, other), &env); code != 200 {
		t.Fatalf("other status %d", code)
	}
	if env.Success || env.Error == nil || !strings.Contains(*env.Error, "not supported") {
		t.Errorf("other-project env = %+v", env)
	}

	// github_url is refused too (Rust cloned; Go is single-project).
	if code := postJSON(t, srv.URL+"/api/project/switch", `{"github_url":"https://example.com/x"}`, &env); code != 200 {
		t.Fatalf("github status %d", code)
	}
	if env.Success {
		t.Error("github_url should be refused by the single-project engine")
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	// Mounted together: APIHandler owns /api/, Handler serves the SPA.
	_, eng := seedFixture(t)
	mux := http.NewServeMux()
	mux.Handle("/api/", APIHandler(eng, nil))
	mux.Handle("/", Handler())
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Unknown api route → JSON 404 (never the SPA shell).
	res, err := http.Get(srv.URL + "/api/does/not/exist")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	var env envelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}

	// A client-side route still gets index.html.
	res2, err := http.Get(srv.URL + "/some/client/route")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Fatalf("spa status = %d", res2.StatusCode)
	}
	if ct := res2.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("spa content-type = %q", ct)
	}
}

func TestAllRoutesReturnJSON(t *testing.T) {
	srv, dir := newServer(t)
	routes := []struct {
		method, path string
		body         string
	}{
		{"GET", "/api/index/status", ""},
		{"GET", "/api/search?q=auth", ""},
		{"GET", "/api/file?path=src/auth.rs", ""},
		{"GET", "/api/graph/clusters", ""},
		{"GET", "/api/graph/report", ""},
		{"GET", "/api/graph/children?parent=", ""},
		{"GET", "/api/graph/expand-service?path=.", ""},
		{"GET", "/api/graph/service-topology", ""},
		{"POST", "/api/query", `{"query":"auth"}`},
		{"POST", "/api/query-graph", `{"question":"auth"}`},
		{"POST", "/api/project/switch", fmt.Sprintf(`{"path":%q}`, dir)},
	}
	for _, rt := range routes {
		var res *http.Response
		var err error
		if rt.method == "GET" {
			res, err = http.Get(srv.URL + rt.path)
		} else {
			res, err = http.Post(srv.URL+rt.path, "application/json", strings.NewReader(rt.body))
		}
		if err != nil {
			t.Fatalf("%s %s: %v", rt.method, rt.path, err)
		}
		body := res.Body
		ct := res.Header.Get("Content-Type")
		res.Body.Close()
		_ = body
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("%s %s: content-type %q, want application/json", rt.method, rt.path, ct)
		}
		if res.StatusCode != 200 {
			t.Errorf("%s %s: status %d", rt.method, rt.path, res.StatusCode)
		}
	}
}

func TestDetectCommunities(t *testing.T) {
	// No calls/imports edges → folder fallback, one cluster per directory.
	_, eng := seedFixture(t)
	snap, err := loadSnapshot(eng.Store())
	if err != nil {
		t.Fatal(err)
	}
	rels, err := allRelationships(eng.Store())
	if err != nil {
		t.Fatal(err)
	}
	// The fixture has calls/imports edges, so restrict to the folder
	// fallback by passing an empty edge set.
	fallback := detectCommunities(snap, nil)
	if len(fallback) != 1 {
		t.Fatalf("folder fallback clusters = %d, want 1 (all under src)", len(fallback))
	}
	c := fallback["cluster_0"]
	if c == nil || c.Label != "src" || len(c.Members) != 4 {
		t.Fatalf("fallback cluster = %+v, want src with 4 members", c)
	}
	if len(c.RepresentativeFiles) != 1 || c.RepresentativeFiles[0] != "src" {
		t.Errorf("representative files = %v", c.RepresentativeFiles)
	}

	// With edges: every element lands in exactly one cluster and IDs are
	// dense/sequential.
	withEdges := detectCommunities(snap, rels)
	seen := map[string]string{}
	ids := map[string]bool{}
	for id, cl := range withEdges {
		if ids[id] {
			t.Fatalf("duplicate cluster id %s", id)
		}
		ids[id] = true
		if cl.Label == "" {
			t.Errorf("cluster %s has empty label", id)
		}
		for _, m := range cl.Members {
			if prev, dup := seen[m]; dup {
				t.Fatalf("member %s in both %s and %s", m, prev, id)
			}
			seen[m] = id
		}
	}
	if len(seen) != snap.count() {
		t.Errorf("covered members = %d, want %d", len(seen), snap.count())
	}
	for i := range len(withEdges) {
		if !ids["cluster_"+strconv.Itoa(i)] {
			t.Errorf("missing dense cluster id cluster_%d", i)
		}
	}
}

// TestChildrenFilterElementTypes pins the multi-type OR filter: the Rust
// handler carried an explicit TODO and applied only the first requested type,
// silently dropping the rest — a comma-separated list must now match any of
// the listed types (and an empty list must match everything).
func TestChildrenFilterElementTypes(t *testing.T) {
	els := []store.Element{
		{QualifiedName: "src/a.go::A", ElementType: "function", FilePath: "src/a.go", Name: "A"},
		{QualifiedName: "src/b.go::B", ElementType: "type", FilePath: "src/b.go", Name: "B"},
		{QualifiedName: "src/c.go::C", ElementType: "import", FilePath: "src/c.go", Name: "C"},
	}
	snap := &snapshot{elements: els}
	for _, tc := range []struct {
		types map[string]bool
		want  int
	}{
		{nil, 3},
		{map[string]bool{"function": true}, 1},
		{map[string]bool{"function": true, "type": true}, 2},
		{map[string]bool{"function": true, "type": true, "import": true}, 3},
		{map[string]bool{"nothing": true}, 0},
	} {
		got := childrenFiltered(snap, nil, "src", tc.types, 200, 0)
		if len(got.elements) != tc.want {
			t.Errorf("types=%v: got %d elements, want %d", tc.types, len(got.elements), tc.want)
		}
	}
}
