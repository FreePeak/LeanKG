package lsp

// Index-time LSP enrichment (the hybrid merge). Runs after the regex
// indexer wrote its elements: for every requested language with a resolvable
// server, the server's document symbols are merged back into the stored
// elements — LSP-accurate names/kinds/spans plus the server-provided
// signature and hover documentation. Symbols the regex extractor missed
// become new elements (only declaration kinds, never locals/fields), so the
// in-process typed resolve sees a richer symbol table than regex alone.
//
// Bounded by construction: one pooled server per (language, workspace root),
// initialize bounded by handshakeTimeout, every request bounded by the
// project's timeout_ms (default 5000), pool idle-reaped after defaultTTL and
// shut down when the pass returns, and the whole pass stops at enrichTimeout.
// A language with no configured or resolvable server degrades to a recorded
// reason — never an error.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// maxHoversPerFile bounds the hover requests one file may cost; signatures
// already present in DocumentSymbol.detail make most hovers unnecessary.
//
// ponytail: a fixed cap, not an adaptive budget — a file with more than
// maxHoversPerFile undocumented declarations keeps detail-only enrichment.
// Upgrade path: per-language hover budget carried in Config.
const maxHoversPerFile = 20

// enrichTimeout is the overall wall-clock guard for one Enrich call; the
// per-request timeout still bounds each individual server call. A var so
// tests can shrink it.
//
// ponytail: a coarse ceiling, not a per-language budget — a huge project may
// end enrichment early with a partial (still valid) merge. Upgrade path:
// caller-supplied context deadline.
var enrichTimeout = 5 * time.Minute

// EnrichResult reports one enrichment pass. Enabled=false means the pass was
// a no-op and Reason says why.
type EnrichResult struct {
	Enabled       bool         `json:"enabled"`
	Reason        string       `json:"reason,omitempty"`
	Languages     []LangReport `json:"languages,omitempty"`
	Files         int          `json:"files"`
	Symbols       int          `json:"symbols"`
	Merged        int          `json:"merged"`
	NewElements   int          `json:"new_elements"`
	Signatures    int          `json:"signatures"`
	CallsUpgraded int          `json:"calls_upgraded"`
}

// LangReport is the per-language outcome.
type LangReport struct {
	Language   string `json:"language"`
	Tier       string `json:"tier,omitempty"` // configured | catalog
	Files      int    `json:"files"`
	Symbols    int    `json:"symbols"`
	Merged     int    `json:"merged"`
	New        int    `json:"new"`
	Signatures int    `json:"signatures"`
	Reason     string `json:"reason,omitempty"` // skip / degradation reason
}

// Enrich merges language-server symbols into the elements already indexed
// under dir, then runs the in-process typed resolve. want is the set of
// languages to enrich (Go engine tags or catalog names, e.g. "go", "ts",
// "typescript"); an empty list means every language present in the store.
// Absent or unresolvable servers never fail the pass.
func Enrich(st store.Backend, dir string, want []string) (EnrichResult, error) {
	b := FromLeanKGYAMLOrDefault(dir)
	defer b.Shutdown()
	return b.Enrich(st, dir, want)
}

