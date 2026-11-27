package ccmemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// writeFile creates the parent directories of path and writes content into it.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// linkedWorktree lays out a main checkout and a linked worktree under one
// temp dir, with the .git file, gitdir and commondir that git itself writes.
func linkedWorktree(t *testing.T) (mainRoot, wtRoot string) {
	t.Helper()
	base := t.TempDir()
	mainRoot = filepath.Join(base, "acme-app")
	gitdir := filepath.Join(mainRoot, ".git", "worktrees", "feature")
	writeFile(t, filepath.Join(gitdir, "commondir"), "../..\n")
	writeFile(t, filepath.Join(gitdir, "gitdir"), filepath.Join(base, "acme-app-feature", ".git")+"\n")
	wtRoot = filepath.Join(base, "acme-app-feature")
	writeFile(t, filepath.Join(wtRoot, ".git"), "gitdir: "+gitdir+"\n")
	return mainRoot, wtRoot
}

// withMemoryDir creates <projectsRoot>/<slug>/memory.
func withMemoryDir(t *testing.T, projectsRoot, slug string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(projectsRoot, slug, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestProjectKeyLinkedWorktreeResolvesToMainRoot(t *testing.T) {
	mainRoot, wtRoot := linkedWorktree(t)
	nested := filepath.Join(wtRoot, "internal", "pkg")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got := ProjectKey(t.TempDir(), nested)
	want := claudecode.Slug(mainRoot)
	if got != want {
		t.Fatalf("ProjectKey(worktree subdir) = %q, want main-root slug %q", got, want)
	}
}

func TestProjectKeyMainRootWithMemoryWinsOverOwnSlug(t *testing.T) {
	mainRoot, wtRoot := linkedWorktree(t)
	projects := t.TempDir()
	withMemoryDir(t, projects, claudecode.Slug(mainRoot))
	withMemoryDir(t, projects, claudecode.Slug(wtRoot))
	got := ProjectKey(projects, wtRoot)
	want := claudecode.Slug(mainRoot)
	if got != want {
		t.Fatalf("ProjectKey = %q, want main-root slug %q", got, want)
	}
}

func TestProjectKeyOwnSlugWithMemoryWinsWhenMainHasNone(t *testing.T) {
	_, wtRoot := linkedWorktree(t)
	projects := t.TempDir()
	withMemoryDir(t, projects, claudecode.Slug(wtRoot))
	got := ProjectKey(projects, wtRoot)
	want := claudecode.Slug(wtRoot)
	if got != want {
		t.Fatalf("ProjectKey = %q, want own slug %q", got, want)
	}
}

func TestProjectKeyOutsideRepoUsesOwnSlug(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes", "scratch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got := ProjectKey(t.TempDir(), dir)
	want := claudecode.Slug(dir)
	if got != want {
		t.Fatalf("ProjectKey(outside repo) = %q, want own slug %q", got, want)
	}
}

func TestProjectKeyUnreadablePointerFallsThroughToOwnSlug(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wt")
	missing := filepath.Join(t.TempDir(), "missing", "worktrees", "x")
	writeFile(t, filepath.Join(dir, ".git"), "gitdir: "+missing+"\n")
	got := ProjectKey(t.TempDir(), dir)
	want := claudecode.Slug(dir)
	if got != want {
		t.Fatalf("ProjectKey(unreadable pointer) = %q, want own slug %q", got, want)
	}
}

func TestBankForKindsSplitGlobalFromProject(t *testing.T) {
	slug := "-Users-dev-acme-app"
	project := ProjectBank(slug)
	for _, kind := range []string{"user", "feedback"} {
		if got := BankFor(kind, slug); got != GlobalBank {
			t.Errorf("BankFor(%q) = %q, want %q", kind, got, GlobalBank)
		}
	}
	for _, kind := range []string{"project", "reference", "unknown", ""} {
		if got := BankFor(kind, slug); got != project {
			t.Errorf("BankFor(%q) = %q, want project bank %q", kind, got, project)
		}
	}
}

func TestBankForProjectBankShape(t *testing.T) {
	got := ProjectBank("-Users-dev-acme-app")
	if !strings.HasPrefix(got, "cc-p-") || len(got) != len("cc-p-")+16 {
		t.Fatalf("ProjectBank = %q, want cc-p- plus 16 hex chars", got)
	}
	if got != ProjectBank("-Users-dev-acme-app") {
		t.Fatalf("ProjectBank is not deterministic")
	}
}

func TestBankForLongSharedPrefixGetsDistinctBanks(t *testing.T) {
	prefix := strings.Repeat("a", 80)
	a := BankFor("project", prefix+"-alpha")
	b := BankFor("project", prefix+"-beta")
	if a == b {
		t.Fatalf("slugs sharing an 80-char prefix got the same bank %q", a)
	}
}

func TestBankForRoundTripsThroughMemory(t *testing.T) {
	ctx := context.Background()
	m, err := memory.Open(ctx, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })

	banks := []string{GlobalBank, ProjectBank("-Users-dev-acme-app"), ProjectBank(strings.Repeat("x", 300))}
	for _, b := range banks {
		if len(b) > 64 {
			t.Fatalf("bank %q is %d chars, want <= 64", b, len(b))
		}
		row := memory.Entry{ID: DocID("-Users-dev-acme-app", "a.md"), Content: "row"}
		if err := m.RetainRaw(ctx, b, []memory.Entry{row}); err != nil {
			t.Fatalf("RetainRaw(%q): %v", b, err)
		}
	}
	listed, err := m.Banks()
	if err != nil {
		t.Fatal(err)
	}
	present := map[string]bool{}
	for _, b := range listed {
		present[b] = true
	}
	for _, b := range banks {
		if !present[b] {
			t.Errorf("bank %q did not round-trip; listed banks = %v", b, listed)
		}
	}
}

func TestDocIDShapeAndDeterminism(t *testing.T) {
	got := DocID("-Users-dev-acme-app", "project_plan.md")
	if !strings.HasPrefix(got, "ccm-") || len(got) != len("ccm-")+24 {
		t.Fatalf("DocID = %q, want ccm- plus 24 hex chars", got)
	}
	if got != DocID("-Users-dev-acme-app", "project_plan.md") {
		t.Fatalf("DocID is not deterministic")
	}
	if got == DocID("-Users-dev-acme-app", "other.md") {
		t.Fatalf("DocID ignores the file name")
	}
}

func TestDocIDSurvivesRedact(t *testing.T) {
	slugs := []string{
		"-Users-dev-acme-token",
		"-Users-dev-db-password",
		"-Users-dev-svc-secret",
	}
	files := []string{"project_plan.md", "token: rotate.md", "password=hunter22.md"}
	for _, slug := range slugs {
		for _, file := range files {
			id := DocID(slug, file)
			if got := memory.Redact(id); got != id {
				t.Errorf("Redact(%q) = %q, want unchanged", id, got)
			}
		}
	}
}
