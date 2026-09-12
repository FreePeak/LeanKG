package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
)

// isolateEnv clears every knob the pipeline reads, so a host environment
// cannot leak into a test. Individual tests then set what they exercise.
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"LEANKG_WORKSPACE_DIR", "LEANKG_WORKSPACE_MAX_DEPTH",
		"LEANKG_PROJECT_DIRS", "LEANKG_REPOS", "LEANKG_CLONE_ROOT",
		"LEANKG_GIT_REF", "LEANKG_GIT_HOST", "LEANKG_GIT_OWNER",
		"LEANKG_ENV", "LEANKG_SKIP_FRESHNESS_CHECK",
		"CLONE_ROOT", "GIT_REF", "GITLAB_TOKEN", "GIT_TOKEN", "GITHUB_TOKEN",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", t.TempDir()) // nothing may touch the real registry
	// The setup marker is written under the clone root: never let a test write
	// it into the package directory (or any other real path).
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
}

func collectLogs() (func(string, ...any), func() []string) {
	var lines []string
	return func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}, func() []string { return lines }
}

func mustFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	path, err := WriteProjectConfig(dir)
	if err != nil {
		t.Fatalf("WriteProjectConfig: %v", err)
	}
	return path
}

// ---------------------------------------------------------------------------
// env helpers
// ---------------------------------------------------------------------------

func TestEnvHelperOverrides(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_GIT_HOST", "gitlab.example.com")
	t.Setenv("LEANKG_CLONE_ROOT", "/srv/repos")
	t.Setenv("LEANKG_GIT_REF", "master")
	t.Setenv("LEANKG_GIT_OWNER", "acme")
	t.Setenv("LEANKG_ENV", "prod")
	t.Setenv("GITLAB_TOKEN", "glpat-1")

	if got := GitHost(); got != "gitlab.example.com" {
		t.Errorf("GitHost = %q", got)
	}
	if got := CloneRoot(); got != "/srv/repos" {
		t.Errorf("CloneRoot = %q", got)
	}
	if got := GitRef(); got != "master" {
		t.Errorf("GitRef = %q", got)
	}
	if got := GitOwner(); got != "acme" {
		t.Errorf("GitOwner = %q", got)
	}
	if got := EnvName(); got != "prod" {
		t.Errorf("EnvName = %q", got)
	}
	if got := GitToken(); got != "glpat-1" {
		t.Errorf("GitToken = %q", got)
	}
}

