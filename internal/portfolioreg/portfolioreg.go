// Package portfolioreg is the Go engine's SERVER-SIDE project registry and the
// portfolio (fleet) scope built on top of it: which projects a deployment
// serves, which of them are HOT enough to answer a query, and what a
// cross-project query returns (issue #376, the umbrella of #277's T0/T1 tiers
// and #278's migration-fleet remainder).
//
// # Two registries, deliberately
//
// internal/registry owns ~/.leankg/registry.json: the CLI's per-user list of
// NAMED repositories (leankg register <name>), the format and location the Rust
// engine wrote. That file stays what it always was — a local alias table, and
// the JSON file is NOT grown into a fleet registry.
//
// This package owns the projects table (store migration 013, both backends):
// the org-scale registry a SERVER reads, keyed on the canonical project
// DIRECTORY. The two answer different questions — registry.json answers "what
// is blogs?", this answers "what does this deployment serve?" — and they are
// reconciled at exactly one point: Register accepts a name from either surface
// and stamps the directory-keyed row.
//
// # Three tiers (#277 vocabulary)
//
// T0  Manifest: registry rows + on-disk .leankg presence. Opens no project
//
//	store, indexes nothing, and is therefore safe on a portfolio of 80 repos.
//
// T1  FanOut: the hot set — at most MaxRepos (LEANKG_PORTFOLIO_MAX_REPOS,
//
//	default 8) registered projects, each opened READ-ONLY for the duration
//	of one query. Nothing here ever indexes: a project with no store is a
//	per-child error entry, not a background job.
//
// T2  (not implemented here): a durable warm cache with real detach-to-cold.
//
//	The ceiling of this package is that its LRU is in-process — see HotSet.
package portfolioreg

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

	"github.com/FreePeak/LeanKG/internal/projectcfg"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Caps and knobs, all env-overridable so a deployment tunes the fan-out without
// a rebuild and tests pin it without a mock.
const (
	// DefaultMaxRepos bounds one portfolio fan-out (the T1 hot set). Chosen to
	// match the Rust design note for FR-ZCP-09's hot-set cap.
	DefaultMaxRepos = 8

	// MaxReposEnv overrides DefaultMaxRepos.
	MaxReposEnv = "LEANKG_PORTFOLIO_MAX_REPOS"

	// DBPathEnv overrides the sqlite registry store location.
	DBPathEnv = "LEANKG_PORTFOLIO_DB"

	// pgRegistryKey is the synthetic project identity the PG registry schema is
	// keyed on. It is ABSOLUTE on purpose: store.pgSchemaForDir runs
	// filepath.Abs first, so a relative key would make the registry schema
	// depend on the server's working directory.
	pgRegistryKey = "/.leankg-portfolio-registry"
)

// ErrNoRegistry marks "this deployment has no fleet registry yet" — distinct
// from a store failure so callers (the portfolio query, doctor's fleet check,
// the projects verb) can say "register something" instead of reporting a fault.
var ErrNoRegistry = errors.New("portfolioreg: no portfolio registry")

// Options says where the registry lives and how its projects are opened. The
// zero value is "the deployment default": the sqlite registry store at
// DBPath(), engine from LEANKG_DB_ENGINE, Postgres dsn from LEANKG_PG_URL or a
// project's own leankg.yaml db: block.
type Options struct {
	// Engine is store.EngineSQLite ("", the default) or store.EnginePostgres.
	Engine string
	// PGURL is the shared Postgres dsn for the postgres engine. Empty falls
	// back to LEANKG_PG_URL, then to the per-dir leankg.yaml db: block (the
	// precedence cmd/leankg's pgURLFor applies).
	PGURL string
	// DBPath overrides the sqlite registry store path (tests; "" = DBPath()).
	DBPath string
}

// DBPath returns the sqlite registry store location: $LEANKG_PORTFOLIO_DB, else
// $HOME/.leankg/portfolio.db — the sibling of the CLI's registry.json, which is
// the point: one file per owner of a repo list. HOME unset falls back to
// ./.leankg/portfolio.db (the internal/registry.Path fallback shape).
func DBPath() string {
	if v := os.Getenv(DBPathEnv); v != "" {
		return v
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".leankg", "portfolio.db")
}

