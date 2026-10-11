// delete-annotation <element>: remove an element's annotation.
//
// The package has always had the operation (annot.Delete) and the CLI has
// always had the other three (annotate, link, search-annotations), so a
// caller who annotated the WRONG element had no way out: the row lived in the
// kv document forever and only a hand-edited store cleared it.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/internal/annot"
	"github.com/FreePeak/LeanKG/internal/store"
)

func cmdDeleteAnnotation(args []string) {
	fs := flag.NewFlagSet("delete-annotation", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	_ = fs.Parse(args)

	positional := parseInterspersed("delete-annotation", fs, args, 1)
	if len(positional) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg delete-annotation <element> [--project DIR]")
		os.Exit(2)
	}
	element := positional[0]

	st, _, err := openVerbStore(*project, store.RW)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()

	// Read first so a typo is reported as "no such annotation" rather than a
	// silent success — an idempotent delete that says "removed" when nothing
	// was there is how a caller learns they mistyped the qualified name.
	existing, err := annot.Get(st, element)
	if err != nil {
		verbFatal(err)
	}
	if existing == nil {
		fmt.Printf("No annotation found for '%s' (nothing removed)\n", element)
		return
	}
	if err := annot.Delete(st, element); err != nil {
		verbFatal(err)
	}
	fmt.Printf("Removed annotation for '%s' (was: %s)\n", element, existing.Description)
}
