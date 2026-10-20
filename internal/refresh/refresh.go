// Package refresh implements the Rust `refresh` verb: index code, index docs
// and run the embedding pipeline in one command (full refresh). Each stage is
// the existing Go flow — internal/index, internal/docindex, internal/embed —
// composed in the Rust order over one store handle.
//
// --source is a remote URI: the tree is synced into <project>/.leankg/sources
// through internal/sources and that tree is indexed, exactly as in the Rust
// handler (the store still lives at <project>/.leankg).
package refresh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/docindex"
	"github.com/FreePeak/LeanKG/internal/embed"
	"github.com/FreePeak/LeanKG/internal/flock"
	"github.com/FreePeak/LeanKG/internal/index"
	"github.com/FreePeak/LeanKG/internal/langs"
	"github.com/FreePeak/LeanKG/internal/sources"
	"github.com/FreePeak/LeanKG/internal/store"
)

// Options configures one refresh run.
type Options struct {
	// Project is the project root: the store lives at <Project>/.leankg.
	// Empty means the process working directory.
	Project string
	// Path is the code tree to index. Empty means "." (the Rust default).
	Path string
	// Docs is the documentation directory. Empty means <Project>/docs when
	// that directory exists, else no docs stage at all (the Rust rule).
	Docs string
	// Full reconstructs the embedding collection instead of the incremental
	// scan. NOTE: accepted for CLI parity only — the Rust Refresh handler
	// destructured `full` and never passed it to the embed driver, which always
	// ran incremental. Honoring it would be new product semantics.
	//
	// ponytail: vestigial flag, ceiling = a user asking for a rebuild gets an
	// incremental run; upgrade path = thread Full into embed.Run's mode and
	// document the behavior change.
	Full bool
	// Source is a remote source URI (Rust --source). Non-empty overrides Path.
	Source string
	// RefName is the git ref for a git+ source ("" means main).
	RefName string
	// Auth is the credential for Source ("" falls back to the source's env vars).
	Auth string
}

// Result summarizes one refresh run.
type Result struct {
	Path        string
	DocsDir     string
	Code        index.Result
	DocsResult  docindex.Result
	DocsIndexed bool
	Embed       embed.Report
	// EmbedSkipped is non-empty when the embed stage could not run (no
	// provider/sidecar): the refresh still succeeds, like Rust's
	// maybe_run_embed printing the reason and returning Ok.
	EmbedSkipped string
}

// Run executes the three refresh stages in Rust order and fails fast on the
// first stage error (the Rust handler used `?` on every stage).
func Run(ctx context.Context, opts Options) (Result, error) {
	var res Result
	project := opts.Project
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return res, fmt.Errorf("refresh: resolve cwd: %w", err)
		}
		project = cwd
	}
	indexPath := opts.Path
	if opts.Source != "" {
		synced, err := sources.Resolve(ctx, project, opts.Source, opts.Auth, opts.RefName, sources.CLIProgress{})
		if err != nil {
			return res, fmt.Errorf("refresh: source sync: %w", err)
		}
		indexPath = synced
	} else if indexPath == "" {
		indexPath = "."
	}
	res.Path = indexPath

	st, err := store.OpenBackend(ctx, project, os.Getenv("LEANKG_DB_ENGINE"), os.Getenv("LEANKG_PG_URL"), "", store.RW)
	if err != nil {
		return res, fmt.Errorf("refresh: open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return res, fmt.Errorf("refresh: migrate: %w", err)
	}

	// 1. Index code — lazy language activation governs what gets indexed.
	reg := langs.DefaultRegistry()
	if _, err := reg.Activate(indexPath); err != nil {
		return res, fmt.Errorf("refresh: language detection: %w", err)
	}
	res.Code, err = index.IndexDirWith(ctx, st, indexPath, reg)
	if err != nil {
		return res, fmt.Errorf("refresh: index %s: %w", indexPath, err)
	}

	// 2. Index docs — explicit --docs, else <project>/docs when it exists.
	docsDir := opts.Docs
	if docsDir == "" {
		candidate := filepath.Join(project, "docs")
		if info, statErr := os.Stat(candidate); statErr == nil && info.IsDir() {
			docsDir = candidate
		}
	}
	if docsDir != "" {
		res.DocsResult, err = docindex.IndexDocsUnder(ctx, st, project, docsDir)
		if err != nil {
			return res, fmt.Errorf("refresh: index docs %s: %w", docsDir, err)
		}
		res.DocsDir = docsDir
		res.DocsIndexed = true
	}

	// 3. Embed — always incremental (see Options.Full on the Rust vestige).
	mode := "incremental"
	if opts.Full {
		mode = "full"
	}
	res.Embed, res.EmbedSkipped, err = EmbedStage(ctx, st, project, embed.StartProvider, mode)
	if err != nil {
		return res, fmt.Errorf("refresh: embed: %w", err)
	}
	return res, nil
}

