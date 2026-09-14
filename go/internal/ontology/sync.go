package ontology

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Idempotent ontology YAML → store sync (parity with sync.rs): load
// concepts.yaml + workflows.yaml from an ontology directory and replace
// the stored ontology layer (YAML is the source of truth).

// syncMu serializes ontology sync across watcher / control / post-index
// hook callers within one process (parity with ONTOLOGY_SYNC_LOCK).
var syncMu sync.Mutex

// SyncStats is the result of syncing ontology YAML into the store
// (parity with OntologySyncStats).
type SyncStats struct {
	Concepts      int    `json:"concepts"`
	Workflows     int    `json:"workflows"`
	WorkflowSteps int    `json:"workflow_steps"`
	FailureModes  int    `json:"failure_modes"`
	Relationships int    `json:"relationships"`
	OntologyDir   string `json:"ontology_dir"`
	MarkerPath    string `json:"marker_path,omitempty"`
	SyncedAtUnix  int64  `json:"synced_at_unix"`
}

// kvNamespace is the store KV namespace the ontology layer persists under.
const kvNamespace = "ontology"

// kvWorkflowKey / kvConceptKey store the loaded node sets (JSON) so the
// query layer can read workflows back without re-parsing YAML
// (round-trip: load -> persist -> trace).
const (
	kvWorkflowKey = "workflows"
	kvConceptKey  = "concepts"
)

// ResolveOntologyDir resolves the ontology source directory for a project:
// LEANKG_ONTOLOGY_DIR env (when a directory), then <project>/ontology.
// Returns "" when neither exists (parity with resolve_ontology_dir).
func ResolveOntologyDir(projectRoot string) string {
	if dir := os.Getenv("LEANKG_ONTOLOGY_DIR"); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	candidate := filepath.Join(projectRoot, "ontology")
	if info, err := os.Stat(candidate); err == nil && info.IsDir() {
		return candidate
	}
	return ""
}

// SyncedMarkerPath is the boot/freshness marker under .leankg.
func SyncedMarkerPath(leankgDir string) string {
	return filepath.Join(leankgDir, "ontology_synced")
}

// TouchSyncedMarker creates/truncates .leankg/ontology_synced so its mtime
// advances on every successful sync (parity with
// touch_ontology_synced_marker).
func TouchSyncedMarker(leankgDir string) (string, error) {
	marker := SyncedMarkerPath(leankgDir)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		return "", err
	}
	return marker, nil
}

func unixNow() int64 { return time.Now().Unix() }

// LoadWorkflows loads concepts.yaml + workflows.yaml from dir and persists
// them into the store (declarative replace, no .leankg marker): the entry
// point the core layer calls for import{action:"ontology"}.
func LoadWorkflows(st store.Backend, dir string) (SyncStats, error) {
	return SyncFromDir(dir, st, "")
}

