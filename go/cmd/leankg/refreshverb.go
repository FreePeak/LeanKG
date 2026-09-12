// `leankg refresh` — index code + docs + embed in one command (Rust `refresh`
// verb; the stages themselves live in internal/refresh).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/FreePeak/LeanKG/go/internal/refresh"
)

// cmdRefresh runs the three refresh stages in Rust order.
//
// --full is registered for CLI parity only: the Rust Refresh handler
// destructured `full` and never passed it to the embed driver, which always
// ran incremental (see refresh.Options.Full).
func cmdRefresh(args []string) {
	fs := flag.NewFlagSet("refresh", flag.ExitOnError)
	project := fs.String("project", ".", "project root (the store lives at <project>/.leankg)")
	docs := fs.String("docs", "", "docs directory (default <project>/docs when it exists)")
	source := fs.String("source", "", "remote source URI (unsupported by the Go engine)")
	full := fs.Bool("full", false, "accepted for Rust CLI parity; refresh always embeds incrementally")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	path := ""
	if fs.NArg() > 0 {
		path = fs.Arg(0)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := refresh.Run(ctx, refresh.Options{
		Project: *project,
		Path:    path,
		Docs:    *docs,
		Full:    *full,
		Source:  *source,
	})
	if err != nil {
		fatalText(err)
	}
	fmt.Print(refresh.Render(res))
}
