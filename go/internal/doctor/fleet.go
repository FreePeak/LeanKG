// Fleet doctor: the `fleet` check of `leankg doctor --deep` (issue #376, the
// #278 fleet remainder) — migration drift, freshness and totals across every
// project in the portfolio registry. Registry access, the hot-set policy and
// the T0 manifest live in internal/portfolioreg; this file is the check.
//
// The check emits one PASS/WARN/FAIL Finding like every other deep check, so
// doctor's CI contract is untouched: 0 all-pass, 1 any warn, 2 any fail. A
// project that cannot be read is reported, never dropped — the same rule the
// portfolio query follows.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/portfolioreg"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// FleetStatus is one registered project's observed state: pure data, so a test
// can describe a drifting fleet without a backend. Applied is the project's
// schema_migrations ledger (nil when Unreadable is set); Elements and Files are
// the counts in the registry row; Fresh is the project's own freshness label
// ("fresh", "possibly_stale", "cold", or "" when unreadable).
type FleetStatus struct {
	Project     string `json:"project"`
	Dir         string `json:"dir"`
	LastIndexed string `json:"last_indexed,omitempty"`
	Applied     []int  `json:"applied"`
	Elements    int    `json:"elements"`
	Files       int    `json:"files"`
	Fresh       string `json:"freshness,omitempty"`
	Unreadable  string `json:"error,omitempty"`
	// Missing marks a registration whose checkout no longer exists on disk.
	// Informational, like a T0 parent (Manifest below) — a vanished fixture
	// must not drag the exit code.
	Missing bool `json:"missing,omitempty"`
	// Manifest marks a T0 portfolio registration the registry itself reports
	// as never indexed with nothing counted (a manifest-only PARENT whose
	// children carry the stores). By design it has no store of its own, so
	// a failed store open must read MANIFEST (informational), never
	// UNREADABLE (#406).
	Manifest bool `json:"manifest_only,omitempty"`
}

// FleetSource is the fleet seam: enumerate the registered projects and read
// each one's state. Tests inject a stub (the same role Probes plays for the
// per-project checks); RunDeep wires fleetProbe.
type FleetSource interface {
	Fleet() ([]FleetStatus, error)
}

// fleetProbe is the production FleetSource: one read-only store handle per
// registered project, sqlite or Postgres exactly as the deployment runs it.
// Unlike a portfolio QUERY it walks the WHOLE registry — an un-hot project can
// still be behind on migrations, which is precisely what this check exists to
// catch — and every handle is closed before the next opens.
type fleetProbe struct {
	ctx  context.Context
	opts portfolioreg.Options
}

// Fleet reports every registered project's fleet standing.
func (f fleetProbe) Fleet() ([]FleetStatus, error) {
	reg, err := portfolioreg.Open(f.ctx, f.opts, store.RO)
	if err != nil {
		return nil, err
	}
	defer reg.Close()
	projects, err := portfolioreg.ProjectsOf(reg)
	if err != nil {
		return nil, err
	}
	out := make([]FleetStatus, 0, len(projects))
	for _, p := range projects {
		st := FleetStatus{Project: p.Name, Dir: p.Dir, Elements: p.ElementCount, Files: p.FileCount}
		if p.LastIndexed != nil {
			st.LastIndexed = *p.LastIndexed
		}
		if _, err := os.Stat(p.Dir); errors.Is(err, os.ErrNotExist) {
			st.Missing = true
			out = append(out, st)
			continue
		}
		if p.LastIndexed == nil && p.ElementCount == 0 && p.FileCount == 0 {
			// The registry's own admission: never indexed, nothing counted —
			// a T0 manifest row. No store to open is the designed state.
			st.Manifest = true
			out = append(out, st)
			continue
		}
		child, err := f.opts.OpenChild(f.ctx, p.Dir)
		if err != nil {
			st.Unreadable = err.Error()
			out = append(out, st)
			continue
		}
		applied, err := store.AppliedMigrations(child)
		if err != nil {
			st.Unreadable = err.Error()
		} else {
			st.Applied = applied
			els, _ := child.ElementCount()
			st.Fresh = store.Freshness(child, els)
		}
		_ = child.Close()
		out = append(out, st)
	}
	return out, nil
}

