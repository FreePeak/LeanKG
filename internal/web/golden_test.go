package web

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite the web API goldens in testdata/golden/cx48")

// goldenCase is one request against the seeded fixture. {{PROJECT}} in the
// path or body is replaced with the fixture's project directory, and the
// same directory is replaced with <PROJECT> in the response before comparing.
type goldenCase struct {
	name   string
	method string
	path   string
	body   string
}

var goldenCases = []goldenCase{
	{name: "index_status", method: http.MethodGet, path: "/api/index/status"},
	{name: "children_root", method: http.MethodGet, path: "/api/graph/children"},
	{name: "children_src_page", method: http.MethodGet, path: "/api/graph/children?parent=src&limit=2&offset=1"},
	{name: "children_src_types", method: http.MethodGet, path: "/api/graph/children?parent=src&element_types=function"},
	{name: "expand_root_all", method: http.MethodGet, path: "/api/graph/expand-service?path=.&all=true"},
	{name: "expand_src_page", method: http.MethodGet, path: "/api/graph/expand-service?path=src&limit=2"},
	{name: "expand_service_param", method: http.MethodGet, path: "/api/graph/expand-service?service=service:src"},
	{name: "expand_absolute_path", method: http.MethodGet, path: "/api/graph/expand-service?path={{PROJECT}}/src&all=true"},
	{name: "graph_clusters", method: http.MethodGet, path: "/api/graph/clusters"},
	{name: "service_topology", method: http.MethodGet, path: "/api/graph/service-topology"},
	{name: "service_topology_orphans_depth0", method: http.MethodGet, path: "/api/graph/service-topology?show_orphans=true&depth=0"},
	{name: "graph_report", method: http.MethodGet, path: "/api/graph/report"},
	{name: "query_graph_depth3", method: http.MethodPost, path: "/api/query-graph",
		body: `{"question":"what connects authenticate to query_db?","token_budget":4000,"max_depth":3}`},
	{name: "query_graph_small_budget", method: http.MethodPost, path: "/api/query-graph",
		body: `{"question":"what connects authenticate to query_db?","token_budget":60,"max_depth":3}`},
	{name: "query_graph_depth1", method: http.MethodPost, path: "/api/query-graph",
		body: `{"question":"authenticate","max_depth":1}`},
	{name: "search_substring", method: http.MethodGet, path: "/api/search?q=login"},
	{name: "search_filtered", method: http.MethodGet, path: "/api/search?element_type=function&file_path=src/db"},
	{name: "project_switch_same_dir", method: http.MethodPost, path: "/api/project/switch",
		body: `{"path":"{{PROJECT}}"}`},
}

// TestAPIGoldens pins the response body of every status, graph, search and
// project-switch endpoint for one seeded store. The goldens were captured from
// the pre-refactor base; rewrite them only with -update-golden and review the diff.
func TestAPIGoldens(t *testing.T) {
	dir, eng := seedFixture(t)
	srv := httptest.NewServer(APIHandler(eng, nil))
	t.Cleanup(srv.Close)

	resolved := dir
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		resolved = r
	}
	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.ReplaceAll(tc.path, "{{PROJECT}}", dir)
			body := strings.ReplaceAll(tc.body, "{{PROJECT}}", dir)
			req, err := http.NewRequest(tc.method, srv.URL+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if tc.method == http.MethodPost {
				req.Header.Set("Content-Type", "application/json")
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.ReplaceAll(string(raw), dir, "<PROJECT>")
			got = strings.ReplaceAll(got, resolved, "<PROJECT>")
			got = fmt.Sprintf("HTTP %d\n%s", res.StatusCode, got)

			file := filepath.Join("testdata", "golden", "cx48", tc.name+".json")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("golden missing (run with -update-golden on the base): %v", err)
			}
			if string(want) != got {
				t.Errorf("body differs from golden %s\n--- want\n%s\n--- got\n%s", file, want, got)
			}
		})
	}
}
