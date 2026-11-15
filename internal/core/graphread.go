package core

import (
	"context"

	"github.com/FreePeak/LeanKG/internal/store"
)

// Element and Relationship are the store's row types. Transports that serve
// the graph (internal/web) name them through core, so they do not import
// internal/store.
type (
	Element      = store.Element
	Relationship = store.Relationship
)

// graphRelationshipLimit caps the full relationship read. The store caps
// RelationshipsAll at 1000 rows when limit<=0; the graph endpoints need every
// edge. ponytail: a streaming iterator is the upgrade path.
const graphRelationshipLimit = 1 << 20

// GraphElements reads every element row, in the store's order.
func (e *Engine) GraphElements(ctx context.Context) ([]Element, error) {
	return e.st.Elements(ctx)
}

// GraphRelationships reads the whole relationship table.
func (e *Engine) GraphRelationships(ctx context.Context) ([]Relationship, error) {
	return e.st.RelationshipsAll(ctx, graphRelationshipLimit)
}

// ElementCount counts the element rows.
func (e *Engine) ElementCount(ctx context.Context) (int, error) {
	return e.st.ElementCount(ctx)
}
