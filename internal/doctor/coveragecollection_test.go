package doctor

import "testing"

// TestTheCoverageWarnNamesTheCollection pins the second half of wave 20's rule,
// on the check a user hits most often after freshness.
//
// `embedding-coverage` reports a percentage and two counts:
//
//	1% uncovered (9350/9386 embedded)
//	Run `leankg embed` to build vectors for new/changed elements so semantic
//	search stays complete.
//
// The percentage is the same for the two failures that actually happen, and they
// have opposite fixes:
//
//   - vectors were never built for new elements → `leankg-embed run`;
//   - the collection was built for a DIFFERENT model → the embedding stamp
//     disagrees with the configured provider and `leankg-embed full` is the
//     rebuild (the stamp guard is what makes the second case safe at all).
//
// Nothing in the finding distinguishes them, so the advice is a coin flip — and
// the reader cannot even find out which, because the model the COLLECTION was
// built with is nowhere in the message. This is wave 10's defect in the
// reporting layer: the store knows the identity (emb_stamp) and the finding did
// not say it.
//
// Wave 20's rule, applied: a diagnosis must name the thing it looked at.
func TestTheCoverageWarnNamesTheCollection(t *testing.T) {
	// The optional probe: an implementation that knows its stamped models.
	probes := &stubProbes{
		names:    []string{"a.go::A", "a.go::B", "a.go::C", "a.go::D"},
		embedded: []string{"a.go::A", "a.go::B", "a.go::C"},
	}
	stamped := &stampedProbeProbes{stubProbes: probes, models: []string{"bge-small-en-v1.5-f16.gguf"}}

	f := checkEmbeddingCoverage(stamped, Env{})
	if f.Status != StatusWarn {
		t.Fatalf("fixture must WARN, got %s (%s)", f.Status, f.Detail)
	}
	// It must name the collection the percentage is about.
	if !contains(f.Detail, "bge-small-en-v1.5-f16.gguf") {
		t.Errorf("the finding must name the stamped collection:\n  %s", f.Detail)
	}
	// And it must name a gap, because "1% uncovered" does not tell a reader
	// which element to look at.
	if !contains(f.Detail, "a.go::D") {
		t.Errorf("the finding must name an uncovered element:\n  %s", f.Detail)
	}
	// The two fixes must both be present, since the percentage cannot say which
	// applies and the advice must cover both.
	if !contains(f.Hint, "run") || !contains(f.Hint, "full") {
		t.Errorf("the hint must cover both causes (run for unbuilt, full for a model change):\n  %s", f.Hint)
	}
}

// stampedProbeProbes adds the optional stampedModels probe to stubProbes.
type stampedProbeProbes struct {
	*stubProbes
	models []string
}

func (p *stampedProbeProbes) StampedModelIDs() ([]string, error) { return p.models, nil }

func contains(hay, needle string) bool {
	return len(needle) == 0 || (len(hay) >= len(needle) && indexOf(hay, needle) >= 0)
}

func indexOf(hay, needle string) int {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
