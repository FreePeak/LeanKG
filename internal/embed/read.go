package embed

import (
	"github.com/FreePeak/LeanKG/internal/store"
)

// elementText is one embeddable code element: the file it came from (the
// atomic write unit of the pipeline), its qualified name and its content.
type elementText struct {
	File    string
	QN      string
	Type    string
	Name    string
	Content string
}

// documentText is the text a model embeds for one element (ChunkerVersion 3,
// RS-11): a header naming what the element is and where it lives, then its
// content (which carries the leading doc comment since RS-10). Raw content
// alone made natural-language queries land on type declarations and doc
// sections — 1% of top-8 hits were functions/methods against 78% of the
// corpus; the header alone moved that to 35% in the offline ablation.
// Every embedding writer (run/full and NDJSON export) goes through here, so an
// offsite batch and a local run can never embed different text.
func documentText(e elementText) string {
	head := e.Name
	if e.Type != "" {
		head = e.Type + " " + head
	}
	if e.File != "" {
		head += " — " + e.File
	}
	if head == "" {
		return e.Content
	}
	return head + "\n" + e.Content
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
		out = append(out, elementText{File: e.FilePath, QN: e.QualifiedName, Type: e.ElementType, Name: e.Name, Content: e.Content})
	}
	return out, nil
}

// readVectorCounts reports (covered, orphans) via the Backend.
func readVectorCounts(st store.Backend, modelID string) (covered, orphans int, err error) {
	return st.VectorCoverage(modelID)
}