func TestEnvHelperFallbacks(t *testing.T) {
	// Clear the knobs by hand: this test asserts the cwd default, which
	// isolateEnv deliberately pins to a temp dir.
	for _, key := range []string{
		"LEANKG_WORKSPACE_DIR", "LEANKG_PROJECT_DIRS", "LEANKG_REPOS",
		"LEANKG_GIT_REF", "GIT_REF", "LEANKG_GIT_HOST", "LEANKG_GIT_OWNER",
		"LEANKG_ENV", "GITLAB_TOKEN", "GIT_TOKEN", "GITHUB_TOKEN",
		"CLONE_ROOT", "LEANKG_CLONE_ROOT",
	} {
		t.Setenv(key, "")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := GitHost(); got != "github.com" {
		t.Errorf("GitHost default = %q", got)
	}
	if got := CloneRoot(); got != cwd {
		t.Errorf("CloneRoot default = %q, want cwd %q", got, cwd)
	}
	if got := GitRef(); got != "main" {
		t.Errorf("GitRef default = %q", got)
	}
	if got := GitOwner(); got != "user" {
		t.Errorf("GitOwner default = %q", got)
	}
	if got := EnvName(); got != "local" {
		t.Errorf("EnvName default = %q", got)
	}
	if got := GitToken(); got != "" {
		t.Errorf("GitToken with everything unset = %q", got)
	}
	if got := WorkspaceMaxDepth(); got != 3 {
		t.Errorf("WorkspaceMaxDepth default = %d", got)
	}

	// Legacy aliases: CLONE_ROOT and GIT_REF, but not when the LEANKG_
	// spelling is set.
	t.Setenv("CLONE_ROOT", "/legacy/root")
	t.Setenv("GIT_REF", "develop")
	if got := CloneRoot(); got != "/legacy/root" {
		t.Errorf("CloneRoot legacy = %q", got)
	}
	if got := GitRef(); got != "develop" {
		t.Errorf("GitRef legacy = %q", got)
	}
	t.Setenv("LEANKG_CLONE_ROOT", "/modern/root")
	t.Setenv("LEANKG_GIT_REF", "main")
	if got := CloneRoot(); got != "/modern/root" {
		t.Errorf("LEANKG_CLONE_ROOT must win, got %q", got)
	}
	if got := GitRef(); got != "main" {
		t.Errorf("LEANKG_GIT_REF must win, got %q", got)
	}

	// Token precedence: GITLAB_TOKEN > GIT_TOKEN > GITHUB_TOKEN.
	t.Setenv("GITHUB_TOKEN", "gh")
	if got := GitToken(); got != "gh" {
		t.Errorf("GitToken GITHUB_TOKEN = %q", got)
	}
	t.Setenv("GIT_TOKEN", "generic")
	if got := GitToken(); got != "generic" {
		t.Errorf("GitToken GIT_TOKEN = %q", got)
	}
	t.Setenv("GITLAB_TOKEN", "gitlab")
	if got := GitToken(); got != "gitlab" {
		t.Errorf("GitToken GITLAB_TOKEN = %q", got)
	}
}

func TestWorkspaceMaxDepthHonorsOverride(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_WORKSPACE_MAX_DEPTH", "5")
	if got := WorkspaceMaxDepth(); got != 5 {
		t.Errorf("WorkspaceMaxDepth = %d, want 5", got)
	}
	t.Setenv("LEANKG_WORKSPACE_MAX_DEPTH", "not-a-number")
	if got := WorkspaceMaxDepth(); got != 3 {
		t.Errorf("malformed depth must fall back to 3, got %d", got)
	}
}

func TestProjectDirsAndReposEnv(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_PROJECT_DIRS", " /a/go-repo , ,/b/ts-repo ,")
	got := ProjectDirs()
	want := []string{"/a/go-repo", "/b/ts-repo"}
	if len(got) != len(want) {
		t.Fatalf("ProjectDirs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ProjectDirs = %v, want %v", got, want)
		}
	}
	t.Setenv("LEANKG_REPOS", "github.com/freepeak/leankg,,github.com/org/other")
	if repos := ReposEnv(); len(repos) != 2 {
		t.Fatalf("ReposEnv = %v", repos)
	}
}

// ---------------------------------------------------------------------------
// discover + resolve
// ---------------------------------------------------------------------------

func TestDiscoverGitReposFindsNestedAndSkipsJunkDirs(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	for _, rel := range []string{
		"platform-food/be-food-order",
		"platform-mail/be-mailer",
		"node_modules/dep",
		"target/debug",
		"nested/deep/repo",
	} {
		mustFile(t, filepath.Join(root, rel, ".git", "HEAD"), "ref: refs/heads/main\n")
	}
	// Depth 3 exactly: inside the bound (Rust's `depth > max` prunes deeper).
	mustFile(t, filepath.Join(root, "a", "b", "c", ".git", "HEAD"), "x")
	// One level too deep for the bound.
	mustFile(t, filepath.Join(root, "x", "y", "z", "u", "v", ".git", "HEAD"), "x")
	// A non-repo dir between two repos must not block the walk.
	mustFile(t, filepath.Join(root, "platform-food", "README.md"), "hi")

	got := DiscoverGitRepos(root, 3)
	want := []string{
		filepath.Join(root, "a", "b", "c"),
		filepath.Join(root, "nested", "deep", "repo"),
		filepath.Join(root, "platform-food", "be-food-order"),
		filepath.Join(root, "platform-mail", "be-mailer"),
	}
	if len(got) != len(want) {
		t.Fatalf("DiscoverGitRepos = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DiscoverGitRepos = %v, want %v", got, want)
		}
	}
}

