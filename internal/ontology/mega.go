package ontology

import "strconv"

// FR-HEA-03 (issue #276): mega-graph full-scan visibility.
//
// RefuseFullScanIfMega (discover.go) ports the Rust refusal payload but
// the Go query dispatcher never called it — on a 50k+ element graph the
// full-scan actions below still materialize the whole element table, and
// nothing told the caller the cap exists. These helpers give the core
// wiring (report-only hunks to Main) its two call sites:
//
//  1. the status tool carries the banner + guarded-action list so on-call
//     agents learn the cap before scripting against the full graph;
//  2. each guarded action refuses via RefuseFullScanIfMega before its
//     full-table scan runs.

// GuardedActions lists the query-surface actions that full-scan the
// element table on the Go engine (each reaches store.Backend.Elements()
// through ontologyElements). Match the names emitted in the refusal
// payloads ("ontology/<cmd>").
func GuardedActions() []string {
	return []string{
		"ontology/trace",
		"ontology/status",
		"ontology/feature_flow",
		"ontology/traceability",
	}
}

// IsFullScanOntologyCmd reports whether an ontology cmd argument triggers
// a full element-table scan and must therefore pass the mega-graph guard.
// concept_search and matches stay ungarded: matches is a KV read and
// concept_search is the recommended paginated escape hatch in the refusal
// hint (parity with the Rust guidance that steers callers toward it).
func IsFullScanOntologyCmd(cmd string) bool {
	switch cmd {
	case "trace", "status", "feature_flow", "traceability":
		return true
	}
	return false
}

// MegaGraphBanner returns the FR-HEA-03 status-tool banner fields for a
// mega-graph (elementCount above MegaGraphThreshold): an operator-facing
// warning, the cap, and the guarded-action list. Returns an empty map when
// the graph is not mega, so callers can merge unconditionally.
func MegaGraphBanner(elementCount int) map[string]any {
	max := MegaGraphThreshold()
	if elementCount <= max {
		return map[string]any{}
	}
	return map[string]any{
		"mega_graph": true,
		"banner": "MEGA-GRAPH: " + strconv.Itoa(elementCount) + " elements exceeds the " +
			strconv.Itoa(max) + " full-scan cap (LEANKG_MAX_CACHE_ELEMENTS). Guarded actions " +
			"refuse; use concept_search, semantic search, or paginated queries.",
		"max_full_scan":     max,
		"guarded_tools":     GuardedActions(),
		"element_count":     elementCount,
		"recommended_tools": []string{"ontology/concept_search", "semantic", "exact"},
	}
}