// engine resolves the backend kind ("" = the sqlite default).
func (o Options) engine() string {
	if o.Engine != "" {
		return o.Engine
	}
	if v := os.Getenv("LEANKG_DB_ENGINE"); v != "" {
		return v
	}
	return store.EngineSQLite
}

// pgURL resolves the shared-Postgres dsn for one project dir: an explicit
// Options.PGURL wins, otherwise the engine-wide precedence lives in
// projectcfg.PGURL.
func (o Options) pgURL(dir string) string {
	if o.PGURL != "" {
		return o.PGURL
	}
	return projectcfg.PGURL(dir)
}

// Open opens the registry store itself. RW creates and migrates it on first
// use (that is what bootstraps a fleet); RO requires it to exist, so a reader
// gets ErrNoRegistry rather than an empty fleet it cannot distinguish from a
// missing one.
func Open(ctx context.Context, o Options, mode store.Mode) (store.Backend, error) {
	switch o.engine() {
	case store.EnginePostgres:
		url := o.pgURL("")
		if url == "" {
			return nil, fmt.Errorf("portfolioreg: postgres engine with no dsn (%s unset)", "LEANKG_PG_URL")
		}
		st, err := store.OpenPG(ctx, url, pgRegistryKey, mode)
		if err != nil {
			if mode == store.RO {
				return nil, fmt.Errorf("%w: %v", ErrNoRegistry, err)
			}
			return nil, err
		}
		if mode == store.RW {
			if err := st.Migrate(); err != nil {
				_ = st.Close()
				return nil, fmt.Errorf("portfolioreg: migrate registry: %w", err)
			}
		}
		return st, nil
	case "", store.EngineSQLite:
		path := o.DBPath
		if path == "" {
			path = DBPath()
		}
		if mode == store.RO {
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("%w: %s", ErrNoRegistry, path)
			}
		}
		st, err := store.Open(path, mode)
		if err != nil {
			return nil, err
		}
		if mode == store.RW {
			if err := st.Migrate(); err != nil {
				_ = st.Close()
				return nil, fmt.Errorf("portfolioreg: migrate registry: %w", err)
			}
		}
		return st, nil
	default:
		return nil, fmt.Errorf("portfolioreg: unknown engine %q (want sqlite|postgres)", o.engine())
	}
}

// OpenChild opens one registered project's store READ-ONLY: the sqlite child
// resolves to <dir>/.leankg/leankg.db, the Postgres child to that project's own
// schema on the shared server, search_path pinned by store.OpenPG exactly as it
// is for the project's own readers — so a fleet query reads the same rows the
// project reads, and no cross-schema UNION is built out of concatenated names.
//
// CEILING: one lazily-dialing pool per child, used and closed inside one
// fan-out, so the hot-set cap is also the connection bound (≤ MaxRepos
// children, sequential). The upgrade path if fan-out latency ever matters is a
// shared pool that checks out a connection and SETs search_path per child
// inside a transaction — which needs every Backend reader to run in that tx.
func (o Options) OpenChild(ctx context.Context, dir string) (store.Backend, error) {
	return store.OpenBackend(ctx, dir, o.engine(), o.pgURL(dir), "", store.RO)
}