func TestResolveReposUsesProjectDirs(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_PROJECT_DIRS", "/a/go-repo,/b/ts-repo")
	t.Setenv("LEANKG_CLONE_ROOT", "/")
	logf, _ := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 2 {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[0].Name != "go-repo" || specs[0].Dest != "/a/go-repo" || specs[0].URL != "" {
		t.Fatalf("spec[0] = %+v", specs[0])
	}
	if specs[1].Name != "ts-repo" {
		t.Fatalf("spec[1] = %+v", specs[1])
	}
}

func TestResolveReposUsesRepoList(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_REPOS", "github.com/freepeak/leankg,github.com/org/other")
	t.Setenv("LEANKG_CLONE_ROOT", "/tmp/lkg-clone")
	t.Setenv("LEANKG_GIT_HOST", "github.com")
	logf, _ := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 2 {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[0].Name != "github.com/freepeak/leankg" {
		t.Errorf("name = %q", specs[0].Name)
	}
	if specs[0].URL != "https://github.com/freepeak/leankg" {
		t.Errorf("url = %q", specs[0].URL)
	}
	if specs[0].Dest != "/tmp/lkg-clone/github.com/freepeak/leankg" {
		t.Errorf("dest = %q", specs[0].Dest)
	}
}

func TestResolveReposHostDefaultingAndBareEntries(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_REPOS", "other,bare-repo,gitlab.example.com/team/repo")
	t.Setenv("LEANKG_CLONE_ROOT", "/clones")
	t.Setenv("LEANKG_GIT_HOST", "github.com")
	logf, logs := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 3 {
		t.Fatalf("specs = %+v (logs: %v)", specs, logs())
	}
	// One segment -> host + the default owner.
	if specs[0].URL != "https://github.com/user/other" {
		t.Errorf("other url = %q", specs[0].URL)
	}
	// A bare entry gets host + the default owner.
	if specs[1].URL != "https://github.com/user/bare-repo" {
		t.Errorf("bare-repo url = %q", specs[1].URL)
	}
	// A real host is kept verbatim.
	if specs[2].URL != "https://gitlab.example.com/team/repo" {
		t.Errorf("gitlab url = %q", specs[2].URL)
	}
}

func TestResolveReposSkipsMalformedEntries(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_REPOS", "github.com/ok/repo,/absolute,github.com/ok/../traversal,..%2Fescape,")
	t.Setenv("LEANKG_CLONE_ROOT", "/clones")
	logf, logs := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 1 || specs[0].URL != "https://github.com/ok/repo" {
		t.Fatalf("specs = %+v", specs)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "skipping malformed LEANKG_REPOS entry") {
		t.Fatalf("expected a malformed-entry warning, logs = %v", logs())
	}
	for _, spec := range specs {
		if strings.Contains(spec.Dest, "..") {
			t.Fatalf("dest escaped the clone root: %q", spec.Dest)
		}
	}
}

func TestResolveReposEmptyWithoutEnv(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_CLONE_ROOT", "/tmp/lkg-clone-empty")
	logf, _ := collectLogs()
	if specs := ResolveRepos(logf); len(specs) != 0 {
		t.Fatalf("specs = %+v, want none", specs)
	}
}

func TestResolveReposWorkspaceDiscovery(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	for _, rel := range []string{"platform-food/be-food-order", "platform-mail/be-mailer"} {
		mustFile(t, filepath.Join(root, rel, ".git", "HEAD"), "ref: refs/heads/main\n")
	}
	t.Setenv("LEANKG_WORKSPACE_DIR", root)
	logf, logs := collectLogs()

	specs := ResolveRepos(logf)
	if len(specs) != 2 {
		t.Fatalf("specs = %+v", specs)
	}
	for _, spec := range specs {
		if spec.URL != "" {
			t.Fatalf("mounted workspace repos must not be cloned: %+v", spec)
		}
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "discovered 2 git repos") {
		t.Fatalf("missing discovery log: %v", logs())
	}
	// A workspace dir that does not exist falls through to the repo list.
	t.Setenv("LEANKG_WORKSPACE_DIR", filepath.Join(root, "nope"))
	t.Setenv("LEANKG_REPOS", "github.com/org/other")
	specs = ResolveRepos(logf)
	if len(specs) != 1 || specs[0].URL != "https://github.com/org/other" {
		t.Fatalf("fall-through failed: %+v", specs)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "is not a directory; falling through") {
		t.Fatalf("missing fall-through warning: %v", logs())
	}
}

// ---------------------------------------------------------------------------
// clone
// ---------------------------------------------------------------------------

func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "leankg test"}, {"GIT_AUTHOR_EMAIL", "test@example.invalid"},
		{"GIT_COMMITTER_NAME", "leankg test"}, {"GIT_COMMITTER_EMAIL", "test@example.invalid"},
	} {
		t.Setenv(kv[0], kv[1])
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newOriginRepo(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "checkout", "-q", "-b", "main")
	mustFile(t, filepath.Join(dir, name), "package fixture\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	return dir
}

func TestCloneReposRequiresToken(t *testing.T) {
	isolateEnv(t)
	hermeticGit(t)
	logf, _ := collectLogs()
	specs := []RepoSpec{{Name: "x", URL: "https://github.com/org/x", Dest: filepath.Join(t.TempDir(), "x")}}
	_, err := CloneRepos(context.Background(), specs, logf)
	if err == nil || !strings.Contains(err.Error(), "no git token") {
		t.Fatalf("err = %v, want the mandatory-token refusal", err)
	}
	if pathExists(specs[0].Dest) {
		t.Fatal("a tokenless clone must not touch the destination")
	}
}

func TestCloneReposClonesAndRefreshes(t *testing.T) {
	isolateEnv(t)
	hermeticGit(t)
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("LEANKG_GIT_REF", "main")
	logf, _ := collectLogs()

	origin := newOriginRepo(t, "code.go")
	dest := filepath.Join(t.TempDir(), "clones", "org", "repo")
	specs := []RepoSpec{{Name: "org/repo", URL: origin, Dest: dest}}

	dirs, err := CloneRepos(context.Background(), specs, logf)
	if err != nil {
		t.Fatalf("CloneRepos: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != dest {
		t.Fatalf("dirs = %v", dirs)
	}
	if !pathExists(filepath.Join(dest, ".git")) {
		t.Fatalf("clone missing at %s", dest)
	}

	// A second run refreshes the existing clone instead of re-cloning: a
	// commit made in the clone that the remote does not have survives.
	mustFile(t, filepath.Join(dest, "local-only.txt"), "keep me\n")
	if _, err := CloneRepos(context.Background(), specs, logf); err != nil {
		t.Fatalf("second CloneRepos: %v", err)
	}
	if !pathExists(filepath.Join(dest, "local-only.txt")) {
		t.Fatal("existing clone was re-created instead of fetched")
	}
}

func TestCloneReposRefusesNonRepoDestination(t *testing.T) {
	isolateEnv(t)
	hermeticGit(t)
	t.Setenv("GITHUB_TOKEN", "test-token")
	logf, _ := collectLogs()

	dest := filepath.Join(t.TempDir(), "occupied")
	mustFile(t, filepath.Join(dest, "user-file.txt"), "mine\n")
	specs := []RepoSpec{{Name: "x", URL: t.TempDir(), Dest: dest}}

	_, err := CloneRepos(context.Background(), specs, logf)
	if err == nil || !strings.Contains(err.Error(), "refusing to clone into") {
		t.Fatalf("err = %v, want the non-repo refusal", err)
	}
	if !pathExists(filepath.Join(dest, "user-file.txt")) {
		t.Fatal("the refusal must leave the directory untouched")
	}
}

func TestCloneReposMountedSpecsSkipClone(t *testing.T) {
	isolateEnv(t)
	logf, _ := collectLogs()
	mounted := t.TempDir()
	specs := []RepoSpec{
		{Name: "mounted", URL: "", Dest: mounted},
		{Name: "gone", URL: "", Dest: filepath.Join(t.TempDir(), "missing")},
	}
	dirs, err := CloneRepos(context.Background(), specs, logf)
	if err != nil {
		t.Fatalf("mounted specs must not need a token: %v", err)
	}
	if len(dirs) != 1 || dirs[0] != mounted {
		t.Fatalf("dirs = %v", dirs)
	}
}

// ---------------------------------------------------------------------------
// write_project_config
// ---------------------------------------------------------------------------

func TestWriteProjectConfigCreatesTemplate(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "be-food-order")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := writeConfig(t, dir)
	if path != filepath.Join(dir, ".leankg", projectcfg.ConfigFileName) {
		t.Fatalf("config path = %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{"be-food-order", "languages", "auto_index_on_start", "auto_index_threshold_minutes: 60", "project_path"} {
		if !strings.Contains(content, want) {
			t.Errorf("template missing %q:\n%s", want, content)
		}
	}
	// The template must round-trip through the config reader other packages
	// use (the partial `mcp:` block has no port).
	cfg, err := projectcfg.Parse(raw)
	if err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
	if !cfg.MCP.AutoIndexOnStart || cfg.MCP.AutoIndexThresholdMinutes != 60 {
		t.Fatalf("template mcp block = %+v", cfg.MCP)
	}
	if cfg.Project.ProjectPath != dir {
		t.Fatalf("project_path = %q, want %q", cfg.Project.ProjectPath, dir)
	}
	// The generated file is the template, not a serialized ProjectConfig: no
	// serde-default noise from the Go shape may leak into a user's repo.
	for _, unwanted := range []string{"steer:", "microservice:", "documentation:", "typed_resolve:", "port:", "auth:"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("template leaked the Go config shape (%q):\n%s", unwanted, content)
		}
	}
	if !strings.HasPrefix(content, "project:\n") {
		t.Errorf("template must open with the project block:\n%s", content)
	}
}

func TestTemplateYAMLForRootLikePath(t *testing.T) {
	// A root-ish path has no useful basename: the name falls back to "repo"
	// rather than an empty or "/" name.
	for _, dir := range []string{"/", ""} {
		if !strings.Contains(TemplateYAML(dir), `name: "repo"`) {
			t.Errorf("TemplateYAML(%q) name fallback missing:\n%s", dir, TemplateYAML(dir))
		}
	}
}

func TestWriteProjectConfigIsIdempotent(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "repo-x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	first := writeConfig(t, dir)
	// An unparseable document is not project config: leave it exactly as the
	// user wrote it (Rust returned the path without writing).
	mustFile(t, first, "sentinel")
	second := writeConfig(t, dir)
	if first != second {
		t.Fatalf("paths differ: %s vs %s", first, second)
	}
	raw, _ := os.ReadFile(second)
	if string(raw) != "sentinel" {
		t.Fatalf("existing sentinel was rewritten: %q", raw)
	}
}

func TestWriteProjectConfigLeavesScalarAndSequenceDocumentsAlone(t *testing.T) {
	isolateEnv(t)
	for _, content := range []string{"just-a-string\n", "- one\n- two\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".leankg", projectcfg.ConfigFileName)
		mustFile(t, path, content)
		writeConfig(t, dir)
		raw, _ := os.ReadFile(path)
		if string(raw) != content {
			t.Fatalf("non-mapping document rewritten: %q -> %q", content, raw)
		}
	}
}

func TestWriteProjectConfigPreservesUserFields(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "repo-y")
	path := filepath.Join(dir, ".leankg", projectcfg.ConfigFileName)
	mustFile(t, path, fmt.Sprintf(
		"project:\n  name: user-name\n  root: ./src\n  project_path: %s\n  team_probe: keep-us\n",
		dir))

	writeConfig(t, dir)
	raw, _ := os.ReadFile(path)
	out := string(raw)
	for _, want := range []string{"user-name", "./src", "team_probe", dir} {
		if !strings.Contains(out, want) {
			t.Errorf("user field %q lost:\n%s", want, out)
		}
	}
	// The missing template keys were still filled in.
	if !strings.Contains(out, "auto_index_on_start") {
		t.Errorf("missing template keys not filled:\n%s", out)
	}
}

