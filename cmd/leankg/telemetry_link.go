package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink"
	"github.com/FreePeak/LeanKG/internal/sessionlink/all"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// cmdTelemetryLink implements `leankg telemetry link [--older-than 2m]`: one
// linking pass over unlinked calls older than the cutoff. It prints the
// LinkStats as JSON. Transcripts are opened only for consented clients.
func cmdTelemetryLink(args []string) {
	olderThan, err := parseOlderThan(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "usage: leankg telemetry link [--older-than 2m]\n  links captured calls at least this old to their agent transcripts (granted clients only)")
		return
	}
	if err != nil {
		fatalJSON(err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		fatalJSON(err)
	}
	home := telemetry.Home()
	// With no ledger there is nothing to link. Do not create one here.
	if _, err := os.Stat(telemetry.DBPath(home)); errors.Is(err, os.ErrNotExist) {
		printJSON(sessionlink.LinkStats{})
		return
	}
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		fatalJSON(err)
	}
	st, err := telemetry.OpenStore(home, false)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()
	// The agent stores live under the real user home, never LEANKG_HOME.
	stats, err := sessionlink.Link(context.Background(), st, cfg, userHome, all.Adapters(), time.Now(), olderThan)
	if err != nil {
		fatalJSON(err)
	}
	printJSON(stats)
}

// parseOlderThan reads --older-than (default sessionlink.DefaultOlderThan).
func parseOlderThan(args []string) (time.Duration, error) {
	fs := flag.NewFlagSet("telemetry link", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	d := fs.Duration("older-than", sessionlink.DefaultOlderThan, "link calls at least this old")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *d < 0 {
		return 0, errors.New("telemetry link: --older-than must not be negative")
	}
	return *d, nil
}
