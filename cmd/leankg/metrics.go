package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/FreePeak/LeanKG/internal/cliexit"
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
func cmdMetrics(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("metrics", flag.ContinueOnError)
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
	if _, err := parseInterspersed("metrics", fs, args, 0); err != nil {
		return err
	}

	dir := resolveProjectDir(*project)
	if err := requireStore(ctx, "metrics", dir); err != nil {
		return err
	}
	st, err := openStore(ctx, dir, "", store.RW)
	if err != nil {
		return fatalJSON(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fatalJSON(err)
	}

	out, err := metrics.Show(st, metrics.Options{
		Since:       *since,
		Tool:        *tool,
		JSON:        *jsonOut,
		Session:     *session || *sessionID != "",
		SessionText: sessionTextIf(ctx, *session || *sessionID != "", *sessionID),
		Reset:       *reset,
		Retention:   *retention,
		Cleanup:     *cleanup,
		Seed:        *seed,
	})
	if err != nil {
		return fatalJSON(err)
	}
	fmt.Print(out)
	return nil
}

// cmdDashboard serves the web dashboard (DS-20). With --format it renders the
// H10/FR-PLG-8 usage buckets as text or JSON, unchanged: the buckets come from
// store.Backend (Rust run_dashboard over dashboard/mod.rs), so the default
// sqlite engine serves them too.
//
// The mode comes from the ONE dashboard flag set (cmd/leankg/dashboard_web.go):
// --format asks for the tables, its absence asks for the server. Both modes
// read the same flags, so `leankg dashboard --addr 127.0.0.1:9701 --no-open
// --format text` is one valid invocation.
func cmdDashboard(ctx context.Context, args []string) error {
	opts, err := parseDashboardFlags(args)
	if err != nil {
		return err
	}
	if opts.mode == dashboardModeWeb {
		return cmdDashboardWeb(ctx, opts)
	}

	dir := resolveProjectDir(opts.project)
	if err := requireStore(ctx, "dashboard", dir); err != nil {
		return err
	}
	st, err := openStore(ctx, dir, "", store.RW)
	if err != nil {
		return fatalJSON(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fatalJSON(err)
	}

	out, err := metrics.Dashboard(st, metrics.DashboardOptions{Since: opts.since, Format: opts.format})
	if err != nil {
		// Rust exits non-zero on an invalid --since window or --format value.
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		return cliexit.Code(1)
	}
	fmt.Print(out)
	return nil
}

// sessionTextIf renders the telemetry session only when --session asked.
func sessionTextIf(ctx context.Context, want bool, id string) string {
	if !want {
		return ""
	}
	return telemetrySessionText(ctx, id)
}

// telemetrySessionText renders one session from the per-user telemetry
// ledger (DS-16): id, or the most recent session when id is empty. It opens
// the ledger read-only, so it never creates one, and returns "" when there is
// no ledger or no such session.
func telemetrySessionText(ctx context.Context, id string) string {
	st, err := telemetry.OpenStore(ctx, telemetry.Home(), true)
	if err != nil {
		return ""
	}
	defer st.Close()
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