func TestWriteProjectConfigRefillsMissingIdentityAnchor(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "repo-z")
	path := filepath.Join(dir, ".leankg", projectcfg.ConfigFileName)
	mustFile(t, path, "project:\n  name: anchored\n  team_probe: keep-us\n")

	writeConfig(t, dir)
	raw, _ := os.ReadFile(path)
	out := string(raw)
	for _, want := range []string{"project_path:", "anchored", "keep-us"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q after refill:\n%s", want, out)
		}
	}
}

func TestWriteProjectConfigMergesFixtureWithUnknownKeys(t *testing.T) {
	isolateEnv(t)
	dir := filepath.Join(t.TempDir(), "repo-fixture")
	path := filepath.Join(dir, ".leankg", projectcfg.ConfigFileName)
	mustFile(t, path, `project:
  name: fixture-name
  root: ./fixture-src
  project_path: /fixture/keep
  vendor_probe: v
indexer:
  custom_indexer_key: 7
team_identity_probe: keep-me
mcp:
  auto_index_on_start: false
`)

	writeConfig(t, dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := projectcfg.Parse(raw)
	if err != nil {
		t.Fatalf("merged document does not parse: %v\n%s", err, raw)
	}
	// Existing values win, including the auto-index flag the user turned off.
	if cfg.Project.Name != "fixture-name" || cfg.Project.Root != "./fixture-src" {
		t.Fatalf("project block overwritten: %+v", cfg.Project)
	}
	if cfg.Project.ProjectPath != "/fixture/keep" {
		t.Fatalf("identity anchor overwritten: %q", cfg.Project.ProjectPath)
	}
	if cfg.MCP.AutoIndexOnStart {
		t.Fatal("user's auto_index_on_start=false was overwritten")
	}
	// Unmodelled keys survive.
	out := string(raw)
	for _, want := range []string{"vendor_probe", "custom_indexer_key", "team_identity_probe"} {
		if !strings.Contains(out, want) {
			t.Errorf("unknown key %q dropped:\n%s", want, out)
		}
	}
}

// ---------------------------------------------------------------------------
// run_setup
// ---------------------------------------------------------------------------

type fakeStages struct {
	indexFunc func(dir string) (int, error)
	embedFunc func(dir string) error
	indexed   []string
	embedded  []string
	envs      []string
}

func (f *fakeStages) wasIndexRun(dir string) bool {
	for _, d := range f.indexed {
		if d == dir {
			return true
		}
	}
	return false
}

func (f *fakeStages) IndexOne(_ context.Context, dir, env string, verbose bool) (int, error) {
	f.indexed = append(f.indexed, dir)
	f.envs = append(f.envs, env)
	if f.indexFunc != nil {
		return f.indexFunc(dir)
	}
	return 3, nil
}

func (f *fakeStages) EmbedOne(_ context.Context, dir string) error {
	f.embedded = append(f.embedded, dir)
	if f.embedFunc != nil {
		return f.embedFunc(dir)
	}
	return nil
}

func TestRunSetupStatusPrintsTableAndDoesNotWrite(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir+",/does/not/exist")
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, logs := collectLogs()

	res, err := RunSetup(context.Background(), Options{Status: true, Logf: logf})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if len(res.Specs) != 2 || res.Processed != 0 {
		t.Fatalf("res = %+v", res)
	}
	out := strings.Join(logs(), "\n")
	for _, want := range []string{"=== leankg setup --status ===", dir + " (exists)", "/does/not/exist (missing)"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	if pathExists(filepath.Join(dir, ".leankg")) {
		t.Fatal("--status must not create a project config")
	}
	if res.MarkerWritten {
		t.Fatal("--status must not write the setup marker")
	}
}

func TestRunSetupNoFlagsBehavesLikeStatus(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	logf, logs := collectLogs()
	res, err := RunSetup(context.Background(), Options{Logf: logf})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "--status") {
		t.Fatalf("no-flag run must print the status table: %v", logs())
	}
	if res.Processed != 0 {
		t.Fatalf("res = %+v", res)
	}
}

