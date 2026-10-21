package portfolioreg

// The automatic registry prune.
//
// NOTHING PRUNED THE REGISTRY. Prune and Forget had exactly one caller each
// — the manual `leankg projects --prune` / `--forget` verbs — so a row for a
// project that left the fleet outlived the project forever, and the fleet
// kept answering questions about directories that no longer existed.
// AutoSweep is the automatic prune the serve and maintenance paths call.
//
// SAFETY, and it is the whole contract of this file:
//
//   - A sweep only ever removes REGISTRY ROWS. It never deletes a store
//     file, an element, a vector, or anything else on disk.
//   - A row whose project directory is still LIVE is never removed — not
//     even when its store is missing (a project that is registered but not
//     yet indexed is a legitimate state).
//   - A sweep of a healthy registry changes nothing at all.

import (
	"context"
	"errors"
	"sort"
)

// SweepReport is what one automatic sweep did.
//
// Everything here is a REPORT: the sweep removed registry rows and nothing
// else. A caller that wants to log the sweep logs this.
type SweepReport struct {
	// Removed lists the registry rows the sweep forgot, because the
	// directory they named no longer exists.
	Removed []string `json:"removed"`
	// Kept counts the rows that survived the sweep.
	Kept int `json:"kept"`
}

// AutoSweep is the automatic registry prune: it forgets the rows whose
// project directory is gone.
//
// It is safe to run unattended on a live registry:
//
//   - it removes REGISTRY ROWS only, never a file;
//   - a row whose directory is still live is never removed, whatever state
//     its store is in;
//   - a sweep of a healthy registry changes nothing.
//
// A missing registry is an empty report, not an error: a deployment that
// serves nothing has nothing to prune.
//
// CEILING: the sweep is one stat per registered project. The upgrade path
// is a last_seen column on the projects table (migration 016+) written by
// the registering path, which would let the sweep tell "the project is
// gone" from "the project is on an unmounted volume" without a stat.
func AutoSweep(ctx context.Context, o Options) (SweepReport, error) {
	rep := SweepReport{Removed: []string{}}
	projects, err := Projects(ctx, o)
	if errors.Is(err, ErrNoRegistry) {
		return rep, nil // nothing registered, nothing to sweep
	}
	if err != nil {
		return rep, err
	}
	var gone []string
	for _, p := range projects {
		if !pathPresent(p.Dir) {
			// The project left the fleet: the row outlived its directory.
			gone = append(gone, p.Dir)
			continue
		}
		// The directory is live: the row stays, whatever the store says.
		rep.Kept++
	}
	if len(gone) == 0 {
		return rep, nil
	}
	sort.Strings(gone)
	forgot, err := forgetDirs(ctx, o, gone)
	if err != nil {
		return rep, err
	}
	rep.Removed = forgot
	return rep, nil
}
