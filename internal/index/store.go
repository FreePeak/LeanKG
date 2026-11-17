package index

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/store"
)

// Store is the store surface the indexer writes through: the file, element
// and relationship writers, the KV root/head state, the element and edge
// reads that enrichment needs, and the orphan-vector sweep. The watcher names
// it too, since it feeds the same index pass.
type Store interface {
	store.IndexWriter
	store.KV
	Elements(ctx context.Context) ([]store.Element, error)
	RelationshipsAll(ctx context.Context, limit int) ([]store.Relationship, error)
	DeleteOrphanVectors(ctx context.Context) (int, error)
}
