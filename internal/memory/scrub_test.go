package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// installScrub writes lines to a scrub file, loads it and installs the result
// process-wide. The cleanup restores an empty list so no pattern leaks into a
// later test.
func installScrub(t *testing.T, lines string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scrub.txt")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	patterns, digest, err := LoadScrubFile(path)
	if err != nil {
		t.Fatalf("LoadScrubFile: %v", err)
	}
	SetScrubPatterns(patterns, digest)
	t.Cleanup(func() { SetScrubPatterns(nil, emptyScrubDigest) })
}

const scrubPlaceholder = "SERVICE_DNS_PLACEHOLDER"

func TestScrubLoadFileSkipsBlankAndComments(t *testing.T) {
	const body = "# deny-list for the example repo\n\nSERVICE_DNS_PLACEHOLDER\n   \n# other-repo is allowed\nother-repo-[0-9]+\n"
	path := filepath.Join(t.TempDir(), "scrub.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	patterns, digest, err := LoadScrubFile(path)
	if err != nil {
		t.Fatalf("LoadScrubFile: %v", err)
	}
	if len(patterns) != 2 {
		t.Fatalf("got %d patterns, want 2 (blank and # lines skipped)", len(patterns))
	}
	if !patterns[0].MatchString(scrubPlaceholder) || !patterns[1].MatchString("other-repo-42") {
		t.Errorf("patterns do not compile to the written lines: %v", patterns)
	}
	sum := sha256.Sum256([]byte(body))
	if want := hex.EncodeToString(sum[:]); digest != want {
		t.Errorf("digest = %s, want sha256 of the file bytes %s", digest, want)
	}
}

func TestScrubLoadMissingFileIsEmpty(t *testing.T) {
	patterns, digest, err := LoadScrubFile(filepath.Join(t.TempDir(), "absent.txt"))
	if err != nil {
		t.Fatalf("missing file: err = %v, want nil", err)
	}
	if len(patterns) != 0 {
		t.Fatalf("missing file: %d patterns, want 0", len(patterns))
	}
	const sha256OfNothing = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if digest != sha256OfNothing || emptyScrubDigest != sha256OfNothing {
		t.Errorf("missing-file digest = %s (const %s), want the stable sha256 of zero bytes %s", digest, emptyScrubDigest, sha256OfNothing)
	}
}

func TestScrubLoadBadLineNamesLineNumber(t *testing.T) {
	// Line 4 is the bad one: a comment, a blank line and a valid pattern come first.
	path := filepath.Join(t.TempDir(), "scrub.txt")
	if err := os.WriteFile(path, []byte("# header\n\nSERVICE_DNS_PLACEHOLDER\n[unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patterns, digest, err := LoadScrubFile(path)
	if err == nil {
		t.Fatalf("bad line: err = nil, want an error naming line 4 (patterns=%d digest=%q)", len(patterns), digest)
	}
	if !strings.Contains(err.Error(), "line 4") {
		t.Errorf("error %q does not name line 4", err)
	}
	if strings.Contains(err.Error(), "[unclosed") {
		t.Errorf("error %q echoes the deny-list pattern; it must not", err)
	}
}

func TestScrubMasksCoreFileCreate(t *testing.T) {
	installScrub(t, scrubPlaceholder+"\n")
	m := openTest(t)
	if err := m.Create(context.Background(), "MEMORY.md", "deploy target is "+scrubPlaceholder); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.Root(), "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), scrubPlaceholder) || !strings.Contains(string(data), "[SCRUBBED]") {
		t.Errorf("core create stored %q, want the match replaced by [SCRUBBED]", data)
	}
}

func TestScrubMasksAppend(t *testing.T) {
	installScrub(t, scrubPlaceholder+"\n")
	m := openTest(t)
	ctx := context.Background()
	if err := m.Create(ctx, "topics/notes.md", "seed line\n"); err != nil {
		t.Fatal(err)
	}
	if err := m.Append(ctx, "topics/notes.md", "call "+scrubPlaceholder+" first"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(m.Root(), "topics", "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), scrubPlaceholder) || !strings.Contains(string(data), "[SCRUBBED]") {
		t.Errorf("append stored %q, want the match replaced by [SCRUBBED]", data)
	}
}

func TestScrubMasksBankRetain(t *testing.T) {
	installScrub(t, scrubPlaceholder+"\n")
	m := openTest(t)
	const bank = "scrubprobe"
	if _, err := m.Retain(context.Background(), bank, []Entry{{ID: "r1", Content: "use " + scrubPlaceholder + " for sync"}}, 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(m.bankPath(bank))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), scrubPlaceholder) || !strings.Contains(string(data), "[SCRUBBED]") {
		t.Errorf("bank retain stored %q, want the match replaced by [SCRUBBED]", data)
	}
}

// TestScrubMasksRowWrittenBeforePattern pins the read side: a row stored while
// no pattern was installed is masked when a later read returns it.
func TestScrubMasksRowWrittenBeforePattern(t *testing.T) {
	m := openTest(t)
	ctx := context.Background()
	const bank = "prepattern"
	if _, err := m.Retain(ctx, bank, []Entry{{ID: "old-1", Content: "deploy notes mention " + scrubPlaceholder}}, 1); err != nil {
		t.Fatal(err)
	}
	installScrub(t, scrubPlaceholder+"\n")
	rows, err := m.Recall(ctx, bank, "deploy", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("recall returned %d rows, want 1", len(rows))
	}
	if strings.Contains(rows[0].Content, scrubPlaceholder) || !strings.Contains(rows[0].Content, "[SCRUBBED]") {
		t.Errorf("recall returned %q, want the pre-pattern row masked on read", rows[0].Content)
	}
}

func TestScrubNoFileMeansNoChange(t *testing.T) {
	installScrub(t, "")
	const in = "call SERVICE_DNS_PLACEHOLDER with token=abc123def"
	want := telemetry.Redact(in)
	if got := Redact(in); got != want {
		t.Errorf("empty scrub list changed text: got %q, want the fixed-rules result %q", got, want)
	}
	if !strings.Contains(Redact(in), scrubPlaceholder) {
		t.Errorf("empty scrub list removed a non-secret placeholder: %q", Redact(in))
	}
}

// TestScrubIsIdempotent uses a pattern that matches the marker's own letters.
// Without protecting the marker, a second pass would rewrite [SCRUBBED] to
// [[SCRUBBED]] and the read path would drift from the write path.
func TestScrubIsIdempotent(t *testing.T) {
	installScrub(t, "\\w+\n")
	once := Redact("call SERVICE_DNS_PLACEHOLDER now")
	if once != "[SCRUBBED] [SCRUBBED] [SCRUBBED]" {
		t.Fatalf("first pass = %q, want every word masked", once)
	}
	if twice := Redact(once); twice != once {
		t.Errorf("second pass changed masked text:\n once:  %q\n twice: %q", once, twice)
	}
}

func TestScrubDigestGetter(t *testing.T) {
	SetScrubPatterns(nil, emptyScrubDigest)
	t.Cleanup(func() { SetScrubPatterns(nil, emptyScrubDigest) })
	if got := ScrubDigest(); got != emptyScrubDigest {
		t.Errorf("ScrubDigest() = %q after install, want %q", got, emptyScrubDigest)
	}
	SetScrubPatterns([]*regexp.Regexp{regexp.MustCompile("x")}, "abc")
	if got := ScrubDigest(); got != "abc" {
		t.Errorf("ScrubDigest() = %q, want the installed digest abc", got)
	}
}
