// Shared-server graph sync: `leankg push` / `leankg pull` (Rust main.rs
// push_to_remote / pull_from_remote, CLI arms at 1634-1639). The wire protocol
// lives in internal/federation.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/internal/federation"
	"github.com/FreePeak/LeanKG/internal/store"
)

// cmdPush ports `leankg push --remote URL --token TOKEN [--env local]`.
//
// Rust resolved the project root by walking up from cwd for .leankg or
// leankg.yaml; no Go verb walks up, so this uses the shared
// --project/$LEANKG_PROJECT/cwd convention. The store is opened read-write the
// way Rust's init_db was: a missing one is created and migrated, and an empty
// graph is pushed as 0/0.
func cmdPush(args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	remote := fs.String("remote", "", "shared server base URL (required)")
	token := fs.String("token", "", "team token, sent as X-LeanKG-Token and Authorization: Bearer (required)")
	env := fs.String("env", "local", "environment label")
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	parseInterspersed("push", fs, args, 0)
	if *remote == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "leankg push: --remote and --token are required")
		os.Exit(2)
	}

	st, dir, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	// The payload's service name is the root's basename (Rust
	// project_path.file_name()); resolve the directory so a relative "." or
	// "--project foo" names the directory instead of "unknown".
	abs, err := filepath.Abs(dir)
	if err != nil {
		verbFatal(err)
	}

	res, err := federation.Push(context.Background(), st, federation.Options{
		Remote:     *remote,
		Token:      *token,
		Env:        *env,
		ProjectDir: abs,
	})
	if err != nil {
		verbFatal(err) // Rust's `?`: a transport failure exits 1
	}
	// Like Rust, a rejected push is reported on stderr and still exits 0.
	res.Report(os.Stdout, os.Stderr)
}

// cmdPull ports `leankg pull --remote URL --token TOKEN [--env production]`:
// fetch the shared server's graph and apply it locally with the same upsert
// policy a push applies. A remote without the graph route (a Rust shared
// server, any pre-#372 build) degrades to Rust's connectivity probe and
// touches nothing locally — same command, older server, no surprise.
func cmdPull(args []string) {
	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	remote := fs.String("remote", "", "shared server base URL (required)")
	token := fs.String("token", "", "team token, sent as X-LeanKG-Token and Authorization: Bearer (required)")
	env := fs.String("env", "production", "environment to pull")
	project := fs.String("project", "", "project directory the pulled graph is applied to (default cwd, or LEANKG_PROJECT)")
	parseInterspersed("pull", fs, args, 0)
	if *remote == "" || *token == "" {
		fmt.Fprintln(os.Stderr, "leankg pull: --remote and --token are required")
		os.Exit(2)
	}

	st, _, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	res, err := federation.Pull(context.Background(), st, federation.Options{
		Remote: *remote,
		Token:  *token,
		Env:    *env,
	})
	if err != nil {
		verbFatal(err)
	}
	res.Report(os.Stdout, os.Stderr)
	// A real pull upserts a remote graph locally; keep the freshness snapshot
	// in step with what this writer changed (the connectivity-probe fallback
	// applies nothing, so it needs no refresh).
	if res.Applied {
		if _, ierr := store.RefreshInventory(st); ierr != nil {
			fmt.Fprintf(os.Stderr, "pull: inventory snapshot: %v\n", ierr)
		}
	}
}
