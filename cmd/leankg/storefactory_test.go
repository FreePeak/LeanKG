package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/store"
)

// TestStoreOpensGoThroughTheFactory pins CX-04: every production store in
// cmd/leankg is opened by the one factory in store.go. A direct store.Open,
// store.OpenBackend or store.OpenPG call anywhere else is how `writer` came to
// open a file that ignored LEANKG_DB_PATH, the leankg.yaml anchor and the
// PostgreSQL engine while `serve` honoured all three.
//
// Test files are held to store.Open as well: every fixture opens through
// openFixtureStore, which calls the factory. OpenBackend and OpenPG are still
// checked in production files only.
func TestStoreOpensGoThroughTheFactory(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var bypass []string
	for _, name := range files {
		isTest := strings.HasSuffix(name, "_test.go")
		if name == "store.go" {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "store" {
				return true
			}
			switch sel.Sel.Name {
			case "Open":
				bypass = append(bypass, fset.Position(call.Pos()).String()+" store.Open")
			case "OpenBackend", "OpenPG":
				if !isTest {
					bypass = append(bypass, fset.Position(call.Pos()).String()+" store."+sel.Sel.Name)
				}
			}
			return true
		})
	}
	if len(bypass) > 0 {
		t.Fatalf("store opened outside the factory (cmd/leankg/store.go); route each through openStore or openStoreWith (tests: openFixtureStore):\n%s",
			strings.Join(bypass, "\n"))
	}
}

// openFixtureStore is the one way a test opens a SQLite fixture store. It goes
// through the factory (openStoreWith), so fixtures resolve like production. The
// engine is pinned to sqlite: a developer's LEANKG_DB_ENGINE must not turn a
// file fixture into a PostgreSQL one.
func openFixtureStore(dbPath string, mode store.Mode) (store.Backend, error) {
	return openStoreWith(context.Background(), filepath.Dir(dbPath), store.EngineSQLite, dbPath, mode)
}

// resolvedPath compares store locations through symlinks: t.TempDir sits under
// /var, which macOS resolves to /private/var.
func resolvedPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

// TestWriterAndServeOpenTheSameStore pins the CX-04 acceptance that `writer`
// and `serve` resolve the same target: the same project directory after the
// leankg.yaml anchor, and the same store file under LEANKG_DB_PATH. serve's
// flags are at their defaults here, which is how serve resolves its store
// when none are given. The PostgreSQL case needs LEANKG_TEST_PG_URL.
func TestWriterAndServeOpenTheSameStore(t *testing.T) {
	ctx := context.Background()

	t.Run("project-scoped store", func(t *testing.T) {
		t.Setenv("LEANKG_DB_ENGINE", "")
		t.Setenv("LEANKG_DB_PATH", "")
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
			t.Fatal(err)
		}
		assertWriterMatchesServe(ctx, t, dir, filepath.Join(dir, ".leankg", "leankg.db"))
	})

	t.Run("LEANKG_DB_PATH standalone store", func(t *testing.T) {
		t.Setenv("LEANKG_DB_ENGINE", "")
		shared := filepath.Join(t.TempDir(), "shared.db")
		t.Setenv("LEANKG_DB_PATH", shared)
		dir := t.TempDir()
		assertWriterMatchesServe(ctx, t, dir, shared)
	})

	t.Run("leankg.yaml project_path anchor", func(t *testing.T) {
		t.Setenv("LEANKG_DB_ENGINE", "")
		t.Setenv("LEANKG_DB_PATH", "")
		root := t.TempDir()
		anchored := t.TempDir()
		for _, d := range []string{filepath.Join(root, ".leankg"), filepath.Join(anchored, ".leankg")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		cfg := "project:\n  project_path: " + anchored + "\n"
		if err := os.WriteFile(filepath.Join(root, ".leankg", "leankg.yaml"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
		assertWriterMatchesServe(ctx, t, root, filepath.Join(anchored, ".leankg", "leankg.db"))
	})

	t.Run("postgres engine", func(t *testing.T) {
		url := os.Getenv("LEANKG_TEST_PG_URL")
		if url == "" {
			t.Skip("LEANKG_TEST_PG_URL unset: no live PostgreSQL fixture")
		}
		t.Setenv("LEANKG_DB_ENGINE", "postgres")
		t.Setenv("LEANKG_PG_URL", url)
		t.Setenv("LEANKG_DB_PATH", "")
		dir := t.TempDir()
		w, wdir, err := openWriterStore(ctx, dir)
		if err != nil {
			t.Fatalf("writer open: %v", err)
		}
		defer w.Close()
		serveDir := anchorProjectDir(dir)
		s, err := openStoreWith(ctx, serveDir, "", "", store.RW)
		if err != nil {
			t.Fatalf("serve open: %v", err)
		}
		defer s.Close()
		if wdir != serveDir {
			t.Errorf("writer dir %q, serve dir %q", wdir, serveDir)
		}
		if w.Engine() != store.EnginePostgres || s.Engine() != store.EnginePostgres {
			t.Errorf("engines: writer %q, serve %q, want postgres for both", w.Engine(), s.Engine())
		}
		if w.Path() != s.Path() {
			t.Errorf("writer and serve open different PostgreSQL targets: %q vs %q", w.Path(), s.Path())
		}
	})
}

// assertWriterMatchesServe opens the writer's store and serve's store for dir
// and checks both land on wantPath.
func assertWriterMatchesServe(ctx context.Context, t *testing.T, dir, wantPath string) {
	t.Helper()
	w, wdir, err := openWriterStore(ctx, dir)
	if err != nil {
		t.Fatalf("writer open: %v", err)
	}
	defer w.Close()
	serveDir := anchorProjectDir(dir)
	s, err := openStoreWith(ctx, serveDir, "", "", store.RW)
	if err != nil {
		t.Fatalf("serve open: %v", err)
	}
	defer s.Close()
	if wdir != serveDir {
		t.Errorf("writer resolved dir %q, serve resolved dir %q", wdir, serveDir)
	}
	if got, want := resolvedPath(t, w.Path()), resolvedPath(t, wantPath); got != want {
		t.Errorf("writer opened %s, want %s", got, want)
	}
	if got, want := resolvedPath(t, s.Path()), resolvedPath(t, wantPath); got != want {
		t.Errorf("serve opened %s, want %s", got, want)
	}
}

// TestExplicitEngineFlagWins pins that gc and vacuum honour --engine. The flag
// used to be read and then dropped: the store was opened with the environment's
// engine. With --engine postgres and no LEANKG_PG_URL the verb must refuse, not
// fall back to the sqlite store the environment names.
func TestExplicitEngineFlagWins(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "sqlite")
	t.Setenv("LEANKG_PG_URL", "")
	t.Setenv("LEANKG_DB_PATH", "")
	for _, verb := range []string{"gc", "vacuum"} {
		t.Run(verb, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr, code := runCLIIn(t, dir, verb, "--engine", "postgres", dir)
			if code == 0 {
				t.Fatalf("%s --engine postgres with no LEANKG_PG_URL must fail, exit 0:\n%s%s", verb, stdout, stderr)
			}
			if !strings.Contains(stderr, "LEANKG_PG_URL") {
				t.Errorf("%s must report the missing PostgreSQL URL, got:\n%s", verb, stderr)
			}
			if _, err := os.Stat(filepath.Join(dir, ".leankg")); err == nil {
				t.Errorf("%s created a sqlite store although --engine named postgres", verb)
			}
		})
	}
}
