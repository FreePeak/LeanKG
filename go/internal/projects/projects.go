// Package projects restores the Rust engine's multi-project serving
// (LEANKG_PROJECT_DIRS): one serving process fronts several project
// stores. The router owns the registry — env-parsed plus an explicit
// list — and lazily opens a store.Backend + core.Engine per project
// directory, keyed and selectable by directory path or by name. The
// FR-ZCP-02 rule carries over: an explicit route key that resolves to
// nothing NEVER falls back to the default project (that fallback served
// wrong-project data silently); it errors naming the known projects.
package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// ParseDirs parses LEANKG_PROJECT_DIRS (comma-separated) into a sorted,
// deduplicated list of project paths. Empty / whitespace-only entries are
// skipped (Rust parse_project_dirs parity).
func ParseDirs(list string) []string {
	var out []string
	for _, s := range strings.Split(list, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	out = dedup(out)
	return out
}

func dedup(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// Project is one lazily-opened project: its store, engine, and (when
// --memory) markdown memory. Default is true for the serving process's
// own project (the cwd) — seeded by cmd, never re-opened or closed here.
type Project struct {
	Dir    string // canonical absolute directory
	Name   string // filepath.Base(Dir)
	Store  store.Backend
	Engine *core.Engine
	Memory *memory.Memory

	isDefault bool
}

// Config controls lazy per-project construction (mirrors `leankg serve`
// flags so a routed project is built exactly like the default one).
type Config struct {
	// ExtraDirs lists additional registered projects (LEANKG_PROJECT_DIRS
	// plus any explicit list the caller holds).
	ExtraDirs []string
	Mode      store.Mode
	// EngineName is the storage engine ("" resolves LEANKG_DB_ENGINE,
	// then sqlite).
	EngineName string
	PGURL      string
	Memory     bool
	Embedder   core.QueryEmbedder
	// Logf receives one line per lazily-opened project (may be nil).
	Logf func(format string, args ...any)
}

// Router is the multi-project registry.
type Router struct {
	cfg     Config
	def     string   // canonical default project dir
	dirs    []string // registered extra dirs, canonical, sorted
	mu      sync.Mutex
	entries map[string]*Project // keyed by canonical dir
	closed  bool
}

// NewRouter builds the registry over defaultDir (the serving process's
// own project) plus cfg.ExtraDirs. It performs no I/O: projects open
// lazily on first routing.
func NewRouter(defaultDir string, cfg Config) *Router {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	r := &Router{
		cfg:     cfg,
		def:     canonical(defaultDir),
		entries: map[string]*Project{},
	}
	for _, d := range cfg.ExtraDirs {
		c := canonical(d)
		if c == r.def || contains(r.dirs, c) {
			continue
		}
		r.dirs = append(r.dirs, c)
	}
	sort.Strings(r.dirs)
	return r
}

func contains(dirs []string, d string) bool {
	for _, x := range dirs {
		if x == d {
			return true
		}
	}
	return false
}

// SeedDefault binds the already-opened default project into the router
// so `?project=<default>` routes to the SAME handle instead of opening a
// second (WAL-locking) store.
func (r *Router) SeedDefault(p *Project) {
	p.isDefault = true
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[r.def] = p
}

// List returns every registered project's canonical dir, default first.
func (r *Router) List() []string {
	out := append([]string{r.def}, r.dirs...)
	return out
}

// Default is the serving process's own project directory.
func (r *Router) Default() string { return r.def }

// resolve maps a selector to a registered canonical dir:
//   - "" → default (never errors)
//   - exact canonical dir match
//   - exact name (basename) match; ambiguous names error
//   - a filesystem path whose ancestor walk finds a registered project
//     (a file inside a routed project routes to that project)
//
// Everything else errors (FR-ZCP-02): no silent default fallback.
func (r *Router) resolve(selector string) (string, error) {
	if selector == "" {
		return r.def, nil
	}
	if sel := canonical(selector); sel != "" {
		if contains(r.List(), sel) {
			return sel, nil
		}
		name := filepath.Base(sel)
		match, nerr := r.byName(name)
		if nerr != nil {
			return "", fmt.Errorf("ambiguous project %q — several registered projects share that name", selector)
		}
		if match != "" {
			return match, nil
		}
		// Ancestor walk: the selector may be a file or subdirectory of a
		// registered project (Rust find_leankg_for_path parity).
		for dir := sel; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if contains(r.List(), dir) {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf("unknown project %q — known projects: %s (queries never fall back to another project's data)",
		selector, strings.Join(r.List(), ", "))
}

var errAmbiguousName = errors.New("ambiguous project name")

func (r *Router) byName(name string) (string, error) {
	var hit string
	found := false
	for _, d := range r.List() {
		if filepath.Base(d) == name {
			if found {
				return "", errAmbiguousName
			}
			hit, found = d, true
		}
	}
	return hit, nil
}

// EngineFor returns the project's engine, opening it on first use.
// selector "" = default project.
func (r *Router) EngineFor(ctx context.Context, selector string) (*core.Engine, error) {
	p, err := r.Open(ctx, selector)
	if err != nil {
		return nil, err
	}
	return p.Engine, nil
}

// Open resolves the selector and lazily opens the project's store,
// engine, and memory (built exactly like the serving process builds the
// default: same engine selection, same Migrate, same language
// activation).
func (r *Router) Open(ctx context.Context, selector string) (*Project, error) {
	dir, err := r.resolve(selector)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, fmt.Errorf("projects: router closed")
	}
	if p, ok := r.entries[dir]; ok {
		return p, nil
	}
	p, err := r.cfg.open(ctx, dir)
	if err != nil {
		return nil, err
	}
	r.entries[dir] = p
	r.cfg.Logf("project %s (%s) opened engine=%s", p.Name, p.Dir, p.Store.Engine())
	return p, nil
}

// open builds one project's serving state (cmd serve's construction
// sequence, parameterized on the project dir).
func (cfg Config) open(ctx context.Context, dir string) (*Project, error) {
	engineName := cfg.EngineName
	if engineName == "" {
		engineName = os.Getenv("LEANKG_DB_ENGINE")
	}
	st, err := store.OpenBackend(ctx, dir, engineName, cfg.PGURL, cfg.Mode)
	if err != nil {
		return nil, fmt.Errorf("projects: open store for %s: %w", dir, err)
	}
	if cfg.Mode != store.RO {
		if err := st.Migrate(); err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("projects: migrate %s: %w", dir, err)
		}
	}
	var mem *memory.Memory
	if cfg.Memory {
		if mem, err = memory.Open(dir, false); err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("projects: memory for %s: %w", dir, err)
		}
	}
	eng := core.New(st, mem, cfg.Embedder)
	eng.SetProjectDir(dir)
	// Lazy language activation: the routed project gets the same
	// detection the default project got at startup.
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(dir); aerr != nil {
		cfg.Logf("language detection failed for %s: %v", dir, aerr)
	}
	eng.SetLangsRegistry(reg)
	return &Project{Dir: dir, Name: filepath.Base(dir), Store: st, Engine: eng, Memory: mem}, nil
}

// Close closes every project store the router opened itself. The seeded
// default project is owned by cmd and closed there.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	var firstErr error
	for dir, p := range r.entries {
		if p.isDefault {
			continue
		}
		if err := p.Store.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("projects: close %s: %w", dir, err)
		}
		delete(r.entries, dir)
	}
	return firstErr
}

// canonical absolutizes a dir (resolving symlinks where possible) so
// path spellings route identically. Empty input stays empty.
func canonical(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}
