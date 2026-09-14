package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
	"github.com/FreePeak/LeanKG/go/internal/sources"
)

// RepoSpec is a resolved repo: display name, clone URL (empty when the dir is
// mounted, not cloned), and the local destination.
type RepoSpec struct {
	Name string
	URL  string
	Dest string
}

// ResolveRepos resolves the repo list. Discovery precedence (Rust
// resolve_repos):
//
//  1. LEANKG_WORKSPACE_DIR (a monorepo dir) — every nested git repo found by
//     walking up to LEANKG_WORKSPACE_MAX_DEPTH (default 3). URLs are empty
//     (mounted workspace repos are never cloned); a workspace path that is not
//     a directory falls through with a warning.
//  2. LEANKG_PROJECT_DIRS (comma-separated mounted dirs) — returned as-is
//     (skip clone); an empty list falls through.
//  3. LEANKG_REPOS (comma-separated `host/namespace` paths, e.g.
//     `github.com/org/repo`) — the clone list, each repo cloned to
//     `<clone_root>/<namespace>`. A bare entry (`repo`) resolves to
//     `<LEANKG_GIT_HOST>/<LEANKG_GIT_OWNER>/repo`.
//
// Malformed LEANKG_REPOS entries are skipped with a warning rather than
// panicking (Rust had an unreachable `expect` arm there); an entry with no
// usable namespace must not take down the whole pipeline.
func ResolveRepos(logf func(string, ...any)) []RepoSpec {
	if ws := strings.TrimSpace(os.Getenv("LEANKG_WORKSPACE_DIR")); ws != "" {
		if isDir(ws) {
			depth := WorkspaceMaxDepth()
			repos := DiscoverGitRepos(ws, depth)
			specs := make([]RepoSpec, 0, len(repos))
			for _, dest := range repos {
				specs = append(specs, RepoSpec{
					Name: filepath.Base(dest),
					URL:  "", // mounted workspace repos are not cloned
					Dest: dest,
				})
			}
			logf("Workspace %s: discovered %d git repos (depth <= %d)", ws, len(specs), depth)
			return specs
		}
		logf("WARN: LEANKG_WORKSPACE_DIR %s is not a directory; falling through", ws)
	}

	if dirs := ProjectDirs(); len(dirs) > 0 {
		specs := make([]RepoSpec, 0, len(dirs))
		for _, d := range dirs {
			specs = append(specs, RepoSpec{
				Name: filepath.Base(filepath.Clean(d)),
				URL:  "", // mounted dirs are not cloned
				Dest: d,
			})
		}
		return specs
	}

	host := GitHost()
	root := CloneRoot()
	var specs []RepoSpec
	for _, entry := range ReposEnv() {
		ns := normalizeNamespace(entry, host)
		if ns == "" {
			logf("WARN: skipping malformed LEANKG_REPOS entry %q", entry)
			continue
		}
		specs = append(specs, RepoSpec{
			Name: entry,
			URL:  "https://" + ns,
			Dest: filepath.Join(root, filepath.FromSlash(ns)),
		})
	}
	return specs
}

// normalizeNamespace expands one LEANKG_REPOS entry to a host/namespace path.
// `host/org/repo` (a `.` in the first segment marks a real host) is kept
// verbatim except for the host substitution Rust did; `org/repo` gets the
// default host prepended; `repo` becomes `<host>/<owner>/repo`. An empty or
// `/`-leading entry yields "" (skipped). Namespace segments are validated
// here because they become path components under the clone root.
func normalizeNamespace(entry, host string) string {
	if entry == "" || strings.HasPrefix(entry, "/") {
		return ""
	}
	if !strings.Contains(entry, "/") {
		if sanitizeSegment(entry) == "" {
			return ""
		}
		return host + "/" + GitOwner() + "/" + entry
	}
	first, rest, _ := strings.Cut(entry, "/")
	if first == "" || rest == "" {
		return ""
	}
	if !strings.Contains(first, ".") {
		first = host
	}
	if sanitizeSegment(first) == "" || strings.HasPrefix(first, ".") {
		return ""
	}
	for _, seg := range strings.Split(rest, "/") {
		if sanitizeSegment(seg) == "" {
			return ""
		}
	}
	return first + "/" + rest
}

// sanitizeSegment rejects a namespace/name segment that could escape the
// clone root (guard: remote lists are config input, but they become paths).
// "." and "..", any separator or colon, and "%" (a percent-encoded separator
// decodes back into one on the way to the remote) are refused.
func sanitizeSegment(seg string) string {
	if seg == "" || seg == "." || seg == ".." {
		return ""
	}
	if strings.ContainsAny(seg, "/\\:%") {
		return ""
	}
	return seg
}

