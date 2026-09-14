// Portfolio registry and scope tests (issue #376). Everything here is hermetic:
// the registry and every project store live under t.TempDir(), and the Postgres
// legs ride the same LEANKG_TEST_PG_URL gate as the rest of the engine.
package portfolioreg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/store"
)

// newProject creates one indexed project: a real store file under
// <dir>/.leankg with `name` as a callable element, so a child query over the
// read-only handle finds it by exact match.
func newProject(t *testing.T, dir, name string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	qn := "pkg." + name
	if err := st.UpsertElements([]store.Element{{
		QualifiedName: qn, ElementType: "function", Name: name,
		FilePath: strings.ToLower(name) + ".go", Language: "go",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.BumpWatermark(); err != nil {
		t.Fatal(err)
	}
}

// storeChild is the test's per-project executor: it opens the project through
// the SAME read-only path production uses (Options.OpenChild) and answers in
// the envelope shape the core child emits (hits + freshness + retrieval.rung).
// That keeps the test honest about the open-per-project contract (a missing
// store errors, it never silently indexes) without importing core's ladder,
// which core's own tests cover.
func storeChild(o Options) Child {
	return func(ctx context.Context, p store.ProjectRecord, q Query) (map[string]any, error) {
		st, err := o.OpenChild(ctx, p.Dir)
		if err != nil {
			return nil, err
		}
		defer st.Close()
		els, err := st.FindExact(q.Text)
		if err != nil {
			return nil, err
		}
		hits := make([]any, 0, len(els))
		for _, el := range els {
			hits = append(hits, map[string]any{"qualified_name": el.QualifiedName, "name": el.Name})
		}
		n, err := st.ElementCount()
		if err != nil {
			return nil, err
		}
		freshness := "fresh"
		if n == 0 {
			freshness = "cold"
		}
		return map[string]any{
			"hits":      hits,
			"freshness": freshness,
			"retrieval": map[string]any{"rung": "L1", "reason": "exact identifier match"},
		}, nil
	}
}

// ptr is the one-pointer helper the test tables need.
func ptr[T any](v T) *T { return &v }

func TestRegisterListAndForget(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()

	// A deployment with no registry is ErrNoRegistry, not an empty fleet a
	// caller cannot distinguish from "registered nothing yet".
	if _, err := Projects(ctx, o); !errors.Is(err, ErrNoRegistry) {
		t.Fatalf("Projects before any register = %v; want ErrNoRegistry", err)
	}

	dirA, dirB := t.TempDir(), t.TempDir()
	newProject(t, dirA, "Alpha")
	newProject(t, dirB, "Beta")

	stamped, err := Register(ctx, o, dirA, "alpha", 1, 1, ptr(time.Unix(1700000000, 0).UTC()))
	if err != nil {
		t.Fatal(err)
	}
	if stamped.Name != "alpha" || stamped.LastIndexed == nil {
		t.Fatalf("Register returned %+v", stamped)
	}
	// Name omitted on re-register keeps the existing row's name; no stamp keeps
	// the existing stamp (the COALESCE contract).
	again, err := Register(ctx, o, dirA, "", 9, 9, nil)
	if err != nil {
		t.Fatal(err)
	}
	if again.Name != "alpha" || again.ElementCount != 9 ||
		again.LastIndexed == nil || *again.LastIndexed != *stamped.LastIndexed {
		t.Fatalf("re-register = %+v; want name/stamp preserved, counts replaced", again)
	}
	if _, err := Register(ctx, o, dirB, "beta", 2, 2, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := Projects(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "alpha" || rows[1].Name != "beta" {
		t.Fatalf("Projects = %+v; want [alpha beta] in name order", rows)
	}
	if gone, err := Forget(ctx, o, dirB); err != nil || !gone {
		t.Fatalf("Forget = %v, %v; want true, nil", gone, err)
	}
	if rows, _ := Projects(ctx, o); len(rows) != 1 {
		t.Fatalf("registry after forget: %+v", rows)
	}
}

func TestPortfolioFanOutMergesAndAttributes(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	newProject(t, dirA, "Alpha")
	newProject(t, dirB, "Alpha") // same symbol in both: the merge has real work
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	if _, err := Register(ctx, o, dirA, "alpha", 1, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, dirB, "beta", 1, 1, nil); err != nil {
		t.Fatal(err)
	}

	rep, err := FanOut(ctx, o, Query{Text: "Alpha", Limit: 5}, NewHotSet(0), storeChild(o))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Registered != 2 || rep.Served != 2 || rep.Failed != 0 || rep.NotHot != 0 {
		t.Fatalf("counts = %+v; want 2 registered, 2 served, 0 failed, 0 not-hot", rep)
	}
	if len(rep.Projects) != 2 {
		t.Fatalf("every registered project must be reported, got %+v", rep.Projects)
	}
	for _, c := range rep.Projects {
		if c.Status != "ok" || len(c.Hits) != 1 || c.Rung != "L1" {
			t.Fatalf("child %s = %+v; want ok with 1 hit and its rung", c.Project, c)
		}
		if c.Hits[0]["project"] != c.Project || c.Hits[0]["project_dir"] != c.Dir {
			t.Fatalf("hit not attributed to its project: %+v", c.Hits[0])
		}
	}
	if len(rep.Hits) != 2 {
		t.Fatalf("merged hits = %+v; want one per project", rep.Hits)
	}
	seen := map[string]bool{}
	for _, h := range rep.Hits {
		seen[h["project"].(string)] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Fatalf("merge lost a project's hit: %+v", rep.Hits)
	}
}

func TestPortfolioFanOutReportsFailedChild(t *testing.T) {
	good := t.TempDir()
	newProject(t, good, "Alpha")
	ghost := filepath.Join(t.TempDir(), "gone") // registered, never indexed
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	for _, d := range []string{good, ghost} {
		if _, err := Register(ctx, o, d, filepath.Base(d), 1, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := FanOut(ctx, o, Query{Text: "Alpha", Limit: 5}, NewHotSet(0), storeChild(o))
	if err != nil {
		t.Fatal(err)
	}
	// The un-indexable project is an error ENTRY, not a dropped child.
	if rep.Served != 1 || rep.Failed != 1 || len(rep.Projects) != 2 {
		t.Fatalf("counts = %+v; want 1 served, 1 failed, 2 reported", rep)
	}
	var ghostChild *ChildResult
	for i := range rep.Projects {
		if rep.Projects[i].Dir == ghost {
			ghostChild = &rep.Projects[i]
		}
	}
	if ghostChild == nil || ghostChild.Status != "error" ||
		!strings.Contains(ghostChild.Error, "read-only open of missing store") {
		t.Fatalf("ghost child = %+v; want status=error naming the missing store", ghostChild)
	}
	if len(rep.Hits) != 1 || rep.Hits[0]["project"] != filepath.Base(good) {
		t.Fatalf("merged hits = %+v; want only the readable project's", rep.Hits)
	}
}

func TestPortfolioHotSetCapBoundsService(t *testing.T) {
	// The cap is re-read per fan-out (a serving knob, not a build-time
	// constant), so the test pins the environment.
	t.Setenv(MaxReposEnv, "2")
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	for i, d := range dirs {
		newProject(t, d, "Alpha")
		if _, err := Register(ctx, o, d, string(rune('a'+i)), 1, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := FanOut(ctx, o, Query{Text: "Alpha", Limit: 5}, NewHotSet(0), storeChild(o))
	if err != nil {
		t.Fatal(err)
	}
	if rep.HotLimit != 2 {
		t.Fatalf("hot_limit = %d; want the env cap", rep.HotLimit)
	}
	if rep.Registered != 3 || rep.Served != 2 || rep.NotHot != 1 {
		t.Fatalf("counts = %+v; want 3 registered, 2 served, 1 not hot", rep)
	}
	if len(rep.Hits) != 2 {
		t.Fatalf("merged hits = %+v; want 2 (cap-bounded)", rep.Hits)
	}
	var cold *ChildResult
	for i := range rep.Projects {
		if rep.Projects[i].Status == "not_hot" {
			cold = &rep.Projects[i]
		}
	}
	if cold == nil || !strings.Contains(cold.Reason, MaxReposEnv) {
		t.Fatalf("not-hot child = %+v; want a reason naming %s", cold, MaxReposEnv)
	}
	for _, c := range rep.Projects {
		if c.Status == "error" {
			t.Fatalf("a cold project must never be opened (it would error): %+v", c)
		}
	}
}

func TestHotSetPlanOrderAndTouch(t *testing.T) {
	older, newer := "2026-09-01T00:00:00.000Z", "2026-09-13T00:00:00.000Z"
	rows := []store.ProjectRecord{
		{Dir: "/f/never", Name: "never"},
		{Dir: "/f/old", Name: "old", LastIndexed: &older},
		{Dir: "/f/new", Name: "new", LastIndexed: &newer},
	}
	h := NewHotSet(2)
	hot, cold := h.Plan(rows)
	if len(hot) != 2 || hot[0].Dir != "/f/new" || hot[1].Dir != "/f/old" {
		t.Fatalf("initial hot set = %+v; want newest-indexed first, never-indexed excluded", hot)
	}
	if len(cold) != 1 || cold[0].Dir != "/f/never" {
		t.Fatalf("cold set = %+v; want the never-indexed project", cold)
	}

	// Serving a project promotes it above the index-time order, and only
	// projects inside the cap stay in the recency list.
	h.Touch("/f/old")
	h.Touch("/f/never")
	hot, _ = h.Plan(rows)
	if hot[0].Dir != "/f/never" || hot[1].Dir != "/f/old" {
		t.Fatalf("hot set after touches = %+v; want MRU order [/f/never /f/old]", hot)
	}
	if got := h.Hot(); len(got) != 2 || got[0] != "/f/never" {
		t.Fatalf("recency list = %v; want [/f/never /f/old]", got)
	}
}

func TestMaxReposEnv(t *testing.T) {
	t.Setenv(MaxReposEnv, "3")
	if got := MaxRepos(); got != 3 {
		t.Fatalf("MaxRepos() = %d; want the env value", got)
	}
	// Garbage and self-defeating values fall back to the default: a cap of
	// zero would serve no project at all.
	for _, bad := range []string{"", "zero", "0", "-4"} {
		t.Setenv(MaxReposEnv, bad)
		if got := MaxRepos(); got != DefaultMaxRepos {
			t.Fatalf("MaxRepos() with %q = %d; want %d", bad, got, DefaultMaxRepos)
		}
	}
}

func TestManifestIsT0(t *testing.T) {
	indexed := t.TempDir()
	newProject(t, indexed, "Alpha")
	never := t.TempDir() // on disk, never indexed: no .leankg at all
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	if _, err := Register(ctx, o, indexed, "alpha", 412, 71, ptr(time.Unix(1700000000, 0).UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(ctx, o, never, "never", 0, 0, nil); err != nil {
		t.Fatal(err)
	}

	entries, err := Manifest(ctx, o, NewHotSet(8))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("manifest = %+v; want both rows", entries)
	}
	byName := map[string]ManifestEntry{}
	for _, e := range entries {
		byName[e.Project] = e
	}
	alpha := byName["alpha"]
	if !alpha.StoreOnDisk || alpha.Elements != 412 || alpha.Files != 71 || !alpha.Hot || alpha.LastIndexed == nil {
		t.Fatalf("indexed project row = %+v; want registry counts, hot, on-disk store", alpha)
	}
	// A never-indexed project: no stamp, no store on disk, still listed.
	n := byName["never"]
	if n.StoreOnDisk || n.LastIndexed != nil {
		t.Fatalf("never-indexed row = %+v; want no stamp and no on-disk store", n)
	}
}

func TestFanOutRefusesRecursionAndMissingChild(t *testing.T) {
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	if _, err := FanOut(ctx, o, Query{Text: "x", Action: PortfolioAction}, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "cannot fan out") {
		t.Fatalf("recursive portfolio query = %v; want the recursion guard", err)
	}
	if _, err := FanOut(ctx, o, Query{Text: "x"}, nil, nil); err == nil {
		t.Fatal("FanOut without a child executor must fail, not answer an empty fleet")
	}
}

func TestCanonicalAndStamp(t *testing.T) {
	dir := t.TempDir()
	c, err := Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The registry key must be the same identity store.OpenPG hashes into a
	// schema name: absolute and symlink-resolved (macOS t.TempDir lives under
	// /var -> /private/var, exactly the divergence that broke the Rust sweep).
	if !filepath.IsAbs(c) {
		t.Fatalf("Canonical(%s) = %s; want absolute", dir, c)
	}
	if strings.HasPrefix(dir, "/var/") && c == dir {
		t.Fatalf("Canonical left a /var path unresolved: %s", c)
	}
	if _, err := Canonical("   "); err == nil {
		t.Fatal("empty dir must fail")
	}
	if got := Stamp(time.Date(2026, 9, 13, 10, 0, 0, 123_000_000, time.UTC)); got != "2026-09-13T10:00:00.123Z" {
		t.Fatalf("Stamp = %q; want the ledger's own format", got)
	}
}

func TestOpenChildNeverWrites(t *testing.T) {
	dir := t.TempDir()
	o := Options{DBPath: filepath.Join(t.TempDir(), "portfolio.db")}
	ctx := context.Background()
	// A portfolio child opens read-only: with no store at all it fails — it
	// does not create one, which is the "never eager-index" rule.
	if _, err := o.OpenChild(ctx, dir); err == nil {
		t.Fatal("OpenChild on an unindexed project must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, ".leankg")); err == nil {
		t.Fatal("OpenChild created a .leankg directory: a fleet read must not write")
	}
}

func TestPortfolioAcrossPGSiblingSchemas(t *testing.T) {
	url := os.Getenv("LEANKG_TEST_PG_URL")
	if url == "" {
		t.Skip("LEANKG_TEST_PG_URL not set; skipping the Postgres fleet path")
	}
	ctx := context.Background()
	o := Options{Engine: store.EnginePostgres, PGURL: url}
	clearPGRegistry(t, o)
	t.Cleanup(func() { clearPGRegistry(t, o) })

	// Two projects, two sibling schemas, one shared registry schema: index
	// each project into its own schema the way a writer would, then register.
	// The dirs are STABLE (not t.TempDir) so the run leaves a bounded number
	// of schemas in the local database instead of two per execution.
	for i := range 2 {
		d := filepath.Join(os.TempDir(), "leankg-portfolio-pgtest", string(rune('a'+i)))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		st, err := store.OpenPG(ctx, url, d, store.RW)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertElements([]store.Element{{
			QualifiedName: "pkg.Alpha", ElementType: "function", Name: "Alpha",
			FilePath: "alpha.go", Language: "go",
		}}); err != nil {
			t.Fatal(err)
		}
		st.Close()
		if _, err := Register(ctx, o, d, filepath.Base(d), 1, 1, nil); err != nil {
			t.Fatal(err)
		}
	}

	rep, err := FanOut(ctx, o, Query{Text: "Alpha", Limit: 5}, NewHotSet(8), storeChild(o))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Registered != 2 || rep.Served != 2 || rep.Failed != 0 {
		t.Fatalf("PG fleet counts = %+v; want 2 registered, 2 served, 0 failed", rep)
	}
	if len(rep.Hits) != 2 {
		t.Fatalf("PG merged hits = %+v; want one per sibling schema", rep.Hits)
	}
	seen := map[string]bool{}
	for _, h := range rep.Hits {
		seen[h["project"].(string)] = true
	}
	if len(seen) != 2 {
		t.Fatalf("PG fan-out lost project attribution: %+v", rep.Hits)
	}
}

// clearPGRegistry empties the shared PG registry through the public registry
// API, so a rerun against the same database starts from zero rows without any
// destructive schema surgery.
func clearPGRegistry(t *testing.T, o Options) {
	t.Helper()
	ctx := context.Background()
	reg, err := Open(ctx, o, store.RW)
	if err != nil {
		t.Fatalf("open PG registry: %v", err)
	}
	defer reg.Close()
	rows, err := reg.ProjectList()
	if err != nil {
		t.Fatalf("list PG registry: %v", err)
	}
	for _, r := range rows {
		if _, err := reg.ProjectForget(r.Dir); err != nil {
			t.Fatalf("clear PG registry row %s: %v", r.Dir, err)
		}
	}
}
