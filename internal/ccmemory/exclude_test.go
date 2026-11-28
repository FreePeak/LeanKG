package ccmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// syntheticExcluded is a fake project path. Its slug is the one Claude Code
// gives a project at that path.
const syntheticExcluded = "/tmp/synthetic-excluded-project"

// setExcludeFile points LEANKG_MEMORY_EXCLUDE_FILE at a new file holding content.
func setExcludeFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory-exclude")
	writeFile(t, path, content)
	t.Setenv("LEANKG_MEMORY_EXCLUDE_FILE", path)
}

func TestExcludeMissingFileExcludesNothing(t *testing.T) {
	t.Setenv("LEANKG_MEMORY_EXCLUDE_FILE", filepath.Join(t.TempDir(), "absent"))

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList with no file: %v", err)
	}
	if ex.excludes(claudecode.Slug(syntheticExcluded)) {
		t.Error("a missing exclude file excluded a slug, want no exclusions")
	}
	projects, mem := applySetup(t)
	if res := runSync(t, mem, syncOpts(projects, mem)); res.Documents != 6 {
		t.Errorf("documents with no exclude file = %d, want all 6 fixture documents", res.Documents)
	}
}

func TestExcludeDefaultPathIsUnderHomeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEANKG_MEMORY_EXCLUDE_FILE", "")
	writeFile(t, filepath.Join(home, ".config", "leankg", "memory-exclude"), syntheticExcluded+"\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList from the default path: %v", err)
	}
	if !ex.excludes(claudecode.Slug(syntheticExcluded)) {
		t.Error("the default ~/.config/leankg/memory-exclude was not read")
	}
}

func TestExcludeFileFormatAndPrefixMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	setExcludeFile(t, "# company boundary\n\n   "+syntheticExcluded+"/   \n~/synthetic-home-project\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	excluded := claudecode.Slug(syntheticExcluded)
	for slug, want := range map[string]bool{
		excluded:               true, // trailing slash and whitespace stripped
		excluded + "-worktree": true, // a prefix of the slug counts as excluded
		claudecode.Slug(filepath.Join(home, "synthetic-home-project")): true, // ~ expanded
		"-tmp-synthetic":               false, // a parent of the excluded slug is not excluded
		"-tmp-synthetic-other-project": false,
		"":                             false,
	} {
		if got := ex.excludes(slug); got != want {
			t.Errorf("excludes(%q) = %v, want %v", slug, got, want)
		}
	}
}

// TestExcludeUnderscoreLineMatchesDashDirectory: Claude Code names the project
// directory of a path with '_' or ' ' by turning each such character into '-'.
// The path does not exist, so only its as-written slug is matched.
func TestExcludeUnderscoreLineMatchesDashDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setExcludeFile(t, "/tmp/tmp.nhqY1EBK2e/syn_proj x@y+z.v2\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	const dashDir = "-tmp-tmp-nhqY1EBK2e-syn-proj-x-y-z-v2"
	if !ex.excludes(dashDir) {
		t.Errorf("excludes(%q) = false, want true: an underscore/space/@/+ line must match the dash-form directory", dashDir)
	}
}

// TestExcludeMatchesResolvedSymlinkSlug: Claude Code names a project by its
// symlink-resolved working directory (macOS /tmp is /private/tmp). The exclude
// line names a symlink, so the directory Claude Code writes is the target's slug.
func TestExcludeMatchesResolvedSymlinkSlug(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	realDir := filepath.Join(t.TempDir(), "syn_proj real")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-to-proj")
	if err := os.Symlink(realDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	setExcludeFile(t, link+"\n")
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatal(err)
	}

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	for _, slug := range []string{claudecode.Slug(resolved), claudecode.Slug(link)} {
		if !ex.excludes(slug) {
			t.Errorf("excludes(%q) = false, want true for the symlink line", slug)
		}
	}
}

// TestExcludeMatchesResolvedTempDirSlug: a real temp dir, which on macOS lies
// under /var that resolves to /private/var. Its resolved slug must be excluded.
func TestExcludeMatchesResolvedTempDirSlug(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	setExcludeFile(t, dir+"\n")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	if !ex.excludes(claudecode.Slug(resolved)) {
		t.Errorf("excludes(%q) = false, want true for the resolved form of %q", claudecode.Slug(resolved), dir)
	}
}

// TestExcludeUnreadableFailsClosed points the exclude file at a directory: it
// exists, but it cannot be read as a file. Neither Discover nor Sync may go on
// without the list, and Sync must write nothing.
func TestExcludeUnreadableFailsClosed(t *testing.T) {
	projects, mem := applySetup(t)
	t.Setenv("LEANKG_MEMORY_EXCLUDE_FILE", t.TempDir())

	if _, err := Discover(context.Background(), projects); err == nil {
		t.Error("Discover with an unreadable exclude file returned nil error, want an error")
	}
	if _, err := Sync(context.Background(), mem, syncOpts(projects, mem)); err == nil {
		t.Error("Sync with an unreadable exclude file returned nil error, want an error")
	}
	if n := ccRowCount(t, mem); n != 0 {
		t.Errorf("cc- rows after a failed sync = %d, want 0: a sync that cannot read the list writes nothing", n)
	}
}

