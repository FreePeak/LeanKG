package embed

import "github.com/FreePeak/LeanKG/internal/store"

// Store is the part of the store contract the embed pipeline uses: element
// reads, embedding rows and stamps, the run ledger, the freshness inventory and
// the KV catalog that calibration persists in (plan section 3.3, rule 2). Engine
// names the backend in the run report; it is lifecycle, not a role.
type Store interface {
	store.ElementReader
	store.Vectors
	store.EmbedRuns
	store.Inventories
	store.KV
	store.Searcher
	store.Maintainer
	Engine() string
}
