package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateIsAPrintOnlyVerbWithoutOut pins the one `leankg generate` does
// that its usage does not say: it writes a file into the user's repository.
//
// The defect, found by wave 17 running the verb on the repository the loop
// works in. `leankg generate` is documented as "render AGENTS.md from the graph"
// and prints the body — and then, with no flag and no output path, silently
// wrote `docs/AGENTS.md` into the current working directory:
//
//	$ leankg generate
//	Generated documentation:
//	# Agent Guidelines for LeanKG
//	…
//	Saved to docs/AGENTS.md
//
// Two things are wrong with that. It is an UNANNOUNCED write: a verb whose
// documented output is stdout has a side effect on the user's tree, and the
// side effect lands in a TRACKED path (this repository's `docs/`), so the next
// `git status` shows a file the operator never asked for and never knew to
// review. And the destination is not configurable, so there is no way to send
// it somewhere harmless.
//
// This is the same class as waves 15 and 16 — a write that does not report
// itself — one level up: those wrote nothing, and this one writes something
// somewhere the caller did not name. The wave-15/16 rule generalises to the
// whole product as:
//
//	A write must report itself: what it wrote, where, and that it is opt-in.
//
// The fix follows the verbs that already get it right in this repo: print by
// default, write only when the caller names a destination (`--out`), and say so
// either way. Nothing is lost — the body is still on stdout — and a caller who
// wants the file asks for it by name.
func TestGenerateIsAPrintOnlyVerbWithoutOut(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package p\n\nfunc Thing() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := runCLIIn(t, dir, "index", "."); code != 0 {
		t.Fatalf("index: %d\n%s\n%s", code, out, errOut)
	}

	// With no --out, the body is printed and NO file appears anywhere under the
	// project. Before the fix, `docs/AGENTS.md` appeared without being asked for.
	stdout, stderr, code := runCLIIn(t, dir, "generate")
	body := stdout + stderr
	if code != 0 {
		t.Fatalf("generate must still succeed without --out (exit %d):\n%s", code, body)
	}
	if !strings.Contains(stdout, "Agent Guidelines") {
		t.Errorf("generate must still print the body, got:\n%s", body)
	}
	// No file anywhere, in the project or a docs/ subdir of it.
	for _, rel := range []string{"docs/AGENTS.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err == nil {
			t.Errorf("generate wrote %s without being asked: a verb whose documented output is stdout must not write into the repository", rel)
		}
	}

	// With --out, the write happens where the caller named it, and is reported.
	outPath := filepath.Join(dir, "out", "AGENTS.md")
	stdout, stderr, code = runCLIIn(t, dir, "generate", "--out", outPath)
	body = stdout + stderr
	if code != 0 {
		t.Fatalf("generate --out must write (exit %d):\n%s", code, body)
	}
	written, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("generate --out must create the file it was given: %v", err)
	}
	if !strings.Contains(string(written), "Agent Guidelines") {
		t.Errorf("the written file does not carry the body:\n%s", written)
	}
	if !strings.Contains(body, outPath) {
		t.Errorf("generate --out must REPORT where it wrote, got:\n%s", body)
	}
}

// TestWriteVerbRuleHoldsAcrossTheProduct is the standing check behind the
// wave-15/16 rule. It is one test over the verbs that take a name or a path, so
// a new one added later is covered by adding a case rather than by remembering.
func TestWriteVerbRuleHoldsAcrossTheProduct(t *testing.T) {
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

	// Every case: a write verb handed a target that does not exist, or nothing
	// to write, must FAIL rather than report success.
	for _, tc := range []struct {
		name, verb string
		args       []string
	}{
		{"annotate unanchored", "annotate", []string{"annotate", "no.such.go::Nope", "--description", "x"}},
		{"annotate empty target", "annotate", []string{"annotate", "", "--description", "x"}},
		{"link unanchored", "link", []string{"link", "no.such.go::Nope", "STORY-1"}},
		{"annotate nothing to say", "annotate", []string{"annotate", "p.go::Real", "--description", "   "}},
		{"obsidian push without a vault", "obsidian", []string{"obsidian", "push", "--vault", ""}},
		{"export to a path that cannot exist", "export", []string{"export", "out.json", "--out", "/nonexistent-root-zzz/x.json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCLIIn(t, dir, tc.args...)
			body := stdout + stderr
			if code == 0 {
				t.Errorf("%s reported success on an impossible write (exit 0):\n%s", tc.verb, body)
			}
		})
	}
}