// SyncFromDir loads concepts.yaml + workflows.yaml from ontologyDir and
// replaces the ontology layer in the store (YAML is source of truth,
// parity with sync_from_dir):
//   - clears every stored ontology:// element and its outgoing
//     relationships,
//   - re-inserts concept + workflow + step + failure-mode elements,
//   - re-inserts has_step / next_step / has_failure_mode relationships,
//   - persists the node sets in the "ontology" KV namespace,
//   - touches .leankg/ontology_synced when leankgDir is non-empty.
func SyncFromDir(ontologyDir string, st store.Backend, leankgDir string) (SyncStats, error) {
	syncMu.Lock()
	defer syncMu.Unlock()

	info, err := os.Stat(ontologyDir)
	if err != nil || !info.IsDir() {
		return SyncStats{}, fmt.Errorf("ontology directory does not exist: %s", ontologyDir)
	}

	stats := SyncStats{
		OntologyDir:  ontologyDir,
		SyncedAtUnix: unixNow(),
	}

	// Declarative replace: wipe the prior ontology layer so renames and
	// removals apply (parity with clear_ontology_layer before re-insert).
	if err := clearOntologyLayer(st); err != nil {
		return stats, fmt.Errorf("clear ontology layer: %w", err)
	}

	var elements []store.Element
	var relationships []store.Relationship
	var conceptNodes []ConceptNode
	var workflowSet *WorkflowSet

	conceptsFile := filepath.Join(ontologyDir, "concepts.yaml")
	if _, err := os.Stat(conceptsFile); err == nil {
		nodes, err := LoadConceptsYAML(conceptsFile)
		if err != nil {
			return stats, fmt.Errorf("load concepts.yaml: %w", err)
		}
		conceptNodes = nodes
		elements = append(elements, ConceptNodesToElements(nodes)...)
		stats.Concepts = len(nodes)
	}

	workflowsFile := filepath.Join(ontologyDir, "workflows.yaml")
	if _, err := os.Stat(workflowsFile); err == nil {
		set, err := LoadWorkflowsYAML(workflowsFile)
		if err != nil {
			return stats, fmt.Errorf("load workflows.yaml: %w", err)
		}
		workflowSet = set
		elements = append(elements, WorkflowNodesToElements(set.Workflows)...)
		elements = append(elements, WorkflowStepNodesToElements(set.Steps)...)
		elements = append(elements, FailureModeNodesToElements(set.FailureModes)...)
		relationships = set.Relationships
		stats.Workflows = len(set.Workflows)
		stats.WorkflowSteps = len(set.Steps)
		stats.FailureModes = len(set.FailureModes)
		stats.Relationships = len(set.Relationships)
	}

	if len(elements) > 0 {
		if err := st.UpsertElements(elements); err != nil {
			return stats, fmt.Errorf("insert ontology elements: %w", err)
		}
	}
	if len(relationships) > 0 {
		if err := st.UpsertRelationships(relationships); err != nil {
			return stats, fmt.Errorf("insert ontology relationships: %w", err)
		}
	}

	// Persist the node sets in KV so query-side reads survive element
	// table churn.
	if conceptNodes != nil {
		if b, err := json.Marshal(conceptNodes); err == nil {
			_ = st.KVSet(kvNamespace, kvConceptKey, string(b))
		}
	}
	if workflowSet != nil {
		if b, err := json.Marshal(workflowSet); err == nil {
			_ = st.KVSet(kvNamespace, kvWorkflowKey, string(b))
		}
	}

	if leankgDir != "" {
		if marker, err := TouchSyncedMarker(leankgDir); err == nil {
			stats.MarkerPath = marker
		}
	}

	stats.SyncedAtUnix = unixNow()
	return stats, nil
}

// SyncForProject syncs ontology for a project root: resolves the ontology
// directory (LEANKG_ONTOLOGY_DIR or <root>/ontology) and the .leankg
// marker location (parity with sync_for_project).
func SyncForProject(projectRoot string, st store.Backend) (SyncStats, error) {
	dir := ResolveOntologyDir(projectRoot)
	if dir == "" {
		return SyncStats{}, fmt.Errorf(
			"no ontology directory found under %s (set LEANKG_ONTOLOGY_DIR)", projectRoot)
	}
	return SyncFromDir(dir, st, filepath.Join(projectRoot, ".leankg"))
}

// clearOntologyLayer removes every ontology element (file_path
// ontology://...); DeleteByFile cascades to the relationships sourced at
// them (parity with clear_ontology_layer).
func clearOntologyLayer(st store.Backend) error {
	els, err := st.Elements()
	if err != nil {
		return err
	}
	for _, el := range els {
		if IsOntologyFilePath(el.FilePath) {
			if err := st.DeleteByFile(el.FilePath); err != nil {
				return err
			}
		}
	}
	return nil
}

