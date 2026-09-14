package embed

import (
	"github.com/FreePeak/LeanKG/internal/store"
)

// elementText is one embeddable code element: the file it came from (the
// atomic write unit of the pipeline), its qualified name and its content.
type elementText struct {
	File    string
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
		out = append(out, elementText{File: e.FilePath, QN: e.QualifiedName, Content: e.Content})
	}
	return out, nil
}

// readVectorCounts reports (covered, orphans) via the Backend.
func readVectorCounts(st store.Backend, modelID string) (covered, orphans int, err error) {
	return st.VectorCoverage(modelID)
}
