package ontology

import "testing"

// TestMegaGraphBanner covers the FR-HEA-03 status-banner shape: quiet on
// normal graphs, full disclosure above the cap.
func TestMegaGraphBanner(t *testing.T) {
	t.Setenv("LEANKG_MAX_CACHE_ELEMENTS", "10")
	if got := MegaGraphBanner(10); len(got) != 0 {
		t.Errorf("banner at threshold = %+v, want empty", got)
	}
	banner := MegaGraphBanner(11)
	if banner["mega_graph"] != true {
		t.Fatalf("mega_graph flag missing: %+v", banner)
	}
	if banner["max_full_scan"] != 10 {
		t.Errorf("max_full_scan = %v, want 10", banner["max_full_scan"])
	}
	guarded, ok := banner["guarded_tools"].([]string)
	if !ok || len(guarded) != len(GuardedActions()) {
		t.Errorf("guarded_tools = %v, want %v", banner["guarded_tools"], GuardedActions())
	}
	if s, _ := banner["banner"].(string); s == "" {
		t.Error("banner text empty")
	}
}

// TestIsFullScanOntologyCmd pins the guard set: the four full-table cmds
// guarded, the KV read and the recommended escape hatch not.
func TestIsFullScanOntologyCmd(t *testing.T) {
	cases := map[string]bool{
		"status": true, "trace": true, "feature_flow": true, "traceability": true,
		"concept_search": false, "matches": false, "": false, "bogus": false,
	}
	for cmd, want := range cases {
		if got := IsFullScanOntologyCmd(cmd); got != want {
			t.Errorf("IsFullScanOntologyCmd(%q) = %v, want %v", cmd, got, want)
		}
	}
	for _, a := range GuardedActions() {
		cmd := a[len("ontology/"):]
		if !IsFullScanOntologyCmd(cmd) {
			t.Errorf("GuardedActions lists %q but the cmd predicate refuses it", a)
		}
	}
}