// Enrich is the bridge-scoped pass (callers holding a Bridge reuse its pool).
func (b *Bridge) Enrich(st store.Backend, dir string, want []string) (EnrichResult, error) {
	var res EnrichResult
	els, err := st.Elements()
	if err != nil {
		return res, err
	}
	if len(els) == 0 {
		res.Reason = "no indexed elements"
		return res, nil
	}

	// Group elements by catalog language id (elements carry the indexer's
	// extension-without-dot tags; catalog ids unify ts/tsx, js/jsx, ...).
	byLang := map[string][]*store.Element{}
	existingQN := make(map[string]bool, len(els))
	for i := range els {
		existingQN[els[i].QualifiedName] = true
		id, ok := tagToCatalogID(els[i].Language)
		if !ok {
			spec, found := ForLanguage(els[i].Language)
			if !found {
				continue
			}
			id = spec.Language
		}
		byLang[id] = append(byLang[id], &els[i])
	}

	deadline := time.Now().Add(enrichTimeout)
	var changed []store.Element
	for _, id := range requestedLanguages(want, byLang) {
		rep := LangReport{Language: id}
		sc, tier, ok := b.ServerFor(id)
		if ok {
			rep.Tier = tier
		}
		switch {
		case !ok:
			rep.Reason = "no LSP server configured or known for this language"
		case !commandOnPath(sc.Command):
			rep.Reason = fmt.Sprintf("server %q is not on PATH (install it or name one in leankg.yaml)", sc.Command)
		default:
			changed = append(changed, b.enrichLanguage(&rep, dir, id, tier, sc, byLang[id], existingQN, deadline)...)
		}
		res.Languages = append(res.Languages, rep)
		res.Files += rep.Files
		res.Symbols += rep.Symbols
		res.Merged += rep.Merged
		res.NewElements += rep.New
		res.Signatures += rep.Signatures
	}

	if len(changed) > 0 {
		if err := st.UpsertElements(changed); err != nil {
			return res, err
		}
	}

	// In-process hybrid tier: upgrade CALLS edges against the (now richer)
	// symbol table, gated by the project's indexer.typed_resolve setting
	// (Rust default "off"). Runs regardless of server availability — it
	// spawns nothing (Rust parity: FR-LSP-A).
	res.CallsUpgraded, err = HybridResolve(st, b.typedResolve)
	if err != nil {
		return res, err
	}

	res.Enabled = res.Merged > 0 || res.NewElements > 0 || res.CallsUpgraded > 0
	if !res.Enabled {
		res.Reason = noOpReason(res.Languages)
	}
	return res, nil
}

// enrichLanguage scans one language's files, merges server symbols into their
// elements and returns the elements to persist (enriched rows and newly
// discovered ones). A server failure degrades the language (recorded in
// rep.Reason) instead of failing the pass.
func (b *Bridge) enrichLanguage(rep *LangReport, dir, id, tier string, sc ServerConfig, els []*store.Element, existingQN map[string]bool, deadline time.Time) []store.Element {
	files := map[string][]*store.Element{}
	for _, e := range els {
		files[e.FilePath] = append(files[e.FilePath], e)
	}
	rels := make([]string, 0, len(files))
	for f := range files {
		rels = append(rels, f)
	}
	sort.Strings(rels)

	var out []store.Element
	failures := 0
	for _, rel := range rels {
		if time.Now().After(deadline) {
			rep.Reason = "enrichment deadline reached (partial merge)"
			return out
		}
		abs := rel
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(dir, filepath.FromSlash(rel))
		}
		if _, err := os.Stat(abs); err != nil {
			continue // deleted since the index; not enrichment's business
		}
		targets := files[rel]
		root := b.WorkspaceFor(abs)
		syms, err := b.documentSymbols(id, sc, root, abs)
		if err != nil {
			failures++
			if failures > 1 {
				rep.Reason = fmt.Sprintf("server %q stopped answering after %d files: %v", sc.Command, rep.Files, err)
				return out
			}
			continue
		}
		rep.Files++
		rep.Symbols += len(syms)
		merged, newEls, sigs := b.mergeFile(targets, syms, rel, id, tier, existingQN)
		rep.Merged += merged
		rep.New += len(newEls)
		rep.Signatures += sigs
		out = append(out, newEls...)
		for _, e := range targets {
			if e.Metadata != nil && e.Metadata["lsp_enriched"] == true {
				out = append(out, *e)
			}
		}
		if merged > 0 {
			// Hover documentation for elements the server did not describe
			// inline (bounded per file).
			hovers := 0
			for _, el := range targets {
				if hovers >= maxHoversPerFile {
					break
				}
				if el.Metadata == nil || el.Metadata["lsp_enriched"] != true {
					continue
				}
				if _, has := el.Metadata["lsp_signature"]; has {
					continue
				}
				doc, err := b.hover(id, sc, root, abs, el)
				if err != nil || doc == "" {
					continue
				}
				el.Metadata["lsp_doc"] = doc
				rep.Signatures++
				hovers++
				out = append(out, *el)
			}
		}
	}
	return out
}

