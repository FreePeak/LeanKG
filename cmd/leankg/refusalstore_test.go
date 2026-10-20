package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheRefusalNamesTheStoreItSearched pins the one hole left in wave 16's
// annotation guard, found by wave 20's regression sweep.
//
// Wave 16 made `annotate` and `link` refuse a `qualified_name` the index does
// not contain, and told the caller to recover with `leankg query <name>`. The
// sweep found the escape hatch is a dead end: **the refusal and its own advice
// resolve the store the same way**, so a caller who is looking in the wrong
// store is told the element does not exist, follows the advice, and is told the
// element does not exist again.
//
// Reproduced live during the sweep. A project indexed through
// LEANKG_DB_PATH=/tmp/store.sqlite holds `main.go::Alpha`. Running
// `leankg annotate "main.go::Alpha"` from that project WITHOUT the env var
// answers:
//
//	no indexed element matches "main.go::Alpha" — annotations attach to an
//	element the index has, so run `leankg index .` if this project is stale, or
//	`leankg query <name>` to find the right qualified_name
//
// …while the project-scoped store the call actually opened holds a different
// index. The message is accurate about what it searched and useless about the
// only thing the caller needs: WHICH store it searched. Two situations —
// a typo, and the wrong store — produce identical words, and they have opposite
// fixes.
//
// This is the same class the loop has now hit three times, at three layers: wave
// 1 (one verb reading a store path only `serve` honoured), wave 10 (freshness
// that could not see its own staleness), and here — a diagnosis whose own
// recovery step shares its blind spot. The answer already exists on the handle
// (`store.Path()`), and `status` already prints it as `store`; the refusal just
// did not.
func TestTheRefusalNamesTheStoreItSearched(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package p\n\nfunc Alpha() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := runCLIIn(t, dir, "index", "."); code != 0 {
		t.Fatalf("index: %d\n%s\n%s", code, out, errOut)
	}

	_, stderr, code := runCLIIn(t, dir, "annotate", "no.such.go::Nope", "--description", "x")
	if code == 0 {
		t.Fatal("a missing element must still be refused")
	}
	body := stderr
	// The refusal must name the STORE it searched, so a caller looking in the
	// wrong place can tell that from a typo.
	if !strings.Contains(body, ".leankg") {
		t.Errorf("the refusal must name the store it searched, or a caller looking in the wrong store cannot tell it from a typo:\n%s", body)
	}
	// And the advice must still name the index command, which is the other way
	// out of the same ambiguity.
	if !strings.Contains(body, "leankg index") {
		t.Errorf("the refusal must keep naming the way to rebuild the index:\n%s", body)
	}
}
