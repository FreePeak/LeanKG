// Package projects restores the Rust engine's multi-project serving
// (LEANKG_PROJECT_DIRS): one serving process fronts several project
// stores. The router owns the registry — env-parsed plus an explicit
// list — and lazily opens a project store + core.Engine per project
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/internal/auth"
	"github.com/FreePeak/LeanKG/internal/core"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
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

// projectStore is the part of a project store the router uses: it names the
// engine in its log line and closes the store on Close. Reads and writes go
// through the engine, so the router holds no wider store contract.
type projectStore interface {
	Engine() string
	Close() error
}

// Project is one lazily-opened project: its store, engine, and (when
// --memory) markdown memory. Default is true for the serving process's
// own project (the cwd) — seeded by cmd, never re-opened or closed here.
type Project struct {
	Dir    string // canonical absolute directory
	Name   string // filepath.Base(Dir)
	Store  projectStore
	Engine *core.Engine
	Memory *memory.Memory

	isDefault bool
	// refs counts the open leases (requests) using this project. A project
	// with a live lease is never evicted, and a removed one closes when the
	// last lease is released. used orders eviction (least recently used first).
	refs    int
	used    uint64
	retired bool
}

// defaultMaxOpen is the cap on lazily-opened projects when neither
// Config.MaxOpen nor LEANKG_HOSTED_MAX_OPEN_PROJECTS sets one.
const defaultMaxOpen = 64

// MaxOpenEnv names the environment variable that caps how many routed
// projects stay open at once.
const MaxOpenEnv = "LEANKG_HOSTED_MAX_OPEN_PROJECTS"

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
	Embedder   embed.Provider
	// MaxOpen caps how many routed projects stay open (0 = the env
	// LEANKG_HOSTED_MAX_OPEN_PROJECTS, else 64). The default project is not
	// counted: it is seeded, never evicted.
	MaxOpen int
	// Hosted serves a control plane: routed project stores open read-only
	// (a separate worker is their only writer), they never migrate, and the
	// engines refuse the fleet paths. The home store is not routed, so it is
	// unaffected.
	Hosted bool
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
	maxOpen int
	tick    uint64
	onEvict []func(dir string)
	// open builds one project's serving state. NewRouter sets it to
	// Config.open; tests substitute a fake store.
	open func(ctx context.Context, dir string) (*Project, error)
}

// NewRouter builds the registry over defaultDir (the serving process's
// own project) plus cfg.ExtraDirs. It performs no I/O: projects open
// lazily on first routing.
func NewRouter(defaultDir string, cfg Config) *Router {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.Hosted {
		cfg.Mode = store.RO
	}
	r := &Router{
		cfg:     cfg,
		def:     canonical(defaultDir),
		entries: map[string]*Project{},
		maxOpen: cfg.MaxOpen,
		open:    cfg.open,
	}
	if r.maxOpen <= 0 {
		r.maxOpen = maxOpenFromEnv()
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
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string{r.def}, r.dirs...)
}

// Default is the serving process's own project directory.
func (r *Router) Default() string { return r.def }

// Hosted reports whether the router serves a hosted control plane (see
// Config.Hosted). A hosted router refuses a request that names no project.
func (r *Router) Hosted() bool { return r.cfg.Hosted }

// Add registers dir at runtime, so it routes like a LEANKG_PROJECT_DIRS
// entry. The store is opened lazily on first use, so Add performs no I/O.
// Adding a project that is already registered is a no-op.
func (r *Router) Add(dir string) error {
	c := canonical(dir)
	if c == "" {
		return errors.New("projects: add: empty project dir")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c == r.def || contains(r.dirs, c) {
		return nil
	}
	r.dirs = append(r.dirs, c)
	sort.Strings(r.dirs)
	return nil
}

// Remove unregisters dir. The project stops routing at once and its engine
// and store close: now if no request holds it, else when the last request
// releases it. Removing an unregistered project is a no-op; the serving
// process's own project cannot be removed.
func (r *Router) Remove(dir string) error {
	c := canonical(dir)
	r.mu.Lock()
	if c == r.def {
		r.mu.Unlock()
		return fmt.Errorf("projects: %s is the serving process's own project and cannot be removed", c)
	}
	i := indexOf(r.dirs, c)
	if i < 0 {
		r.mu.Unlock()
		return nil
	}
	r.dirs = append(r.dirs[:i], r.dirs[i+1:]...)
	var err error
	if p, ok := r.entries[c]; ok {
		delete(r.entries, c)
		err = r.closeLocked(p)
	}
	r.mu.Unlock()
	r.notifyEvicted(c)
	if err != nil {
		return fmt.Errorf("projects: close %s: %w", c, err)
	}
	return nil
}

// OnEvict registers fn to be called with the canonical dir of every project
// the router closes (LRU eviction or Remove), after the router's lock is
// released. The HTTP layers use it to drop the handlers they built over a
// project's engine.
func (r *Router) OnEvict(fn func(dir string)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onEvict = append(r.onEvict, fn)
}

func (r *Router) notifyEvicted(dirs ...string) {
	r.mu.Lock()
	fns := append([]func(string){}, r.onEvict...)
	r.mu.Unlock()
	for _, d := range dirs {
		for _, fn := range fns {
			fn(d)
		}
	}
}

// MultiProject reports whether more than one project is registered, which is
// what makes an omitted `project` argument ambiguous (see mcp's fail-closed
// rule). A single registered project — including the common one-project
// server — is never ambiguous.
func (r *Router) MultiProject() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.dirs) > 0
}

