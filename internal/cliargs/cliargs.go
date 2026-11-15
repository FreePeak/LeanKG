// Package cliargs parses a verb's flags and positionals for the command
// binaries (cmd/leankg and cmd/leankg-embed).
package cliargs

import (
	"flag"
	"fmt"

	"github.com/FreePeak/LeanKG/internal/cliexit"
)

// Interspersed parses args into fs and returns the positionals in order. Flags
// may appear before, between or after positionals (clap semantics). Go's flag
// package stops at the first positional, which left a `verb <positional>
// --flag` flag and every token after it unparsed.
//
// max is how many positionals the verb accepts; a negative max accepts any
// number. A surplus positional fails as soon as it is collected, before any
// trailing flag is parsed, so nothing is silently ignored. A bad flag is the
// flag package's exit-2 status (cliexit.Parse); -h is success.
func Interspersed(verb string, fs *flag.FlagSet, args []string, max int) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := cliexit.Parse(fs, rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		if max >= 0 && len(positional) > max {
			return nil, cliexit.Usage(fmt.Errorf("%s: unexpected argument %q", verb, rest[0]))
		}
		rest = rest[1:]
	}
}
