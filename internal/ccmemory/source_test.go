package ccmemory

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	got, err := Discover(root)
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
	got, err := Discover(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Discover error for a missing root: %v", err)
	}
	if !got.RootMissing || len(got.Docs) != 0 || len(got.Skipped) != 0 {
		t.Fatalf("got %+v, want RootMissing with no docs or skips", got)
	}
}