// maxOpenFromEnv reads LEANKG_HOSTED_MAX_OPEN_PROJECTS: a positive integer,
// else the default.
func maxOpenFromEnv() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(MaxOpenEnv)))
	if err != nil || n <= 0 {
		return defaultMaxOpen
	}
	return n
}

func indexOf(dirs []string, d string) int {
	for i, x := range dirs {
		if x == d {
			return i
		}
	}
	return -1
}

// resolve maps a selector to a registered canonical dir:
//   - "" → default (never errors)
//   - exact canonical dir match
//   - a PATH whose ancestor walk finds a registered project (a file or
//     subdirectory inside a routed project routes to that project)
//   - a NAME matching a project's basename; ambiguous names error
//
// Everything else errors (FR-ZCP-02): no silent default fallback.
func (r *Router) resolve(selector string) (string, error) {
	return r.resolveVisible(selector, nil)
}

// resolveVisible is resolve over the projects a caller may see: visible admits
// a canonical dir, and nil admits every project. A project the caller may not
// see is refused exactly as an unknown one is: the same error, naming only the
// projects the caller can see. Names are matched among the visible projects
// only, so a hidden project can neither answer nor make a name ambiguous.
func (r *Router) resolveVisible(selector string, visible func(dir string) bool) (string, error) {
	admit := func(dir string) bool { return visible == nil || visible(dir) }
	unknown := func() error { return r.unknownProjectError(selector, r.admitted(admit)) }
	if selector == "" {
		if !admit(r.def) {
			return "", unknown()
		}
		return r.def, nil
	}
	sel := canonical(selector)
	if sel == "" {
		return "", unknown()
	}
	if contains(r.List(), sel) {
		if admit(sel) {
			return sel, nil
		}
		return "", unknown()
	}
	// The ancestor walk is for PATHS only (Rust find_leankg_for_path
	// parity): the selector may be a file or subdirectory of a registered
	// project.
	//
	// It used to run for EVERY selector, and a bare name was canonicalized
	// against the serve process's cwd — so an unknown name ("leankgg")
	// walked up to the cwd, and on a host whose cwd IS a registered project
	// it resolved to THAT project. Measured live on the 286-project
	// self-host: {"project":"totally-not-a-real-project-xyz"} answered with
	// the leankg project's status (11,246 elements) instead of an error,
	// and ?project=totally-not-a-real-project-xyz answered 200. That is
	// the silent wrong-project fallback FR-ZCP-02 forbids, and it is the
	// exact failure the fail-closed rule exists to prevent: a confident
	// answer built from the WRONG graph.
	if isPathSelector(selector) {
		for dir := sel; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
			if contains(r.List(), dir) {
				if admit(dir) {
					return dir, nil
				}
				return "", unknown()
			}
		}
	}
	// A bare name (or a path that is inside no project) matches by
	// basename. Several matches are ambiguous — the error names them.
	matches := r.byName(filepath.Base(sel), admit)
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", unknown()
	default:
		return "", r.ambiguousProjectError(selector, matches)
	}
}

// isPathSelector reports whether a selector is a PATH rather than a bare
// project name: absolute, or carrying a separator.
//
// The distinction decides whether the ancestor walk may run. A path says
// "this place"; a name says "this project". Letting a name walk up the
// filesystem lets an unknown name land on whatever project happens to
// contain the server's cwd (see resolve).
func isPathSelector(selector string) bool {
	if filepath.IsAbs(selector) || strings.ContainsRune(selector, filepath.Separator) {
		return true
	}
	// "." and ".." are places, not names.
	return selector == "." || selector == ".."
}