// CloneRepos clones or refreshes every repo into the clone root. Returns the
// list of dirs that exist on disk afterwards.
//
// Mandatory token: as soon as one spec actually needs a clone, a missing
// token is an immediate error (Rust refused with "no git token: set
// GITLAB_TOKEN, GIT_TOKEN, or GITHUB_TOKEN"). A purely mounted list — the
// LEANKG_PROJECT_DIRS / workspace modes — needs no token at all.
//
// Per spec: a destination that already holds a repo (`.git` present) is
// fetched + checked out to the ref, never re-cloned; a destination that
// exists as a non-repo directory is REFUSED (a plain dir there means
// something else owns it — overwriting it would destroy user data); anything
// else is cloned through internal/sources (argv-only, deadline-bounded).
func CloneRepos(ctx context.Context, specs []RepoSpec, logf func(string, ...any)) ([]string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(specs) == 0 {
		return nil, nil
	}
	refName := GitRef()
	token := ""
	for _, spec := range specs {
		if spec.URL != "" {
			token = GitToken()
			if token == "" {
				return nil, errors.New("no git token: set GITLAB_TOKEN, GIT_TOKEN, or GITHUB_TOKEN")
			}
			break
		}
	}
	dirs := make([]string, 0, len(specs))
	for _, spec := range specs {
		if spec.URL == "" {
			// Mounted entry (LEANKG_PROJECT_DIRS / workspace): nothing to
			// clone; the pipeline's dir-exists check owns it.
			if isDir(spec.Dest) {
				dirs = append(dirs, spec.Dest)
			}
			continue
		}
		if pathExists(filepath.Join(spec.Dest, ".git")) {
			if err := sources.FetchAndCheckout(ctx, spec.Dest, refName, reportFunc(logf)); err != nil {
				return nil, fmt.Errorf("fetch %s: %w", spec.Dest, err)
			}
			dirs = append(dirs, spec.Dest)
			continue
		}
		if pathExists(spec.Dest) {
			return nil, fmt.Errorf("refusing to clone into %s: directory exists and is not a git repo", spec.Dest)
		}
		if err := os.MkdirAll(filepath.Dir(spec.Dest), 0o755); err != nil {
			return nil, err
		}
		logf("Cloning %s -> %s (ref=%s)", spec.URL, spec.Dest, refName)
		if err := sources.CloneRepo(ctx, withToken(spec.URL, token), spec.Dest, refName, reportFunc(logf)); err != nil {
			return nil, fmt.Errorf("git clone failed for %s: %w", spec.URL, err)
		}
		dirs = append(dirs, spec.Dest)
	}
	return dirs, nil
}

// withToken injects the credential the way Rust did (`oauth2:<token>@host`)
// into an https clone URL. Any other transport — notably the file:// URLs
// tests and local mirrors use — is passed through untouched (Rust's
// `replace("https://", ...)` was a no-op there too, and prefixing a token
// onto a non-https URL corrupts it).
func withToken(url, token string) string {
	if token == "" {
		return url
	}
	if rest, ok := strings.CutPrefix(url, "https://"); ok {
		return "https://oauth2:" + token + "@" + rest
	}
	return url
}

func reportFunc(logf func(string, ...any)) sources.ProgressReporter {
	if logf == nil {
		return nil
	}
	return progressFunc(func(msg string) { logf("%s", msg) })
}

type progressFunc func(string)

func (f progressFunc) Report(msg string) { f(msg) }

// TemplateYAML is the leankg.yaml the Rust setup pipeline wrote for each repo
// dir (setup/mod.rs write_project_config), byte-for-byte in shape: a project
// block anchored on the repo dir, the setup language list, and the auto-index
// gates the serving process reads. It omits `mcp.port` (Rust omitted it too;
// projectcfg's lenient parse posture accepts a partial `mcp:` block), and it
// is a raw document rather than a serialized ProjectConfig so the generated
// file does not gain the Go shape's serde-default keys (an empty `steer:`
// block, `microservice: null`, `documentation`, a zero `mcp.port`).
func TemplateYAML(dir string) string {
	return fmt.Sprintf(`project:
  name: "%s"
  root: .
  project_path: "%s"
  languages:
    - go
    - typescript
    - python
    - java
    - kotlin
    - rust
mcp:
  enabled: true
  auto_index_on_start: true
  auto_index_threshold_minutes: 60
  auto_index_on_db_write: false
  require_git_for_auto_index: false
indexer:
  exclude:
    - "**/node_modules/**"
    - "**/vendor/**"
  include:
    - "*.go"
    - "*.ts"
    - "*.tsx"
    - "*.js"
    - "*.py"
    - "*.java"
    - "*.kt"
    - "*.rs"
`, repoName(dir), dir)
}

