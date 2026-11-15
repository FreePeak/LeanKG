// delete-annotation <element>: remove an element's annotation.
//
// The package has always had the operation (annot.Delete) and the CLI has
// always had the other three (annotate, link, search-annotations), so a
// caller who annotated the WRONG element had no way out: the row lived in the
// kv document forever and only a hand-edited store cleared it.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/internal/annot"
	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/store"
)

func cmdDeleteAnnotation(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete-annotation", flag.ContinueOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	_ = cliexit.Parse(fs, args)

	positional, err := parseInterspersed("delete-annotation", fs, args, 1)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg delete-annotation <element> [--project DIR]")
		return cliexit.Code(2)
	}
	element := positional[0]

	st, _, err := openVerbStore(ctx, *project, store.RW)
	if err != nil {
		return verbFatal(err)
	}
	defer st.Close()

	// Read first so a typo is reported as "no such annotation" rather than a
	// silent success — an idempotent delete that says "removed" when nothing
	// was there is how a caller learns they mistyped the qualified name.
	existing, err := annot.Get(st, element)
	if err != nil {
		return verbFatal(err)
	}
	if existing == nil {
		fmt.Printf("No annotation found for '%s' (nothing removed)\n", element)
		return nil
	}
	if err := annot.Delete(st, element); err != nil {
		return verbFatal(err)
	}
	fmt.Printf("Removed annotation for '%s' (was: %s)\n", element, existing.Description)
	return nil
}
