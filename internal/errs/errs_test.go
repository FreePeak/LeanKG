package errs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const codeMarker = "LEANKG_ERROR_"

func TestCatalogEntriesAreComplete(t *testing.T) {
	if len(Catalog) == 0 {
		t.Fatal("catalog must not be empty")
	}
	seen := map[string]bool{}
	for _, e := range Catalog {
		if !strings.HasPrefix(e.Code, codeMarker) {
			t.Errorf("%s: code must use the %s prefix", e.Code, codeMarker)
		}
		if strings.TrimSpace(e.Cause) == "" {
			t.Errorf("%s: cause clause must be non-empty", e.Code)
		}
		if strings.TrimSpace(e.Fix) == "" {
			t.Errorf("%s: runnable fix must be non-empty", e.Code)
		}
		if strings.TrimSpace(e.DocAnchor) == "" {
			t.Errorf("%s: doc anchor must be non-empty", e.Code)
		}
		if seen[e.Code] {
			t.Errorf("%s: duplicate code in catalog", e.Code)
		}
		seen[e.Code] = true
	}
}

func TestLookupResolvesRegisteredCodesOnly(t *testing.T) {
	entry, ok := Lookup("LEANKG_ERROR_UNKNOWN_PROJECT")
	if !ok {
		t.Fatal("UNKNOWN_PROJECT must resolve")
	}
	if entry.DocAnchor != "README.md#get-started" {
		t.Fatalf("unexpected anchor: %q", entry.DocAnchor)
	}
	if _, ok := Lookup("LEANKG_ERROR_NO_SUCH_CODE"); ok {
		t.Fatal("unknown code resolved")
	}
}

func TestRenderRoundTrip(t *testing.T) {
	rendered := Render("LEANKG_ERROR_UNKNOWN_PROJECT", "cause", "run this")
	if rendered != "LEANKG_ERROR_UNKNOWN_PROJECT: cause. Fix: run this (docs: README.md#get-started)" {
		t.Fatalf("registered render drifted: %s", rendered)
	}
	// An unregistered code still renders (cause + fix) so callers never dead-end.
	if got := Render("LEANKG_ERROR_NO_SUCH_CODE", "cause", "run this"); got != "LEANKG_ERROR_NO_SUCH_CODE: cause. Fix: run this" {
		t.Fatalf("unregistered render drifted: %s", got)
	}
}

// TestEveryEntryRendersItsOwnCauseAndFix pins the shape of the static render:
// code, cause, fix and anchor all appear in order.
func TestEveryEntryRendersItsOwnCauseAndFix(t *testing.T) {
	for _, e := range Catalog {
		got := e.Message()
		want := e.Code + ": " + e.Cause + ". Fix: " + e.Fix + " (docs: " + e.DocAnchor + ")"
		if got != want {
			t.Errorf("%s: message drifted:\n got %s\nwant %s", e.Code, got, want)
		}
		if e.Render(e.Cause, e.Fix) != got {
			t.Errorf("%s: Render(cause,fix) must equal Message()", e.Code)
		}
	}
}

func TestCatalogTableMatchesAccessors(t *testing.T) {
	// Guards against a new constant being added without a Catalog row.
	consts := []ErrorCode{
		PGUnreachable, PGURLMalformed, ProjectNotInitialized, UnknownProject,
		AutoAttachFailed, Unauthorized, UnknownTool, NoVectors, TrgmUnavailable,
		MethodNotFound, ReadOnly, UnknownAction, MissingParam, PermissionDenied,
	}
	if len(consts) != len(Catalog) {
		t.Fatalf("const count %d != catalog size %d", len(consts), len(Catalog))
	}
	for _, e := range consts {
		found, ok := Lookup(e.Code)
		if !ok {
			t.Errorf("constant %s is missing from Catalog", e.Code)
			continue
		}
		if found != e {
			t.Errorf("constant %s differs from its catalog row", e.Code)
		}
	}
	if got := Codes(); len(got) != len(Catalog) {
		t.Fatalf("Codes() returned %d entries, want %d", len(got), len(Catalog))
	}
	if got := All(); len(got) != len(Catalog) {
		t.Fatalf("All() returned %d entries, want %d", len(got), len(Catalog))
	}
}