// maxKnownProjectsInError bounds how many projects the unknown-project error
// names. The self-host registers every repository under the scan root — 286
// of them on the machine this was measured on — and an error that lists all
// of them is a wall of absolute paths in the caller's context. An agent pays
// for that wall on every retry, and the caller already knows the two forms a
// selector takes (a directory path, or a name).
const maxKnownProjectsInError = 10

// unknownProjectError names the selector that failed, says how to pick a
// project, and lists a bounded sample of the projects the server does serve.
// The list is truncated with a count of the remainder rather than dropped:
// "which projects exist?" is a real question, and the answer must not be a
// 30 KB string.
func (r *Router) unknownProjectError(selector string, known []string) error {
	shown, more := known, 0
	if len(shown) > maxKnownProjectsInError {
		more = len(shown) - maxKnownProjectsInError
		shown = shown[:maxKnownProjectsInError]
	}
	// Name and path: the name is what a caller can pass back, the path is
	// what makes it unambiguous (names repeat across a 286-project host).
	parts := make([]string, 0, len(shown))
	for _, d := range shown {
		parts = append(parts, fmt.Sprintf("%s (%s)", filepath.Base(d), d))
	}
	msg := fmt.Sprintf("unknown project %q — pass the absolute path of the repository you are working in "+
		"(a path inside a registered project resolves to that project), or the project's name; known projects: %s",
		selector, strings.Join(parts, ", "))
	if more > 0 {
		msg += fmt.Sprintf(" (+%d more)", more)
	}
	return errors.New(msg + " (queries never fall back to another project's data)")
}

// ambiguousProjectError names every registered project that shares the
// selector's name, so the caller can pick one by path instead of guessing.
// The old message said "several registered projects share that name" and
// stopped there: on a 286-project host the caller had no way to choose, so
// the call could only be retried with a guess.
func (r *Router) ambiguousProjectError(selector string, matches []string) error {
	shown, more := matches, 0
	if len(shown) > maxKnownProjectsInError {
		more = len(shown) - maxKnownProjectsInError
		shown = shown[:maxKnownProjectsInError]
	}
	msg := fmt.Sprintf("ambiguous project %q — %d registered projects share that name: %s",
		selector, len(matches), strings.Join(shown, ", "))
	if more > 0 {
		msg += fmt.Sprintf(" (+%d more)", more)
	}
	return errors.New(msg + " — pass the absolute path instead")
}

// byName returns every admitted project whose basename is name. More than
// one is ambiguous; the caller decides what to do about it.
func (r *Router) byName(name string, admit func(dir string) bool) []string {
	var hits []string
	for _, d := range r.List() {
		if filepath.Base(d) == name && admit(d) {
			hits = append(hits, d)
		}
	}
	return hits
}

// admitted returns the registered projects that admit accepts, default first.
func (r *Router) admitted(admit func(dir string) bool) []string {
	var out []string
	for _, d := range r.List() {
		if admit(d) {
			out = append(out, d)
		}
	}
	return out
}

// EngineFor returns the project's engine, opening it on first use, and the
// lease that keeps it open: call release when the request is done with the
// engine. selector "" = default project.
func (r *Router) EngineFor(ctx context.Context, selector string) (*core.Engine, func(), error) {
	return r.EngineAs(ctx, selector, nil)
}

// EngineAs is EngineFor for a caller limited to the projects visible admits
// (see resolveVisible); nil admits every project.
func (r *Router) EngineAs(ctx context.Context, selector string, visible func(dir string) bool) (*core.Engine, func(), error) {
	p, release, err := r.OpenAs(ctx, selector, visible)
	if err != nil {
		return nil, nil, err
	}
	return p.Engine, release, nil
}

// Authorize resolves the selector and applies the caller's project grant
// (auth.AuthFrom on ctx) without opening anything. It returns the project's
// directory. A caller with no grant is unrestricted.
func (r *Router) Authorize(ctx context.Context, selector string) (string, error) {
	return r.AuthorizeAs(ctx, selector, nil)
}

