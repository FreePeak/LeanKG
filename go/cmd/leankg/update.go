// `leankg update` (issue #73): replace the running binary with the newest
// GitHub Release. All the logic lives in internal/update; this file is flag
// parsing, printing, and exit codes.
//
//	leankg update                  # fetch and install the newest release
//	leankg update --check          # report current vs latest (exit 1 if behind)
//	leankg update --repo o/r       # update from another repository
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/update"
)

// cmdUpdate returns the process exit code: 0 when nothing needed doing or the
// install succeeded, 1 when --check found a newer release or anything failed,
// 2 on a flag error (flag.ExitOnError handles that path itself).
func cmdUpdate(args []string) int {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	check := fs.Bool("check", false, "report the current and latest version without installing (exit 1 when behind)")
	repo := fs.String("repo", update.DefaultRepo, "repository to update from, as owner/name")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if extra := fs.Args(); len(extra) > 0 {
		fmt.Fprintf(os.Stderr, "update: unexpected argument %q\n", extra[0])
		return 2
	}

	token := os.Getenv("GITHUB_TOKEN")
	cur := Version()
	if *check {
		fmt.Printf("current: %s\n", cur)
	}
	res, err := update.Run(context.Background(), update.Options{
		Repo:           *repo,
		CurrentVersion: cur,
		Token:          token,
		Check:          *check,
		Logf:           func(format string, args ...any) { fmt.Printf("update: "+format+"\n", args...) },
	})
	var pe *update.PermError
	switch {
	case errors.As(err, &pe):
		// The one failure with a known fix: print it as a copy-paste command.
		fmt.Fprintln(os.Stderr, pe)
		return 1
	case err != nil:
		fmt.Fprintf(os.Stderr, "update: %v\n", err)
		return 1
	}

	switch res.Status {
	case update.StatusUpToDate:
		fmt.Printf("leankg %s is already up to date\n", res.Current)
	case update.StatusCheckedCurrent:
		fmt.Printf("latest:  %s — up to date\n", res.Latest)
	case update.StatusCheckedBehind:
		fmt.Printf("latest:  %s — a newer release is available (run `leankg update`)\n", res.Latest)
		return 1
	case update.StatusUpdated:
		fmt.Printf("updated leankg %s -> %s (%s)\n", res.Current, res.Latest, res.AssetName)
		fmt.Printf("installed: %s\n", strings.Join(res.Replaced, "\ninstalled: "))
		if len(res.Backups) > 0 {
			fmt.Printf("previous binaries kept: %s\n", strings.Join(res.Backups, ", "))
		}
		fmt.Printf("restart any running `leankg serve` to pick up the new version\n")
	}
	if res.Notice != "" {
		fmt.Println(res.Notice)
	}
	return 0
}