func TestRunSetupIndexWritesConfigAndRecords(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	cloneRoot := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	t.Setenv("LEANKG_CLONE_ROOT", cloneRoot)
	t.Setenv("LEANKG_ENV", "prod")
	logf, _ := collectLogs()
	stages := &fakeStages{indexFunc: func(string) (int, error) { return 42, nil }}

	res, err := RunSetup(context.Background(), Options{Index: true, Stages: stages, Logf: logf})
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}
	if res.Processed != 1 {
		t.Fatalf("processed = %d", res.Processed)
	}
	if !pathExists(filepath.Join(dir, ".leankg", projectcfg.ConfigFileName)) {
		t.Fatal("project config was not written")
	}
	if got := res.Indexed[dir]; got != 42 {
		t.Fatalf("indexed count = %d", got)
	}
	if len(res.IndexedRepos) != 1 || res.IndexedRepos[0].Dir != dir {
		t.Fatalf("indexed repos = %+v", res.IndexedRepos)
	}
	if len(stages.envs) != 1 || stages.envs[0] != "prod" {
		t.Fatalf("index env = %v, want [prod]", stages.envs)
	}
	if !res.MarkerWritten {
		t.Fatal("marker must be written after a successful run")
	}
	marker := filepath.Join(cloneRoot, ".leankg", "setup.done")
	if !pathExists(marker) {
		t.Fatalf("marker missing at %s", marker)
	}
	if !SetupDone() {
		t.Fatal("SetupDone must see the marker")
	}
}

