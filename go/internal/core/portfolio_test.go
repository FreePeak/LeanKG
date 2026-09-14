// Portfolio fan-out through the REAL ladder (issue #376). The policy tests live
// in internal/portfolioreg; these prove the one thing only core can prove: that
// a fleet query opens each registered project read-only, runs the normal
// L0–L3 ladder inside it, and hands back merged, project-attributed answers.
package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// seedFleetProject gives dir a real store with one callable element.
func seedFleetProject(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: "pkg.Shared", ElementType: "function", Name: "Shared",
		FilePath: "shared.go", Language: "go",
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestPortfolioQueryFansOutThroughLadder(t *testing.T) {
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	dirA, dirB := t.TempDir(), t.TempDir()
	seedFleetProject(t, dirA)
	seedFleetProject(t, dirB) // same symbol in both projects
	ctx := context.Background()
	opts := portfolioreg.Options{DBPath: regDB}
	if _, err := portfolioreg.Register(ctx, opts, dirA, "alpha", 1, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := portfolioreg.Register(ctx, opts, dirB, "beta", 1, 1, nil); err != nil {
		t.Fatal(err)
	}

	e, _ := newEngine(t) // the serving project is a third, unrelated store
	out, err := e.portfolioQuery(ctx, QueryRequest{Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["action"] != PortfolioAction || out["projects_registered"] != 2 || out["projects_served"] != 2 {
		t.Fatalf("portfolio envelope = %+v", out)
	}
	hits, _ := out["hits"].([]map[string]any)
	if len(hits) != 2 {
		t.Fatalf("merged hits = %+v; want one per project", out["hits"])
	}
	seen := map[string]bool{}
	for _, h := range hits {
		name, _ := h["project"].(string)
		if name == "" || h["qualified_name"] != "pkg.Shared" {
			t.Fatalf("unattributed or reshaped hit: %+v", h)
		}
		seen[name] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Fatalf("fleet query lost a project's answer: %+v", hits)
	}
	// Every child carries its own freshness label, so a stale repo is visible
	// per project rather than folded into one fleet-wide claim.
	children, _ := out["children"].([]portfolioreg.ChildResult)
	if len(children) != 2 {
		t.Fatalf("children = %+v", out["children"])
	}
	for _, c := range children {
		if c.Status != "ok" || c.Freshness == "" {
			t.Fatalf("child %s = %+v; want ok with a freshness label", c.Project, c)
		}
	}
}

func TestPortfolioQueryReportsUnindexedProjectAsChildError(t *testing.T) {
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	good := t.TempDir()
	seedFleetProject(t, good)
	ghost := filepath.Join(t.TempDir(), "never-indexed")
	ctx := context.Background()
	opts := portfolioreg.Options{DBPath: regDB}
	for _, d := range []string{good, ghost} {
		if _, err := portfolioreg.Register(ctx, opts, d, filepath.Base(d), 1, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	e, _ := newEngine(t)
	out, err := e.portfolioQuery(ctx, QueryRequest{Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["projects_served"] != 1 || out["projects_failed"] != 1 {
		t.Fatalf("counts = %+v; want 1 served, 1 failed (never a dropped child)", out)
	}
	children, _ := out["children"].([]portfolioreg.ChildResult)
	var ghostChild *portfolioreg.ChildResult
	for i := range children {
		if children[i].Dir == ghost {
			ghostChild = &children[i]
		}
	}
	if ghostChild == nil || ghostChild.Status != "error" {
		t.Fatalf("unindexed project = %+v; want an error entry naming the missing store", ghostChild)
	}
	// The fan-out never indexed it: a fleet read must not create a store.
	if _, err := os.Stat(filepath.Join(ghost, ".leankg")); err == nil {
		t.Fatal("portfolio query created .leankg under an unindexed project")
	}
}

// #406-2 on the QUERY path: a T0 manifest PARENT (registered never-indexed
// with zero counts — children carry the stores) must render not_indexed, not
// a store-open error, and must not count toward projects_failed. A ghost that
// CLAIMS counts but has no store stays `error` (the test above) — the shape
// is what distinguishes design from fault.
func TestPortfolioQueryReportsT0ParentAsNotIndexed(t *testing.T) {
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	ctx := context.Background()
	opts := portfolioreg.Options{DBPath: regDB}

	parent := filepath.Join(t.TempDir(), "games")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := portfolioreg.Register(ctx, opts, parent, "games", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	child := t.TempDir()
	seedFleetProject(t, child)
	if _, err := portfolioreg.Register(ctx, opts, child, "the-game", 1, 1, nil); err != nil {
		t.Fatal(err)
	}

	e, _ := newEngine(t)
	out, err := e.portfolioQuery(ctx, QueryRequest{Query: "Shared", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if out["projects_served"] != 1 || out["projects_failed"] != 0 || out["projects_not_indexed"] != 1 {
		t.Fatalf("counts = %+v; want 1 served, 0 failed, 1 not_indexed", out)
	}
	children, _ := out["children"].([]portfolioreg.ChildResult)
	for _, c := range children {
		if c.Dir == parent && (c.Status != "not_indexed" || c.Error != "" || c.Reason == "") {
			t.Fatalf("T0 parent = %+v; want not_indexed with a reason and no error", c)
		}
	}
	// Reading the fleet created no store under the parent.
	if _, err := os.Stat(filepath.Join(parent, ".leankg")); err == nil {
		t.Fatal("portfolio query created .leankg under the T0 parent")
	}
}

func TestPortfolioSummaryIsT0Manifest(t *testing.T) {
	regDB := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv(portfolioreg.DBPathEnv, regDB)
	indexed := t.TempDir()
	seedFleetProject(t, indexed)
	ctx := context.Background()
	if _, err := portfolioreg.Register(ctx, portfolioreg.Options{DBPath: regDB}, indexed, "api", 7, 3, nil); err != nil {
		t.Fatal(err)
	}

	e, _ := newEngine(t)
	// The manifest needs no query text: it is a registry read, not a search.
	out, err := e.portfolioQuery(ctx, QueryRequest{Args: map[string]any{"cmd": PortfolioSummaryAction}})
	if err != nil {
		t.Fatalf("summary must not require query text: %v", err)
	}
	if out["cmd"] != PortfolioSummaryAction || out["count"] != 1 {
		t.Fatalf("summary envelope = %+v", out)
	}
	entries, _ := out["projects"].([]portfolioreg.ManifestEntry)
	if len(entries) != 1 || entries[0].Project != "api" || entries[0].Elements != 7 ||
		entries[0].Files != 3 || !entries[0].StoreOnDisk {
		t.Fatalf("manifest entries = %+v", out["projects"])
	}
	if !strings.HasPrefix(out["registry"].(string), "sqlite portfolio registry at") {
		t.Fatalf("registry provenance = %v", out["registry"])
	}
}

func TestPortfolioQueryRefusesRecursion(t *testing.T) {
	e, _ := newEngine(t)
	if _, err := e.portfolioQuery(context.Background(), QueryRequest{
		Query: "x", Args: map[string]any{"action": PortfolioAction},
	}); err == nil || !strings.Contains(err.Error(), "cannot fan out") {
		t.Fatalf("recursive child action = %v; want the guard", err)
	}
	if _, err := e.portfolioQuery(context.Background(), QueryRequest{}); err == nil {
		t.Fatal("a fan-out with neither query text nor cmd=summary must ask for one")
	}
}
