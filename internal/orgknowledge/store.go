package orgknowledge

import "github.com/FreePeak/LeanKG/internal/store"

// Store is the part of the store contract the org knowledge surface uses: the
// incident, knowledge-entry, service-metadata and environment-snapshot roles,
// plus the element and graph reads that the conflict checks compare against
// (plan section 3.3, rule 2).
type Store interface {
	store.Knowledge
	store.ElementReader
	store.GraphReader
}