// AuthorizeAs is Authorize for a caller limited to the projects visible admits
// (see resolveVisible); nil admits every project.
func (r *Router) AuthorizeAs(ctx context.Context, selector string, visible func(dir string) bool) (string, error) {
	grant, _ := auth.AuthFrom(ctx)
	dir, err := r.resolveVisible(selector, visible)
	if err != nil {
		if grant.ProjectRestricted() {
			// A restricted caller learns nothing from a failed selector: the same
			// refusal as a withheld project, with no list of known projects.
			return "", auth.ProjectDenied(selector)
		}
		return "", err
	}
	name := filepath.Base(dir)
	// A basename shared with any registered project, visible or not, is shared.
	shared := len(r.byName(name, func(string) bool { return true })) > 1
	if !grant.ProjectAllowed(name, shared) {
		return "", auth.ProjectDenied(selector)
	}
	return dir, nil
}

// Open resolves the selector and lazily opens the project's store,
// engine, and memory (built exactly like the serving process builds the
// default: same engine selection, same language activation). It returns the
// project and a lease: the project is not evicted until release is called.
// release is idempotent.
//
// It never migrates the store: a routed project is served, and a read must
// not rewrite the schema of the store it reads. A project with no store is
// refused as not indexed (see Config.open).
//
// The caller's project grant (auth.AuthFrom on ctx) is checked here, before any
// store is opened, so every selector path (MCP, REST, auto-config) shares one
// check. A caller with no grant in ctx is unrestricted.
//
// Opening a project over the cap evicts the least recently used project with
// no live lease. When every open project is in use the cap is exceeded
// until one of them is released, rather than closing a store under a request.
func (r *Router) Open(ctx context.Context, selector string) (*Project, func(), error) {
	return r.OpenAs(ctx, selector, nil)
}

// OpenAs is Open for a caller limited to the projects visible admits (see
// resolveVisible); nil admits every project.
func (r *Router) OpenAs(ctx context.Context, selector string, visible func(dir string) bool) (*Project, func(), error) {
	dir, err := r.AuthorizeAs(ctx, selector, visible)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil, fmt.Errorf("projects: router closed")
	}
	p, ok := r.entries[dir]
	var evicted []string
	if !ok {
		evicted = r.makeRoomLocked()
		p, err = r.open(ctx, dir)
		if err != nil {
			r.mu.Unlock()
			r.notifyEvicted(evicted...)
			return nil, nil, err
		}
		r.entries[dir] = p
		r.cfg.Logf("project %s (%s) opened engine=%s", p.Name, p.Dir, p.Store.Engine())
	}
	r.tick++
	p.used = r.tick
	p.refs++
	r.mu.Unlock()
	r.notifyEvicted(evicted...)
	var once sync.Once
	return p, func() { once.Do(func() { r.release(p) }) }, nil
}

// makeRoomLocked closes idle least-recently-used projects until one more can
// open under the cap, and returns their dirs. Projects with a live lease are
// never chosen; if none is idle the loop stops and the cap is exceeded.
func (r *Router) makeRoomLocked() []string {
	var evicted []string
	for r.openProjectsLocked() >= r.maxOpen {
		victim := r.idleLRULocked()
		if victim == nil {
			break
		}
		delete(r.entries, victim.Dir)
		if err := r.closeLocked(victim); err != nil {
			r.cfg.Logf("projects: evict %s: %v", victim.Dir, err)
		}
		evicted = append(evicted, victim.Dir)
	}
	return evicted
}

// openProjectsLocked counts the open routed projects (the default project is
// seeded and never counted).
func (r *Router) openProjectsLocked() int {
	n := 0
	for _, p := range r.entries {
		if !p.isDefault {
			n++
		}
	}
	return n
}

// idleLRULocked returns the least recently used routed project with no live
// lease, or nil when every open project is in use.
func (r *Router) idleLRULocked() *Project {
	var victim *Project
	for _, p := range r.entries {
		if p.isDefault || p.refs > 0 {
			continue
		}
		if victim == nil || p.used < victim.used {
			victim = p
		}
	}
	return victim
}

// closeLocked closes p now, or marks it retired when a lease still holds it
// (release closes it then). The caller holds r.mu and has already removed p
// from r.entries.
func (r *Router) closeLocked(p *Project) error {
	if p.refs > 0 {
		p.retired = true
		return nil
	}
	return closeProject(p)
}

// release drops one lease and closes the project if it was retired while the
// lease was held.
func (r *Router) release(p *Project) {
	r.mu.Lock()
	p.refs--
	closeNow := p.refs == 0 && p.retired
	if closeNow {
		p.retired = false
	}
	r.mu.Unlock()
	if closeNow {
		if err := closeProject(p); err != nil {
			r.cfg.Logf("projects: close %s: %v", p.Dir, err)
		}
	}
}

// closeProject closes a project's engine and store.
func closeProject(p *Project) error {
	if p.Engine != nil {
		p.Engine.Close()
	}
	return p.Store.Close()
}

