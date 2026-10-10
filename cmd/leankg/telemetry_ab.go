package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/metrics"
)

// cmdTelemetryImportAB implements `leankg telemetry import-ab <dir>`: it loads
// cross_tool run rows, judge scores and benchmark/ab trials under <dir> into
// the telemetry ledger as controlled A/B runs (plan v4.15 DS-18).
func cmdTelemetryImportAB(args []string) {
	if len(args) != 1 || args[0] == "" {
		fatalJSON(errors.New("usage: leankg telemetry import-ab <dir>"))
	}
	st, err := telemetry.OpenStore(telemetry.Home(), false)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()
	if err := importABDir(context.Background(), args[0], st, time.Now(), os.Stdout); err != nil {
		fatalJSON(err)
	}
}

// importABDir parses dir and inserts the runs into st, then writes a summary
// and any warnings to w. Runs that fail pins or leak checks are inserted as
// invalid, so the dashboard can show why they were excluded.
func importABDir(ctx context.Context, dir string, st telemetry.Store, now time.Time, w io.Writer) error {
	imp, err := metrics.ParseABDir(dir, now)
	if err != nil {
		return err
	}
	if len(imp.Runs) > 0 {
		if err := st.InsertABRuns(ctx, imp.Runs); err != nil {
			return err
		}
	}
	valid := 0
	for _, r := range imp.Runs {
		if r.Valid {
			valid++
		}
	}
	for _, warn := range imp.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
	fmt.Fprintf(w, "imported %d runs from %d files (%d valid, %d invalid)\n",
		len(imp.Runs), imp.Files, valid, len(imp.Runs)-valid)
	return nil
}
