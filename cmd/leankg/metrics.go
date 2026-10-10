package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/internal/metrics"
	"github.com/FreePeak/LeanKG/internal/store"
	"github.com/FreePeak/LeanKG/internal/telemetry"
	tmetrics "github.com/FreePeak/LeanKG/internal/telemetry/metrics"
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
	session := fs.Bool("session", false, "show the latest agent session from the telemetry ledger")
	sessionID := fs.String("session-id", "", "show this telemetry session (implies --session)")
	reset := fs.Bool("reset", false, "delete every metric record")
	retention := fs.Int("retention", 0, "retention period in days (default 30)")
	cleanup := fs.Bool("cleanup", false, "delete records older than the retention window")
	seed := fs.Bool("seed", false, "seed test metrics data")
	parseInterspersed("metrics", fs, args, 0)

	dir := resolveProjectDir(*project)
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), "", store.RW)
	if err != nil {
		fatalJSON(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		fatalJSON(err)
	}

	out, err := metrics.Show(st, metrics.Options{
		Since:       *since,
		Tool:        *tool,
		JSON:        *jsonOut,
		Session:     *session || *sessionID != "",
		SessionText: sessionTextIf(*session || *sessionID != "", *sessionID),
		Reset:       *reset,
		Retention:   *retention,
		Cleanup:     *cleanup,
		Seed:        *seed,
	})
	if err != nil {
		fatalJSON(err)
	}
	fmt.Print(out)
}

// cmdDashboard serves the web dashboard (DS-20). With --format it renders the
// H10/FR-PLG-8 usage buckets as text or JSON, unchanged: the buckets come from
// store.Backend (Rust run_dashboard over dashboard/mod.rs), so the default
// sqlite engine serves them too.
func cmdDashboard(args []string) {
	if !dashboardWantsText(args) {
		cmdDashboardWeb(args)
		return
	}
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	since := fs.String("since", "", "window: <N>h, <N>d or <N>w (e.g. 24h, 7d, 30d); default all time")
	format := fs.String("format", "text", "output format: text|json")
	parseInterspersed("dashboard", fs, args, 0)

	dir := resolveProjectDir(*project)
	st, err := store.OpenBackend(context.Background(), dir,
		envOr("LEANKG_DB_ENGINE", "sqlite"), os.Getenv("LEANKG_PG_URL"), "", store.RW)
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

// sessionTextIf renders the telemetry session only when --session asked.
func sessionTextIf(want bool, id string) string {
	if !want {
		return ""
	}
	return telemetrySessionText(id)
}

// telemetrySessionText renders one session from the per-user telemetry
// ledger (DS-16): id, or the most recent session when id is empty. It opens
// the ledger read-only, so it never creates one, and returns "" when there is
// no ledger or no such session.
func telemetrySessionText(id string) string {
	st, err := telemetry.OpenStore(telemetry.Home(), true)
	if err != nil {
		return ""
	}
	defer st.Close()
	ctx := context.Background()
	if id == "" {
		list, err := tmetrics.SessionList(ctx, st, tmetrics.Options{}, "", 1, 0)
		if err != nil || len(list.Sessions) == 0 {
			return ""
		}
		id = list.Sessions[0].ID
	}
	d, ok, err := tmetrics.SessionDetail(ctx, st, id)
	if err != nil || !ok {
		return ""
	}
	return tmetrics.SessionText(d)
}
