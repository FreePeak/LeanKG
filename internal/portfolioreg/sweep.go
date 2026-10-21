package portfolioreg

// The automatic registry sweep.
//
// NOTHING PRUNED THE REGISTRY. Prune and Forget had exactly one caller each
// — the manual `leankg projects --prune` / `--forget` verbs — so a row for a
// project that left the fleet outlived the project forever, and the fleet
// kept answering questions about directories that no longer existed.
// AutoSweep is the automatic prune the serve and maintenance paths call.
//
// "THE STORE IS PRESENT" USED TO MEAN "THE FILE EXISTS". A store whose
// recorded index root no longer matches the directory it sits in is dead
// weight: the root guard refuses to walk a store from a different root
// (every path in a store is relative to the root it was built from), so it
// can never be repaired by re-indexing, and no route, doctor or sweep can
// see it. The sweep REPORTS that as a root mismatch — it never deletes the
// store.
//
// SAFETY, and it is the whole contract of this file:
//
//   - A sweep only ever removes REGISTRY ROWS. It never deletes a store
//     file, an element, a vector, or anything else on disk.
//   - A row whose project directory is still LIVE is never removed — not
//     even when its store is missing (a project that is registered but not
//     yet indexed is a legitimate state) and not even when its store records
//     a different root (that is reported, not deleted).
//   - A sweep of a healthy registry changes nothing at all.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/FreePeak/LeanKG/internal/store"
)

const (
	// rootKVNamespace and rootKVKey are the kv coordinates the index root
	// is recorded under — the same pair internal/index writes and compares
	// against (internal/index/index.go). They are repeated here because
	// internal/index keeps them unexported; the regression test plants a
	// root through index.RebaseRoot, so a rename on that side fails the
	// test instead of silently making every store "root unknown".
	rootKVNamespace = "index"
	rootKVKey       = "root"

	// StoreOK: the store is there and its recorded root names the project
	// directory (or it records no root at all — a store built before the
	// root guard — which is "unknown", never a mismatch).
	StoreOK = "ok"
	// StoreNoStore: the project has no store yet (registered, never
	// indexed). A legitimate state, not a fault.
	StoreNoStore = "no_store"
	// StoreUnreadable: a store is there but could not be read.
	StoreUnreadable = "unreadable"
	// StoreRootMismatch: the store records an index root that is not the
	// project directory. REPORTED, never deleted.
	StoreRootMismatch = "root_mismatch"
)

// StoreStatus is the verdict on one project's store.
type StoreStatus struct {
	// Dir is the project directory the store was opened for.
	Dir string `json:"dir"`
	// StorePath is the store the verdict was read from.
	StorePath string `json:"store_path"`
	// RecordedRoot is the index root the store recorded for itself ("" when
	// it records none).
	RecordedRoot string `json:"recorded_root"`
	// Reason is one of StoreOK, StoreNoStore, StoreUnreadable or
	// StoreRootMismatch.
	Reason string `json:"reason"`
}

// RootMismatch is one registered project whose store records an index root
// that is not the directory the store lives in.
//
// Such a store can never be repaired by re-indexing: the root guard refuses
// to walk a store from a different root, because every path in the store is
// relative to the root it was built from. It is REPORTED — never deleted, and
// never silently forgotten.
type RootMismatch struct {
	// Dir is the registered project directory.
	Dir string `json:"dir"`
	// RecordedRoot is the index root the store recorded for itself.
	RecordedRoot string `json:"recorded_root"`
	// StorePath is the store the mismatch was read from.
	StorePath string `json:"store_path"`
	// Reason is the stable label (StoreRootMismatch).
	Reason string `json:"reason"`
}

// SweepReport is what one automatic sweep did and what it found.
//
// Everything here is a REPORT: the sweep removed registry rows and nothing
// else. A caller that wants to log the sweep logs this.
type SweepReport struct {
	// Removed lists the registry rows the sweep forgot, because the
	// directory they named no longer exists.
	Removed []string `json:"removed"`
	// RootMismatch lists the stores whose recorded index root is not the
	// directory they live in. Reported, never deleted.
	RootMismatch []RootMismatch `json:"root_mismatch"`
	// NoStore lists the registered projects that have no store at all: a
	// project that is registered but not yet indexed. Their rows are KEPT.
	NoStore []string `json:"no_store"`
	// Unreadable lists the registered projects whose store exists but could
	// not be read. Their rows are KEPT: an unreadable store says nothing
	// about the project.
	Unreadable []string `json:"unreadable"`
	// Kept counts the rows that survived the sweep.
	Kept int `json:"kept"`
}