func TestRunSetupRecordsIndexFailuresWithoutAborting(t *testing.T) {
	isolateEnv(t)
	good, bad := t.TempDir(), t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", good+","+bad)
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, logs := collectLogs()
	stages := &fakeStages{indexFunc: func(dir string) (int, error) {
		if dir == bad {
			return 0, errors.New("boom")
		}
		return 1, nil
	}}

	res, err := RunSetup(context.Background(), Options{Index: true, Stages: stages, Logf: logf})
	if err != nil {
		t.Fatalf("a per-repo index failure must not abort the pipeline: %v", err)
	}
	if res.Processed != 2 {
		t.Fatalf("processed = %d", res.Processed)
	}
	if res.IndexFailures[bad] == nil {
		t.Fatal("the failure was not recorded")
	}
	if len(res.IndexedRepos) != 1 || res.IndexedRepos[0].Dir != good {
		t.Fatalf("only the good repo may be registered: %+v", res.IndexedRepos)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "WARN: index failed") {
		t.Fatalf("no warning logged: %v", logs())
	}
	// The marker still lands: the run did process repos.
	if !res.MarkerWritten {
		t.Fatal("marker must be written")
	}
}

func TestRunSetupEmbedFailureWarnsAndContinues(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, logs := collectLogs()
	stages := &fakeStages{embedFunc: func(string) error { return errors.New("no provider") }}

	res, err := RunSetup(context.Background(), Options{Embed: true, Stages: stages, Logf: logf})
	if err != nil {
		t.Fatalf("embed failure must be a warning: %v", err)
	}
	if res.EmbedFailures[dir] == nil {
		t.Fatal("embed failure not recorded")
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "WARN: embed failed") {
		t.Fatalf("no warning: %v", logs())
	}
}

