package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/obsidian"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// obsidianUsage prints the Rust `leankg obsidian` verb set.
func obsidianUsage() {
	fmt.Fprint(os.Stderr, `usage: leankg obsidian <init|push|pull|watch|status> [--project DIR] [--vault PATH] [--debounce-ms N]

  init    create the vault and its README
  push    generate notes from LeanKG data
  pull    import note edits (annotations, notes, wiki-links) into LeanKG
  watch   watch the vault and auto-pull
  status  show vault status

Defaults: --project cwd, --vault <project>/.leankg/obsidian/vault.
`)
}

// nearestLeankgRoot ports the Rust find_project_root: the first directory at or
// above dir owning a .leankg directory, or dir itself when there is none.
func nearestLeankgRoot(dir string) string {
	start := dir
	for {
		if info, err := os.Stat(filepath.Join(dir, ".leankg")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

// cmdObsidian is the Rust `leankg obsidian` parity verb set.
func cmdObsidian(args []string) {
	if len(args) == 0 {
		obsidianUsage()
		os.Exit(2)
	}
	verb, rest := args[0], args[1:]
	fs := flag.NewFlagSet("obsidian "+verb, flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	vaultFlag := fs.String("vault", "", "custom vault path (default <project>/.leankg/obsidian/vault)")
	debounce := fs.Int("debounce-ms", 1000, "watch debounce delay in milliseconds")
	parseInterspersed("obsidian "+verb, fs, rest, 0)

	dir := *project
	if dir == "" {
		dir = envOr("LEANKG_PROJECT", "")
	}
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("resolve cwd: %v", err)
		}
		dir = nearestLeankgRoot(cwd)
	}
	vault := obsidian.VaultPath(filepath.Join(dir, ".leankg"), *vaultFlag)

	switch verb {
	case "init":
		if err := obsidian.New(vault, nil).Init(); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Obsidian vault initialized at:\n  %s\n\nNext steps:\n  leankg obsidian push    # Generate notes from LeanKG\n  leankg obsidian status  # Check vault status\n", vault)

	case "status":
		s, err := obsidian.New(vault, nil).Status()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("LeanKG Obsidian Vault Status")
		fmt.Println("============================")
		fmt.Println()
		fmt.Printf("  Vault: %s\n", vault)
		fmt.Printf("  Exists: %t\n", s.Initialized)
		if s.Initialized {
			fmt.Printf("  Notes: %d\n", s.NoteCount)
		} else {
			fmt.Println()
			fmt.Println("  Run 'leankg obsidian init' to initialize.")
		}

	case "push", "pull", "watch":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		st, err := store.Open(filepath.Join(dir, ".leankg", "leankg.db"), store.RW)
		if err != nil {
			log.Fatalf("open store: %v", err)
		}
		defer st.Close()
		if err := st.Migrate(); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		e := obsidian.New(vault, st)
		if s, err := e.Status(); err != nil {
			log.Fatal(err)
		} else if !s.Initialized {
			fmt.Fprintln(os.Stderr, "Vault not initialized. Run 'leankg obsidian init' first.")
			return
		}

		switch verb {
		case "push":
			fmt.Println("Pushing LeanKG data to Obsidian vault...")
			res, err := e.Push()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println()
			fmt.Println("Push complete:")
			fmt.Printf("  Notes generated: %d\n", res.Notes)
			if res.Failed > 0 {
				fmt.Printf("  Failed: %d\n", res.Failed)
			}
		case "pull":
			fmt.Println("Pulling annotations from Obsidian vault...")
			res, err := e.Pull()
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println()
			fmt.Println("Pull complete:")
			fmt.Printf("  Notes synced: %d\n", res.Notes)
			fmt.Printf("  Links synced: %d\n", res.Links+res.Documents)
			fmt.Printf("  Annotations imported: %d\n", res.Annotations)
			fmt.Printf("  Conflicts: %d\n", len(res.Conflicts))
			for _, c := range res.Conflicts {
				fmt.Printf("    %s: store %q vs note %q\n", c.ElementID, c.LocalAnnotation, c.RemoteAnnotation)
			}
			if len(res.Conflicts) > 0 {
				fmt.Println()
				fmt.Println("Conflicts detected (manual merge required).")
			}
		case "watch":
			fmt.Println("LeanKG Obsidian Watcher")
			fmt.Printf("  Vault: %s\n", vault)
			fmt.Printf("  Debounce: %dms\n", *debounce)
			fmt.Println("  Press Ctrl+C to stop.")
			fmt.Println()
			err := e.Watch(ctx, obsidian.WatchOptions{
				Debounce: time.Duration(*debounce) * time.Millisecond,
				OnEvent:  func(p string) { fmt.Printf("Detected change in: %s\n", p) },
				OnSync: func(res obsidian.PullResult, err error) {
					if err != nil {
						fmt.Fprintf(os.Stderr, "Pull failed: %v\n", err)
						return
					}
					fmt.Printf("Synced: notes=%d links=%d annotations=%d conflicts=%d\n",
						res.Notes, res.Links+res.Documents, res.Annotations, len(res.Conflicts))
				},
			})
			if err != nil && ctx.Err() == nil {
				log.Fatal(err)
			}
		}

	default:
		obsidianUsage()
		os.Exit(2)
	}
}
