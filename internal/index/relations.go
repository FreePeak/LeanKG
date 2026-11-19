package index

import (
	"regexp"
	"slices"
	"strings"

	"github.com/FreePeak/LeanKG/internal/store"
)

// wordRe tokenizes element content into whole identifiers.
var wordRe = regexp.MustCompile(`\w+`)

// trailingLineComment matches a `//` comment that follows whitespace, to the
// end of the line.
var trailingLineComment = regexp.MustCompile(`\s//.*$`)

// stripLineComments removes line comments from code in the syntax of style: a
// whole line that starts with a line-comment prefix, and a trailing `//` after
// whitespace. String literals are not parsed (a documented ceiling of the regex
// tier).
func stripLineComments(code string, style commentStyle) string {
	if len(style.line) == 0 {
		return code
	}
	lines := strings.Split(code, "\n")
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if slices.ContainsFunc(style.line, func(p string) bool { return strings.HasPrefix(t, p) }) {
			lines[i] = ""
			continue
		}
		if slices.Contains(style.line, "//") {
			lines[i] = trailingLineComment.ReplaceAllString(l, "")
		}
	}
	return strings.Join(lines, "\n")
}

// callEdgeTypes are the edge types relationships() derives from element names.
// An incremental run replaces exactly these on an unchanged sibling; contains
// and every resource or framework edge stay as they are.
var callEdgeTypes = []string{"calls", "references"}

// callEdges keeps the edges of callEdgeTypes from a relationships() batch.
func callEdges(rels []store.Relationship) []store.Relationship {
	var out []store.Relationship
	for _, r := range rels {
		if slices.Contains(callEdgeTypes, r.RelType) {
			out = append(out, r)
		}
	}
	return out
}

// nameTable maps the element names of one directory to their qualified names,
// changed and unchanged files alike; call targets may live in any of them.
// forFile adds the cross-package view of one Go file.
type nameTable struct {
	all     map[string][]string // every element: the word heuristic and grammar seeds
	funcs   map[string][]string // non-method elements: a bare Go identifier's targets
	methods map[string][]string // methods: x.Name(...) targets

	cross   *crossIndex
	imports map[string]string // import name -> directory of the imported package
	dirs    map[string]bool   // the directories of imports
	file    string            // the file whose seeds are resolved
}

// nameTableOf indexes the qualified names of a directory's elements by name.
func nameTableOf(els []indexedElem) nameTable {
	nt := nameTable{all: map[string][]string{}, funcs: map[string][]string{}, methods: map[string][]string{}}
	for _, e := range els {
		nt.all[e.name] = append(nt.all[e.name], e.qn)
		if e.etype == "method" {
			nt.methods[e.name] = append(nt.methods[e.name], e.qn)
		} else {
			nt.funcs[e.name] = append(nt.funcs[e.name], e.qn)
		}
	}
	return nt
}

// forFile returns the table with the cross-package view of fe, the Go file the
// seeds come from.
func (nt nameTable) forFile(cx *crossIndex, fe fileElements) nameTable {
	out := nt
	out.cross = cx
	out.file = fe.rel
	out.imports = cx.importDirs(fe.imports)
	out.dirs = map[string]bool{}
	for _, d := range out.imports {
		out.dirs[d] = true
	}
	return out
}

// methodTarget is one candidate of a method seed.
type methodTarget struct {
	qn, resolution string
	conf           float64
}

// methodTargets resolves a method seed by name: same-package methods first,
// then the methods of the imported packages when the package has none, and
// then, for an interface method name, the module's implementers (crosspkg.go).
func (nt nameTable) methodTargets(name string) []methodTarget {
	var out []methodTarget
	for _, q := range nt.methods[name] {
		out = append(out, methodTarget{qn: q, conf: methodConf, resolution: "method-by-name"})
	}
	if nt.cross == nil {
		return out
	}
	if len(out) == 0 {
		for _, q := range nt.cross.methodsIn(name, nt.dirs) {
			out = append(out, methodTarget{qn: q, conf: methodConf, resolution: "method-by-name"})
		}
	}
	for _, q := range nt.cross.dispatchTargets(name, nt.file, dirOf(nt.file)) {
		out = append(out, methodTarget{qn: q, conf: dispatchConf, resolution: "interface-dispatch"})
	}
	return out
}

// qualifiedEdges resolves a file's qualified seeds (alias.Name) to the exported
// elements of the imported directory, at confidence 0.8 with the use line. The
// caps of relationships() do not apply to them: each is an exact import use.
func qualifiedEdges(quals []qualifiedSeed, names nameTable) []store.Relationship {
	var out []store.Relationship
	seen := map[[3]string]bool{}
	for _, q := range quals {
		dir, ok := names.imports[q.Alias]
		if !ok {
			continue
		}
		rel := "references"
		if q.Call {
			rel = "calls"
		}
		for _, tqn := range names.cross.qualifiedTargets(dir, q.Name) {
			key := [3]string{q.Source, tqn, rel}
			if q.Source == tqn || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, store.Relationship{
				Source: q.Source, Target: tqn, RelType: rel, Confidence: qualifiedConf,
				Metadata: map[string]any{"line": q.Line},
			})
		}
	}
	return out
}