func TestRunSetupSkipsMissingDirsAndErrorsWhenNoneProcessed(t *testing.T) {
	isolateEnv(t)
	missing := filepath.Join(t.TempDir(), "gone")
	t.Setenv("LEANKG_PROJECT_DIRS", missing)
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, logs := collectLogs()

	res, err := RunSetup(context.Background(), Options{Index: true, Stages: &fakeStages{}, Logf: logf})
	if err == nil {
		t.Fatal("a run that processed nothing must fail")
	}
	if !strings.Contains(err.Error(), "no project dirs found") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "LEANKG_PROJECT_DIRS") {
		t.Fatalf("err must name the knobs: %v", err)
	}
	if res.Processed != 0 || res.MarkerWritten {
		t.Fatalf("res = %+v", res)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "does not exist, skipping") {
		t.Fatalf("no skip warning: %v", logs())
	}
}

func TestRunSetupCloneFailureFallsBackToMountedDirs(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, logs := collectLogs()
	stages := &fakeStages{}

	// The Rust fallback arm: `clone_repos` failed AND LEANKG_PROJECT_DIRS is
	// set — index whatever already exists on disk (skip clone). The failing
	// clone is injected: a mounted-only spec list never reaches the clone
	// stage with a URL, so only a mix of mounted + remote entries hits it in
	// production.
	cloneErr := errors.New("no git token: set GITLAB_TOKEN, GIT_TOKEN, or GITHUB_TOKEN")
	res, err := RunSetup(context.Background(), Options{
		Clone: true, Index: true, Stages: stages, Logf: logf,
		CloneFunc: func(context.Context, []RepoSpec, func(string, ...any)) ([]string, error) {
			return nil, cloneErr
		},
	})
	if err != nil {
		t.Fatalf("a clone failure with mounted dirs must not abort: %v", err)
	}
	if res.Processed != 1 {
		t.Fatalf("processed = %d, want the mounted dir", res.Processed)
	}
	if !stages.wasIndexRun(dir) {
		t.Fatalf("mounted dir not indexed: indexed=%v", stages.indexed)
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "clone skipped") {
		t.Fatalf("no fallback warning: %v", logs())
	}
	if !res.MarkerWritten {
		t.Fatal("the fallback run still completes")
	}
}