// TemplateConfig is TemplateYAML parsed through the config reader: the shape
// the written file round-trips to. Callers that need fields rather than text
// (tests, and the merge-preserving path) use this.
func TemplateConfig(dir string) projectcfg.ProjectConfig {
	cfg, err := projectcfg.Parse([]byte(TemplateYAML(dir)))
	if err != nil {
		return projectcfg.DefaultProjectConfig()
	}
	return cfg
}

// WriteProjectConfig writes the minimal `.leankg/leankg.yaml` project config
// for a repo dir. When a config already exists this is read-modify-write —
// user fields (including the project.project_path identity anchor and keys
// the Go struct does not model) are preserved and only MISSING template keys
// are filled in; the file is left untouched when nothing was missing. An
// unparseable existing file, or one whose root is not a mapping, is left
// exactly as the user wrote it (Rust returned the path without writing).
func WriteProjectConfig(dir string) (string, error) {
	leankgDir := filepath.Join(dir, ".leankg")
	if err := os.MkdirAll(leankgDir, 0o755); err != nil {
		return "", err
	}
	configPath := filepath.Join(leankgDir, projectcfg.ConfigFileName)
	template := TemplateYAML(dir)

	existing, rerr := os.ReadFile(configPath)
	if rerr != nil {
		return configPath, os.WriteFile(configPath, []byte(template), 0o666)
	}
	merged, ok := projectcfg.MergePreservingExistingUnder(string(existing), template)
	if !ok || merged == string(existing) {
		return configPath, nil
	}
	return configPath, os.WriteFile(configPath, []byte(merged), 0o666)
}

func repoName(dir string) string {
	name := filepath.Base(dir)
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		return "repo"
	}
	return name
}

// Stages carries the per-repo actions the pipeline runs once the config is
// written. The CLI injects its real implementations; tests inject fakes.
type Stages interface {
	// IndexOne runs a full index for dir (env is the LEANKG_ENV value) and
	// returns the number of elements indexed (0 when the count is unknown).
	IndexOne(ctx context.Context, dir, env string, verbose bool) (int, error)
	// EmbedOne runs the embedding build for dir, blocking until done.
	EmbedOne(ctx context.Context, dir string) error
}

// Options configures one RunSetup.
type Options struct {
	// Clone, Index, Embed, Status mirror the Rust flags.
	Clone, Index, Embed, Status bool
	// Env is the LEANKG_ENV value threaded to the index stage (empty means
	// EnvName()).
	Env string
	// Stages is the per-repo index/embed implementation. Required when Index
	// or Embed is set.
	Stages Stages
	// CloneFunc replaces the clone stage (default CloneRepos). Tests inject a
	// failure to exercise the mounted-dirs fallback arm, which production can
	// only reach when a spec list mixes a mounted entry with a remote one.
	CloneFunc func(context.Context, []RepoSpec, func(string, ...any)) ([]string, error)
	// Logf receives the pipeline's progress output (Rust println!/eprintln!).
	Logf func(string, ...any)
	// Now is the clock used for the setup marker (tests inject a fixed one).
	Now func() time.Time
}

// IndexedRepo is one repo whose index stage succeeded; the caller registers
// it in the global repo registry (Rust did this inline, which made the
// pipeline untestable without touching $HOME).
type IndexedRepo struct {
	Name string
	Dir  string
}

// Result summarizes a pipeline run.
type Result struct {
	// Specs is the resolved repo list (present for every run, including
	// --status).
	Specs []RepoSpec
	// Dirs is the dirs the pipeline actually walked.
	Dirs []string
	// Processed counts dirs that existed and ran the per-repo stages.
	Processed int
	// ConfigPaths maps each processed dir to the config file it wrote or left
	// untouched.
	ConfigPaths map[string]string
	// Indexed maps each indexed dir to its element count.
	Indexed map[string]int
	// IndexedRepos lists the dirs whose index succeeded, in run order.
	IndexedRepos []IndexedRepo
	// IndexFailures maps a dir to its index-stage error (warned, not fatal).
	IndexFailures map[string]error
	// EmbedFailures maps a dir to its embed-stage error (warned, not fatal).
	EmbedFailures map[string]error
	// MarkerPath is the setup.done marker location (clone_root/.leankg).
	MarkerPath string
	// MarkerWritten is false when the marker could not be written (soft-fail,
	// exactly as Rust: an unwritable clone root must not abort a successful
	// index/embed).
	MarkerWritten bool
}

