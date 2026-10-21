package maintain

import (
	"context"
	"fmt"

	"github.com/FreePeak/LeanKG/internal/gitobj"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/store"
)

// The HEAD reconcile: make the store match the tree it claims to mirror.
//
// The store NEVER reconciled with the tree on its own. `leankg index` deletes
// the files a walk does not see, but the fleet's hourly maintenance pass only
// swept orphans and vacuumed — it never compared the store against HEAD. So
// a store could hold 1,397 paths that no longer existed in HEAD (13,006
// elements, 59 % of a 22,164-element store), 5.3 days behind, and every
// reader was served those stale elements.
//
// SAFETY — non-negotiable. A wrong delete destroys live, still-queried
// content. A path is deleted only when ALL of these hold:
//
//  1. the store's recorded index root is a git repository whose HEAD is
//     READABLE (an unborn or unreadable HEAD deletes nothing);
//  2. the stored code_files.content_hash for that path starts with "git:"
//     — the marker the git-object index writes, so the store's copy of the
//     path IS the committed content. A filesystem-scanned store records a
//     SHA-256 there instead, so its paths are never touched here; and
//  3. the path is ABSENT from HEAD. A path HEAD contains is never deleted,
//     whatever mode it has.
//
// The "git:" marker is what makes (2) meaningful: it says the store was
// built from the repository's objects, so "absent from HEAD" means "deleted
// from the repository", not "the walk did not see it". A store that was NOT
// git-indexed can therefore never be touched by this pass.
//
// The reconcile does NOT claim the store now matches HEAD: it removes what
// HEAD no longer has, and the head id it compares against stays the commit
// the store was indexed from. The remaining content is still that commit's,
// so head_sync keeps saying "stale" until a re-index runs.
type ReconcileResult struct {
	// DeletedFiles is the number of paths whose store content was removed.
	DeletedFiles int
	// DeletedElements is the number of elements removed with them.
	DeletedElements int
	// SkippedFiles is the number of stored paths left alone: present in
	// HEAD, or not a git-indexed path.
	SkippedFiles int
	// Reason explains why nothing was deleted (an empty string means the
	// reconcile ran and found nothing to delete).
	Reason string
}

// ReconcileHead removes the store content of the paths the project's HEAD no
// longer contains.
//
// The project root is the store's OWN recorded index root, not a directory
// the caller names: the store's paths are relative to that root, and the
// "git:" marker in them is only meaningful against it.
//
// It is a no-op for every store that was not built from a git tree, and for
// every project whose HEAD cannot be read.
func ReconcileHead(ctx context.Context, st store.Backend) (ReconcileResult, error) {
	var res ReconcileResult
	if st == nil {
		res.Reason = "no store"
		return res, nil
	}
	// A read-only store cannot be pruned: every delete below would fail.
	if m, ok := st.(interface{ Mode() store.Mode }); ok && m.Mode() == store.RO {
		res.Reason = "read-only store"
		return res, nil
	}
	root, ok, err := store.RecordedRoot(st)
	if err != nil {
		return res, fmt.Errorf("maintain: read recorded root: %w", err)
	}
	if !ok || root == "" {
		res.Reason = "no recorded index root"
		return res, nil
	}
	// (1) The root must be a git repository with a readable HEAD. HeadPaths
	// refuses a non-repository, a subdirectory of one, and an unborn HEAD.
	headPaths, ok, err := gitobj.HeadPaths(ctx, root)
	if err != nil {
		return res, fmt.Errorf("maintain: list HEAD of %s: %w", root, err)
	}
	if !ok {
		res.Reason = "no readable HEAD"
		return res, nil
	}
	inHead := make(map[string]bool, len(headPaths))
	for _, p := range headPaths {
		inHead[p] = true
	}
	files, err := st.Files()
	if err != nil {
		return res, fmt.Errorf("maintain: read stored files: %w", err)
	}
	// The candidate set: stored paths HEAD does not contain. The git: marker
	// is checked per path, so a store that mixes git-indexed and
	// filesystem-scanned paths keeps the latter.
	var missing []string
	for _, f := range files {
		if inHead[f.Path] {
			res.SkippedFiles++
			continue
		}
		if !index.IsGitKeyed(f.ContentHash) {
			// (2) Not built from the repository's objects: this store knows
			// nothing about the tree, so it must not be pruned against it.
			res.SkippedFiles++
			continue
		}
		missing = append(missing, f.Path)
	}
	if len(missing) == 0 {
		res.Reason = "nothing to delete"
		return res, nil
	}
	before, err := st.ElementCount()
	if err != nil {
		return res, fmt.Errorf("maintain: count elements: %w", err)
	}
	// The shared delete step of the index reconcile (index.DeleteFiles): the
	// elements and relationships of each path, and its file record.
	deleted, err := index.DeleteFiles(ctx, st, missing)
	if err != nil {
		return res, fmt.Errorf("maintain: delete %d stale path(s): %w", len(missing), err)
	}
	res.DeletedFiles = deleted
	after, err := st.ElementCount()
	if err != nil {
		return res, fmt.Errorf("maintain: count elements: %w", err)
	}
	res.DeletedElements = before - after
	// The residue the per-file delete cannot own: vectors are keyed by
	// qualified name, not by path, so a deleted path's vectors are orphans
	// once its elements are gone — and an edge from ANOTHER file into a
	// deleted element dangles. Leaving them would let the L3 arm answer with
	// vectors whose content no longer exists in the store.
	if _, err := st.DeleteOrphanVectors(); err != nil {
		return res, fmt.Errorf("maintain: reclaim orphan vectors: %w", err)
	}
	if _, err := st.DeleteOrphanRelationships(); err != nil {
		return res, fmt.Errorf("maintain: reclaim orphan relationships: %w", err)
	}
	// The inventory is the store's own summary of what it holds, and
	// freshness is DERIVED from it: the inventory's seq is compared with the
	// write watermark, and a difference reads "possibly_stale". A delete
	// advances that watermark (DeleteByFile and DeleteFileRecord both bump
	// it), so without this the store would report "possibly_stale" the
	// moment it was reconciled — the signal would say "the tree may have
	// moved" about a store that was just made to MATCH the tree. The index
	// refreshes the inventory for the same reason, and the reconcile has to
	// do it too, or it makes the fleet's own doctor tell operators to
	// re-index stores that are now more correct than they were.
	if _, err := store.RefreshInventory(st); err != nil {
		return res, fmt.Errorf("maintain: refresh inventory after reconcile: %w", err)
	}
	return res, nil
}