// fileRelationships is the whole relationship batch of one Go or other file:
// relationships() for its elements plus the cross-package edges of its
// qualified seeds. names must come from forFile for a Go file.
func fileRelationships(fe fileElements, names nameTable) []store.Relationship {
	return append(relationships(fe.elements, names), qualifiedEdges(fe.qualified, names)...)
}

// relationships computes the relationship batch for one file's elements:
//   - contains: parent -> child, confidence 1.0;
//   - Go elements from the AST tier (seeded): each name seed resolves in the
//     element's directory. A bare identifier resolves to same-package non-method
//     elements (calls for a call site, references otherwise; confidence 0.5). An
//     x.Name method seed (called or taken as a value) resolves by name through
//     methodTargets (confidence 0.4, resolution "method-by-name", or 0.3 for
//     "interface-dispatch"). Every such edge carries its first use line as
//     metadata {"line": N}. Qualified seeds (alias.Name) make their edges in
//     qualifiedEdges, outside these caps;
//   - other elements: a whole-word identifier in the element's content matching
//     another element's name (confidence 0.5, self-calls dropped, one row per
//     (source, target) pair), plus the grammar-derived call seeds carried by
//     the element (confidence 0.7 — an exact call site rather than a word:
//     an objc message selector such as "log:level:" is not a word and can
//     only be resolved by the tree-sitter tier).
//
// Heuristic guards (documented ceilings — the naive "every word vs every
// name" match explodes on JS/TS corpora where thousands of short identifiers
// (`id`, `get`, `map`, `use`) collide across files: a 12 GB polyrepo produced
// ~870 edges per file, dominated by noise):
//   - targets shorter than minCallNameLen are ignored (noise class); the
//     grammar-derived seeds and the AST seeds are exempt (they are exact, not guesses);
//   - at most maxEdgesPerElement outgoing calls per element;
//   - at most maxEdgesPerFile calls per file.
const (
	minCallNameLen     = 4
	maxEdgesPerElement = 40
	maxEdgesPerFile    = 500
)

func relationships(els []indexedElem, names nameTable) []store.Relationship {
	var out []store.Relationship
	seen := map[[3]string]bool{}
	add := func(src, dst, typ string, conf float64, meta map[string]any) {
		key := [3]string{src, dst, typ}
		if src == dst || seen[key] {
			return
		}
		seen[key] = true
		out = append(out, store.Relationship{
			Source: src, Target: dst, RelType: typ, Confidence: conf, Metadata: meta,
		})
	}

	for i := range els {
		e := els[i]
		if e.parent >= 0 {
			add(els[e.parent].qn, e.qn, "contains", 1.0, nil)
		}
		if e.etype == "doc" {
			continue
		}
		// A type or class names what it holds (an interface's method specs,
		// a struct's fields); it never calls, so its names are references.
		callRel := "calls"
		if e.etype == "type" || e.etype == "class" {
			callRel = "references"
		}
		// Grammar-derived call sites first: they are exact (an objc selector
		// resolves to the methods that spell it, not to a word token), so they
		// do not consume the heuristic per-element edge budget below.
		linked := map[string]bool{}
		for _, sel := range e.calls {
			for _, tqn := range names.all[sel] {
				add(e.qn, tqn, callRel, 0.7, nil)
				linked[tqn] = true
			}
		}
		outgoing := 0
		if e.seeded {
			for _, s := range e.seeds {
				if outgoing >= maxEdgesPerElement {
					break
				}
				if s.Kind == "qualified" {
					continue // cross-package: qualifiedEdges
				}
				rel := "references"
				if s.Call && callRel == "calls" {
					rel = "calls"
				}
				var targets []methodTarget
				if s.Kind == "method" {
					targets = names.methodTargets(s.Name)
				} else {
					for _, q := range names.funcs[s.Name] {
						targets = append(targets, methodTarget{qn: q, conf: 0.5})
					}
				}
				for _, t := range targets {
					if outgoing >= maxEdgesPerElement {
						break
					}
					if linked[t.qn] {
						continue
					}
					meta := map[string]any{"line": s.Line}
					if t.resolution != "" {
						meta["resolution"] = t.resolution
					}
					before := len(out)
					add(e.qn, t.qn, rel, t.conf, meta)
					if len(out) > before {
						outgoing++
					}
				}
			}
		} else {
			// Scan the code only: the leading doc block (docLen bytes) is prose,
			// not a call site, and line comments in the body are not code.
			code := e.content
			if e.docLen > 0 && e.docLen <= len(code) {
				code = code[e.docLen:]
			}
			code = stripLineComments(code, commentStyles[e.lang])
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
				rel := callRel
				if !inBody[w] {
					rel = "references"
				}
				for _, target := range names.all[w] {
					if outgoing >= maxEdgesPerElement {
						break
					}
					if linked[target] {
						continue // the grammar already linked this pair exactly
					}
					before := len(out)
					add(e.qn, target, rel, 0.5, nil)
					if len(out) > before {
						outgoing++
					}
				}
				if outgoing >= maxEdgesPerElement {
					break
				}
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