// TestExcludeRejectsUnsafeLines fails closed. A relative line matches nothing
// and the root or home line matches every project, so the list is refused.
// The error names the file and the line number, never the line text.
func TestExcludeRejectsUnsafeLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct{ name, line string }{
		{"relative path", "work/synthetic-acme"},
		{"dot", "."},
		{"filesystem root", "/"},
		{"root through dot-dot", "/synthetic/.."},
		{"home alone", "~"},
		{"home with slash", "~/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setExcludeFile(t, "# comment\n"+syntheticExcluded+"\n"+tc.line+"\n")

			_, err := loadExcludeList()
			if err == nil {
				t.Fatalf("loadExcludeList with line %q = nil error, want refusal", tc.line)
			}
			if msg := err.Error(); !strings.Contains(msg, "line 3") {
				t.Errorf("error %q, want the line number 3", msg)
			} else if strings.Contains(msg, home) || strings.Contains(msg, syntheticExcluded) {
				t.Errorf("error %q leaks a path", msg)
			}
		})
	}
}

// pathWithSlugLen returns a synthetic absolute path whose slug is n characters
// long. Every character is ASCII alphanumeric or '-', so the slug length equals
// the path length.
func pathWithSlugLen(t *testing.T, n int) string {
	t.Helper()
	path := "/synthetic-" + strings.Repeat("p", n-len("/synthetic-"))
	if got := len(claudecode.Slug(path)); got != n {
		t.Fatalf("slug length = %d, want %d", got, n)
	}
	return path
}

// syntheticHash stands in for the hash Claude Code appends to a directory name
// that it truncates. The matcher must not depend on its value.
const syntheticHash = "0123456789abcdef"

// TestExcludeCapsSlugAt200Characters: Claude Code truncates a project directory
// name to 200 characters and appends a hash. Matching is by prefix, so the first
// 200 characters of an excluded slug match that directory.
func TestExcludeCapsSlugAt200Characters(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	slug := claudecode.Slug(pathWithSlugLen(t, 260))
	setExcludeFile(t, pathWithSlugLen(t, 260)+"\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	dirName := slug[:200] + "-" + syntheticHash
	if !ex.excludes(dirName) {
		t.Errorf("excludes(%d chars) = false, want true: the first 200 characters of the slug must match a truncated directory name", len(dirName))
	}
	differs := slug[:100] + "x" + slug[101:200] + "-" + syntheticHash
	if ex.excludes(differs) {
		t.Errorf("excludes(%q) = true, want false: a name that differs inside the first 200 characters is another project", differs)
	}
}

// TestExcludeKeepsSlugShorterThan200 guards the cap: a 150-character slug is
// kept whole, so a name that stops short of it is not matched.
func TestExcludeKeepsSlugShorterThan200(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := pathWithSlugLen(t, 150)
	setExcludeFile(t, path+"\n")
	slug := claudecode.Slug(path)

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	if !ex.excludes(slug + "-" + syntheticHash) {
		t.Errorf("excludes(slug + suffix) = false, want true for a 150-character slug")
	}
	if ex.excludes(slug[:149]) {
		t.Errorf("excludes(slug[:149]) = true, want false: a 150-character slug must not be cut shorter")
	}
}

// TestExcludeCapsEveryPrefixIncludingRedacted: no stored prefix may exceed 200
// characters, whichever form it came from (as written, resolved or Redact-masked).
func TestExcludeCapsEveryPrefixIncludingRedacted(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	setExcludeFile(t, pathWithSlugLen(t, 260)+"\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	for _, p := range ex.prefixes {
		if len(p) > 200 {
			t.Errorf("prefix has %d characters, want at most 200", len(p))
		}
	}
}

// TestExcludeMatchesNonexistentChildOfSymlink: a project deleted under a
// symlinked prefix (macOS /tmp is /private/tmp) has no Claude directory under
// its as-written name. Its longest existing ancestor resolves, so the resolved
// slug must match too.
func TestExcludeMatchesNonexistentChildOfSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	realBase := t.TempDir()
	link := filepath.Join(t.TempDir(), "link-to-base")
	if err := os.Symlink(realBase, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	const child = "gone-proj"
	setExcludeFile(t, filepath.Join(link, child)+"\n")
	resolvedBase, err := filepath.EvalSymlinks(realBase)
	if err != nil {
		t.Fatal(err)
	}

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	for _, slug := range []string{
		claudecode.Slug(filepath.Join(link, child)),
		claudecode.Slug(filepath.Join(resolvedBase, child)),
	} {
		if !ex.excludes(slug) {
			t.Errorf("excludes(%q) = false, want true for the nonexistent child of the symlink", slug)
		}
	}
}

// TestExcludeNonexistentAbsolutePathMatchesAsWritten: a path with no existing
// ancestor below the root keeps its as-written slug.
func TestExcludeNonexistentAbsolutePathMatchesAsWritten(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const path = "/synthetic-absent-root-ccmemory/gone-proj"
	setExcludeFile(t, path+"\n")

	ex, err := loadExcludeList()
	if err != nil {
		t.Fatalf("loadExcludeList: %v", err)
	}
	if !ex.excludes(claudecode.Slug(path)) {
		t.Errorf("excludes(%q) = false, want true for the as-written slug of an absent path", claudecode.Slug(path))
	}
}