func TestNewErrorRendersAndMatchesByCode(t *testing.T) {
	err := NewError(UnknownTool, "tool 'nope' is not in this server's registry", "")
	if err.Code() != "LEANKG_ERROR_UNKNOWN_TOOL" {
		t.Fatalf("code: %s", err.Code())
	}
	// Empty fix falls back to the catalog's runnable fix.
	if !strings.Contains(err.Error(), UnknownTool.Fix) {
		t.Fatalf("catalog fix missing from %s", err.Error())
	}
	if !strings.Contains(err.Error(), "(docs: "+UnknownTool.DocAnchor+")") {
		t.Fatalf("anchor missing from %s", err.Error())
	}
	other := NewError(UnknownTool, "different cause", "different fix")
	if !errors.Is(err, other) {
		t.Fatal("same code must match under errors.Is")
	}
	if errors.Is(err, NewError(UnknownAction, "c", "f")) {
		t.Fatal("different codes must not match")
	}
	unregistered := NewErrorFor("LEANKG_ERROR_TOTALLY_NEW", "c", "f")
	if unregistered.Error() != "LEANKG_ERROR_TOTALLY_NEW: c. Fix: f" {
		t.Fatalf("unregistered error must still render: %s", unregistered.Error())
	}
}

// goFilesOutsidePackage walks the Go module and returns every .go file outside
// this package.
func goFilesOutsidePackage(t *testing.T) []string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", ".."))

	var files []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "testdata" || name == "node_modules" || name == "vendor" {
				return fs.SkipDir
			}
			if filepath.Join(path) == filepath.Join(wd) { // this package
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// scanErrorCodes extracts every `LEANKG_ERROR_<NAME>` token from text.
// Port of the Rust scanner: the name is the trailing run of [A-Z0-9_], with a
// dangling underscore trimmed so prose like `LEANKG_ERROR_*` is ignored.
func scanErrorCodes(text string) []string {
	var out []string
	cursor := 0
	for {
		pos := strings.Index(text[cursor:], codeMarker)
		if pos < 0 {
			break
		}
		start := cursor + pos
		end := start + len(codeMarker)
		for end < len(text) && isCodeChar(text[end]) {
			end++
		}
		name := strings.TrimRight(text[start+len(codeMarker):end], "_")
		if name != "" {
			out = append(out, codeMarker+name)
		}
		if end <= start+len(codeMarker) {
			end = start + len(codeMarker)
		}
		cursor = end
	}
	return out
}

func isCodeChar(b byte) bool {
	return (b >= 'A' && b <= 'Z') || b == '_' || (b >= '0' && b <= '9')
}

// TestEveryErrorLiteralHasACatalogEntry is the FR-ZCP-12 T1 acceptance
// criterion: 100% coverage — a new `LEANKG_ERROR_*` literal without a catalog
// row fails the build.
func TestEveryErrorLiteralHasACatalogEntry(t *testing.T) {
	files := goFilesOutsidePackage(t)
	if len(files) < 100 {
		t.Fatalf("walk found only %d go files; walker is broken", len(files))
	}
	var offenders []string
	for _, file := range files {
		text, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, code := range scanErrorCodes(string(text)) {
			if _, ok := Lookup(code); !ok {
				offenders = append(offenders, file+": "+code)
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("LEANKG_ERROR_ literals with no catalog entry:\n%s", strings.Join(offenders, "\n"))
	}
}

// pendingWiring lists catalog entries whose call sites Main still has to wire.
// The Rust original could assert "every entry has a use site" because it landed
// with its call sites; this port lands the catalog first, so the audit reports
// the remaining work instead of failing the build on it.
var pendingWiring = map[string]bool{
	"LEANKG_ERROR_PROJECT_NOT_INITIALIZED": true,
	"LEANKG_ERROR_UNKNOWN_PROJECT":         true,
	"LEANKG_ERROR_AUTO_ATTACH_FAILED":      true,
	"LEANKG_ERROR_NO_VECTORS":              true,
	"LEANKG_ERROR_TRGM_UNAVAILABLE":        true,
	"LEANKG_ERROR_METHOD_NOT_FOUND":        true,
	"LEANKG_ERROR_READ_ONLY":               true,
	"LEANKG_ERROR_MISSING_PARAM":           true,
}

// TestCatalogWiringAudit reports every catalog entry no call site can reach
// (the Rust `every_catalog_entry_is_referenced_by_a_site` check). It fails only
// on drift: an entry that IS wired must leave pendingWiring, and the pending
// list must not name a code the catalog does not define.
func TestCatalogWiringAudit(t *testing.T) {
	files := goFilesOutsidePackage(t)
	if len(files) < 100 {
		t.Fatalf("walk found only %d go files; walker is broken", len(files))
	}
	texts := make([]string, 0, len(files))
	for _, file := range files {
		if text, err := os.ReadFile(file); err == nil {
			texts = append(texts, string(text))
		}
	}
	// A wired site references the entry by its exported constant
	// (`errs.UnknownTool` — the Go form of the Rust audit's `errors::<CONST>`
	// match) or by the code literal in a Render call.
	names := catalogConstNames(t)
	referenced := func(code string) bool {
		if containsAny(texts, code) {
			return true
		}
		name, ok := names[code]
		return ok && containsAny(texts, "errs."+name)
	}

	for _, e := range Catalog {
		if referenced(e.Code) && pendingWiring[e.Code] {
			t.Errorf("%s is now used by a call site: drop it from pendingWiring so the audit enforces it", e.Code)
			continue
		}
		if !referenced(e.Code) && !pendingWiring[e.Code] {
			t.Errorf("%s is unreferenced and not marked pending: wire a call site or add it to pendingWiring", e.Code)
		}
	}
	for code := range pendingWiring {
		if _, ok := Lookup(code); !ok {
			t.Errorf("pendingWiring names %s, which is not in the catalog", code)
		}
	}

	var pending []string
	for _, e := range Catalog {
		if pendingWiring[e.Code] {
			pending = append(pending, e.Code)
		}
	}
	t.Logf("catalog wiring: %d/%d entries wired; pending: %s", len(Catalog)-len(pending), len(Catalog), strings.Join(pending, ", "))
}

// catalogConstNames maps each `LEANKG_ERROR_*` code to the exported Go
// constant that carries it, read off the catalog declarations. The Rust audit
// matched `errors::<CONST>` use sites; Go has no constant-name lookup, so the
// names come from the source of truth (errs.go) rather than a hand-maintained
// table that could drift.
func catalogConstNames(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile("errs.go")
	if err != nil {
		t.Fatalf("read catalog source: %v", err)
	}
	decl := regexp.MustCompile(`var (\w+) = ErrorCode\{\s*Code:\s+"([A-Z0-9_]+)"`)
	names := make(map[string]string)
	for _, m := range decl.FindAllStringSubmatch(string(src), -1) {
		names[m[2]] = m[1]
	}
	if len(names) != len(Catalog) {
		t.Fatalf("recovered %d constant names for %d catalog entries; catalog declaration shape changed", len(names), len(Catalog))
	}
	return names
}

func containsAny(texts []string, needle string) bool {
	for _, text := range texts {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

// constName maps LEANKG_ERROR_FOO_BAR -> FooBar (the exported Go constant).
func constName(code string) string {
	var b strings.Builder
	for _, part := range strings.Split(strings.TrimPrefix(code, codeMarker), "_") {
		if part == "" {
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(strings.ToLower(part[1:]))
	}
	return b.String()
}

func TestConstNameMapping(t *testing.T) {
	cases := map[string]string{
		"LEANKG_ERROR_PG_UNREACHABLE":          "PgUnreachable",
		"LEANKG_ERROR_UNKNOWN_PROJECT":         "UnknownProject",
		"LEANKG_ERROR_MISSING_PARAM":           "MissingParam",
		"LEANKG_ERROR_AUTO_ATTACH_FAILED":      "AutoAttachFailed",
		"LEANKG_ERROR_PROJECT_NOT_INITIALIZED": "ProjectNotInitialized",
		"LEANKG_ERROR_TRGM_UNAVAILABLE":        "TrgmUnavailable",
	}
	for code, want := range cases {
		if got := constName(code); got != want {
			t.Errorf("constName(%s) = %s, want %s", code, got, want)
		}
	}
}
