package ccmemory

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// discoverDocs maps "slug/file" to the document Discover reads under root.
func discoverDocs(t *testing.T, root string) map[string]Doc {
	t.Helper()
	disc, err := Discover(context.Background(), root)
	if err != nil {
		t.Fatalf("Discover(%s): %v", root, err)
	}
	out := map[string]Doc{}
	for _, d := range disc.Docs {
		out[d.Slug+"/"+d.File] = d
	}
	return out
}

func runRestore(t *testing.T, mem *memory.Memory, opts RestoreOptions) RestoreResult {
	t.Helper()
	res, err := Restore(context.Background(), mem, opts)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return res
}

// TestRestoreRoundTrip wipes the Claude Code memory tree after a sync, restores
// it into a new directory and checks that Discover reads back the same name,
// kind, description and body for every file.
func TestRestoreRoundTrip(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	want := discoverDocs(t, projects)
	if len(want) == 0 {
		t.Fatal("setup: no documents discovered")
	}
	if err := os.RemoveAll(projects); err != nil {
		t.Fatal(err)
	}

	into := t.TempDir()
	res := runRestore(t, mem, RestoreOptions{Projects: projects, Into: into})
	if res.Written != len(want) || res.Skipped != 0 || res.ArchivedIncluded != 0 {
		t.Fatalf("counts = %+v, want written %d and nothing else", res, len(want))
	}

	got := discoverDocs(t, into)
	if len(got) != len(want) {
		t.Fatalf("restored %d documents, want %d", len(got), len(want))
	}
	for key, w := range want {
		g, ok := got[key]
		if !ok {
			t.Errorf("%s was not restored", key)
			continue
		}
		if g.Name != w.Name || g.Kind != w.Kind || g.Description != w.Description || g.Body != w.Body {
			t.Errorf("%s: restored name/kind/description/body = %q/%q/%q/%q, want %q/%q/%q/%q",
				key, g.Name, g.Kind, g.Description, g.Body, w.Name, w.Kind, w.Description, w.Body)
		}
	}

	dir := filepath.Join(into, "-work-alpha", "memory")
	assertMode(t, dir, 0o700)
	assertMode(t, filepath.Join(dir, "prefer-tabs.md"), 0o600)
	index := readFileString(t, filepath.Join(dir, memoryIndex))
	if !strings.Contains(index, "- [Prefer tabs](prefer-tabs.md) - Use tabs for indentation.\n") {
		t.Errorf("MEMORY.md does not index prefer-tabs.md:\n%s", index)
	}
}

func assertMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != want {
		t.Errorf("%s mode = %o, want %o", path, fi.Mode().Perm(), want)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRestoreNeverOverwrites restores over files that already exist. Those
// files and their MEMORY.md must keep their bytes, and a second run writes
// nothing.
func TestRestoreNeverOverwrites(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	total := len(discoverDocs(t, projects))

	into := t.TempDir()
	dir := filepath.Join(into, "-work-alpha", "memory")
	existing := filepath.Join(dir, "prefer-tabs.md")
	index := filepath.Join(dir, memoryIndex)
	writeFile(t, existing, "synthetic-existing-body\n")
	writeFile(t, index, "synthetic-existing-index\n")

	res := runRestore(t, mem, RestoreOptions{Projects: projects, Into: into})
	if res.Skipped != 1 || res.Written != total-1 {
		t.Fatalf("first run counts = %+v, want written %d and skipped 1", res, total-1)
	}
	if got := readFileString(t, existing); got != "synthetic-existing-body\n" {
		t.Errorf("existing memory file was overwritten: %q", got)
	}
	if got := readFileString(t, index); got != "synthetic-existing-index\n" {
		t.Errorf("existing MEMORY.md was overwritten: %q", got)
	}

	again := runRestore(t, mem, RestoreOptions{Projects: projects, Into: into})
	if again.Written != 0 || again.Skipped != total {
		t.Errorf("second run counts = %+v, want written 0 and skipped %d", again, total)
	}
}

// TestRestoreDeletedIncludesArchive removes a memory file and syncs, so its
// row is archived. A plain restore leaves that file out. With Deleted it is
// restored from the archive and counted as archived-included.
func TestRestoreDeletedIncludesArchive(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	want := discoverDocs(t, projects)
	const gone = "-work-beta/deploy-window.md"
	if err := os.Remove(memFile(projects, "-work-beta", "deploy-window.md")); err != nil {
		t.Fatal(err)
	}
	runSync(t, mem, syncOpts(projects, mem))
	if _, ok := archivedRows(t, mem)[DocID("-work-beta", "deploy-window.md")]; !ok {
		t.Fatal("setup: the removed file was not archived")
	}

	live := t.TempDir()
	res := runRestore(t, mem, RestoreOptions{Projects: projects, Into: live})
	if res.Written != len(want)-1 || res.ArchivedIncluded != 0 {
		t.Fatalf("plain restore counts = %+v, want written %d and no archive", res, len(want)-1)
	}
	if _, err := os.Stat(filepath.Join(live, "-work-beta", "memory", "deploy-window.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("plain restore wrote the archived file: %v", err)
	}

	withArchive := t.TempDir()
	res = runRestore(t, mem, RestoreOptions{Projects: projects, Into: withArchive, Deleted: true})
	if res.Written != len(want) || res.ArchivedIncluded != 1 {
		t.Fatalf("--deleted counts = %+v, want written %d and archived-included 1", res, len(want))
	}
	got, ok := discoverDocs(t, withArchive)[gone]
	if !ok {
		t.Fatalf("%s was not restored from the archive", gone)
	}
	w := want[gone]
	if got.Name != w.Name || got.Kind != w.Kind || got.Description != w.Description || got.Body != w.Body {
		t.Errorf("archived file restored as %+v, want %+v", got, w)
	}
}

// TestRestoreRejectsTraversalSlug plants rows whose project or file name climbs
// out of the target directory. The restore must refuse the run and write nothing.
func TestRestoreRejectsTraversalSlug(t *testing.T) {
	cases := []struct{ name, slug, file string }{
		{"slug climbs out of the target", "../escape", "note.md"},
		{"file name carries separators", "-work-alpha", "../../escape.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projects, mem := applySetup(t)
			seedRow(t, mem, tc.slug, tc.file)

			base := t.TempDir()
			_, err := Restore(context.Background(), mem, RestoreOptions{Projects: projects, Into: filepath.Join(base, "into")})
			if err == nil {
				t.Fatal("Restore accepted an unsafe row")
			}
			var files []string
			walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					files = append(files, path)
				}
				return err
			})
			if walkErr != nil {
				t.Fatal(walkErr)
			}
			if len(files) != 0 {
				t.Errorf("files written before the refusal: %v", files)
			}
		})
	}
}

