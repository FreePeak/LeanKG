// Per-file LLM prose summaries — the persisted checkpoint of pass 1 of the
// deterministic LLM-meaning pipeline (issue #297, grafted from trailhq/Graft
// 05760b0 src/ai/summarize.ts + src/context/build.ts).
//
// The table is the resume mechanism: a summary records the SHA-256 of the
// exact bytes it was generated from, so a re-run over an unchanged file is a
// content-hash HIT and costs zero LLM calls. That is what makes an interrupted
// pass free to restart (Graft's build.ts:170-180 flushes its summary cache for
// the same reason; the Go engine writes one row per summarized file instead of
// buffering, so a crash loses at most the file in flight).
//
// The model id is stored next to the summary because the resume check is
// (path, content_hash) AND model: a provider/model switch changes the meaning
// tier's vocabulary, so old rows stop matching and the file is re-summarized.
// This is the same stamp discipline internal/embed applies to vectors.
package store

// FileSummary is one file_summaries row: the prose summary of one source file
// plus the content hash and model that produced it.
type FileSummary struct {
	Path        string `json:"path"`         // project-relative, forward slashes
	ContentHash string `json:"content_hash"` // SHA-256 hex of the summarized bytes
	Model       string `json:"model"`        // LLM that wrote the summary
	Summary     string `json:"summary"`      // 3-8 sentences of prose
	UpdatedAt   string `json:"updated_at"`   // server-side timestamp, read only
}

// summaryCols is the shared read/write column list of file_summaries. The
// timestamp is written per dialect (strftime vs to_char), so it is deliberately
// absent from the value lists and present in the read list.
const summaryValueCols = "path, content_hash, model, summary"
const summaryReadCols = summaryValueCols + ", updated_at"

// scanFileSummary reads one row from either backend's row iterator.
func scanFileSummary(scan func(dest ...any) error) (FileSummary, error) {
	var s FileSummary
	err := scan(&s.Path, &s.ContentHash, &s.Model, &s.Summary, &s.UpdatedAt)
	return s, err
}
