// hindsight-compat mount (#414): an opt-in alias surface that speaks the
// exact HTTP wire omp's `memory.backend:"hindsight"` client speaks, so the
// harness can use LeanKG as its native agent memory with ZERO omp changes.
//
//	PUT  /v1/default/banks/{bank}                 bank ensure (always ok)
//	POST /v1/default/banks/{bank}/memories        retain  {items:[{content,...}], async?}
//	POST /v1/default/banks/{bank}/memories/recall recall  {query, tags?, tags_match?, budget?, max_tokens?}
//	POST /v1/default/banks/{bank}/reflect         digest of top recall rows {text}
//
// Wire deltas this bridge absorbs (vs the native FR-ZCP-07 surface):
// path prefix `/v1/default/banks` (not `/api/v1/memory/banks`), recall at
// `.../memories/recall` (not `.../recall`), retain body `items[]` with
// snake_case fields (not top-level `entries[]`), and the recall response
// `results[].text` (not `memories[].content`). Client tags ride in entry
// metadata and filter recall (`all`/`all_strict` require every requested
// tag, anything else requires at least one). `update_mode:"replace"` is
// treated as append (the JSONL store has no per-document revision), and the
// documents / mental-models endpoints are deliberately not mounted — the
// wiring disables mental models client-side.
package rest

import (
	"net/http"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// HandlerOption configures Handler.
type HandlerOption func(*handlerConfig)

type handlerConfig struct {
	hindsightCompat bool
}

// WithHindsightCompat mounts the Hindsight-wire alias routes (requires a
// memory backend; a no-op when serve has no --memory).
func WithHindsightCompat() HandlerOption {
	return func(c *handlerConfig) { c.hindsightCompat = true }
}

// hindsightRecallPool bounds how many ranked rows recall pulls before the
// tag filter narrows to the client's page (8, the OMP recall default).
const hindsightRecallPool = 64

type hindsightItem struct {
	Content    string            `json:"content"`
	Timestamp  string            `json:"timestamp,omitempty"`
	Context    string            `json:"context,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	DocumentID string            `json:"document_id,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	UpdateMode string            `json:"update_mode,omitempty"`
}

func registerHindsightCompat(mux *http.ServeMux, mem *memory.Memory) {
	mux.HandleFunc("PUT /v1/default/banks/{bank}", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"bank_id": r.PathValue("bank"), "object": "bank",
		})
	})

	mux.HandleFunc("POST /v1/default/banks/{bank}/memories", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Items []hindsightItem `json:"items"`
			Async *bool           `json:"async,omitempty"`
		}
		if !decode(w, r, &body) {
			return
		}
		entries := make([]memory.Entry, 0, len(body.Items))
		for _, it := range body.Items {
			if strings.TrimSpace(it.Content) == "" {
				continue
			}
			e := memory.Entry{Content: it.Content}
			if ts, err := time.Parse(time.RFC3339, it.Timestamp); err == nil {
				e.Timestamp = ts.Unix()
			}
			meta := make(map[string]any, len(it.Metadata)+3)
			for k, v := range it.Metadata {
				meta[k] = v
			}
			if len(it.Tags) > 0 {
				meta["tags"] = it.Tags
			}
			if it.Context != "" {
				meta["context"] = it.Context
			}
			if it.DocumentID != "" {
				meta["document_id"] = it.DocumentID
			}
			if len(meta) > 0 {
				e.Metadata = meta
			}
			entries = append(entries, e)
		}
		if err := mem.RetainRaw(r.PathValue("bank"), entries); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"bank_id": r.PathValue("bank"), "object": "retain_result", "written": len(entries),
		})
	})

	mux.HandleFunc("POST /v1/default/banks/{bank}/memories/recall", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string   `json:"query"`
			Tags      []string `json:"tags,omitempty"`
			TagsMatch string   `json:"tags_match,omitempty"`
		}
		if !decode(w, r, &body) {
			return
		}
		entries, err := mem.Recall(r.PathValue("bank"), body.Query, hindsightRecallPool)
		if err != nil {
			writeErr(w, err)
			return
		}
		results := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			if !entryHasTags(e, body.Tags, body.TagsMatch) {
				continue
			}
			row := map[string]any{
				"text":         e.Content,
				"id":           e.ID,
				"type":         "observation",
				"mentioned_at": time.Unix(e.Timestamp, 0).UTC().Format(time.RFC3339),
			}
			if tags := entryTags(e); len(tags) > 0 {
				row["tags"] = tags
			}
			results = append(results, row)
			if len(results) == defaultRecallLimit {
				break
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	})

	mux.HandleFunc("POST /v1/default/banks/{bank}/reflect", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if !decode(w, r, &body) {
			return
		}
		entries, err := mem.Recall(r.PathValue("bank"), body.Query, 5)
		if err != nil {
			writeErr(w, err)
			return
		}
		text := "No memories in this bank yet."
		if len(entries) > 0 {
			lines := make([]string, 0, len(entries))
			for _, e := range entries {
				lines = append(lines, "- "+e.Content)
			}
			text = strings.Join(lines, "\n")
		}
		writeJSON(w, http.StatusOK, map[string]any{"text": text})
	})
}

const defaultRecallLimit = 8

// entryTags reads the client tags stored in entry metadata (JSONL decodes
// string slices as []any, so both forms are accepted).
func entryTags(e memory.Entry) []string {
	switch v := e.Metadata["tags"].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// entryHasTags applies the hindsight tag filter: no requested tags admits
// everything; "all"/"all_strict" demand every requested tag, any other mode
// ("any", "any_strict", unset) admits an intersection.
func entryHasTags(e memory.Entry, want []string, mode string) bool {
	if len(want) == 0 {
		return true
	}
	have := entryTags(e)
	all := strings.HasPrefix(mode, "all")
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if all && !found {
			return false
		}
		if !all && found {
			return true
		}
	}
	return all
}