// seedRow stores one live cc- row whose project and file metadata are the
// given values, as a hand-made or damaged row could carry them.
func seedRow(t *testing.T, mem *memory.Memory, slug, file string) {
	t.Helper()
	id := DocID(slug, file)
	entry := memory.Entry{
		ID:        id,
		Content:   "[project] escape - synthetic\nsynthetic-memory-body",
		Source:    claudeCodeSource,
		Timestamp: 1,
		Metadata: map[string]any{
			"document_id": id,
			"project":     slug,
			"file":        file,
			"name":        "escape",
			"description": "synthetic",
			"kind":        "project",
		},
	}
	if err := mem.RetainRaw(context.Background(), BankFor("project", slug), []memory.Entry{entry}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreRequiresInto(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	_, err := Restore(context.Background(), mem, RestoreOptions{Projects: projects})
	if !errors.Is(err, ErrRestoreIntoRequired) {
		t.Fatalf("Restore without Into: err = %v, want ErrRestoreIntoRequired", err)
	}
}

// TestRestoreRefusesRealClaudeProjectsDir restores into the projects root, a
// directory inside it and a symlink to it. Each is refused and the tree is unchanged.
func TestRestoreRefusesRealClaudeProjectsDir(t *testing.T) {
	projects, mem := applySetup(t)
	runSync(t, mem, syncOpts(projects, mem))
	before := snapshotDir(t, projects)

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(projects, link); err != nil {
		t.Fatal(err)
	}
	for _, into := range []string{projects, filepath.Join(projects, "-work-alpha"), link} {
		if _, err := Restore(context.Background(), mem, RestoreOptions{Projects: projects, Into: into}); err == nil {
			t.Errorf("Restore into %s succeeded, want refusal", into)
		}
	}
	if after := snapshotDir(t, projects); !reflect.DeepEqual(before, after) {
		t.Error("the projects tree changed during a refused restore")
	}
}

// TestRestoreSkipsExcludedRows excludes a project after it has a live row and an
// archive copy. A restore with Deleted must write nothing under the project.
func TestRestoreSkipsExcludedRows(t *testing.T) {
	projects, mem := applySetup(t)
	excluded := claudecode.Slug(syntheticExcluded)
	writeFile(t, memFile(projects, excluded, "user-note.md"), "---\ntype: user\n---\nsynthetic user body\n")
	writeFile(t, memFile(projects, excluded, "project-note.md"), "---\ntype: project\n---\nsynthetic project body\n")
	runSync(t, mem, syncOpts(projects, mem))
	if err := os.Remove(memFile(projects, excluded, "user-note.md")); err != nil {
		t.Fatal(err)
	}
	runSync(t, mem, syncOpts(projects, mem))

	setExcludeFile(t, syntheticExcluded+"\n")
	into := t.TempDir()
	res := runRestore(t, mem, RestoreOptions{Projects: projects, Into: into, Deleted: true})

	// The fixture's six other rows are written; the excluded project's live and archived rows are skipped.
	if res.SkippedExcluded != 2 || res.Written != 6 {
		t.Errorf("counts = %+v, want skipped_excluded 2 (live and archived) and written 6", res)
	}
	if _, err := os.Stat(filepath.Join(into, excluded)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("restore wrote under the excluded project: stat err = %v", err)
	}
}

// TestRestoreRefusesSymlinkUnderInto points a directory under the target at the
// Claude Code projects root, then restores a row that has no file there yet. The
// restore must refuse before any write, so the projects tree stays unchanged.
func TestRestoreRefusesSymlinkUnderInto(t *testing.T) {
	cases := []struct {
		name string
		link func(into, projects string) string // makes the symlink, returns the path it replaces
	}{
		{"project directory", func(into, projects string) string {
			link := filepath.Join(into, "-work-alpha")
			mustSymlink(t, projects, link)
			return link
		}},
		{"memory directory", func(into, projects string) string {
			if err := os.Mkdir(filepath.Join(into, "-work-alpha"), 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(into, "-work-alpha", "memory")
			mustSymlink(t, filepath.Join(projects, "-work-alpha", "memory"), link)
			return link
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projects, mem := applySetup(t)
			runSync(t, mem, syncOpts(projects, mem))
			seedRow(t, mem, "-work-alpha", "new-note.md")
			before := snapshotDir(t, projects)

			into := t.TempDir()
			tc.link(into, projects)
			_, err := Restore(context.Background(), mem, RestoreOptions{Projects: projects, Into: into})
			if err == nil {
				t.Fatal("Restore through a symlink under the target succeeded, want refusal")
			}
			if after := snapshotDir(t, projects); !reflect.DeepEqual(before, after) {
				t.Error("the projects tree changed during a refused restore")
			}
		})
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
