package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/portfolioreg"
)

// TestPortfolioHonoursProjectGrant pins the fan-out under a project grant: a
// token scoped to project:alpha fans out to alpha only, and the answer counts
// beta as withheld without naming it. The T0 manifest is filtered the same way.
// A caller with no grant (no auth in the context) sees both, with none withheld.
func TestPortfolioHonoursProjectGrant(t *testing.T) {
	fleet := seedGrantFleet(t)
	ctx := context.Background()
	e := fleet.engine
	granted := auth.WithAuth(ctx, auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:alpha"}})

	out, err := e.Query(granted, QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["projects_registered"] != 1 || out["projects_served"] != 1 || out["projects_withheld"] != 1 {
		t.Fatalf("granted fan-out envelope = registered %v served %v withheld %v; want 1, 1, 1",
			out["projects_registered"], out["projects_served"], out["projects_withheld"])
	}
	hits, _ := out["hits"].([]map[string]any)
	if len(hits) != 1 || hits[0]["project"] != "alpha" {
		t.Fatalf("granted hits = %+v; want only alpha", out["hits"])
	}
	children, _ := out["children"].([]portfolioreg.ChildResult)
	if len(children) != 1 || children[0].Project != "alpha" {
		t.Fatalf("granted children name %+v; want only alpha", children)
	}
	for _, c := range children {
		if strings.Contains(c.Dir, fleet.betaDir) {
			t.Fatalf("withheld project named in children: %+v", c)
		}
	}

	summary, err := e.Query(granted, QueryRequest{Action: "portfolio", Args: map[string]any{"cmd": PortfolioSummaryAction}})
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := summary["projects"].([]portfolioreg.ManifestEntry)
	if summary["count"] != 1 || summary["projects_withheld"] != 1 || len(entries) != 1 || entries[0].Project != "alpha" {
		t.Fatalf("granted manifest = count %v withheld %v entries %+v; want alpha only, 1 withheld",
			summary["count"], summary["projects_withheld"], entries)
	}

	open, err := e.Query(ctx, QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if open["projects_served"] != 2 || open["projects_withheld"] != 0 {
		t.Fatalf("no-grant fan-out served %v withheld %v; want 2 and 0", open["projects_served"], open["projects_withheld"])
	}
}

// seedGrantFleet registers alpha and beta in a fresh portfolio registry and
// returns the registry path, beta's directory and an engine over the fleet.
type grantFleet struct {
	regDB   string
	betaDir string
	engine  *Engine
}

func seedGrantFleet(t *testing.T) grantFleet {
	t.Helper()
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	base := t.TempDir()
	dirA, dirB := filepath.Join(base, "alpha"), filepath.Join(base, "beta")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		seedFleetProject(t, d)
	}
	ctx := context.Background()
	opts := portfolioreg.Options{DBPath: regDB}
	if _, err := portfolioreg.Register(ctx, opts, dirA, "alpha", 1, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := portfolioreg.Register(ctx, opts, dirB, "beta", 1, 1, nil); err != nil {
		t.Fatal(err)
	}
	e, _ := newEngine(t)
	return grantFleet{regDB: regDB, betaDir: dirB, engine: e}
}

// TestPortfolioWithheldOnlyGuidance pins the guidance for a restricted caller
// whose every registered project is withheld: the answer names no project and
// does not claim the registry is empty. An unrestricted caller of an empty
// registry still gets the register-a-project hint.
func TestPortfolioWithheldOnlyGuidance(t *testing.T) {
	fleet := seedGrantFleet(t)
	nobody := auth.WithAuth(context.Background(), auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:gamma"}})
	out, err := fleet.engine.Query(nobody, QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["projects_registered"] != 0 || out["projects_withheld"] != 2 {
		t.Fatalf("withheld-only envelope = registered %v withheld %v; want 0 and 2", out["projects_registered"], out["projects_withheld"])
	}
	const want = "No project is visible to this token."
	if out["guidance"] != want {
		t.Fatalf("withheld-only guidance = %q; want %q", out["guidance"], want)
	}
	body, _ := json.Marshal(out)
	for _, leak := range []string{fleet.regDB, fleet.betaDir, `"alpha"`, `"beta"`} {
		if strings.Contains(string(body), leak) {
			t.Fatalf("withheld-only answer names %q: %s", leak, body)
		}
	}

	t.Setenv(portfolioreg.DBPathEnv, filepath.Join(t.TempDir(), "empty.db"))
	e, _ := newEngine(t)
	empty, err := e.Query(context.Background(), QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := empty["guidance"].(string); !strings.Contains(g, "No projects in the portfolio registry") {
		t.Fatalf("unrestricted empty registry guidance = %q; want the register-a-project hint", g)
	}
}

// TestPortfolioRestrictedOmitsRegistry pins fix 3: a restricted caller's answer
// (fan-out and T0 manifest) carries no registry path and no withheld project's
// directory. The withheld count stays. An unrestricted caller keeps the registry.
func TestPortfolioRestrictedOmitsRegistry(t *testing.T) {
	fleet := seedGrantFleet(t)
	ctx := context.Background()
	granted := auth.WithAuth(ctx, auth.AuthContext{Role: auth.Viewer, Via: auth.ViaToken, Scopes: []string{"project:alpha"}})
	queries := map[string]QueryRequest{
		"fan-out":  {Action: "portfolio", Query: "Shared", Limit: 5},
		"manifest": {Action: "portfolio", Args: map[string]any{"cmd": PortfolioSummaryAction}},
	}
	for name, q := range queries {
		out, err := fleet.engine.Query(granted, q)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(out)
		if _, ok := out["registry"]; ok || strings.Contains(string(body), fleet.regDB) || strings.Contains(string(body), fleet.betaDir) {
			t.Fatalf("%s: restricted answer carries the registry or a withheld directory: %s", name, body)
		}
		if out["projects_withheld"] != 1 {
			t.Fatalf("%s: projects_withheld = %v, want 1", name, out["projects_withheld"])
		}
	}

	open, err := fleet.engine.Query(ctx, QueryRequest{Action: "portfolio", Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := open["registry"]; !ok {
		t.Fatalf("unrestricted answer must keep the registry field; got keys %v", open)
	}
}
