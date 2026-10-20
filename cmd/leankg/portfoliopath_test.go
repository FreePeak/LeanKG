package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegisterProjectRefusesAPathThatIsNotThere pins the one precondition
// `register-project` was missing while its sibling verb had it.
//
// The defect, found by driving the portfolio verbs (wave 7b — the registry
// surface this loop had not touched). `leankg index ./nope` refused a
// non-existent path after wave 2, precisely because a typo there is a silent
// empty index. `register-project` had no such check, so:
//
//	leankg register-project /tmp/does-not-exist
//	  registered nonexistent (/tmp/does-not-exist): elements=0 files=0 last_indexed=never indexed
//	  note: /tmp/does-not-exist has no readable store yet — run `leankg index …`
//	  exit 0
//
// The output is worse than useless for a fleet registry: it tells the operator
// to index a path that does not exist, and the entry is now a permanent member
// of the hot-set manifest that can never resolve. The "registered, not indexed"
// posture the comment describes is for a REAL directory that has no store yet —
// that is a legitimate state and stays legal. A path that is not there is a
// typo, and it must fail the way `index` fails.
func TestRegisterProjectRefusesAPathThatIsNotThere(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	reg := filepath.Join(t.TempDir(), "portfolio.db")
	t.Setenv("LEANKG_PORTFOLIO_DB", reg)

	base := t.TempDir()
	good := filepath.Join(base, "real")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "p.go"),
		[]byte("package p\n\nfunc P() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "typo")

	// A missing path is refused, by name, and writes nothing to the registry.
	stdout, stderr, code := runCLIIn(t, base, "register-project", missing)
	body := stdout + stderr
	if code == 0 {
		t.Fatalf("registering a path that does not exist must fail, got exit 0:\n%s", body)
	}
	if !strings.Contains(body, "typo") {
		t.Errorf("the refusal must name the path it refused:\n%s", body)
	}
	if strings.Contains(stdout, "registered") {
		t.Errorf("a refused registration must not report success:\n%s", stdout)
	}
	if _, err := os.Stat(reg); err == nil {
		body, _ := os.ReadFile(reg)
		if strings.Contains(string(body), "typo") {
			t.Errorf("a refused path must leave no registry row: %s", body)
		}
	}

	// The legitimate posture still works: a REAL directory with no store is
	// registrable and says so, which is what the comment promises.
	if out, errOut, code := runCLIIn(t, base, "register-project", good); code != 0 {
		t.Fatalf("a real un-indexed directory must stay registrable (exit %d):\n%s\n%s", code, out, errOut)
	} else if !strings.Contains(out, "never indexed") {
		t.Errorf("an un-indexed project must report 'never indexed':\n%s", out)
	}
}