func TestRunSetupCloneFailureWithoutMountedDirsIsFatal(t *testing.T) {
	isolateEnv(t)
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	logf, _ := collectLogs()
	_, err := RunSetup(context.Background(), Options{
		Clone: true, Index: true, Stages: &fakeStages{}, Logf: logf,
		CloneFunc: func(context.Context, []RepoSpec, func(string, ...any)) ([]string, error) {
			return nil, errors.New("no git token: set GITLAB_TOKEN, GIT_TOKEN, or GITHUB_TOKEN")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "no git token") {
		t.Fatalf("err = %v, want the clone failure", err)
	}
}

// CloneRepos needs no token for a purely mounted list, but a LEANKG_REPOS
// clone without one must fail before touching the destination.
func TestCloneTokenRequiredOnlyForRepoListMode(t *testing.T) {
	isolateEnv(t)
	hermeticGit(t)
	t.Setenv("LEANKG_REPOS", "github.com/org/repo")
	t.Setenv("LEANKG_CLONE_ROOT", t.TempDir())
	specs := ResolveRepos(func(string, ...any) {})
	if len(specs) != 1 {
		t.Fatalf("specs = %+v", specs)
	}
	if _, err := CloneRepos(context.Background(), specs, nil); err == nil {
		t.Fatal("repo-list mode must require a token")
	}
	t.Setenv("GITLAB_TOKEN", "glpat")
	if _, err := CloneRepos(context.Background(), specs, nil); err == nil {
		t.Fatal("a bogus host must still fail the clone")
	} else if strings.Contains(err.Error(), "no git token") {
		t.Fatalf("token was found but not used: %v", err)
	}
}

// End-to-end: clone a real local repository through the pipeline, write its
// project config, and run both stages.
func TestRunSetupClonesConfiguresIndexesAndEmbeds(t *testing.T) {
	isolateEnv(t)
	hermeticGit(t)
	t.Setenv("GITHUB_TOKEN", "test-token")
	cloneRoot := t.TempDir()
	dest := filepath.Join(cloneRoot, "org", "repo")
	origin := newOriginRepo(t, "code.go")
	logf, logs := collectLogs()
	stages := &fakeStages{}

	// Redirect the resolved spec at a local origin: the configured host is
	// unreachable, and the clone contract itself is covered by the
	// CloneRepos tests.
	redirect := func(ctx context.Context, specs []RepoSpec, lf func(string, ...any)) ([]string, error) {
		if len(specs) != 1 {
			t.Fatalf("specs = %+v", specs)
		}
		spec := specs[0]
		spec.URL = origin
		spec.Dest = dest
		return CloneRepos(ctx, []RepoSpec{spec}, lf)
	}
	t.Setenv("LEANKG_REPOS", "example.invalid/org/repo")
	res, err := RunSetup(context.Background(), Options{
		Clone: true, Index: true, Embed: true,
		Stages: stages, Logf: logf, CloneFunc: redirect,
	})
	if err != nil {
		t.Fatalf("RunSetup: %v (logs %v)", err, logs())
	}
	if res.Processed != 1 {
		t.Fatalf("processed = %d", res.Processed)
	}
	if !pathExists(filepath.Join(dest, ".git")) || !pathExists(filepath.Join(dest, "code.go")) {
		t.Fatalf("clone missing at %s", dest)
	}
	if !pathExists(filepath.Join(dest, ".leankg", projectcfg.ConfigFileName)) {
		t.Fatal("project config not written into the clone")
	}
	if len(res.IndexedRepos) != 1 || res.IndexedRepos[0].Name != "repo" {
		t.Fatalf("indexed repos = %+v", res.IndexedRepos)
	}
	if len(stages.embedded) != 1 || stages.embedded[0] != dest {
		t.Fatalf("embed stage = %v", stages.embedded)
	}
	if !res.MarkerWritten {
		t.Fatal("marker not written")
	}
	out := strings.Join(logs(), "\n")
	for _, want := range []string{
		"Cloning ", "=== repo: config ", "=== repo: index ===", "=== repo: embed ===", "Setup marker written",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("pipeline output missing %q:\n%s", want, out)
		}
	}
}

func TestRunSetupStagesRequired(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	logf, _ := collectLogs()
	if _, err := RunSetup(context.Background(), Options{Index: true, Logf: logf}); err == nil {
		t.Fatal("an index run without Stages must fail loudly")
	}
}

func TestRunSetupMarkerIsSoftFail(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	// A clone root that cannot be created (a file where a dir belongs).
	blocker := filepath.Join(t.TempDir(), "blocker")
	mustFile(t, blocker, "not a dir\n")
	t.Setenv("LEANKG_PROJECT_DIRS", dir)
	t.Setenv("LEANKG_CLONE_ROOT", filepath.Join(blocker, "sub"))
	logf, logs := collectLogs()

	res, err := RunSetup(context.Background(), Options{Index: true, Stages: &fakeStages{}, Logf: logf})
	if err != nil {
		t.Fatalf("an unwritable clone root must not abort a successful index: %v", err)
	}
	if res.MarkerWritten {
		t.Fatal("marker cannot have been written")
	}
	if !strings.Contains(strings.Join(logs(), "\n"), "could not write setup marker") {
		t.Fatalf("no marker warning: %v", logs())
	}
}

func TestSetupDoneMarkerSemantics(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	t.Setenv("LEANKG_CLONE_ROOT", root)
	if SetupDone() {
		t.Fatal("no marker yet")
	}
	mustFile(t, filepath.Join(root, ".leankg", "setup.done"), "1")
	if !SetupDone() {
		t.Fatal("marker present but SetupDone false")
	}
}

func TestWriteMarkerStampsInjectedClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "setup.done")
	fixed := time.Date(2026, 9, 12, 10, 30, 0, 0, time.UTC)
	if err := writeMarker(path, func() time.Time { return fixed }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "2026-09-12T10:30:00Z" {
		t.Fatalf("marker = %q", raw)
	}
}
