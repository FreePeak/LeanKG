// Package web additionally serves the legacy dashboard API (the 11 JSON
// endpoints ui-v2/src/services/backend-client.ts calls) over the Go engine.
// Ported from the deleted Rust web layer (src/web/handlers.rs,
// src/web/query_graph_api.rs, src/web/file_resolve.rs) and
// src/graph/{nl_query,clustering}.rs; the ApiEnvelope shape matches
// ui-v2/src/lib/normalize.ts:
//
//	{"success": bool, "data": <T>|null, "error": string|null}
package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/core"
	"github.com/FreePeak/LeanKG/go/internal/memory"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// apiEnvelope is the exact JSON shape ui-v2 unwrapEnvelope expects.
type apiEnvelope struct {
	Success bool    `json:"success"`
	Data    any     `json:"data"`
	Error   *string `json:"error"`
}

func okEnvelope(data any) apiEnvelope { return apiEnvelope{Success: true, Data: data} }

func failEnvelope(msg string) apiEnvelope { return apiEnvelope{Error: &msg} }

func writeEnvelope(w http.ResponseWriter, env apiEnvelope) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

// APIHandler serves the dashboard API the embedded ui-v2 build calls. It is
// mounted alongside Handler() on the -ui address:
//
//	mux := http.NewServeMux()
//	mux.Handle("/api/", web.APIHandler(engine, mem))
//	mux.Handle("/", web.Handler())
//
// The Go engine is single-project: the project directory is derived from the
// engine's store path (or the memory root) and never switched at runtime.
func APIHandler(engine *core.Engine, mem *memory.Memory) http.Handler {
	h := &apiH{engine: engine, mem: mem, projectDir: deriveProjectDir(engine, mem)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/index/status", h.indexStatus)
	mux.HandleFunc("POST /api/query", h.query)
	mux.HandleFunc("POST /api/query-graph", h.queryGraph)
	mux.HandleFunc("GET /api/search", h.search)
	mux.HandleFunc("GET /api/file", h.getFile)
	mux.HandleFunc("GET /api/graph/clusters", h.graphClusters)
	mux.HandleFunc("GET /api/graph/report", h.graphReport)
	mux.HandleFunc("GET /api/graph/children", h.graphChildren)
	mux.HandleFunc("GET /api/graph/expand-service", h.graphExpandService)
	mux.HandleFunc("GET /api/graph/service-topology", h.serviceTopology)
	mux.HandleFunc("POST /api/project/switch", h.projectSwitch)
	// Unknown /api/* route: a JSON 404, never the SPA shell (the dashboard's
	// fetch handlers read body.error for non-2xx responses).
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelopeStatus(w, http.StatusNotFound, failEnvelope("not found: "+r.URL.Path))
	})
	return mux
}

// apiH carries the engine plus the single project root the dashboard serves.
type apiH struct {
	engine     *core.Engine
	mem        *memory.Memory
	projectDir string
}

// deriveProjectDir recovers the project root from the engine wiring. The
// SQLite store lives at <project>/.leankg/leankg.db and the memory root at
// <project>/.leankg/memory. Empty when neither applies (e.g. PG backend);
// disk-dependent endpoints then degrade gracefully.
func deriveProjectDir(engine *core.Engine, mem *memory.Memory) string {
	if mem != nil {
		if dir := projectDirFromSuffix(mem.Root(), "/.leankg/memory"); dir != "" {
			return dir
		}
	}
	if engine != nil {
		if dir := projectDirFromSuffix(engine.Store().Path(), "/.leankg/leankg.db"); dir != "" {
			return dir
		}
	}
	return ""
}

func projectDirFromSuffix(path, suffix string) string {
	if path == "" || !strings.HasSuffix(path, suffix) {
		return ""
	}
	dir := strings.TrimSuffix(path, suffix)
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return ""
	}
	return dir
}

// --- shared response types (serde field names from the Rust handlers) ---