// SyncStatus is the boot/freshness status payload (parity with
// ontology_sync_status).
func SyncStatus(projectRoot string) map[string]any {
	dir := ResolveOntologyDir(projectRoot)
	leankg := filepath.Join(projectRoot, ".leankg")
	marker := SyncedMarkerPath(leankg)

	var concepts, workflows string
	var conceptsMtime, workflowsMtime, markerMtime *int64
	if dir != "" {
		if p := filepath.Join(dir, "concepts.yaml"); fileExists(p) {
			concepts = p
			conceptsMtime = fileMtime(p)
		}
		if p := filepath.Join(dir, "workflows.yaml"); fileExists(p) {
			workflows = p
			workflowsMtime = fileMtime(p)
		}
	}
	if fileExists(marker) {
		markerMtime = fileMtime(marker)
	}

	out := map[string]any{
		"project_root":         projectRoot,
		"ontology_dir":         nil,
		"concepts_yaml":        nil,
		"workflows_yaml":       nil,
		"concepts_mtime_unix":  conceptsMtime,
		"workflows_mtime_unix": workflowsMtime,
		"marker":               marker,
		"marker_exists":        fileExists(marker),
		"marker_mtime_unix":    markerMtime,
		"watch_debounce_ms":    WatchDebounceMS(),
	}
	if dir != "" {
		out["ontology_dir"] = dir
	}
	if concepts != "" {
		out["concepts_yaml"] = concepts
	}
	if workflows != "" {
		out["workflows_yaml"] = workflows
	}
	return out
}

// WatchDebounceMS is the ontology YAML watcher debounce
// (LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS, default 1500, floor 1000; parity
// with ontology_watch_debounce_ms).
func WatchDebounceMS() int {
	if v := os.Getenv("LEANKG_ONTOLOGY_WATCH_DEBOUNCE_MS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			if n < 1000 {
				return 1000
			}
			return n
		}
	}
	return 1500
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func fileMtime(p string) *int64 {
	info, err := os.Stat(p)
	if err != nil {
		return nil
	}
	unix := info.ModTime().Unix()
	return &unix
}

// LoadWorkflowsFromStore reads the persisted workflow set back from the KV
// namespace (round-trip partner of SyncFromDir). A missing key yields
// (nil, nil).
func LoadWorkflowsFromStore(st store.Backend) (*WorkflowSet, error) {
	s, ok, err := st.KVGet(kvNamespace, kvWorkflowKey)
	if err != nil || !ok {
		return nil, err
	}
	var set WorkflowSet
	if err := json.Unmarshal([]byte(s), &set); err != nil {
		return nil, fmt.Errorf("parse stored workflows: %w", err)
	}
	return &set, nil
}

// LoadConceptsFromStore reads the persisted concept node set back from the
// KV namespace. A missing key yields (nil, nil).
func LoadConceptsFromStore(st store.Backend) ([]ConceptNode, error) {
	s, ok, err := st.KVGet(kvNamespace, kvConceptKey)
	if err != nil || !ok {
		return nil, err
	}
	var nodes []ConceptNode
	if err := json.Unmarshal([]byte(s), &nodes); err != nil {
		return nil, fmt.Errorf("parse stored concepts: %w", err)
	}
	return nodes, nil
}

// WorkflowGIDs lists the stored workflow GIDs (ordered by qualified name).
func WorkflowGIDs(st store.Backend) ([]string, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}
	gids := make([]string, 0)
	for _, el := range els {
		if el.ElementType == TypeWorkflow {
			gids = append(gids, el.QualifiedName)
		}
	}
	return gids, nil
}

// WorkflowIDFromGID extracts the bare workflow id from a GID string; ok is
// false when the string is not a 5-part GID.
func WorkflowIDFromGID(gid string) (string, bool) {
	g, ok := ParseGid(gid)
	if !ok {
		return "", false
	}
	return g.ID, true
}

// ResolveWorkflowID resolves a bare workflow id to its stored GID under
// any env/scope (the get_feature_flow lookup pattern).
func ResolveWorkflowID(st store.Backend, workflowID string) (string, bool, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return "", false, err
	}
	for _, el := range els {
		if el.ElementType != TypeWorkflow {
			continue
		}
		if g, ok := ParseGid(el.QualifiedName); ok && g.ID == workflowID {
			return el.QualifiedName, true, nil
		}
	}
	return "", false, nil
}

// TraceWorkflow is the core-layer entry: trace by workflow id, name,
// alias or GID (parity with trace_workflow / kg_trace_workflow).
func TraceWorkflow(st store.Backend, workflowQuery string) ([]WorkflowStepNode, error) {
	return Trace(st, workflowQuery)
}

// FeatureWorkflow is one workflow implementing a feature requirement,
// with its ordered steps.
type FeatureWorkflow struct {
	Workflow WorkflowNode       `json:"workflow"`
	Steps    []WorkflowStepNode `json:"steps"`
}

