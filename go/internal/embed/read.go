package embed

import (
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// elementText is one embeddable code element: qualified name plus content.
type elementText struct {
	QN      string
	Content string
}

// readElements enumerates every indexed element through the Backend —
// engine-agnostic (was: sidecar SQLite handle duplicating schema knowledge;
// collapsed onto store.Backend.Elements at the W4 gate).
func readElements(st store.Backend) ([]elementText, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	out := make([]elementText, 0, len(els))
	for _, e := range els {
		out = append(out, elementText{QN: e.QualifiedName, Content: e.Content})
	}
	return out, nil
}

// readVectorCounts reports (covered, orphans) via the Backend.
func readVectorCounts(st store.Backend, modelID string) (covered, orphans int, err error) {
	return st.VectorCoverage(modelID)
}
