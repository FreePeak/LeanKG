package ccmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

const fixedMTime = 1_700_000_000

func TestParseDocKind(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    Kind
	}{
		{"nested under metadata is feedback", "---\nmetadata:\n  type: feedback\n---\nbody\n", KindFeedback},
		{"top-level type user", "---\ntype: user\n---\nbody\n", KindUser},
		{"quoted type value", "---\nmetadata:\n  type: \"project\"\n---\nbody\n", KindProject},
		{"nested reference", "---\nmetadata:\n  type: reference\n---\nbody\n", KindReference},
		{"unknown value is unknown", "---\ntype: scratch\n---\nbody\n", KindUnknown},
		{"no frontmatter is unknown", "plain note\n", KindUnknown},
		{"horizontal rule in body is not frontmatter", "Intro line.\n---\ntype: user\n---\nAfter.\n", KindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseDoc("-work-x", "f.md", []byte(tc.content), 0).Kind; got != tc.want {
				t.Fatalf("Kind = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDocFallbacks(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantName string
		wantDesc string
	}{
		{"name and description from frontmatter", "---\nname: Prefer tabs\ndescription: Use tabs.\n---\nbody\n", "Prefer tabs", "Use tabs."},
		{"quoted name and description are unquoted", "---\nname: \"Quoted\"\ndescription: 'Single'\n---\nbody\n", "Quoted", "Single"},
		{"name falls back to file name", "no frontmatter\n", "notes-x", "no frontmatter"},
		{"description is first non-empty body line, trimmed", "---\ntype: user\n---\n\n   Spaced line.   \nSecond.\n", "notes-x", "Spaced line."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := ParseDoc("-work-x", "notes-x.md", []byte(tc.content), 0)
			if d.Name != tc.wantName || d.Description != tc.wantDesc {
				t.Fatalf("Name=%q Description=%q, want Name=%q Description=%q", d.Name, d.Description, tc.wantName, tc.wantDesc)
			}
		})
	}
}

func TestParseDocBodyAndLinks(t *testing.T) {
	content := "---\ntype: user\n---\n\n[[b]] and [[a]] and [[b]] and [[a|alias]]\n"
	d := ParseDoc("-work-x", "f.md", []byte(content), 42)
	if want := "[[b]] and [[a]] and [[b]] and [[a|alias]]"; d.Body != want {
		t.Fatalf("Body = %q, want %q", d.Body, want)
	}
	if want := []string{"b", "a"}; !reflect.DeepEqual(d.Links, want) {
		t.Fatalf("Links = %v, want %v", d.Links, want)
	}
	if d.ModTime != 42 || d.Slug != "-work-x" || d.File != "f.md" || d.SourceHash != "" {
		t.Fatalf("passthrough fields wrong: %+v", d)
	}
}

// buildFixture lays out three project directories under a projects root and
// returns the root. Every file is set to fixedMTime.
func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	writeFile(t, secret, "---\ntype: user\n---\nnot under the root\n")

	alpha := filepath.Join(root, "-work-alpha", "memory")
	beta := filepath.Join(root, "-work-beta", "memory")
	gamma := filepath.Join(root, "-work-gamma", "memory")
	for _, dir := range []string{alpha, beta, gamma} {
		writeFile(t, filepath.Join(dir, "MEMORY.md"), "- [index](index.md) - one line per memory\n")
	}

	files := map[string]string{
		filepath.Join(alpha, "prefer-tabs.md"): "---\nname: Prefer tabs\ndescription: Use tabs for indentation.\nmetadata:\n  type: feedback\n---\n" +
			"Use tabs.\n\nSee [[style-guide]] and [[style-guide|guide]] and [[prefer-tabs#x]].\n",
		filepath.Join(alpha, "user-role.md"):        "---\ntype: user\n---\nFirst line here.\nSecond line.\n",
		filepath.Join(beta, "api-ref.md"):           "---\nmetadata:\n  type: reference\n---\n\nReference notes for the API.\nSee [[service-map]].\n",
		filepath.Join(beta, "deploy-window.md"):     "---\nname: Deploy window\ndescription: Freeze on Fridays.\nmetadata:\n  type: project\n---\nFreeze all deploys on Fridays.\nSee [[release-process]].\n",
		filepath.Join(beta, "notes-plain.md"):       "Plain note first line.\n\nMore text.\n",
		filepath.Join(beta, "oversize.md"):          "---\ntype: project\n---\n" + strings.Repeat("x", 65*1024),
		filepath.Join(beta, "bad-utf8.md"):          "---\ntype: project\n---\n\xff\xfe\n",
		filepath.Join(beta, "nested", "ignored.md"): "---\ntype: user\n---\nin a subdirectory\n",
		filepath.Join(gamma, "ci-quirk.md"):         "---\nname: CI quirk\ndescription: Cache resets nightly.\nmetadata:\n  type: project\n---\nRestore the cache after the nightly reset.\n",
	}
	mtime := time.Unix(fixedMTime, 0)
	for path, content := range files {
		writeFile(t, path, content)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, filepath.Join(gamma, "outside-link.md")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscoverFixture(t *testing.T) {
	root := buildFixture(t)
	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("Discover error: %v", err)
	}
	if got.RootMissing {
		t.Fatal("RootMissing = true for an existing root")
	}

	beta := filepath.Join(root, "-work-beta", "memory")
	gamma := filepath.Join(root, "-work-gamma", "memory")
	want := []Doc{
		{Slug: "-work-alpha", File: "prefer-tabs.md", Name: "Prefer tabs", Description: "Use tabs for indentation.",
			Kind: KindFeedback, Body: "Use tabs.\n\nSee [[style-guide]] and [[style-guide|guide]] and [[prefer-tabs#x]].",
			Links: []string{"style-guide", "prefer-tabs"}, ModTime: fixedMTime},
		{Slug: "-work-alpha", File: "user-role.md", Name: "user-role", Description: "First line here.",
			Kind: KindUser, Body: "First line here.\nSecond line.", ModTime: fixedMTime},
		{Slug: "-work-beta", File: "api-ref.md", Name: "api-ref", Description: "Reference notes for the API.",
			Kind: KindReference, Body: "Reference notes for the API.\nSee [[service-map]].", Links: []string{"service-map"}, ModTime: fixedMTime},
		{Slug: "-work-beta", File: "deploy-window.md", Name: "Deploy window", Description: "Freeze on Fridays.",
			Kind: KindProject, Body: "Freeze all deploys on Fridays.\nSee [[release-process]].", Links: []string{"release-process"}, ModTime: fixedMTime},
		{Slug: "-work-beta", File: "notes-plain.md", Name: "notes-plain", Description: "Plain note first line.",
			Kind: KindUnknown, Body: "Plain note first line.\n\nMore text.", ModTime: fixedMTime},
		{Slug: "-work-gamma", File: "ci-quirk.md", Name: "CI quirk", Description: "Cache resets nightly.",
			Kind: KindProject, Body: "Restore the cache after the nightly reset.", ModTime: fixedMTime},
	}
	for i := range want {
		raw, err := os.ReadFile(filepath.Join(root, want[i].Slug, "memory", want[i].File))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		want[i].FileSHA = hex.EncodeToString(sum[:])
	}
	if !reflect.DeepEqual(got.Docs, want) {
		t.Fatalf("Docs mismatch\n got: %+v\nwant: %+v", got.Docs, want)
	}

	wantSkipped := []struct{ path, reason string }{
		{filepath.Join(beta, "bad-utf8.md"), "UTF-8"},
		{filepath.Join(beta, "oversize.md"), "64 KiB"},
		{filepath.Join(gamma, "outside-link.md"), "outside"},
	}
	if len(got.Skipped) != len(wantSkipped) {
		t.Fatalf("Skipped = %+v, want %d entries", got.Skipped, len(wantSkipped))
	}
	for i, w := range wantSkipped {
		if got.Skipped[i].Path != w.path || !strings.Contains(got.Skipped[i].Reason, w.reason) {
			t.Fatalf("Skipped[%d] = %+v, want path %q and reason containing %q", i, got.Skipped[i], w.path, w.reason)
		}
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	got, err := Discover(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover error for a missing root: %v", err)
	}
	if !got.RootMissing || len(got.Docs) != 0 || len(got.Skipped) != 0 {
		t.Fatalf("got %+v, want RootMissing with no docs or skips", got)
	}
}

// makeUnreadable removes every permission from path and returns a restore
// func that puts the original mode back. The restore also runs at cleanup, so a
// failing test does not leave a directory behind that cannot be removed. It
// skips when the process is root, which can read anything.
func makeUnreadable(t *testing.T, path string) (restore func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := fi.Mode().Perm()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	restore = func() { _ = os.Chmod(path, mode) }
	t.Cleanup(restore)
	return restore
}

func TestDiscoverRecordsSlugsWhoseScanFailed(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "-work-ok", "memory", "a.md"), "---\ntype: user\n---\nok\n")
	writeFile(t, filepath.Join(root, "-work-unreadable", "memory", "b.md"), "---\ntype: user\n---\nlocked\n")
	writeFile(t, filepath.Join(root, "-work-hidden", "memory", "c.md"), "---\ntype: user\n---\nhidden\n")
	if err := os.MkdirAll(filepath.Join(root, "-work-nomemory"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeUnreadable(t, filepath.Join(root, "-work-unreadable", "memory"))
	makeUnreadable(t, filepath.Join(root, "-work-hidden"))

	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if want := []string{"-work-hidden", "-work-unreadable"}; !reflect.DeepEqual(got.FailedSlugs, want) {
		t.Fatalf("FailedSlugs = %v, want %v (a project with no memory directory is not a failure)", got.FailedSlugs, want)
	}
	if len(got.Docs) != 1 || got.Docs[0].Slug != "-work-ok" {
		t.Errorf("docs = %+v, want only the readable project's document", got.Docs)
	}
}

// countingContext reports context.Canceled from its cancelAt-th Err call on.
// Discover checks Err once per project directory and once per file, so calls
// counts how far a scan got before it stopped. No timing is involved.
type countingContext struct {
	context.Context
	calls, cancelAt int
}

func (c *countingContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

// buildManyProjects writes docsEach memory files into each of projects project
// directories under root and returns the number of files written.
func buildManyProjects(t *testing.T, root string, projects, docsEach int) int {
	t.Helper()
	for p := range projects {
		for d := range docsEach {
			writeFile(t, filepath.Join(root, fmt.Sprintf("-work-%03d", p), "memory", fmt.Sprintf("note-%02d.md", d)), "---\ntype: user\n---\nbody\n")
		}
	}
	return projects * docsEach
}

func TestDiscoverStopsAtCancelledContext(t *testing.T) {
	root := t.TempDir()
	total := buildManyProjects(t, root, 40, 5)
	ctx := &countingContext{Context: context.Background(), cancelAt: 10}

	got, err := Discover(ctx, root)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover error = %v, want context.Canceled", err)
	}
	if len(got.Docs) != 0 {
		t.Errorf("a cancelled scan returned %d docs, want none (a partial scan must not look complete)", len(got.Docs))
	}
	if ctx.calls > ctx.cancelAt {
		t.Errorf("Discover checked the context %d times, want it to stop at check %d of %d files", ctx.calls, ctx.cancelAt, total)
	}
}

func TestDiscoverExpiredContextReadsNothing(t *testing.T) {
	root := t.TempDir()
	buildManyProjects(t, root, 40, 5)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := Discover(ctx, root)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Discover error = %v, want context.Canceled", err)
	}
	if len(got.Docs) != 0 || len(got.Skipped) != 0 || len(got.FailedSlugs) != 0 {
		t.Errorf("an expired context read files: %+v", got)
	}
}

// TestDiscoverSkipsExcludedSlug adds a memory directory for an excluded project.
// Discover must neither return its documents nor report it as scanned or failed.
func TestDiscoverSkipsExcludedSlug(t *testing.T) {
	root := buildFixture(t)
	excluded := claudecode.Slug(syntheticExcluded)
	writeFile(t, filepath.Join(root, excluded, "memory", "secret-note.md"), "---\ntype: user\n---\nsynthetic excluded body\n")
	setExcludeFile(t, syntheticExcluded+"\n")

	got, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(got.Docs) != 6 {
		t.Errorf("docs = %d, want the 6 fixture documents without the excluded project", len(got.Docs))
	}
	for _, d := range got.Docs {
		if d.Slug == excluded {
			t.Errorf("Discover returned a document of the excluded slug %s: %s", excluded, d.File)
		}
	}
	for _, s := range got.Skipped {
		if slugOf(root, s.Path) == excluded {
			t.Errorf("excluded slug appears in Skipped: %+v", s)
		}
	}
	if slices.Contains(got.FailedSlugs, excluded) {
		t.Errorf("excluded slug appears in FailedSlugs: %v", got.FailedSlugs)
	}
}
