// hindsight-compat mount (#414): an opt-in alias surface that speaks the
// exact HTTP wire omp's `memory.backend:"hindsight"` client speaks, so the
// harness can use LeanKG as its native agent memory with ZERO omp changes.
//
//	PUT  /v1/default/banks/{bank}                 bank ensure (always ok)
//	POST /v1/default/banks/{bank}/memories        retain  {items:[{content,...}], async?}
//	GET  /v1/default/banks/{bank}/memories        list    ?offset=&limit=
//	GET  /v1/default/banks/{bank}/memories/{id}   read one row by id (or document_id)
//	POST /v1/default/banks/{bank}/memories/recall recall  {query, tags?, tags_match?, budget?, max_tokens?}
//	POST /v1/default/banks/{bank}/reflect         digest of top recall rows {text}
//	GET  /v1/default/banks/{bank}/stats           store report (K4)
//	DELETE /v1/default/banks/{bank}/documents/{document_id}  drop a document's rows
//
// Wire deltas this bridge absorbs (vs the native FR-ZCP-07 surface):
// path prefix `/v1/default/banks` (not `/api/v1/memory/banks`), recall at
// `.../memories/recall` (not `.../recall`), retain body `items[]` with
// snake_case fields (not top-level `entries[]`), and the recall response
// `results[].text` (not `memories[].content`). Client tags ride in entry
// metadata and filter recall with Hindsight's tags_match semantics: `any`
// (default) and `all` also admit untagged (global) rows, the `_strict`
// variants exclude them, and `exact` demands set equality (an empty `exact`
// scope selects only untagged rows). The filter runs before ranking and the
// page limit, as Hindsight's database-level filter does. A retain item with
// a document_id upserts that document (`update_mode` "replace", the default,
// drops the document's earlier rows; "append" keeps them), and the wiring
// disables mental models client-side.
package rest

import (
	"net/http"
	"slices"
	"strconv"
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
		var replaceDocs []string
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
			if it.DocumentID != "" && it.UpdateMode != "append" {
				replaceDocs = append(replaceDocs, it.DocumentID)
			}
		}
		if err := mem.RetainReplacing(r.PathValue("bank"), entries, replaceDocs); err != nil {
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
		entries, err := mem.RecallFiltered([]string{r.PathValue("bank")}, body.Query, defaultRecallLimit,
			func(e memory.Entry) bool { return entryHasTags(e, body.Tags, body.TagsMatch) })
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": hindsightRows(entries)})
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
	// K4: the client's /memory stats calls this and printed "server
	// unreachable" for a healthy server while it 404'd.
	mux.HandleFunc("GET /v1/default/banks/{bank}/stats", func(w http.ResponseWriter, r *http.Request) {
		stats, err := mem.Stats(r.PathValue("bank"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, stats)
	})

	mux.HandleFunc("DELETE /v1/default/banks/{bank}/documents/{document_id}", func(w http.ResponseWriter, r *http.Request) {
		n, err := mem.DeleteDocument(r.PathValue("bank"), r.PathValue("document_id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "document_id": r.PathValue("document_id"), "memory_units_deleted": n,
		})
	})

	// K3: read parity with the native mount's row access — by-id for the
	// client's read-before-edit seam, and a list for indexing/export. Both
	// are reads, so neither is in auth's write set.
	mux.HandleFunc("GET /v1/default/banks/{bank}/memories", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		offset, _ := strconv.Atoi(q.Get("offset"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		entries, total, err := mem.List(r.PathValue("bank"), offset, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"bank_id": r.PathValue("bank"), "object": "list", "total": total,
			"results": hindsightRows(entries),
		})
	})
	mux.HandleFunc("GET /v1/default/banks/{bank}/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		e, found, err := mem.ByID(r.PathValue("bank"), r.PathValue("id"))
		if err != nil {
			writeErr(w, err)
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "no memory with id " + r.PathValue("id") + " in bank " + r.PathValue("bank"),
			})
			return
		}
		writeJSON(w, http.StatusOK, hindsightRows([]memory.Entry{e})[0])
	})
}

// hindsightRows renders entries in the client's recall/list row shape, so a
// read-by-id returns byte-identically to the same row inside a recall.
func hindsightRows(entries []memory.Entry) []map[string]any {
	rows := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		row := map[string]any{
			"text":         e.Content,
			"id":           e.ID,
			"type":         "observation",
			"mentioned_at": time.Unix(e.Timestamp, 0).UTC().Format(time.RFC3339),
		}
		if tags := entryTags(e); len(tags) > 0 {
			row["tags"] = tags
		}
		if doc, _ := e.Metadata["document_id"].(string); doc != "" {
			row["document_id"] = doc
		}
		rows = append(rows, row)
	}
	return rows
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

// entryHasTags applies Hindsight's tags_match filter (engine/search/tags.py
// filter_results_by_tags). Untagged rows are global: "any" (and unset) and
// "all" admit them, "any_strict"/"all_strict"/"exact" do not. "all*" demand
// every requested tag, "any*" at least one, "exact" the same tag set. No
// requested tags means no filter, except "exact", where it selects exactly
// the untagged rows.
func entryHasTags(e memory.Entry, want []string, mode string) bool {
	have := entryTags(e)
	if mode == "exact" {
		if len(have) != len(want) {
			return false
		}
		return containsAll(have, want)
	}
	if len(want) == 0 {
		return true
	}
	if len(have) == 0 {
		return mode != "any_strict" && mode != "all_strict"
	}
	if strings.HasPrefix(mode, "all") {
		return containsAll(have, want)
	}
	for _, w := range want {
		if slices.Contains(have, w) {
			return true
		}
	}
	return false
}

// containsAll reports whether every tag in want appears in have.
func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}
