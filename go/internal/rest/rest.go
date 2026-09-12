// Package rest exposes the engine over plain stdlib net/http (Go ≥1.22
// routing patterns; no framework — rewrite doc §6.7). Endpoints:
//
//	GET  /health                 liveness
//	GET  /api/v1/status          status tool payload
//	POST /api/v1/query           query tool payload (ladder + rungs + memory)
//	POST /api/v1/import          import tool payload (index / memory writes)
//	POST /api/v1/memory/banks/{bank}/memories   hindsight-shaped retain
//	POST /api/v1/memory/banks/{bank}/recall     hindsight-shaped recall
//
// The memory HTTP surface is the FR-ZCP-07 remainder contract (upstream
// memory.backend:"mcp" evidence). Bodies: retain = TOP-LEVEL
// {entries:[{content, id?, metadata?}], through_user_turn} — entries carry
// "content" (NOT "text") — and recall = {query, limit}.
package rest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/errs"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/orgknowledge"
)

// Handler builds the REST mux over an engine.
func Handler(engine *core.Engine, mem *memory.Memory) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		out, err := engine.Status(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/v1/query", func(w http.ResponseWriter, r *http.Request) {
		var req core.QueryRequest
		if !decode(w, r, &req) {
			return
		}
		out, err := engine.Query(r.Context(), req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/v1/import", func(w http.ResponseWriter, r *http.Request) {
		var req core.ImportRequest
		if !decode(w, r, &req) {
			return
		}
		out, err := engine.Import(r.Context(), req)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/v1/session/read", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Command   string `json:"command"` // recall | canvas
			SessionID string `json:"session_id"`
			NodeID    string `json:"node_id"`
		}
		if !decode(w, r, &body) {
			return
		}
		out, err := engine.SessionRead(body.Command, body.SessionID, body.NodeID)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/v1/ontology/match", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Catalog string `json:"catalog"` // path to the concept catalog JSON
		}
		if !decode(w, r, &body) {
			return
		}
		out, err := engine.OntologyMatch(body.Catalog)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/v1/ontology/matches", func(w http.ResponseWriter, r *http.Request) {
		out, err := engine.OntologyMatches()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	// FR-ZCP org-knowledge reads (Rust /api/v2 routes; the Go envelope is the
	// existing writeJSON payload shape). Param defaults follow the Rust
	// handlers: env=production, limit=10.
	org := orgknowledge.New(engine.Store())
	mux.HandleFunc("GET /api/v2/incidents", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		env := q.Get("env")
		if env == "" {
			env = "production"
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		if limit == 0 {
			limit = 10
		}
		incidents, err := org.QueryIncidents(q.Get("service"), q.Get("pattern"), env, limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
	})
	mux.HandleFunc("GET /api/v2/env/diff", func(w http.ResponseWriter, r *http.Request) {
		conflicts, err := org.FindEnvConflicts(r.URL.Query().Get("service"))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
	})
	mux.HandleFunc("GET /api/v2/service/context", func(w http.ResponseWriter, r *http.Request) {
		env := r.URL.Query().Get("env")
		if env == "" {
			env = "production"
		}
		ctx, err := org.ServiceContext(r.URL.Query().Get("service"), env)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ctx)
	})
	if mem != nil {
		mux.HandleFunc("POST /api/v1/memory/banks/{bank}/memories", func(w http.ResponseWriter, r *http.Request) {
			bank := r.PathValue("bank")
			// Hindsight-shaped retain body: TOP-LEVEL through_user_turn
			// cursor; entries carry {"content", optional "id", "metadata"}
			// (NOT "text" — pinned here and in tests so harness authors
			// don't hit the same mismatch twice).
			var body struct {
				Entries         []memory.Entry `json:"entries"`
				ThroughUserTurn int            `json:"through_user_turn"`
			}
			if !decode(w, r, &body) {
				return
			}
			// Trust boundary: an empty-content entry is permanently
			// unrecallable (zero-match filtering) — reject rather than
			// silently swallow it.
			for i, e := range body.Entries {
				if strings.TrimSpace(e.Content) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{
						"error": fmt.Sprintf("entries[%d].content is empty — empty entries would be unrecallable", i),
					})
					return
				}
			}
			if err := mem.Retain(bank, body.Entries, body.ThroughUserTurn); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "retained": len(body.Entries)})
		})
		mux.HandleFunc("POST /api/v1/memory/banks/{bank}/recall", func(w http.ResponseWriter, r *http.Request) {
			bank := r.PathValue("bank")
			var body struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if !decode(w, r, &body) {
				return
			}
			entries, err := mem.Recall(bank, body.Query, body.Limit)
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
		})
	}
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr renders a failed REST call. An error carrying a catalog code
// (internal/errs) also exposes it as `code`, so clients branch on the stable
// token instead of matching prose (FR-ZCP-12).
func writeErr(w http.ResponseWriter, err error) {
	body := map[string]any{"error": err.Error()}
	var coded *errs.Error
	if errors.As(err, &coded) {
		body["code"] = coded.Code()
	}
	writeJSON(w, http.StatusBadRequest, body)
}

func decode[T any](w http.ResponseWriter, r *http.Request, into *T) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body: " + err.Error()})
		return false
	}
	return true
}