// documentSymbols fetches one document's symbols, respawning a dead pooled
// server once (Rust bridge: a failed request drops the entry so the next call
// spawns fresh). Each attempt is bounded by the configured request timeout.
func (b *Bridge) documentSymbols(id string, sc ServerConfig, root, abs string) ([]Symbol, error) {
	var lastErr error
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), b.requestTimeout())
		syms, err := b.getAndSymbols(ctx, id, sc, root, abs)
		cancel()
		if err == nil {
			return syms, nil
		}
		lastErr = err
		b.manager.Evict(langs.Language(id), root)
	}
	return nil, lastErr
}

func (b *Bridge) getAndSymbols(ctx context.Context, id string, sc ServerConfig, root, abs string) ([]Symbol, error) {
	c, err := b.manager.GetCommand(ctx, langs.Language(id), sc.Command, sc.Args, root, sc.InitializationOptions)
	if err != nil {
		return nil, err
	}
	return c.DocumentSymbols(ctx, abs)
}

// hover sends one hover request positioned at the element's declaration.
func (b *Bridge) hover(id string, sc ServerConfig, root, abs string, el *store.Element) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), b.requestTimeout())
	defer cancel()
	c, err := b.manager.GetCommand(ctx, langs.Language(id), sc.Command, sc.Args, root, sc.InitializationOptions)
	if err != nil {
		return "", err
	}
	line := el.LineStart - 1
	if line < 0 {
		line = 0
	}
	char := 0
	if first := firstLine(el.Content); first != "" {
		if i := strings.Index(first, el.Name); i > 0 {
			char = i
		}
	}
	return c.Hover(ctx, abs, line, char)
}

// mergeFile merges one file's symbols into its elements: matched elements get
// the LSP span/kind/name plus signature docs; unmatched declaration symbols
// become new elements (existing qualified names are never duplicated).
func (b *Bridge) mergeFile(els []*store.Element, syms []Symbol, rel, id, tier string, existingQN map[string]bool) (merged int, newEls []store.Element, sigs int) {
	matched := make([]bool, len(syms))
	for _, el := range els {
		best := bestSymbol(el, syms)
		if best < 0 {
			continue
		}
		s := syms[best]
		matched[best] = true
		meta := el.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		meta["lsp_enriched"] = true
		meta["lsp_kind"] = s.Kind
		meta["lsp_kind_name"] = KindName(s.Kind)
		meta["lsp_tier"] = tier
		if s.Name != "" && s.Name != el.Name {
			meta["lsp_name"] = s.Name
		}
		if s.Detail != "" {
			meta["lsp_signature"] = s.Detail
			sigs++
		}
		if s.StartLine > 0 && (s.StartLine != el.LineStart || s.EndLine != el.LineEnd) {
			el.LineStart, el.LineEnd = s.StartLine, s.EndLine
		}
		el.Metadata = meta
		merged++
	}

	for i, s := range syms {
		if matched[i] || s.Name == "" {
			continue
		}
		etype, ok := creatableKind(s.Kind)
		if !ok {
			continue
		}
		qn := rel + "::" + s.Name
		if existingQN[qn] {
			continue
		}
		existingQN[qn] = true
		newEls = append(newEls, store.Element{
			QualifiedName: qn, ElementType: etype, Name: s.Name,
			FilePath: rel, LineStart: s.StartLine, LineEnd: s.EndLine,
			Language: fileLanguage(els, id),
			Metadata: map[string]any{
				"source": "lsp", "lsp_kind": s.Kind, "lsp_kind_name": KindName(s.Kind),
			},
		})
	}
	return merged, newEls, sigs
}