// Canonical turns a project path into the registry key: absolute, with symlinks
// resolved — the same canonicalization store.pgSchemaForDir applies, so a row
// and the PG schema derived from it can never disagree about identity. Paths
// that do not exist still canonicalize (a registered dir may be temporarily
// unmounted); only filepath.Abs can fail.
func Canonical(dir string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("portfolioreg: empty project dir")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("portfolioreg: resolve %s: %w", dir, err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return abs, nil
}

// Stamp is the registry's timestamp format: UTC ISO-8601 with millisecond
// precision, rendering identically to both backends' schema_migrations ledger
// (sqlite's strftime(...%fZ...), Postgres's pgNow).
func Stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// Register adds or refreshes dir in the registry. name defaults to
// filepath.Base(dir) — the same default internal/projects gives a routed
// project — and an existing row's name survives an empty name argument.
// elements/files are the totals the manifest (T0) reports without opening the
// project store; indexedAt is the stamp a real index run passes, nil for a
// bare registration ("registered, never indexed").
//
// The registry is opened RW (creating it on first use), so this is the
// bootstrap path for a fleet. Callers close nothing: Register owns the handle.
func Register(ctx context.Context, o Options, dir, name string, elements, files int, indexedAt *time.Time) (store.ProjectRecord, error) {
	canonical, err := Canonical(dir)
	if err != nil {
		return store.ProjectRecord{}, err
	}
	reg, err := Open(ctx, o, store.RW)
	if err != nil {
		return store.ProjectRecord{}, err
	}
	defer reg.Close()
	return stamp(reg, canonical, name, elements, files, indexedAt)
}

// stamp upserts one row against an already-open registry handle.
func stamp(reg store.Backend, dir, name string, elements, files int, indexedAt *time.Time) (store.ProjectRecord, error) {
	if name == "" {
		if prev, ok, err := reg.ProjectGet(dir); err == nil && ok {
			name = prev.Name
		}
	}
	if name == "" {
		name = filepath.Base(dir)
	}
	rec := store.ProjectRecord{
		Dir: dir, Name: name, RegisteredAt: Stamp(time.Now()),
		ElementCount: elements, FileCount: files,
	}
	if indexedAt != nil {
		s := Stamp(*indexedAt)
		rec.LastIndexed = &s
	}
	if err := reg.ProjectUpsert(rec); err != nil {
		return store.ProjectRecord{}, err
	}
	// Read the row back: registered_at and last_indexed are COALESCEd by the
	// upsert, so the stored record — not the argument — is the truth.
	saved, _, err := reg.ProjectGet(dir)
	if err != nil {
		return store.ProjectRecord{}, err
	}
	return saved, nil
}

// RegisterAfterIndex is the leankg index completion hook: it reads the totals
// from the store the run just wrote and registers the project. It is
// BEST-EFFORT by contract — a fleet registration must never turn a successful
// index into a failed command, so callers log the error and move on.
func RegisterAfterIndex(ctx context.Context, o Options, st store.Backend, dir string) error {
	elements, err := st.ElementCount()
	if err != nil {
		return fmt.Errorf("portfolioreg: read element count: %w", err)
	}
	files, err := st.FileCount()
	if err != nil {
		return fmt.Errorf("portfolioreg: read file count: %w", err)
	}
	now := time.Now()
	_, err = Register(ctx, o, dir, "", elements, files, &now)
	return err
}

// Forget removes dir from the registry (the inverse of Register, for a repo
// that has left the fleet). It reports whether a row went.
func Forget(ctx context.Context, o Options, dir string) (bool, error) {
	canonical, err := Canonical(dir)
	if err != nil {
		return false, err
	}
	reg, err := Open(ctx, o, store.RW)
	if err != nil {
		return false, err
	}
	defer reg.Close()
	return reg.ProjectForget(canonical)
}

// Projects is the registry list (name-ordered; see store.ProjectList).
func Projects(ctx context.Context, o Options) ([]store.ProjectRecord, error) {
	reg, err := Open(ctx, o, store.RO)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	return ProjectsOf(reg)
}

// ProjectsOf lists the registry from an already-open handle.
func ProjectsOf(reg store.Backend) ([]store.ProjectRecord, error) {
	rows, err := reg.ProjectList()
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []store.ProjectRecord{}
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// T0: the manifest, with no store opens
// ---------------------------------------------------------------------------

// ManifestEntry is one project's T0 row: registry facts plus whether the
// project's store is on disk. Producing a manifest NEVER opens a project store
// and NEVER indexes — that is the whole point of the tier: it answers "what is
// in the portfolio, and how big is each piece?" in one registry read and a
// handful of stats.
type ManifestEntry struct {
	Project      string  `json:"project"`
	Dir          string  `json:"dir"`
	RegisteredAt string  `json:"registered_at"`
	LastIndexed  *string `json:"last_indexed"`
	Elements     int     `json:"elements"`
	Files        int     `json:"files"`
	StoreOnDisk  bool    `json:"store_on_disk"`
	Hot          bool    `json:"hot"`
}

// Manifest builds the T0 summary. hot may be nil (nothing is labeled hot).
func Manifest(ctx context.Context, o Options, hot *HotSet) ([]ManifestEntry, error) {
	projects, err := Projects(ctx, o)
	if err != nil {
		return nil, err
	}
	return ManifestOf(o, projects, hot), nil
}

// ManifestOf is Manifest against an already-loaded registry list.
func ManifestOf(o Options, projects []store.ProjectRecord, hot *HotSet) []ManifestEntry {
	var hotSet []store.ProjectRecord
	if hot != nil {
		hotSet, _ = hot.Plan(projects)
	}
	out := make([]ManifestEntry, 0, len(projects))
	for _, p := range projects {
		out = append(out, ManifestEntry{
			Project: p.Name, Dir: p.Dir, RegisteredAt: p.RegisteredAt,
			LastIndexed: p.LastIndexed, Elements: p.ElementCount, Files: p.FileCount,
			StoreOnDisk: storePresent(o.engine(), p.Dir),
			Hot:         containsDir(hotSet, p.Dir),
		})
	}
	return out
}

// storePresent is the T0 presence probe: the sqlite store file, or — for the
// Postgres fleet — the project's .leankg directory (the schema itself is only
// observable by connecting, which is exactly what this tier refuses to do).
func storePresent(engine, dir string) bool {
	target := filepath.Join(dir, ".leankg")
	if engine != store.EnginePostgres {
		target = filepath.Join(target, "leankg.db")
	}
	_, err := os.Stat(target)
	return err == nil
}

func containsDir(rows []store.ProjectRecord, dir string) bool {
	for _, r := range rows {
		if r.Dir == dir {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// T1: the hot set
// ---------------------------------------------------------------------------

// MaxRepos is the hot-set cap for this process: LEANKG_PORTFOLIO_MAX_REPOS when
// it parses as a positive integer, else DefaultMaxRepos. A cap is always at
// least one — a portfolio query that serves nothing is a bug, not a config.
func MaxRepos() int {
	v := os.Getenv(MaxReposEnv)
	if v == "" {
		return DefaultMaxRepos
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 1 {
		return DefaultMaxRepos
	}
	return n
}

// HotSet is the T1 serving policy: at most Cap registered projects answer one
// portfolio query, most-recently-served first.
//
// "Detach" is a SERVING decision, never a delete: a cold project keeps its
// registry row and its index, is simply not opened, and is reported in the
// result as not_hot with its reason. Nothing is eagerly indexed to make a
// project hot.
//
// CEILING: the recency list is in-process (per serving process, and per
// HotSet instance — a CLI command's is discarded when it exits). The upgrade
// path to a durable LRU is a last_served column on the projects table
// (migration 016+) touched by the same Plan/Touch pair; the ordering fallback
// below (last_indexed) is the durable half that already works cross-process.
type HotSet struct {
	mu  sync.Mutex
	cap int
	mru []string // canonical dirs, most recently served first
}

// NewHotSet builds a hot set; cap <= 0 takes MaxRepos().
func NewHotSet(cap int) *HotSet {
	if cap <= 0 {
		cap = MaxRepos()
	}
	return &HotSet{cap: cap}
}

// Limit reports the current cap. The cap is a locked read because
// RefreshCap can move it under a serving process.
func (h *HotSet) Limit() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cap
}

// RefreshCap re-reads MaxRepos() and trims the recency list to fit. Serving
// calls it once per fan-out, so LEANKG_PORTFOLIO_MAX_REPOS is honored without a
// restart (and a test can pin it per case).
func (h *HotSet) RefreshCap() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cap = MaxRepos()
	if h.cap > 0 && len(h.mru) > h.cap {
		h.mru = h.mru[:h.cap]
	}
	return h.cap
}

// Plan splits the registry into the served hot set (at most Limit(), MRU first,
// then most-recently-indexed, then name) and the detached remainder.
func (h *HotSet) Plan(projects []store.ProjectRecord) (hot, cold []store.ProjectRecord) {
	ordered := make([]store.ProjectRecord, len(projects))
	copy(ordered, projects)

	h.mu.Lock()
	rank := make(map[string]int, len(h.mru))
	for i, d := range h.mru {
		rank[d] = i
	}
	h.mu.Unlock()

	// Projects never touched this process rank below touched ones, in
	// last_indexed order (most recent first, never-indexed last, name/dir for
	// determinism) — the durable half of the LRU.
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, iok := rank[ordered[i].Dir]
		rj, jok := rank[ordered[j].Dir]
		if iok != jok {
			return iok // touched projects first
		}
		if iok && ri != rj {
			return ri < rj
		}
		ai, bi := stampOf(ordered[i].LastIndexed), stampOf(ordered[j].LastIndexed)
		if ai != bi {
			if ai == "" {
				return false
			}
			if bi == "" {
				return true
			}
			return ai > bi // UTC ISO stamps sort lexicographically in time order
		}
		if ordered[i].Name != ordered[j].Name {
			return ordered[i].Name < ordered[j].Name
		}
		return ordered[i].Dir < ordered[j].Dir
	})

	if cap := h.Limit(); cap > 0 && len(ordered) > cap {
		return ordered[:cap], ordered[cap:]
	}
	return ordered, nil
}

// stampOf reads an optional stamp as a plain string ("" for never indexed).
func stampOf(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Touch records that dir served a query, promoting it to most-recently-used.
// Directories outside the registry are ignored: a touch is not a registration.
func (h *HotSet) Touch(dir string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, d := range h.mru {
		if d == dir {
			if i == 0 {
				return
			}
			h.mru = append(h.mru[:i], h.mru[i+1:]...)
			break
		}
	}
	h.mru = append([]string{dir}, h.mru...)
	if h.cap > 0 && len(h.mru) > h.cap {
		h.mru = h.mru[:h.cap]
	}
}

// Hot reports the current MRU order (a copy; safe to hold).
func (h *HotSet) Hot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.mru...)
}

// ---------------------------------------------------------------------------
// T1: the fan-out
// ---------------------------------------------------------------------------

// PortfolioAction is the query-tool action this package serves. core owns the
// dispatch (report-only file); the constant lives here so the recursion guard
// and the CLI agree on the name.
const PortfolioAction = "portfolio"

// Query is one portfolio read, run identically inside each hot project.
type Query struct {
	Text   string
	Action string         // child action; "" = that project's ladder router
	Limit  int            // merged cap
	Args   map[string]any // child args (depth, to, service, ...)
}

// Child serves q inside one registered project. Implementations open the
// project read-only (Options.OpenChild) and return that project's own query
// envelope; hits and freshness are lifted out of it. An error becomes a
// per-child error entry — a failing project NEVER disappears from the result.
type Child func(ctx context.Context, p store.ProjectRecord, q Query) (map[string]any, error)

// ChildResult is one registered project's standing in a portfolio answer.
type ChildResult struct {
	Project   string           `json:"project"`
	Dir       string           `json:"dir"`
	Status    string           `json:"status"` // ok | error | not_hot | not_indexed
	Freshness string           `json:"freshness,omitempty"`
	Rung      string           `json:"rung,omitempty"`
	Hits      []map[string]any `json:"hits,omitempty"`
	Error     string           `json:"error,omitempty"`
	Reason    string           `json:"reason,omitempty"`
}

// Report is a portfolio fan-out answer: per-project standing (never silently
// a dropped child) plus the merged, project-attributed hit list.
type Report struct {
	Query      string `json:"query"`
	Action     string `json:"action,omitempty"`
	HotLimit   int    `json:"hot_limit"`
	Registered int    `json:"projects_registered"`
	Served     int    `json:"projects_served"`
	Failed     int    `json:"projects_failed"`
	NotHot     int    `json:"projects_not_hot"`
	// NotIndexed counts T0 manifest PARENTS skipped without a store-open:
	// registered never-indexed with zero counts (children carry the stores).
	// Distinct from Failed — a manifest parent is not a fault, it is the
	// designed pre-index state (#406-2, on the query path this time).
	NotIndexed int              `json:"projects_not_indexed"`
	Projects   []ChildResult    `json:"projects"`
	Hits       []map[string]any `json:"hits"`
}

// FanOut runs q across the registry's hot set and merges the answers. child is
// the per-project executor (core supplies the real ladder; tests supply a
// store-level one). A missing registry is an EMPTY Report, not an error:
// "portfolio" is a legitimate question to ask a single-repo deployment.
func FanOut(ctx context.Context, o Options, q Query, hot *HotSet, child Child) (*Report, error) {
	if q.Action == PortfolioAction {
		return nil, errors.New("portfolioreg: a portfolio query cannot fan out to another portfolio query")
	}
	if child == nil {
		return nil, errors.New("portfolioreg: FanOut requires a child query function")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	rep := &Report{Query: q.Text, Action: q.Action, Hits: []map[string]any{}}
	if hot == nil {
		hot = NewHotSet(0)
	}
	// Re-read the cap for this fan-out: LEANKG_PORTFOLIO_MAX_REPOS is a
	// serving knob, not a build-time constant.
	rep.HotLimit = hot.RefreshCap()

	projects, err := Projects(ctx, o)
	if err != nil {
		if errors.Is(err, ErrNoRegistry) {
			return rep, nil
		}
		return nil, err
	}
	rep.Registered = len(projects)
	if len(projects) == 0 {
		return rep, nil
	}

	hotRows, coldRows := hot.Plan(projects)
	for _, p := range hotRows {
		if err := ctx.Err(); err != nil {
			rep.Projects = append(rep.Projects, ChildResult{
				Project: p.Name, Dir: p.Dir, Status: "error", Error: err.Error(),
			})
			rep.Failed++
			continue
		}
		// A T0 manifest PARENT is registered with no store of its own by
		// design (its children carry the stores) — opening one and reading
		// the failure as `error` is the same lie #406-2 removed from the
		// doctor fleet leg; class it here too, before touching the child
		// executor.
		if p.LastIndexed == nil && p.ElementCount == 0 && p.FileCount == 0 {
			rep.Projects = append(rep.Projects, ChildResult{
				Project: p.Name, Dir: p.Dir, Status: "not_indexed",
				Reason: "T0 manifest parent — no own store yet; query its children or index the parent",
			})
			rep.NotIndexed++
			continue
		}
		cr := ChildResult{Project: p.Name, Dir: p.Dir}
		resp, err := child(ctx, p, q)
		switch {
		case err != nil:
			cr.Status, cr.Error = "error", err.Error()
			rep.Failed++
		default:
			cr.Status = "ok"
			cr.Freshness, cr.Rung = envelopeString(resp, "freshness"), envelopeRung(resp)
			cr.Hits = envelopeHits(resp)
			for _, h := range cr.Hits {
				h["project"] = p.Name
				h["project_dir"] = p.Dir
			}
			rep.Served++
			hot.Touch(p.Dir)
		}
		rep.Projects = append(rep.Projects, cr)
	}
	for _, p := range coldRows {
		rep.Projects = append(rep.Projects, ChildResult{
			Project: p.Name, Dir: p.Dir, Status: "not_hot",
			Reason: fmt.Sprintf("outside the hot set: %d registered, %d served per query (raise %s)",
				rep.Registered, rep.HotLimit, MaxReposEnv),
		})
		rep.NotHot++
	}
	rep.Hits = mergeHits(rep.Projects, limit)
	return rep, nil
}

// mergeHits flattens the per-project hit lists into one list, round-robin by
// per-project rank, so the first registered project cannot crowd every other
// project's answer out of a cross-repo search. Scores are NOT compared across
// projects: two stores' L2 relevance numbers are not the same scale.
func mergeHits(children []ChildResult, limit int) []map[string]any {
	out := make([]map[string]any, 0, limit)
	for rank := 0; len(out) < limit; rank++ {
		advanced := false
		for i := range children {
			if children[i].Status != "ok" || rank >= len(children[i].Hits) {
				continue
			}
			out = append(out, children[i].Hits[rank])
			advanced = true
			if len(out) == limit {
				return out
			}
		}
		if !advanced {
			break
		}
	}
	return out
}

// envelopeHits lifts hits out of a child envelope. The slice type depends on
// how the answer got here: core builds []map[string]any in-process, while a
// JSON round trip through a transport decodes []any. Both are the same list to
// a fleet reader; a hit shape neither side recognizes is skipped, not guessed.
func envelopeHits(resp map[string]any) []map[string]any {
	var out []map[string]any
	switch raw := resp["hits"].(type) {
	case []map[string]any:
		out = append(out, raw...)
	case []any:
		for _, v := range raw {
			if m, ok := v.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}

// envelopeString lifts a string field out of a child envelope.
func envelopeString(resp map[string]any, key string) string {
	s, _ := resp[key].(string)
	return s
}

// envelopeRung lifts retrieval{rung,reason}.rung out of a child envelope.
func envelopeRung(resp map[string]any) string {
	r, _ := resp["retrieval"].(map[string]any)
	s, _ := r["rung"].(string)
	return s
}
