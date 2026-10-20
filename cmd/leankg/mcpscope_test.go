package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	leankgmcp "github.com/FreePeak/LeanKG/internal/mcp"
	"github.com/FreePeak/LeanKG/internal/projects"
	"github.com/FreePeak/LeanKG/internal/store"
)

// TestScopedMCPServerAnswersWithoutAProject pins the stdio contract.
//
// A stdio session is ONE client over ONE repository: the client spawned the
// process inside the repo it is working in, so the serve process's own
// project IS the answer to "which project?". The multi-project fail-closed
// rule is for the remote HTTP surface, where the server cannot know which
// project a question is about.
//
// Measured live on the 298-project self-host before this: a stdio session
// was refused with
//
//	LEANKG_ERROR_MISSING_PARAM: this server serves multiple projects
//	(LEANKG_PROJECT_DIRS), so a tool call must name one
//
// even though the process was serving exactly the repository the caller
// was in. That made `leankg serve --stdio` unusable on any host that
// exports LEANKG_PROJECT_DIRS — which is every host running the self-host.
func TestScopedMCPServerAnswersWithoutAProject(t *testing.T) {
	defaultDir := t.TempDir()
	dirA := t.TempDir()
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, dirA, "AlphaSym")

	router := projects.NewRouter(defaultDir, projects.Config{
		ExtraDirs: []string{dirA}, Mode: store.RW, EngineName: "sqlite",
	})
	router.SeedDefault(&projects.Project{
		Dir: defaultDir, Name: filepath.Base(defaultDir), Engine: engineOver(t, defaultDir),
	})
	defer router.Close()

	srv := newScopedMCPServer(engineOver(t, defaultDir), router)
	body := mcpCallStatus(t, srv)

	if strings.Contains(body, "LEANKG_ERROR_MISSING_PARAM") {
		t.Fatalf("a scoped (stdio) session must answer a project-less call; got %s", body)
	}
	if !strings.Contains(body, "elements") {
		t.Fatalf("expected the project's own status, got %s", body)
	}
}

// TestUnscopedMCPServerStillFailsClosed is the other half of the pair: the
// remote HTTP surface keeps refusing a project-less call. Weakening the
// stdio path must not weaken the shared server — an omitted project there
// used to be answered from the serve process's cwd engine, so a question
// about another project got a confident answer built from the WRONG graph.
func TestUnscopedMCPServerStillFailsClosed(t *testing.T) {
	defaultDir := t.TempDir()
	dirA := t.TempDir()
	seedProjectDir(t, defaultDir, "DefaultSym")
	seedProjectDir(t, dirA, "AlphaSym")

	router := projects.NewRouter(defaultDir, projects.Config{
		ExtraDirs: []string{dirA}, Mode: store.RW, EngineName: "sqlite",
	})
	router.SeedDefault(&projects.Project{
		Dir: defaultDir, Name: filepath.Base(defaultDir), Engine: engineOver(t, defaultDir),
	})
	defer router.Close()

	srv := leankgmcp.New(engineOver(t, defaultDir), leankgmcp.WithProjectRequired(router.MultiProject()))
	srv.SetProjectRouter(router)
	body := mcpCallStatus(t, srv)

	if !strings.Contains(body, "LEANKG_ERROR_MISSING_PARAM") {
		t.Fatalf("the unscoped multi-project surface must still fail closed; got %s", body)
	}
}

// mcpCallStatus posts one `status` tool call with NO arguments and returns
// the response body.
func mcpCallStatus(t *testing.T, srv *leankgmcp.Server) string {
	t.Helper()
	h := srv.HTTPHandler()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"status","arguments":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Body.String()
}