// checkFleet folds the fleet into one finding: per-project migration drift
// against the embedded migration list, freshness, and the fleet totals.
func checkFleet(_ Probes, env Env) Finding {
	const check = "fleet"
	if env.Fleet == nil {
		return Finding{check, StatusPass, "fleet check not wired (single-project deployment)", ""}
	}
	projects, err := env.Fleet.Fleet()
	if err != nil {
		if errors.Is(err, portfolioreg.ErrNoRegistry) {
			// A deployment with no fleet registry is not sick: the
			// single-project checks already cover the project at hand.
			return Finding{check, StatusPass, "no portfolio registry; single-project checks only", ""}
		}
		return Finding{check, StatusFail, fmt.Sprintf("cannot read portfolio registry: %v", err),
			"Point LEANKG_PORTFOLIO_DB (sqlite) or LEANKG_PG_URL (postgres) at the fleet " +
				"registry, or register one with `leankg register-project DIR --name N`."}
	}
	if len(projects) == 0 {
		return Finding{check, StatusPass, "portfolio registry empty (0 projects)", ""}
	}

	embedded := store.Migrations()
	var behind, ahead, unreadable, stale, missing, manifest int
	var elements, files int
	var lines []string
	for _, p := range projects {
		elements += p.Elements
		files += p.Files
		switch {
		case p.Missing:
			// Informational only: a vanished checkout is not an unhealthy
			// deployment, and warning on it would leave every long-lived dev
			// machine with permanent doctor noise.
			missing++
			lines = append(lines, fmt.Sprintf("%s: MISSING (checkout gone: %s)", p.Project, p.Dir))
			continue
		case p.Manifest:
			// Informational: a T0 parent answers portfolio queries through its
			// children's stores; its own store appears only when something is
			// indexed at/under it.
			manifest++
			lines = append(lines, fmt.Sprintf("%s: MANIFEST (T0 parent, no own store yet)", p.Project))
			continue
		case p.Unreadable != "":
			unreadable++
			lines = append(lines, fmt.Sprintf("%s: UNREADABLE (%s)", p.Project, p.Unreadable))
			continue
		}
		pending, unknown := migrationDrift(embedded, p.Applied)
		switch {
		case len(pending) > 0:
			behind++
			lines = append(lines, fmt.Sprintf("%s behind by %d (next: %s)", p.Project, len(pending),
				strings.Join(pending, ", ")))
		case len(unknown) > 0:
			ahead++
			lines = append(lines, fmt.Sprintf("%s ahead by %d (%s)", p.Project, len(unknown),
				strings.Join(unknown, ", ")))
		case p.Fresh == store.FreshnessPossiblyStale || p.Fresh == store.FreshnessCold:
			stale++
			lines = append(lines, fmt.Sprintf("%s: current, %s", p.Project, p.Fresh))
		default:
			lines = append(lines, fmt.Sprintf("%s: current, %s", p.Project, p.Fresh))
		}
	}
	detail := fmt.Sprintf("%d projects, %d elements, %d files — %s",
		len(projects), elements, files, strings.Join(lines, "; "))

	switch {
	case behind > 0:
		return Finding{check, StatusFail, detail,
			"Run a writer command in each behind project (e.g. `leankg index`) — migrations apply " +
				"automatically when a writer opens the schema."}
	case unreadable > 0:
		return Finding{check, StatusWarn, detail,
			"Re-index an unreadable project, or drop it from the fleet with " +
				"`leankg projects --forget DIR` if it has moved."}
	case ahead > 0:
		return Finding{check, StatusWarn, detail,
			"These projects were migrated by a newer leankg; upgrade this binary to match " +
				"the database schema."}
	case stale == 0 && missing+manifest > 0:
		// Only vanished checkouts and/or T0 manifests: healthy deployment,
		// housekeeping informational at most.
		return Finding{check, StatusPass, detail,
			"Drop stale registrations with `leankg projects --forget DIR`; index a T0 parent's child (or the parent) to give it a store."}
	case stale > 0:
		return Finding{check, StatusWarn, detail,
			"Re-index the stale projects (`leankg index <dir>`) so the fleet answers from " +
				"current inventories."}
	default:
		return Finding{check, StatusPass, detail, ""}
	}
}

// migrationDrift compares one project's applied ledger with the embedded
// migration list: pending = "<%03d %s>" labels for steps this binary has and
// the project lacks, unknown = bare version labels for steps the project has
// and this binary lacks. Mirrors checkMigrations' vocabulary at fleet scale.
func migrationDrift(embedded []store.MigrationStep, applied []int) (pending, unknown []string) {
	set := make(map[int]bool, len(applied))
	for _, v := range applied {
		set[v] = true
	}
	for _, m := range embedded {
		if !set[m.Version] {
			pending = append(pending, fmt.Sprintf("%03d %s", m.Version, m.Name))
		}
		delete(set, m.Version)
	}
	rest := make([]int, 0, len(set))
	for v := range set {
		rest = append(rest, v)
	}
	sort.Ints(rest)
	for _, v := range rest {
		unknown = append(unknown, fmt.Sprintf("%03d", v))
	}
	return pending, unknown
}
