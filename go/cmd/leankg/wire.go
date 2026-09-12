package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/FreePeak/LeanKG/go/internal/index"
	"github.com/FreePeak/LeanKG/go/internal/langs"
	"github.com/FreePeak/LeanKG/go/internal/store"
	"github.com/FreePeak/LeanKG/go/internal/watch"
)

// cmdWriter is the W2 writer role: one index pass, then watch loop. Pairs
// with `leankg serve --read-only` (WAL readers never block the writer).
func cmdWriter(args []string) {
	fs := flag.NewFlagSet("writer", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd)")
	parseInterspersed("writer", fs, args, 0)
	dir := *project
	if dir == "" {
		var err error
		dir, err = os.Getwd()
		if err != nil {
			log.Fatalf("resolve cwd: %v", err)
		}
	}
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
	langReg := langs.DefaultRegistry()
	if _, aerr := langReg.Activate(dir); aerr != nil {
		log.Fatalf("language detection: %v", aerr)
	}
	res, err := index.IndexDirWith(ctx, st, dir, langReg)
	if err != nil {
		log.Fatalf("initial index: %v", err)
	}
	log.Printf("writer: initial index %s: files=%d elements=%d relationships=%d skipped=%d",
		dir, res.Files, res.Elements, res.Relationships, res.Skipped)

	w, err := watch.Start(ctx, st, dir, watch.Options{
		Registry: langReg,
		OnEvent: func(path, kind string) {
			log.Printf("writer: %s %s", kind, path)
		},
	})
	if err != nil {
		log.Fatalf("watch: %v", err)
	}
	log.Printf("writer: watching %s (Ctrl-C to stop)", dir)
	<-ctx.Done()
	w.Stop()
}

func cmdConnect(args []string) {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	httpMode := fs.Bool("http", false, "write a remote (HTTP URL) entry instead of stdio")
	url := fs.String("url", "", "remote MCP URL (with --http; default http://localhost:9699/mcp)")
	project := fs.String("project", "", "explicit project path escape hatch (stdio only)")
	positional := parseInterspersed("connect", fs, args, 1)
	if len(positional) != 1 {
		log.Fatalf("connect requires exactly one client: %s", strings.Join(Clients(), ", "))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolve home: %v", err)
	}
	cfg := Config{Exe: exePath()}
	if *httpMode {
		cfg.Mode = "http"
		cfg.URL = *url
		if cfg.URL == "" {
			cfg.URL = "http://localhost:9699/mcp"
		}
	} else {
		cfg.Mode = "stdio"
		cfg.Project = *project
	}
	if err := WriteClient(home, positional[0], cfg); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("connected %s (%s)\n", positional[0], cfg.Mode)
}

func cmdInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	target := fs.String("target", "", "client target: "+strings.Join(Clients(), "|"))
	httpMode := fs.Bool("http", false, "remote entry instead of stdio")
	url := fs.String("url", "", "remote MCP URL (with --http)")
	project := fs.String("project", "", "explicit project path (stdio escape hatch)")
	registerCWD := fs.Bool("register-cwd", false, "write a session-start hook running `leankg index <project>` (claude-code)")
	parseInterspersed("install", fs, args, 0)
	if *target == "" {
		log.Fatalf("install requires --target (%s)", strings.Join(Clients(), "|"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("resolve home: %v", err)
	}
	cfg := Config{Exe: exePath()}
	if *httpMode {
		cfg.Mode = "http"
		cfg.URL = *url
		if cfg.URL == "" {
			cfg.URL = "http://localhost:9699/mcp"
		}
	} else {
		cfg.Mode = "stdio"
		cfg.Project = *project
	}
	opts := InstallOptions{HTTP: *httpMode, URL: cfg.URL, Project: *project, RegisterCWD: *registerCWD}
	if err := Install(home, *target, opts); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("installed %s (%s)%s\n", *target, cfg.Mode, registerHint(*registerCWD))
}

func registerHint(on bool) string {
	if on {
		return " + session-start hook"
	}
	return ""
}

func exePath() string {
	exe, err := os.Executable()
	if err != nil {
		return "leankg"
	}
	return exe
}
