package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/projects"
	"github.com/FreePeak/LeanKG/go/internal/rest"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// seedProjectDir creates a project directory with an initialized store
// holding one element per name in `symbols`.
func seedProjectDir(t *testing.T, dir string, symbols ...string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	var els []store.Element
	for i, s := range symbols {
		els = append(els, store.Element{
			QualifiedName: "pkg." + s, ElementType: "func", Name: s,
			FilePath: "a.go", LineStart: i + 1, LineEnd: i + 1, Language: "go",
		})
	}
	if len(els) > 0 {
		if err := st.UpsertElements(els); err != nil {
			t.Fatal(err)
		}
	}
}

// engineOver opens a serving-style engine over a seeded project dir.
func engineOver(t *testing.T, dir string) *core.Engine {
	t.Helper()
	st, err := store.OpenBackend(context.Background(), dir, "sqlite", "", store.RW)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	eng := core.New(st, nil, nil)
	eng.SetProjectDir(dir)
	t.Cleanup(func() { _ = st.Close() })
	return eng
}

// realOrSelf resolves symlinks (macOS /tmp -> /private/tmp) so path
// comparisons compare the same spelling on both sides.
func realOrSelf(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}

func statusElements(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, body)
	}
	var out struct {
		Elements float64 `json:"elements"`
		Store    string  `json:"store"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode status %s: %v", body, err)
	}
	return int(out.Elements), out.Store
}

// TestRESTProjectRouting proves `?project=` selects the project's own
// store over one REST surface: dir path and name both route, the default
// project answers when the parameter is absent, and an unknown selector
// is rejected instead of falling back to another project's data
// (FR-ZCP-02).
func TestRESTProjectRouting(t *testing.T) {
	defaultDir := t.TempDir()
	dirA := t.TempDir()
	dirB := t.TempDir()
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, dirA, "AlphaSym")
	seedProjectDir(t, dirB, "BetaSym", "BetaSym2")

	defaultEng := engineOver(t, defaultDir)
	router := projects.NewRouter(defaultDir, projects.Config{
		ExtraDirs: []string{dirA, dirB}, Mode: store.RW, EngineName: "sqlite",
	})
	router.SeedDefault(&projects.Project{
		Dir: defaultDir, Name: filepath.Base(defaultDir), Engine: defaultEng,
	})
	defer router.Close()

	ctx := context.Background()
	h := routeByProject(ctx, router, rest.Handler(defaultEng, nil), func(p *projects.Project) http.Handler {
		return rest.Handler(p.Engine, p.Memory)
	})
	srv := httptest.NewServer(h)
	defer srv.Close()

	nameA, nameB := filepath.Base(dirA), filepath.Base(dirB)
	cases := []struct {
		name     string
		sel      string
		wantEls  int
		wantDir  string
		wantCode int
	}{
		{"default-no-param", "", 1, defaultDir, 200},
		{"by-name", nameA, 1, dirA, 200},
		{"by-dir-path", dirB, 2, dirB, 200},
		{"by-name-second", nameB, 2, dirB, 200},
		{"unknown", "does-not-exist", 0, "", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url := srv.URL + "/api/v1/status"
			if tc.sel != "" {
				url += "?project=" + tc.sel
			}
			if tc.wantCode != 200 {
				resp, err := http.Get(url)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != tc.wantCode {
					t.Fatalf("status = %d, want %d (%s)", resp.StatusCode, tc.wantCode, body)
				}
				if !strings.Contains(string(body), "known projects") {
					t.Fatalf("404 body must list known projects, got %s", body)
				}
				return
			}
			els, storePath := statusElements(t, url)
			if els != tc.wantEls {
				t.Fatalf("elements = %d, want %d (store %s)", els, tc.wantEls, storePath)
			}
			wantStore := filepath.Join(realOrSelf(tc.wantDir), ".leankg", "leankg.db")
			if filepath.Clean(realOrSelf(storePath)) != filepath.Clean(wantStore) {
				t.Fatalf("routed to store %s, want %s", storePath, wantStore)
			}
		})
	}
}

// TestServeMultiProjectEndToEnd is the acceptance guard for
// LEANKG_PROJECT_DIRS: one `serve` process fronts two registered
// projects and each `?project=` request answers from its own store.
func TestServeMultiProjectEndToEnd(t *testing.T) {
	bin := buildLeanKG(t)
	defaultDir := t.TempDir()
	dirA := t.TempDir()
	dirB := t.TempDir()
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, dirA, "AlphaSym")
	seedProjectDir(t, dirB, "BetaSym", "BetaSym2")

	port := freePort(t)
	cmd := exec.Command(bin, "serve", "--project", defaultDir, "--rest", fmt.Sprintf("127.0.0.1:%d", port))
	cmd.Env = append(os.Environ(),
		"LEANKG_PROJECT_DIRS="+dirA+","+dirB,
		"LEANKG_DB_ENGINE=sqlite",
	)
	out := &strings.Builder{}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	waitHealthy(t, base+"/health", out)

	nameA, nameB := filepath.Base(dirA), filepath.Base(dirB)
	for _, tc := range []struct {
		sel     string
		wantEls int
		dir     string
	}{
		{nameA, 1, dirA},
		{nameB, 2, dirB},
		{dirA, 1, dirA},
	} {
		els, storePath := statusElements(t, base+"/api/v1/status?project="+tc.sel)
		if els != tc.wantEls {
			t.Fatalf("project %q: elements = %d, want %d (%s)\nserve log:\n%s",
				tc.sel, els, tc.wantEls, storePath, out)
		}
		realDir, rerr := filepath.EvalSymlinks(tc.dir)
		if rerr != nil {
			realDir = tc.dir
		}
		if !strings.HasPrefix(filepath.Clean(storePath), filepath.Clean(realDir)) {
			t.Fatalf("project %q routed to %s, want a store under %s", tc.sel, storePath, tc.dir)
		}
	}
	// The default project (no selector) still answers.
	if els, _ := statusElements(t, base+"/api/v1/status"); els != 1 {
		t.Fatalf("default project elements = %d, want 1", els)
	}
	// Unknown selector: rejected, never silently defaulted.
	resp, err := http.Get(base + "/api/v1/status?project=nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown project status = %d, want 404", resp.StatusCode)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func waitHealthy(t *testing.T, healthURL string, log *strings.Builder) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("serve did not become healthy at %s\nlog:\n%s", healthURL, log)
}