type nodeProperties struct {
	Name        string `json:"name"`
	FilePath    string `json:"filePath"`
	ElementType string `json:"elementType"`
}

type graphNode struct {
	ID         string         `json:"id"`
	Label      string         `json:"label"`
	Properties nodeProperties `json:"properties"`
}

type graphRelationship struct {
	ID              string `json:"id"`
	SourceID        string `json:"sourceId"`
	TargetID        string `json:"targetId"`
	RelType         string `json:"type"`
	ConfidenceLabel string `json:"confidenceLabel"`
}

func newGraphRelationship(sourceID, targetID, relType, confidenceLabel string) graphRelationship {
	relType = strings.ToUpper(relType)
	return graphRelationship{
		ID:              sourceID + "_" + relType + "_" + targetID,
		SourceID:        sourceID,
		TargetID:        targetID,
		RelType:         relType,
		ConfidenceLabel: confidenceLabel,
	}
}

func relFromStore(r store.Relationship) graphRelationship {
	return newGraphRelationship(r.Source, r.Target, r.RelType, confidenceLabelFor(r))
}

// graphFilterInfo mirrors the Rust GraphFilterInfo.
type graphFilterInfo struct {
	TestsFiltered int    `json:"tests_filtered"`
	Message       string `json:"message"`
}

// graphData mirrors the Rust GraphData (has_more serialized camelCase).
type graphData struct {
	Nodes         []graphNode         `json:"nodes"`
	Relationships []graphRelationship `json:"relationships"`
	Filtered      *graphFilterInfo    `json:"filtered"`
	HasMore       bool                `json:"hasMore"`
}

// confidenceLabelFor ports graph/provenance.rs confidence_label_for:
// resolution_method overrides, then the confidence threshold ladder.
func confidenceLabelFor(r store.Relationship) string {
	method, _ := r.Metadata["resolution_method"].(string)
	switch {
	case method == "typed":
		return "EXTRACTED"
	case method == "name" && r.Confidence >= 0.8:
		return "EXTRACTED"
	case method == "name_file_hint" && r.Confidence >= 0.6:
		return "INFERRED"
	case method == "name":
		return "INFERRED"
	case method == "unresolved":
		return "AMBIGUOUS"
	case r.Confidence >= 0.8:
		return "EXTRACTED"
	case r.Confidence >= 0.5:
		return "INFERRED"
	default:
		return "AMBIGUOUS"
	}
}

// allRelationships loads the full relationship table. ponytail: the store
// Backend caps RelationshipsAll at 1000 rows when limit<=0; the dashboard
// endpoints need the whole edge set, so this passes a hard 1M cap — the
// upgrade path is a store-side streaming iterator.
func allRelationships(st store.Backend) ([]store.Relationship, error) {
	return st.RelationshipsAll(1 << 20)
}

// --- store snapshot: every endpoint works off one ordered element read ---

type elementSet map[string]store.Element

type snapshot struct {
	elements []store.Element
	byQN     elementSet
}

func loadSnapshot(st store.Backend) (*snapshot, error) {
	els, err := st.Elements()
	if err != nil {
		return nil, err
	}
	byQN := make(elementSet, len(els))
	for _, e := range els {
		byQN[e.QualifiedName] = e
	}
	return &snapshot{elements: els, byQN: byQN}, nil
}

func (s *snapshot) count() int { return len(s.elements) }

// isMegaGraph ports the Rust mega probe: more elements than the cache
// threshold (LEANKG_MAX_CACHE_ELEMENTS, default 50_000).
func (s *snapshot) isMegaGraph() bool { return s.count() > maxCacheElements() }