// TraceFeature traces every workflow linked to a feature requirement id:
// every stored workflow_step whose metadata.feature_ids contains featureID
// links its parent workflow. Returns the workflows (with ordered steps)
// that implement the feature — the get_feature_flow chain minus the
// Rust-era knowledge_entries lookup, which lives in the core layer.
func TraceFeature(st store.Backend, featureID string) ([]FeatureWorkflow, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}

	workflowByGID := map[string]WorkflowNode{}
	for _, el := range els {
		if el.ElementType == TypeWorkflow {
			if w, err := workflowFromElement(el); err == nil {
				workflowByGID[el.QualifiedName] = w
			}
		}
	}

	linkedWorkflows := map[string]bool{}
	stepElementsByWorkflow := map[string][]store.Element{}
	for _, el := range els {
		if el.ElementType != TypeWorkflowStep {
			continue
		}
		wg := el.ParentQualified
		if wg == "" {
			if v, ok := el.Metadata["workflow_gid"].(string); ok {
				wg = v
			}
		}
		if wg == "" {
			continue
		}
		stepElementsByWorkflow[wg] = append(stepElementsByWorkflow[wg], el)
		for _, fid := range jsonStrArray(el.Metadata, "feature_ids") {
			if fid == featureID {
				linkedWorkflows[wg] = true
			}
		}
	}

	out := make([]FeatureWorkflow, 0, len(linkedWorkflows))
	for wg := range linkedWorkflows {
		node, ok := workflowByGID[wg]
		if !ok {
			continue
		}
		fw := FeatureWorkflow{Workflow: node}
		for _, el := range stepElementsByWorkflow[wg] {
			if step, err := workflowStepFromElement(el); err == nil {
				fw.Steps = append(fw.Steps, step)
			}
		}
		sort.SliceStable(fw.Steps, func(i, j int) bool { return fw.Steps[i].Order < fw.Steps[j].Order })
		out = append(out, fw)
	}
	// Deterministic output order.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Steps) != len(out[j].Steps) {
			return len(out[i].Steps) > len(out[j].Steps)
		}
		return out[i].Workflow.GID < out[j].Workflow.GID
	})
	return out, nil
}

// FeatureFlow assembles the FR -> workflow -> steps chain
// (get_feature_flow parity) for a feature id: every workflow whose steps
// carry the feature id, each with ordered steps including code refs and
// failure modes.
func FeatureFlow(st store.Backend, featureID string) (map[string]any, error) {
	workflows, err := TraceFeature(st, featureID)
	if err != nil {
		return nil, err
	}
	wfJSON := make([]map[string]any, 0, len(workflows))
	for _, w := range workflows {
		stepsJSON := make([]map[string]any, 0, len(w.Steps))
		for _, s := range w.Steps {
			stepsJSON = append(stepsJSON, map[string]any{
				"id":            s.GID,
				"name":          s.Name,
				"order":         s.Order,
				"description":   s.Description,
				"code_refs":     s.Metadata.CodeRefs,
				"failure_modes": s.Metadata.FailureModes,
			})
		}
		wfJSON = append(wfJSON, map[string]any{
			"id":    w.Workflow.GID,
			"name":  w.Workflow.Name,
			"steps": stepsJSON,
		})
	}
	return map[string]any{
		"feature_id": featureID,
		"workflows":  wfJSON,
		"count":      len(wfJSON),
	}, nil
}

// SaveFeatureTrace persists a feature -> workflows trace under the
// ontology KV namespace (feature:<id>) so traceability reads survive
// process restarts.
func SaveFeatureTrace(st store.Backend, featureID string, flow map[string]any) error {
	b, err := json.Marshal(flow)
	if err != nil {
		return err
	}
	return st.KVSet(kvNamespace, "feature:"+featureID, string(b))
}

// LoadFeatureTrace reads a persisted feature trace back; a missing key
// yields (nil, nil).
func LoadFeatureTrace(st store.Backend, featureID string) (map[string]any, error) {
	s, ok, err := st.KVGet(kvNamespace, "feature:"+featureID)
	if err != nil || !ok {
		return nil, err
	}
	var flow map[string]any
	if err := json.Unmarshal([]byte(s), &flow); err != nil {
		return nil, fmt.Errorf("parse stored feature trace: %w", err)
	}
	return flow, nil
}

