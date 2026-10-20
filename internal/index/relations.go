package index

import (
	"regexp"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// wordRe tokenizes element content into whole identifiers.
var wordRe = regexp.MustCompile(`\w+`)

// relationships computes the relationship batch for one file's elements:
//   - contains: parent -> child, confidence 1.0;
//   - calls: a whole-word identifier in the element's content matching another
//     element's name (confidence 0.5, self-calls dropped, one row per
//     (source, target) pair), plus the grammar-derived call seeds carried by
//     the element (confidence 0.7 — an exact call site rather than a word:
//     an objc message selector such as "log:level:" is not a word and can
//     only be resolved by the tree-sitter tier).
//
// nameTargets maps every element name seen this run to its qualified names;
// call targets may live in other files processed in this run.
//
// Heuristic guards (documented ceilings — the naive "every word vs every
// name" match explodes on JS/TS corpora where thousands of short identifiers
// (`id`, `get`, `map`, `use`) collide across files: a 12 GB polyrepo produced
// ~870 edges per file, dominated by noise):
//   - targets shorter than minCallNameLen are ignored (noise class); the
//     grammar-derived seeds are exempt (they are exact, not guesses);
//   - at most maxEdgesPerElement outgoing calls per element;
//   - at most maxEdgesPerFile calls per file.
const (
	minCallNameLen     = 4
	maxEdgesPerElement = 40
	maxEdgesPerFile    = 500
)

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
		// Grammar-derived call sites first: they are exact (an objc selector
		// resolves to the methods that spell it, not to a word token), so they
		// do not consume the heuristic per-element edge budget below.
		linked := map[string]bool{}
		for _, sel := range e.calls {
			for _, tqn := range nameTargets[sel] {
				add(e.qn, tqn, "calls", 0.7)
				linked[tqn] = true
			}
		}
		outgoing := 0
		// Scan the code only: a doc comment naming another function is
		// prose, not a call site (RS-10 prepends it to content).
		code := e.content
		if e.docLen > 0 && e.docLen <= len(code) {
			code = code[e.docLen:]
		}
		// A name used only in the declaration's signature — a parameter,
		// return or receiver type — is a reference, not a call: `Engine.Query`
		// does not call `Engine`. Names in the body (Python `Greeter()`, Go
		// `Widget{}`) stay calls. impact follows both edge types.
		sig, body := splitSignature(code)
		inBody := map[string]bool{}
		for _, w := range wordRe.FindAllString(body, -1) {
			inBody[w] = true
		}
		for _, w := range wordRe.FindAllString(sig+"\n"+body, -1) {
			if len(w) < minCallNameLen {
				continue // short identifiers are noise, not call sites
			}
			rel := "calls"
			if !inBody[w] {
				rel = "references"
			}
			for _, target := range nameTargets[w] {
				if outgoing >= maxEdgesPerElement {
					break
				}
				if linked[target] {
					continue // the grammar already linked this pair exactly
				}
				before := len(out)
				add(e.qn, target, rel, 0.5)
				if len(out) > before {
					outgoing++
				}
			}
			if outgoing >= maxEdgesPerElement {
				break
			}
		}
		if len(out) >= maxEdgesPerFile {
			break // per-file ceiling: stop scanning further elements
		}
	}
	return out
}

// splitSignature separates a declaration's signature (its first line up to
// the opening brace, or up to the `):` of a Python-style def) from the rest.
func splitSignature(code string) (sig, body string) {
	first, rest, _ := strings.Cut(code, "\n")
	if i := strings.Index(first, "{"); i >= 0 {
		return first[:i], first[i:] + "\n" + rest
	}
	if i := strings.Index(first, "):"); i >= 0 {
		return first[:i+2], first[i+2:] + "\n" + rest
	}
	return first, rest
}