// EmbedStage is the embed stage shared by refresh and `leankg index`: one embed
// run over st, serialized with leankg-embed through <storeDir>/embed.lock.
// start resolves the provider and the release that stops it: embed.StartProvider
// for a manual run, embed.AttachHookProvider for the SessionStart hook.
//
// The lock belongs NEXT TO THE STORE, not next to the project: a standalone
// store (FR-P2) has no <project>/.leankg at all, and the lock must not create
// one — that is what made the embed stage die with "open
// .../.leankg/embed.lock: no such file or directory" the moment the store
// moved out of the project.
//
// A run that cannot happen is a skip, not an error: an unavailable provider or a
// lock another embed run holds comes back as a non-empty skip reason with a zero
// report. The error is set only when embed.Run itself fails.
func EmbedStage(ctx context.Context, st store.Backend, storeDir string, start func(context.Context) (embed.Provider, func(), error), mode string) (embed.Report, string, error) {
	lockDir := filepath.Dir(store.StandaloneDBPath(storeDir))
	if lockDir == "" || lockDir == "." {
		lockDir = filepath.Join(storeDir, ".leankg")
	}
	lockPath := filepath.Join(lockDir, "embed.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return embed.Report{}, "", fmt.Errorf("embed lock dir: %w", err)
	}
	unlock, err := flock.TryLock(lockPath)
	if errors.Is(err, flock.ErrHeld) {
		return embed.Report{}, "another embed run holds " + lockPath, nil
	}
	if err != nil {
		return embed.Report{}, "", err
	}
	defer unlock()
	provider, release, err := start(ctx)
	if err != nil {
		// Rust parity (main.rs maybe_run_embed): an unavailable embedder is a
		// reported skip, not a failed refresh — code and docs are already
		// indexed at this point, and the reason names the fix.
		return embed.Report{}, err.Error(), nil
	}
	defer release()
	rep, err := embed.Run(ctx, st, provider, mode)
	return rep, "", err
}

// Render renders the Rust refresh verb output verbatim. The Rust handler
// printed the ingest lines before each stage and the embed driver added its own
// "[watch] Running incremental embed..." line; this renders the same text once
// the run has finished, so the stages are not streamed.
//
// Count mapping: the Go docindex emits one element per Markdown heading (the
// Rust engine also emitted a per-file document element), so Rust's
// "documents" reads as indexed .md files and "sections" as heading elements.
func Render(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Indexing code from %s...\n", res.Path)
	fmt.Fprintf(&b, "Indexed %d code files\n", res.Code.Files)
	if res.DocsIndexed {
		fmt.Fprintf(&b, "Indexing docs from %s...\n", res.DocsDir)
		fmt.Fprintf(&b, "Indexed %d documents and %d sections\n", res.DocsResult.Files, res.DocsResult.Elements)
	}
	if res.EmbedSkipped != "" {
		// Rust parity: report the reason and finish (no hard failure).
		fmt.Fprintf(&b, "Embedding skipped: %s\n", res.EmbedSkipped)
	} else {
		b.WriteString("Running embed...\n")
		mode := res.Embed.Mode
		if mode == "" {
			mode = "incremental"
		}
		fmt.Fprintf(&b, "[watch] Running %s embed...\n", mode)
	}
	b.WriteString("Refresh complete.\n")
	return b.String()
}
