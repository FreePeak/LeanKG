package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestObsidianPushToAFileFails pins the one obsidian posture that was wrong:
// pointing the vault at something that is not a vault.
//
// The defect, found by driving `obsidian push --vault <a regular file>` (wave
// 7c). The push reported:
//
//	Push complete:
//	  Notes generated: 0
//	  Failed: 1
//	  exit 0
//
// "Failed: 1" is a real signal, but it is the same signal a caller gets when a
// note could not be written for any reason at all, and the exit code says the
// operation succeeded. A script that checks `$?` — the ordinary way to drive a
// CLI — concludes the notes were exported. They were not; every one failed,
// because a file cannot be a directory.
//
// The rule is the same one `register-project` and `index` now share: a
// destination that cannot hold the output is refused BEFORE the work starts, so
// a partial or total failure is never reported as success. A vault path that
// does not exist yet is a different case and stays legal, because `init`
// creates it and `push --vault` to a fresh directory is a normal first move.
func TestObsidianPushToAFileFails(t *testing.T) {
	t.Setenv("LEANKG_DB_ENGINE", "")
	t.Setenv("LEANKG_PG_URL", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.go"),
		[]byte("package p\n\n// AlphaDoc is the doc comment.\nfunc AlphaDoc() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := runCLIIn(t, dir, "index", "."); code != 0 {
		t.Fatalf("index: %d\n%s\n%s", code, out, errOut)
	}
	notAVault := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(notAVault, []byte("I am a file, not a vault\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLIIn(t, dir, "obsidian", "push", "--vault", notAVault)
	body := stdout + stderr
	if code == 0 {
		t.Fatalf("pushing to a regular file must fail, got exit 0:\n%s", body)
	}
	if strings.Contains(stdout, "Push complete") {
		t.Errorf("a failed push must not print 'Push complete':\n%s", body)
	}
	if !strings.Contains(body, notAVault) {
		t.Errorf("the failure must name the unusable vault path:\n%s", body)
	}

	// A path that does not exist yet is still fine: `init` creates it, and
	// pushing to a fresh directory is a normal first move. Pin it so the fix
	// cannot be "require the vault to exist".
	fresh := filepath.Join(dir, "newvault")
	if out, errOut, code := runCLIIn(t, dir, "obsidian", "init", "--vault", fresh); code != 0 {
		t.Fatalf("init into a fresh path must work (exit %d):\n%s\n%s", code, out, errOut)
	}
}
