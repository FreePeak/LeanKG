package index

import (
	"regexp"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// wordRe tokenizes element content into whole identifiers.
var wordRe = regexp.MustCompile(`\w+`)

// relationships computes the relationship batch for one file's elements:
//   - contains: parent -> child, confidence 1.0;
//   - calls: whole-word identifier in the element's content matching another
//     element's name (confidence 0.5, self-calls dropped, one row per
//     (source, target) pair).
//
// nameTargets maps every element name seen this run to its qualified names;
// call targets may live in other files processed in this run.
func relationships(els []indexedElem, nameTargets map[string][]string) []store.Relationship {
	var out []store.Relationship
	seen := map[[3]string]bool{}
	add := func(src, dst, typ string, conf float64) {
		key := [3]string{src, dst, typ}
		if src == dst || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, store.Relationship{
			Source: src, Target: dst, RelType: typ, Confidence: conf,
		})
	}

	for i := range els {
		e := els[i]
		if e.parent >= 0 {
			add(els[e.parent].qn, e.qn, "contains", 1.0)
		}
		if e.etype == "doc" {
			continue
		}
		for _, w := range wordRe.FindAllString(e.content, -1) {
			for _, target := range nameTargets[w] {
				add(e.qn, target, "calls", 0.5)
			}
		}
	}
	return out
}
