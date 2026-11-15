// `leankg refresh` — index code + docs + embed in one command (Rust `refresh`
// verb; the stages themselves live in internal/refresh).
package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/FreePeak/LeanKG/internal/refresh"
)

// cmdRefresh runs the three refresh stages in Rust order.
//
// --full is registered for CLI parity only: the Rust Refresh handler
// destructured `full` and never passed it to the embed driver, which always
// ran incremental (see refresh.Options.Full).
func cmdRefresh(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("refresh", flag.ContinueOnError)
	project := fs.String("project", ".", "project root (the store lives at <project>/.leankg)")
	docs := fs.String("docs", "", "docs directory (default <project>/docs when it exists)")
	source := fs.String("source", "", "remote source URI: git+<url>, gs://bucket/prefix, or a local path")
	refName := fs.String("ref-name", "", "git ref for --source git+... (default: main)")
	authFlag := fs.String("auth", "", "credential for --source (git token or GCS access token)")
	full := fs.Bool("full", false, "accepted for Rust CLI parity; refresh always embeds incrementally")
	positional, err := parseInterspersed("refresh", fs, args, 1)
	if err != nil {
		return err
	}
	path := ""
	if len(positional) == 1 {
		path = positional[0]
	}
	res, err := refresh.Run(ctx, refresh.Options{
		Project: *project,
		Path:    path,
		Docs:    *docs,
		Full:    *full,
		Source:  *source,
		RefName: *refName,
		Auth:    sourceAuth(*authFlag),
		Start:   embedStartFromEnv(),
	})
	if err != nil {
		return verbFatal(err)
	}
	fmt.Print(refresh.Render(res))
	return nil
}