// open builds one project's serving state (cmd serve's construction
// sequence, parameterized on the project dir).
func (cfg Config) open(ctx context.Context, dir string) (*Project, error) {
	engineName := cfg.EngineName
	if engineName == "" {
		engineName = os.Getenv("LEANKG_DB_ENGINE")
	}
	// Each routed project honors its own leankg.yaml anchor (Rust per-project
	// db_path resolution): the store opens under the anchored project so the
	// Postgres schema key matches what the project declares. cfg.PGURL stays
	// process-wide — Rust had a single db tier from db_config_from_cwd.
	dbDir := projectcfg.ResolveProjectRoot(filepath.Join(dir, ".leankg"))
	projectDir := filepath.Dir(dbDir)
	// A READ must not rewrite the store it reads.
	//
	// Opening a routed project used to Migrate() it, so a single query
	// against a project whose store was one migration behind silently
	// rewrote that store's schema — and a query against a project nobody
	// had indexed CREATED the store, schema and all, which then made that
	// project look indexed forever. The write path owns the schema: the
	// import path migrates the store it is about to write to
	// (core.Engine.Import), and `leankg index` / `leankg migrate` own it
	// outside the server.
	//
	// So the router asks FIRST whether there is a store to serve, and
	// refuses a project that has none: the same rule the maintenance fleet
	// and the CLI verbs already follow.
	exists, err := store.Exists(ctx, projectDir, engineName, cfg.PGURL)
	if err != nil {
		return nil, fmt.Errorf("projects: check store for %s: %w", dir, err)
	}
	if !exists {
		return nil, notIndexedError(dir, engineName, projectDir)
	}
	st, err := store.OpenBackend(ctx, store.OpenOptions{ProjectDir: projectDir, Engine: engineName, PGURL: cfg.PGURL, DBPath: "", Mode: cfg.Mode})
	if err != nil {
		return nil, fmt.Errorf("projects: open store for %s: %w", dir, err)
	}
	var mem *memory.Memory
	if cfg.Memory {
		// Shared open: this handle serves every later request for the project,
		// so it must not be bound to the first request's deadline.
		const memoryOpenTimeout = 2 * time.Minute
		octx, cancel := context.WithTimeout(context.WithoutCancel(ctx), memoryOpenTimeout)
		mem, err = memory.Open(octx, dir, false)
		cancel()
		if err != nil {
			_ = st.Close()
			return nil, fmt.Errorf("projects: memory for %s: %w", dir, err)
		}
	}
	// Lazy language activation: the routed project gets the same
	// detection the default project got at startup.
	reg := langs.DefaultRegistry()
	if _, aerr := reg.Activate(ctx, dir); aerr != nil {
		cfg.Logf("language detection failed for %s: %v", dir, aerr)
	}
	eng := core.New(st, core.Options{Memory: mem, Embedder: cfg.Embedder, ProjectDir: dir, Langs: reg, Hosted: cfg.Hosted})
	return &Project{Dir: dir, Name: filepath.Base(dir), Store: st, Engine: eng, Memory: mem}, nil
}

// notIndexedError is the refusal a routed project earns when it has no
// store to serve. It names the project, the store it does not have, and the
// command that creates one — a refusal that names its subject, so the
// operator does not have to guess which half of the setup is missing.
func notIndexedError(dir, engine, projectDir string) error {
	subject := store.SQLitePath(projectDir)
	if engine == store.EnginePostgres {
		subject = fmt.Sprintf("%s (postgres schema)", dir)
	}
	return fmt.Errorf("projects: project %s is not indexed: no store at %s — run `leankg index %s` to create it",
		filepath.Base(dir), subject, dir)
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
		if p.Engine != nil {
			p.Engine.Close()
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
// canonical returns the absolute, symlink-resolved form of dir.
//
// EvalSymlinks fails outright when the LEAF does not exist, so a path to a
// not-yet-created file (/work/api/new.go) came back unresolved while the
// project directory it points into came back resolved — and the two never
// compared equal, so a path inside a registered project failed to route to
// it. The deepest existing ancestor is resolved instead and the missing
// tail is re-appended, so a selector resolves the same way whether or not
// the file it names exists yet.
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
	// Walk up to the deepest ancestor that exists, resolve it, and put the
	// missing tail back.
	var tail []string
	for cur := abs; ; {
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs // nothing above the root exists either
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			for _, part := range tail {
				resolved = filepath.Join(resolved, part)
			}
			return resolved
		}
	}
}
