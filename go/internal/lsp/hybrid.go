package lsp

// In-process hybrid typed resolve (Rust parity: src/lsp/type_registry.rs +
// src/lsp/hybrid.rs, FR-LSP-A/C/D). No child process, no JSON-RPC: a symbol
// registry built from the indexed elements upgrades `calls` edges to
// resolution_method=typed, matching the Rust tiers (type.method, same module,
// unique project-wide name). Edges the registry cannot decide are left
// untouched.

import (
	"path"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// SymbolKind classifies one registry entry (Rust parity: SymbolKind).
type SymbolKind int

const (
	KindFunction SymbolKind = iota
	KindMethod
	KindType
)

// SymbolRef is one resolvable symbol (Rust parity: SymbolRef).
type SymbolRef struct {
	QualifiedName string
	FilePath      string
	Name          string
	Kind          SymbolKind
	TypeName      string // owning type/class/struct for methods
	Language      string
}

// TypeRegistry is the project-wide lookup table for hybrid typed resolve.
type TypeRegistry struct {
	byName       map[string][]SymbolRef
	byModuleName map[moduleName]SymbolRef
	byTypeMethod map[moduleName]SymbolRef
	fileModule   map[string]string
}

type moduleName struct{ Module, Name string }

// NewTypeRegistry builds the registry from indexed elements (Rust parity:
// TypeRegistry::from_elements). Methods/constructors are keyed by their
// parent's last qualified-name segment so Type.method lookups work.
func NewTypeRegistry(elements []store.Element) *TypeRegistry {
	r := &TypeRegistry{
		byName:       map[string][]SymbolRef{},
		byModuleName: map[moduleName]SymbolRef{},
		byTypeMethod: map[moduleName]SymbolRef{},
		fileModule:   map[string]string{},
	}
	for _, e := range elements {
		module := moduleKeyForFile(e.FilePath)
		r.fileModule[e.FilePath] = module
		switch e.ElementType {
		case "function":
			r.insertSymbol(module, SymbolRef{
				QualifiedName: e.QualifiedName, FilePath: e.FilePath, Name: e.Name,
				Kind: KindFunction, Language: e.Language,
			})
		case "method", "constructor":
			typeName := ""
			if e.ParentQualified != "" && e.ParentQualified != e.FilePath {
				if i := strings.LastIndex(e.ParentQualified, "::"); i >= 0 {
					typeName = e.ParentQualified[i+2:]
				}
			}
			sym := SymbolRef{
				QualifiedName: e.QualifiedName, FilePath: e.FilePath, Name: e.Name,
				Kind: KindMethod, TypeName: typeName, Language: e.Language,
			}
			if typeName != "" {
				r.byTypeMethod[moduleName{typeName, e.Name}] = sym
			}
			r.insertSymbol(module, sym)
		case "class", "struct", "interface", "type", "enum":
			r.insertSymbol(module, SymbolRef{
				QualifiedName: e.QualifiedName, FilePath: e.FilePath, Name: e.Name,
				Kind: KindType, Language: e.Language,
			})
		}
	}
	return r
}

func (r *TypeRegistry) insertSymbol(module string, sym SymbolRef) {
	r.byModuleName[moduleName{module, sym.Name}] = sym
	r.byName[sym.Name] = append(r.byName[sym.Name], sym)
}

// ModuleForFile returns the module key recorded for a file path.
func (r *TypeRegistry) ModuleForFile(filePath string) (string, bool) {
	m, ok := r.fileModule[filePath]
	return m, ok
}

// LookupInModule is the exact (module, name) hit — preferred for same-package
// Go / same-folder TS.
func (r *TypeRegistry) LookupInModule(module, name string) (SymbolRef, bool) {
	s, ok := r.byModuleName[moduleName{module, name}]
	return s, ok
}

// LookupUniqueName is the project-wide match when exactly one candidate
// exists.
func (r *TypeRegistry) LookupUniqueName(name string) (SymbolRef, bool) {
	hits := r.byName[name]
	if len(hits) == 1 {
		return hits[0], true
	}
	return SymbolRef{}, false
}

// LookupTypeMethod resolves Type.method on a known type.
func (r *TypeRegistry) LookupTypeMethod(typeName, method string) (SymbolRef, bool) {
	s, ok := r.byTypeMethod[moduleName{typeName, method}]
	return s, ok
}

// Candidates returns every symbol sharing a bare name.
func (r *TypeRegistry) Candidates(name string) []SymbolRef { return r.byName[name] }

// Len returns the number of registered symbols.
func (r *TypeRegistry) Len() int {
	n := 0
	for _, v := range r.byName {
		n += len(v)
	}
	return n
}

// IsEmpty reports whether nothing was registered.
func (r *TypeRegistry) IsEmpty() bool { return len(r.byName) == 0 }

// moduleKeyForFile is the directory containing the file ("." at the root),
// used as the lightweight module key when no package/import is parsed (Rust
// parity: module_key_for_file).
func moduleKeyForFile(filePath string) string {
	d := path.Dir(strings.ReplaceAll(filePath, "\\", "/"))
	if d == "" || d == "." || d == "/" {
		return "."
	}
	return d
}

// TypedHit is a successful hybrid resolution (Rust parity: TypedHit).
type TypedHit struct {
	QualifiedName string
	Confidence    float64
}

// ResolveCall resolves a bare callee name in the context of a calling file,
// in Rust tier order: receiver type method (0.97), same module (0.94 same
// file / 0.98 cross file), unique project-wide name (0.92).
func ResolveCall(r *TypeRegistry, callerFile, calleeName, receiver string) (TypedHit, bool) {
	if calleeName == "" {
		return TypedHit{}, false
	}
	if rest, ok := strings.CutPrefix(calleeName, "__unresolved__"); ok {
		return ResolveCall(r, callerFile, rest, receiver)
	}
	if receiver != "" {
		if hit, ok := r.LookupTypeMethod(receiver, calleeName); ok {
			return TypedHit{hit.QualifiedName, 0.97}, true
		}
	}
	module := moduleKeyForFile(callerFile)
	if hit, ok := r.LookupInModule(module, calleeName); ok {
		conf := 0.98
		if hit.FilePath == callerFile {
			conf = 0.94
		}
		return TypedHit{hit.QualifiedName, conf}, true
	}
	if hit, ok := r.LookupUniqueName(calleeName); ok {
		return TypedHit{hit.QualifiedName, 0.92}, true
	}
	return TypedHit{}, false
}

// CallerFileFromQN infers the caller file from a qualified name
// (`file::fn` / `file::Type.method`, Rust parity: caller_file_from_qn).
func CallerFileFromQN(sourceQualified string) string {
	if i := strings.Index(sourceQualified, "::"); i > 0 {
		prefix := sourceQualified[:i]
		if strings.ContainsAny(prefix, "./") {
			return prefix
		}
	}
	return sourceQualified
}

// LanguageFromFile maps a caller file path to the hybrid-resolve language id
// (Rust parity: hybrid.rs language_from_file — go / typescript / swift /
// objc only).
func LanguageFromFile(filePath string) (string, bool) {
	switch strings.ToLower(path.Ext(filePath)) {
	case ".go":
		return "go", true
	case ".ts", ".tsx", ".js", ".jsx":
		return "typescript", true
	case ".swift":
		return "swift", true
	case ".m", ".mm", ".h":
		return "objc", true
	}
	return "", false
}

// typedResolveAliases mirrors the Rust alias table: canonical -> aliases.
var typedResolveAliases = [][2]string{
	{"typescript", "ts,tsx,typescript,javascript,js,jsx"},
	{"javascript", "js,jsx,javascript"},
	{"python", "py,python"},
	{"rust", "rs,rust"},
	{"ruby", "rb,ruby"},
	{"csharp", "cs,csharp,c#"},
	{"swift", "swift"},
	{"objc", "objc,objective-c,objectivec,m,mm"},
}

// TypedResolveEnabled interprets the typed_resolve setting for one language
// (Rust parity: config::project::typed_resolve_enabled). Accepted values:
// off/""/false/no, all/true/yes/on, or a CSV of language names/aliases.
func TypedResolveEnabled(setting, language string) bool {
	s := strings.ToLower(strings.TrimSpace(setting))
	switch s {
	case "off", "", "false", "no":
		return false
	case "all", "true", "yes", "on":
		return true
	}
	langLower := strings.ToLower(language)
	accepted := map[string]bool{langLower: true}
	for _, a := range typedResolveAliases {
		canonical, list := a[0], strings.Split(a[1], ",")
		if contains(list, langLower) {
			accepted[canonical] = true
		}
		if canonical == langLower {
			for _, alias := range list {
				accepted[alias] = true
			}
		}
	}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == ';'
	}) {
		if accepted[part] || part == "all" {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ApplyTypedResolve upgrades CALLS edges in place (Rust parity:
// apply_typed_resolve). Only edges whose source file language is enabled by
// the typed_resolve setting are touched; already-typed edges are skipped.
// Returns the number of upgraded edges.
func ApplyTypedResolve(rels []store.Relationship, r *TypeRegistry, setting string) int {
	n, _ := applyTypedResolveIdx(rels, r, setting)
	return n
}

// applyTypedResolveIdx is ApplyTypedResolve plus the indices it rewrote (the
// store pass writes back exactly those rows).
func applyTypedResolveIdx(rels []store.Relationship, r *TypeRegistry, setting string) (int, []int) {
	if r.IsEmpty() {
		return 0, nil
	}
	var idx []int
	for i := range rels {
		rel := &rels[i]
		if rel.RelType != "calls" {
			continue
		}
		callerFile := CallerFileFromQN(rel.Source)
		lang, ok := LanguageFromFile(callerFile)
		if !ok || !TypedResolveEnabled(setting, lang) {
			continue
		}
		if method, _ := rel.Metadata["resolution_method"].(string); method == "typed" {
			continue
		}
		receiver, _ := rel.Metadata["receiver"].(string)
		hit, ok := ResolveCall(r, callerFile, BareCallee(rel.Target), receiver)
		if !ok {
			continue
		}
		rel.Target = hit.QualifiedName
		if hit.Confidence > rel.Confidence {
			rel.Confidence = hit.Confidence
		}
		if rel.Metadata == nil {
			rel.Metadata = map[string]any{}
		}
		rel.Metadata["resolution_method"] = "typed"
		rel.Metadata["is_resolved"] = true
		rel.Metadata["hybrid_tier"] = "in_process"
		idx = append(idx, i)
	}
	return len(idx), idx
}

// BareCallee strips the __unresolved__ marker, any receiver prefix
// (`file::`) and the Go method type prefix (`Type.method`, the engine's
// method qualified-name shape). Rust parity: hybrid.rs bare_callee plus the
// Go QN separator.
func BareCallee(target string) string {
	if rest, ok := strings.CutPrefix(target, "__unresolved__"); ok {
		target = rest
	}
	if i := strings.LastIndex(target, "::"); i >= 0 {
		target = target[i+2:]
	}
	if i := strings.LastIndexByte(target, '.'); i >= 0 {
		target = target[i+1:]
	}
	return target
}

// HybridResolve runs the in-process typed resolve over the whole store:
// elements build the registry, every `calls` edge is upgraded in place and
// only the upgraded rows are written back. It spawns nothing.
func HybridResolve(st store.Backend, setting string) (int, error) {
	if setting == "" {
		setting = "off"
	}
	els, err := st.Elements()
	if err != nil {
		return 0, err
	}
	reg := NewTypeRegistry(els)
	if reg.IsEmpty() {
		return 0, nil
	}
	rels, err := st.RelationshipsAll(allRelationshipsLimit)
	if err != nil {
		return 0, err
	}
	n, idx := applyTypedResolveIdx(rels, reg, setting)
	if n == 0 {
		return 0, nil
	}
	changed := make([]store.Relationship, 0, n)
	for _, i := range idx {
		changed = append(changed, rels[i])
	}
	if err := st.UpsertRelationships(changed); err != nil {
		return 0, err
	}
	return n, nil
}

// allRelationshipsLimit bounds the store scan for hybrid resolve; the store's
// own default (1000) is too small for real projects.
const allRelationshipsLimit = 1 << 30