// bestSymbol picks the most specific symbol matching an element: a containing
// symbol whose name equals the element's wins; otherwise the smallest
// containing span; a near name match (same name, start within 3 lines) is the
// last resort.
func bestSymbol(el *store.Element, syms []Symbol) int {
	best, bestSpan, bestNamed := -1, 0, false
	for i, s := range syms {
		if s.StartLine > el.LineStart || s.EndLine < el.LineEnd {
			continue
		}
		span := s.EndLine - s.StartLine
		named := s.Name == el.Name
		switch {
		case best < 0:
		case named && !bestNamed:
		case named == bestNamed && span < bestSpan:
		default:
			continue
		}
		best, bestSpan, bestNamed = i, span, named
	}
	if best >= 0 {
		return best
	}
	for i, s := range syms {
		if s.Name == el.Name && absInt(s.StartLine-el.LineStart) <= 3 {
			return i
		}
	}
	return -1
}

// requestedLanguages resolves the wanted set against the languages actually
// present in the store, in deterministic order. Empty want = everything
// present.
func requestedLanguages(want []string, byLang map[string][]*store.Element) []string {
	if len(want) == 0 {
		out := make([]string, 0, len(byLang))
		for id := range byLang {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}
	seen := map[string]bool{}
	var out []string
	for _, w := range want {
		id, ok := tagToCatalogID(w)
		if !ok {
			spec, found := ForLanguage(w)
			if !found {
				continue
			}
			id = spec.Language
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// noOpReason summarizes why nothing was enriched.
func noOpReason(reports []LangReport) string {
	if len(reports) == 0 {
		return "no known language present in the index"
	}
	var parts []string
	for _, r := range reports {
		if r.Reason != "" {
			parts = append(parts, r.Language+": "+r.Reason)
		}
	}
	if len(parts) == 0 {
		return "no LSP-visible declarations to merge"
	}
	return "no LSP server available (" + strings.Join(parts, "; ") + ")"
}

// fileLanguage returns the indexer's language tag for a file's elements
// (elements store extension-without-dot tags), falling back to the catalog
// spec's first extension.
func fileLanguage(els []*store.Element, id string) string {
	for _, e := range els {
		if e.Language != "" {
			return e.Language
		}
	}
	if spec, ok := ForLanguage(id); ok && len(spec.Extensions) > 0 {
		return spec.Extensions[0]
	}
	return id
}

// creatableKind maps an LSP SymbolKind to an element type for symbols the
// regex extractor missed. Locals/fields/parameters are deliberately excluded
// (they are not call-graph nodes).
//
// ponytail: hierarchy is not reconstructed (normalizeSymbols flattens), so a
// discovered method is filed without a parent qualified name; the module/name
// registry tier still resolves it. Upgrade path: carry DocumentSymbol
// parentage through normalizeSymbols.
func creatableKind(kind int) (string, bool) {
	switch kind {
	case 12:
		return "function", true
	case 6:
		return "method", true
	case 9:
		return "constructor", true
	case 5:
		return "class", true
	case 10:
		return "enum", true
	case 11:
		return "interface", true
	case 23:
		return "struct", true
	}
	return "", false
}

// KindName renders an LSP SymbolKind (subset used by the engine).
func KindName(kind int) string {
	switch kind {
	case 1:
		return "file"
	case 2:
		return "module"
	case 3:
		return "namespace"
	case 4:
		return "package"
	case 5:
		return "class"
	case 6:
		return "method"
	case 7:
		return "property"
	case 8:
		return "field"
	case 9:
		return "constructor"
	case 10:
		return "enum"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 22:
		return "enum_member"
	case 23:
		return "struct"
	case 24:
		return "event"
	case 25:
		return "operator"
	case 26:
		return "type_parameter"
	}
	return "unknown"
}

// firstLine returns the first line of a stored content snippet.
func firstLine(content string) string {
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		return content[:i]
	}
	return content
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