// TraceabilityMatrixItem is one row of the FR traceability matrix
// (parity with get_traceability_matrix's per-FR row, driven by workflow
// steps' feature_ids instead of the Rust-era knowledge_entries table).
type TraceabilityMatrixItem struct {
	FeatureID             string `json:"feature_id"`
	WorkflowCount         int    `json:"workflow_count"`
	StepCount             int    `json:"step_count"`
	AnnotatedElementCount int    `json:"annotated_element_count"`
	DocCount              int    `json:"doc_count"`
}

// TraceabilityMatrix builds the FR -> workflow/element/doc coverage matrix
// from the stored ontology layer. Annotated elements come from the
// code_refs of feature-linked steps; doc counts from concept docs entries
// tagged with the feature id.
func TraceabilityMatrix(st store.Backend) ([]TraceabilityMatrixItem, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}

	workflowsByFeature := map[string]map[string]bool{}
	stepsByFeature := map[string]int{}
	docsByFeature := map[string]int{}
	annotatedByFeature := map[string]map[string]bool{}

	for _, el := range els {
		switch el.ElementType {
		case TypeWorkflowStep:
			refs := jsonStrArray(el.Metadata, "code_refs")
			for _, fid := range jsonStrArray(el.Metadata, "feature_ids") {
				stepsByFeature[fid]++
				if wg, _ := el.Metadata["workflow_gid"].(string); wg != "" {
					if workflowsByFeature[fid] == nil {
						workflowsByFeature[fid] = map[string]bool{}
					}
					workflowsByFeature[fid][wg] = true
				}
				if annotatedByFeature[fid] == nil {
					annotatedByFeature[fid] = map[string]bool{}
				}
				for _, r := range refs {
					annotatedByFeature[fid][r] = true
				}
			}
		default:
			for _, fid := range jsonStrArray(el.Metadata, "feature_ids") {
				docsByFeature[fid] += len(jsonStrArray(el.Metadata, "docs"))
			}
		}
	}

	ids := make([]string, 0, len(stepsByFeature))
	for fid := range stepsByFeature {
		ids = append(ids, fid)
	}
	sort.Strings(ids)

	out := make([]TraceabilityMatrixItem, 0, len(ids))
	for _, fid := range ids {
		out = append(out, TraceabilityMatrixItem{
			FeatureID:             fid,
			WorkflowCount:         len(workflowsByFeature[fid]),
			StepCount:             stepsByFeature[fid],
			AnnotatedElementCount: len(annotatedByFeature[fid]),
			DocCount:              docsByFeature[fid],
		})
	}
	return out, nil
}

// TraceQuery returns the ordered trace of a workflow as a JSON-friendly
// map (the shape kg_trace_workflow returns over MCP): workflow identity
// plus ordered steps with their code refs and failure modes.
func TraceQuery(st store.Backend, workflowQuery string) (map[string]any, error) {
	steps, err := Trace(st, workflowQuery)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"query":      workflowQuery,
		"steps":      steps,
		"step_count": len(steps),
		"found":      len(steps) > 0,
	}
	if len(steps) > 0 {
		wg := steps[0].WorkflowGid
		out["workflow_gid"] = wg
		if g, ok := ParseGid(wg); ok {
			out["workflow_id"] = g.ID
			out["env"] = g.Env
		}
	}
	return out, nil
}

// TraceByGID traces a workflow by its exact GID (the resolved-workflow
// path get_feature_flow falls back to).
func TraceByGID(st store.Backend, gid string) ([]WorkflowStepNode, error) {
	els, err := ontologyElements(st)
	if err != nil {
		return nil, err
	}
	steps := make([]WorkflowStepNode, 0)
	for _, el := range els {
		if el.ElementType != TypeWorkflowStep || el.ParentQualified != gid {
			continue
		}
		if step, err := workflowStepFromElement(el); err == nil {
			steps = append(steps, step)
		}
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Order < steps[j].Order })
	return steps, nil
}