// RunSetup is the full setup pipeline (Rust run_setup). With Status, or with
// no stage flags at all, it reports the resolved repo table and returns. With
// stage flags it clones (when asked), then per dir: writes the project
// config, indexes (warning on failure, recording on success), and embeds
// (warning on failure). A run that processed zero dirs is an error naming the
// knobs to set.
func RunSetup(ctx context.Context, opts Options) (Result, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	res := Result{
		ConfigPaths:   map[string]string{},
		Indexed:       map[string]int{},
		IndexFailures: map[string]error{},
		EmbedFailures: map[string]error{},
		MarkerPath:    filepath.Join(CloneRoot(), ".leankg", "setup.done"),
	}
	specs := ResolveRepos(logf)
	res.Specs = specs

	if opts.Status || !(opts.Clone || opts.Index || opts.Embed) {
		logf("=== leankg setup --status ===")
		for _, spec := range specs {
			state := "missing"
			if pathExists(spec.Dest) {
				state = "exists"
			}
			logf("  %s | %s | %s (%s)", spec.Name, spec.URL, spec.Dest, state)
		}
		return res, nil
	}

	cloneFn := opts.CloneFunc
	if cloneFn == nil {
		cloneFn = CloneRepos
	}
	var dirs []string
	if opts.Clone {
		cloned, err := cloneFn(ctx, specs, logf)
		if err != nil {
			if len(ProjectDirs()) > 0 {
				// No git token but dirs are mounted — fall back to indexing
				// whatever already exists on disk (skip clone).
				logf("WARN: clone skipped (%v); using mounted dirs only.", err)
				for _, s := range specs {
					dirs = append(dirs, s.Dest)
				}
			} else {
				return res, err
			}
		} else {
			dirs = cloned
		}
	} else {
		for _, s := range specs {
			dirs = append(dirs, s.Dest)
		}
	}
	res.Dirs = dirs

	if (opts.Index || opts.Embed) && opts.Stages == nil {
		return res, errors.New("setup: index/embed stage requested but no Stages implementation was provided")
	}
	env := opts.Env
	if env == "" {
		env = EnvName()
	}

	for _, dir := range dirs {
		if !isDir(dir) {
			logf("WARN: %s does not exist, skipping", dir)
			continue
		}
		res.Processed++
		name := repoName(dir)

		configPath, err := WriteProjectConfig(dir)
		if err != nil {
			return res, fmt.Errorf("write config for %s: %w", name, err)
		}
		res.ConfigPaths[dir] = configPath
		logf("=== %s: config %s ===", name, configPath)

		if opts.Index {
			logf("=== %s: index ===", name)
			count, err := opts.Stages.IndexOne(ctx, dir, env, true)
			if err != nil {
				logf("WARN: index failed for %s: %v", name, err)
				res.IndexFailures[dir] = err
			} else {
				res.Indexed[dir] = count
				res.IndexedRepos = append(res.IndexedRepos, IndexedRepo{Name: name, Dir: dir})
			}
		}

		if opts.Embed {
			logf("=== %s: embed ===", name)
			if err := opts.Stages.EmbedOne(ctx, dir); err != nil {
				logf("WARN: embed failed for %s: %v", name, err)
				res.EmbedFailures[dir] = err
			}
		}
	}

	if res.Processed == 0 {
		return res, fmt.Errorf(
			"no project dirs found (clone_root=%s, LEANKG_PROJECT_DIRS=%q, LEANKG_REPOS=%q); "+
				"set LEANKG_PROJECT_DIRS to mounted dirs or LEANKG_REPOS to a repo list",
			CloneRoot(), os.Getenv("LEANKG_PROJECT_DIRS"), os.Getenv("LEANKG_REPOS"))
	}

	// Record the run marker so the serve-path trigger only runs once.
	// Soft-fail: an unwritable clone root must not abort a successful run.
	if err := writeMarker(res.MarkerPath, opts.Now); err != nil {
		logf("WARN: could not write setup marker %s: %v", res.MarkerPath, err)
	} else {
		logf("Setup marker written: %s", res.MarkerPath)
		res.MarkerWritten = true
	}
	return res, nil
}

// writeMarker stamps the setup.done marker. Rust wrote seconds-since-epoch;
// RFC3339 is strictly more informative and both are opaque to the reader
// (SetupDone only checks existence).
func writeMarker(path string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(now().UTC().Format(time.RFC3339)), 0o644)
}

// SetupDone reports whether the setup pipeline has already completed for the
// current clone root (a setup.done marker exists).
func SetupDone() bool {
	return pathExists(filepath.Join(CloneRoot(), ".leankg", "setup.done"))
}