// AutoSweep is the automatic registry prune: it forgets the rows whose
// project directory is gone, and reports the rows that need a human.
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
// CEILING: the root check opens each registered project's store READ-ONLY to
// read one kv row, so the sweep costs one cheap open per registered project
// (the same cost the doctor's fleet check already pays). The upgrade path
// is a root column on the projects table (migration 016+), written by the
// indexer at the moment it writes the kv row, which would make the whole
// sweep a pure registry read.
func AutoSweep(ctx context.Context, o Options) (SweepReport, error) {
	rep := SweepReport{
		Removed:      []string{},
		RootMismatch: []RootMismatch{},
		NoStore:      []string{},
		Unreadable:   []string{},
	}
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
		st, err := CheckStore(ctx, o, p.Dir)
		if err != nil {
			rep.Unreadable = append(rep.Unreadable, p.Dir)
			continue
		}
		switch st.Reason {
		case StoreRootMismatch:
			rep.RootMismatch = append(rep.RootMismatch, RootMismatch{
				Dir:          p.Dir,
				RecordedRoot: st.RecordedRoot,
				StorePath:    st.StorePath,
				Reason:       st.Reason,
			})
		case StoreNoStore:
			rep.NoStore = append(rep.NoStore, p.Dir)
		case StoreUnreadable:
			rep.Unreadable = append(rep.Unreadable, p.Dir)
		}
	}
	if len(gone) > 0 {
		sort.Strings(gone)
		forgot, err := forgetDirs(ctx, o, gone)
		if err != nil {
			return rep, err
		}
		rep.Removed = forgot
		if rep.Removed == nil {
			rep.Removed = []string{}
		}
	}
	sort.Strings(rep.NoStore)
	sort.Strings(rep.Unreadable)
	sort.Slice(rep.RootMismatch, func(i, j int) bool {
		return rep.RootMismatch[i].Dir < rep.RootMismatch[j].Dir
	})
	return rep, nil
}

// CheckStore opens one project's store READ-ONLY and says what it is:
//
//   - StoreOK: the store is there and its recorded index root names the
//     project directory. A store that records no root at all (one built
//     before the root guard) is also OK: "unknown" is not a mismatch.
//   - StoreNoStore: the project has no store yet. A registered project that
//     was never indexed is a legitimate state, not a fault.
//   - StoreUnreadable: a store is there but could not be read (a corrupt
//     file, a locked parent, a Postgres schema that exists but refuses the
//     read). The error is returned so a caller can log it.
//   - StoreRootMismatch: the store records an index root that is NOT the
//     project directory — the store indexes a different tree than the one
//     it is filed under, and can therefore never be repaired by re-indexing.
//
// A store that lives OUTSIDE the project (LEANKG_DB_PATH, or a leankg.yaml
// standalone_db_path) is shared by definition, so its recorded root names
// the project that owns it, not this one: the root check does not apply and
// the verdict is StoreOK.
func CheckStore(ctx context.Context, o Options, dir string) (StoreStatus, error) {
	st := StoreStatus{Dir: dir, Reason: StoreOK}
	// A shared store is one file for many projects: its recorded root is
	// the owner's, so comparing it with this directory would report every
	// project as a mismatch.
	shared := store.StandaloneDBPath(dir) != ""
	child, err := o.OpenChild(ctx, dir)
	if err != nil {
		if storeAbsent(err) {
			st.Reason = StoreNoStore
			return st, nil
		}
		st.Reason = StoreUnreadable
		return st, err
	}
	st.StorePath = child.Path()
	root, ok, kerr := child.KVGet(rootKVNamespace, rootKVKey)
	_ = child.Close()
	if kerr != nil {
		st.Reason = StoreUnreadable
		return st, kerr
	}
	st.RecordedRoot = root
	if shared || !ok || root == "" {
		// No recorded root: the root is UNKNOWN, which is never a mismatch.
		return st, nil
	}
	if sameRoot(root, dir) {
		return st, nil
	}
	st.Reason = StoreRootMismatch
	return st, nil
}

// storeAbsent reports whether an open failure means "there is no store here"
// (a project that is registered but not yet indexed) rather than "the store
// is there and something went wrong".
func storeAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, store.ErrSchemaMissing)
}

// sameRoot reports whether a recorded index root names the same directory as
// the registered one.
//
// Both sides are compared as they were WRITTEN — the registry key is the
// canonical (absolute, symlink-resolved) directory, and so is the recorded
// root — so the plain comparison is the common case. The resolved
// comparison is the fallback for a root whose symlinks have since been
// re-pointed: if the recorded root still resolves to the registered
// directory, the store is NOT a mismatch.
func sameRoot(recorded, dir string) bool {
	if recorded == dir {
		return true
	}
	a, err := filepath.EvalSymlinks(recorded)
	if err != nil {
		return false
	}
	b, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	return a == b
}