func maxCacheElements() int {
	if v := os.Getenv("LEANKG_MAX_CACHE_ELEMENTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50_000
}

// --- handlers ---

func (h *apiH) indexStatus(w http.ResponseWriter, _ *http.Request) {
	st := h.engine.Store()
	els, err := st.ElementCount()
	var elsPtr *int
	if err == nil {
		elsPtr = &els
	}
	rels, err := st.RelationshipCount()
	var relsPtr *int
	if err == nil {
		relsPtr = &rels
	}
	pp := h.projectDir
	data := map[string]any{
		// The Go serve process does not run background reindexing; the
		"is_indexing":        false,
		"progress_percent":   0,
		"current_file":       "",
		"total_files":        0,
		"indexed_files":      0,
		"element_count":      elsPtr,
		"relationship_count": relsPtr,
		"project_path":       &pp,
	}
	writeEnvelope(w, okEnvelope(data))
}

func (h *apiH) query(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Query  string         `json:"query"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeEnvelope(w, failEnvelope("invalid JSON body: "+err.Error()))
		return
	}
	qr := core.QueryRequest{Query: req.Query}
	for k, v := range req.Params {
		if k == "action" {
			if s, ok := v.(string); ok {
				qr.Action = s
			}
			continue
		}
		if qr.Args == nil {
			qr.Args = map[string]any{}
		}
		qr.Args[k] = v
	}
	resp, err := h.engine.Query(r.Context(), qr)
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	writeEnvelope(w, okEnvelope(map[string]any{"result": resp}))
}

func (h *apiH) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(r.URL.Query().Get("q"))
	elementType := r.URL.Query().Get("element_type")
	filePath := r.URL.Query().Get("file_path")

	snap, err := loadSnapshot(h.engine.Store())
	if err != nil {
		writeEnvelope(w, failEnvelope(err.Error()))
		return
	}
	out := make([]store.Element, 0, len(snap.elements))
	for _, e := range snap.elements {
		if q != "" &&
			!strings.Contains(strings.ToLower(e.QualifiedName), q) &&
			!strings.Contains(strings.ToLower(e.Name), q) &&
			!strings.Contains(strings.ToLower(e.FilePath), q) {
			continue
		}
		if elementType != "" && e.ElementType != elementType {
			continue
		}
		if filePath != "" && !strings.Contains(e.FilePath, filePath) {
			continue
		}
		out = append(out, e)
	}
	writeEnvelope(w, okEnvelope(out))
}

// --- /api/project/switch ---

func (h *apiH) projectSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string `json:"path"`
		GithubURL string `json:"github_url"`
		Reindex   bool   `json:"reindex"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeEnvelope(w, failEnvelope("invalid JSON body: "+err.Error()))
		return
	}
	if req.GithubURL != "" {
		// The Rust engine cloned + indexed the repo; the single-project Go
		// engine cannot switch, so surface that explicitly.
		writeEnvelope(w, failEnvelope("project switching is not supported by this engine (single-project serve)"))
		return
	}
	if req.Path == "" {
		writeEnvelope(w, failEnvelope("Either path or github_url must be provided"))
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "/" || path == "" {
		writeEnvelope(w, failEnvelope("Invalid project path: filesystem root is not a LeanKG project"))
		return
	}
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		writeEnvelope(w, failEnvelope("Directory not found. Please check the path and try again."))
		return
	}
	// Single-project engine: the dashboard keeps the served project; switching
	// to a different root is unsupported (the Rust engine swapped its DB here).
	if h.projectDir != "" && !sameDir(abs, h.projectDir) {
		writeEnvelope(w, failEnvelope(fmt.Sprintf(
			"project switching is not supported by this engine; it serves %s", h.projectDir)))
		return
	}
	els, err := h.engine.Store().ElementCount()
	var count *int
	if err == nil {
		count = &els
	}
	_, statErr := os.Stat(filepath.Join(abs, ".leankg"))
	data := map[string]any{
		"is_directory":   true,
		"has_database":   statErr == nil,
		"needs_indexing": false,
		"is_github":      false,
		"project_path":   abs,
		"element_count":  count,
	}
	writeEnvelope(w, okEnvelope(data))
}

func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
