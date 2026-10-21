package store

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/gitobj"
)

// The head state: which git commit the store was built from.
//
// Freshness compares two counters that live in the SAME database (the write
// watermark and the inventory snapshot seq), so it can never notice that the
// TREE moved — a store 5.3 days and 59 % behind HEAD still reported "fresh".
// The head commit id is the one fact that lives OUTSIDE the database: it is
// recorded at index time and compared with the repository's current HEAD at
// status time.
//
// The values are stored through the same generic kv table the index uses for
// its root marker (namespace "index"), so they need no schema migration and
// no new Backend method.
const (
	// IndexKVNamespace is the kv namespace the index layer writes its build
	// markers under (the walk root, the extractor version, the head id).
	IndexKVNamespace = "index"
	// RootKVKey is the kv key holding the canonical root the store was
	// indexed from.
	RootKVKey = "root"
	// HeadKVKey is the kv key holding the git commit id the store was
	// indexed from.
	HeadKVKey = "head"
)

// The head_sync values: what the status payload says about the tree the
// store mirrors.
//
//   - match: the store was built from the repository's current HEAD.
//   - stale: the store was built from an OLDER commit; the tree has moved.
//   - unknown: the store was not git-indexed, the head id was never
//     recorded, or the current HEAD cannot be read (not a repository,
//     unborn HEAD, unreadable). "unknown" never claims freshness it
//     cannot prove.
const (
	HeadSyncMatch   = "match"
	HeadSyncStale   = "stale"
	HeadSyncUnknown = "unknown"
)

// SetHead records the git commit id the store was indexed from. An empty id
// records "this store was not built from a git tree" (a filesystem-scanned
// store), which is a fact, not the absence of one.
func SetHead(st Backend, head string) error {
	return st.KVSet(IndexKVNamespace, HeadKVKey, head)
}

// Head reads the recorded git commit id. ok is false when the key was never
// written (a store indexed before this fact existed); an empty value with ok
// true means the store was recorded as NOT git-indexed.
func Head(st Backend) (string, bool, error) {
	v, ok, err := st.KVGet(IndexKVNamespace, HeadKVKey)
	if err != nil {
		return "", false, err
	}
	return v, ok, nil
}

// RecordedRoot reads the canonical root the store was indexed from. ok is
// false when no root was ever recorded.
func RecordedRoot(st Backend) (string, bool, error) {
	v, ok, err := st.KVGet(IndexKVNamespace, RootKVKey)
	if err != nil {
		return "", false, err
	}
	if !ok || v == "" {
		return "", false, nil
	}
	return v, true, nil
}

// CompareHead says whether the store still mirrors the tree.
//
// stored is the commit id the store was indexed from, current is the
// repository's HEAD right now. An empty value on either side is "unknown":
// the store was not git-indexed, or the current HEAD cannot be read — never
// a claim of freshness that was not proven.
func CompareHead(stored, current string) string {
	switch {
	case stored == "" || current == "":
		return HeadSyncUnknown
	case stored == current:
		return HeadSyncMatch
	default:
		return HeadSyncStale
	}
}

// HeadSync reports whether the store still mirrors the tree it was indexed
// from.
//
// It is the truthful answer to a question the freshness label cannot answer:
// freshness compares two counters inside the same database, so it stays
// "fresh" while the repository moves on. HeadSync compares the commit the
// store was built from with the repository's current HEAD.
//
// The comparison uses the store's OWN recorded root — not the caller's cwd —
// so a store keeps reporting its own tree's state even when it is served
// from somewhere else.
func HeadSync(ctx context.Context, st Backend) string {
	head, ok, err := Head(st)
	if err != nil || !ok {
		return HeadSyncUnknown
	}
	root, ok, err := RecordedRoot(st)
	if err != nil || !ok || root == "" {
		return HeadSyncUnknown
	}
	current, ok, err := gitobj.HeadCommit(ctx, root)
	if err != nil || !ok {
		return HeadSyncUnknown
	}
	return CompareHead(head, current)
}
