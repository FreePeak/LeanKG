package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/go/internal/metrics"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cmdMetrics renders the persisted usage ledger (Rust `leankg metrics`:
// main.rs show_metrics + seed_test_metrics over db::get_metrics_summary).
// The verb opens the store read-write on purpose: --reset, --cleanup and --seed
// write, and Migrate has to be able to add the ledger table (migration 011) to a
// store created before it.
func cmdMetrics(args []string) {
	fs := flag.NewFlagSet("metrics", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	since := fs.String("since", "", "window in days, e.g. 7d or 7 (default: --retention, else 30)")
	tool := fs.String("tool", "", "only count calls of this tool (e.g. query)")
	jsonOut := fs.Bool("json", false, "print the summary as JSON")
	fs.BoolVar(jsonOut, "j", false, "shorthand for --json")
	session := fs.Bool("session", false, "show current-session metrics")
	reset := fs.Bool("reset", false, "delete every metric record")
	retention := fs.Int("retention", 0, "retention period in days (default 30)")
	cleanup := fs.Bool("cleanup", false, "delete records older than the retention window")
	seed := fs.Bool("seed", false, "seed test metrics data")
	parseInterspersed("metrics", fs, args, 0)

	dir := resolveProjectDir(*project)
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fatalJSON(err)
	}

	out, err := metrics.Show(st, metrics.Options{
		Since:     *since,
		Tool:      *tool,
		JSON:      *jsonOut,
		Session:   *session,
		Reset:     *reset,
		Retention: *retention,
		Cleanup:   *cleanup,
		Seed:      *seed,
	})
	if err != nil {
		fatalJSON(err)
	}
	fmt.Print(out)
}

// cmdDashboard renders the H10/FR-PLG-8 usage buckets (Rust run_dashboard over
// dashboard/mod.rs). Unlike Rust — whose bucket queries existed only on the
// PostgreSQL backend — the buckets come from store.Backend, so the default
// sqlite engine serves the dashboard too.
func cmdDashboard(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	since := fs.String("since", "", "window: <N>h, <N>d or <N>w (e.g. 24h, 7d, 30d); default all time")
	format := fs.String("format", "text", "output format: text|json")
	parseInterspersed("dashboard", fs, args, 0)

	dir := resolveProjectDir(*project)
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fatalJSON(err)
	}

	out, err := metrics.Dashboard(st, metrics.DashboardOptions{Since: *since, Format: *format})
	if err != nil {
		// Rust exits non-zero on an invalid --since window or --format value.
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}
