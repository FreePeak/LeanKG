package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnnotateAndLinkRefuseAnElementTheIndexDoesNotHave pins the two write
// verbs that take a `qualified_name`, found by wave 16 driving every verb the
// product exposes for the first time.
//
// The defect. `leankg annotate` and `leankg link` are the ONLY ways an agent
// records business knowledge against a specific element — the prose a code
// reader cannot get from the source. Both take a `qualified_name` and neither
// checks that the index has one:
//
//	$ leankg annotate "no.such.go::Nope" --description x
//	Updated annotation for 'no.such.go::Nope'
//	$ leankg link "no.such.go::Nope" STORY-1
//	Linked 'no.such.go::Nope' to story STORY-1
//
// Both report SUCCESS for an element that does not exist, and both persist the
// record. This is the silent-write class waves 6, 7, 11 and 15 exist to remove,
// and it is the worst instance yet: an annotation is the one place a human
// writes what the code does NOT say, so an annotation attached to a typo'd or
// renamed identifier is invisible forever — it never surfaces in a query (the
// element is not in the graph), never appears in `search-annotations` unless
// the agent guesses to grep for it, and is never migrated when the element is
// renamed. The knowledge is not merely wrong; it is unreachable.
//
// The fix refuses a `qualified_name` the index does not contain, BEFORE writing,
// and says so with the way out. It does not rename, does not suggest a
// near-match, and does not warn-and-continue: an annotation is cheap to redo
// and expensive to discover later, so the refusal is the cheap side.
func TestAnnotateAndLinkRefuseAnElementTheIndexDoesNotHave(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package p\n\nfunc Real() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := runCLIIn(t, dir, "index", "."); code != 0 {
		t.Fatalf("index: %d\n%s\n%s", code, out, errOut)
	}
	const real = "p.go::Real"

	t.Run("a real element still annotates and links", func(t *testing.T) {
		if out, errOut, code := runCLIIn(t, dir, "annotate", real, "--description", "the real thing"); code != 0 {
			t.Fatalf("annotate on a real element must work (exit %d):\n%s\n%s", code, out, errOut)
		}
		if out, errOut, code := runCLIIn(t, dir, "link", real, "STORY-1"); code != 0 {
			t.Fatalf("link on a real element must work (exit %d):\n%s\n%s", code, out, errOut)
		}
	})

	for _, tc := range []struct {
		name, verb string
		args       []string
	}{
		{"annotate a missing element", "annotate", []string{"annotate", "no.such.go::Nope", "--description", "x"}},
		{"annotate an empty qualified name", "annotate", []string{"annotate", "", "--description", "x"}},
		{"link a missing element", "link", []string{"link", "no.such.go::Nope", "STORY-1"}},
		{"link an empty qualified name", "link", []string{"link", "", "STORY-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLIIn(t, dir, tc.args...)
			body := stdout + stderr
			if code == 0 {
				t.Fatalf("%s on an element the index does not have must fail, got exit 0:\n%s", tc.verb, body)
			}
			if strings.Contains(stdout, "Created annotation") || strings.Contains(stdout, "Updated annotation") ||
				strings.Contains(stdout, "Linked") {
				t.Errorf("%s reported success for a missing element:\n%s", tc.verb, body)
			}
			// The refusal must say the element is not in the index, and how to
			// find the right one — an agent that only sees "not found" cannot
			// tell a typo from a stale index.
			if !strings.Contains(strings.ToLower(body), "index") {
				t.Errorf("the refusal must name the index as the reason:\n%s", body)
			}
		})
	}

	// And nothing was persisted for the rejected ones: a refused write leaves
	// no orphan annotation, which is the whole point of refusing.
	if out, _, _ := runCLIIn(t, dir, "search-annotations", "x"); strings.Contains(out, "Nope") {
		t.Errorf("a refused annotation must not be persisted:\n%s", out)
	}
}
