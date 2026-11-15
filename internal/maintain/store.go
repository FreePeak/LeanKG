package maintain

import "github.com/FreePeak/LeanKG/internal/store"

// Store is the part of the store contract the maintenance passes use: the
// maintenance ops and orphan sweeps (Maintainer), vector and stamp reads and
// deletes (Vectors), the element and file reads a sweep compares against, the
// run ledger, the freshness inventory, metrics retention and the KV catalog
// (plan section 3.3, rule 2). Mode names the open mode a pass checks before it
// writes; it is lifecycle, not a role.
type Store interface {
	store.Maintainer
	store.Vectors
	store.ElementReader
	store.IndexWriter
	store.EmbedRuns // KeepModelIDsFunc reads the live model from the run ledger
	store.Inventories
	store.Metrics
	store.KV
	Mode() store.Mode
}
