package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/FreePeak/LeanKG/internal/dashboard"
	"github.com/FreePeak/LeanKG/internal/sessionlink/all"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

const defaultDashboardAddr = "127.0.0.1:9701"

// dashboardMode is what `leankg dashboard` runs: the usage tables or the web
// dashboard. It is decided from the PARSED flags — the mode is a property of
// the values, never of the spelling of the command line.
type dashboardMode int

const (
	// dashboardModeWeb serves the dashboard and opens the browser. It is the
	// default (DS-20): a bare `leankg dashboard` starts the server, so the
	// web mode is the zero value — an unset mode is never silently the
	// tables.
	dashboardModeWeb dashboardMode = iota
	// dashboardModeTables renders the usage buckets as text or JSON.
	dashboardModeTables
)

// dashboardFlags is the ONE flag set of `leankg dashboard`: the usage tables
// and the web dashboard read the same definitions, so a single invocation
// can carry both (`leankg dashboard --addr 127.0.0.1:9701 --no-open
// --format text`). Before this set existed, the web flags lived in one
// FlagSet and the table flags in another, and the choice between them was
// made by scanning argv for "--format" — so the documented invocation sent
// the whole argv to the table-only FlagSet and died with "flag provided but
// not defined: -addr".
type dashboardFlags struct {
	addr        string
	noOpen      bool
	allowRemote bool
	token       string
	project     string
	since       string
	format      string
	// mode is the parsed mode: the tables when --format was given, the web
	// dashboard otherwise (DS-20).
	mode dashboardMode
}

// parseDashboardFlags parses the one `leankg dashboard` flag set. Flags may
// appear before or after positionals, as in every other verb here, and a
// positional argument is refused. A parse error exits 2 with the usage.
func parseDashboardFlags(args []string) *dashboardFlags {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	f := &dashboardFlags{}
	fs.StringVar(&f.addr, "addr", defaultDashboardAddr, "listen address (loopback unless --allow-remote)")
	fs.BoolVar(&f.noOpen, "no-open", false, "do not open the browser")
	fs.BoolVar(&f.allowRemote, "allow-remote", false, "serve beyond loopback; every request needs --token")
	fs.StringVar(&f.token, "token", "", "bearer token required with --allow-remote")
	// The two modes read different ledgers, so --project means what it
	// always meant in each: the project directory the tables are read from,
	// and the project filter the dashboard reports are narrowed by.
	fs.StringVar(&f.project, "project", "", "project directory (tables) or project filter (dashboard)")
	fs.StringVar(&f.since, "since", "", "window: <N>h, <N>d or <N>w (e.g. 24h, 7d, 30d); default all time")
	fs.StringVar(&f.format, "format", "", "output format: text|json (prints the usage tables instead of serving the dashboard)")
	parseInterspersed("dashboard", fs, args, 0)
	// Visit reports the flags that were actually given, so the mode comes
	// from the parsed values and not from a scan of argv: --format=text and
	// --format "" both ask for the tables, no --format asks for the server.
	f.mode = dashboardModeWeb
	fs.Visit(func(fl *flag.Flag) {
		if fl.Name == "format" {
			f.mode = dashboardModeTables
		}
	})
	return f
}

// cmdDashboardWeb starts the loopback dashboard server and opens the browser.
func cmdDashboardWeb(f *dashboardFlags) {
	if err := runDashboardWeb(context.Background(), f, os.Stdout, dashboard.OpenBrowser); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}

// runDashboardWeb serves the dashboard until ctx is done or SIGINT/SIGTERM
// arrives. opener is called with the URL unless --no-open is given.
func runDashboardWeb(ctx context.Context, f *dashboardFlags, out io.Writer, opener func(url string) error) error {
	if err := dashboard.CheckListen(f.addr, f.allowRemote, f.token); err != nil {
		return err
	}
	if f.token != "" && !f.allowRemote {
		fmt.Fprintln(os.Stderr, "dashboard: --token is ignored without --allow-remote (loopback needs no token)")
	}
	start, err := dashboard.ParseSince(f.since, time.Now())
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	home := telemetry.Home()
	srv, err := dashboard.New(dashboard.Options{
		Home:        home,
		Since:       start,
		Project:     f.project,
		Adapters:    all.Adapters(),
		Token:       f.token,
		AllowRemote: f.allowRemote,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", f.addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", f.addr, err)
	}
	url := "http://" + ln.Addr().String() + "/"

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		srv.Run(ctx)
	}()

	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	fmt.Fprintf(out, "LeanKG dashboard: %s (ledger %s)\n", url, telemetry.DBPath(home))
	if f.allowRemote {
		fmt.Fprintln(out, "remote mode: every request needs 'Authorization: Bearer <token>'")
	}
	if !f.noOpen && opener != nil {
		if err := opener(url); err != nil && !errors.Is(err, dashboard.ErrNoDisplay) {
			fmt.Fprintln(os.Stderr, "dashboard: could not open a browser:", err)
		}
	}

	var serveFailed error
	select {
	case <-ctx.Done():
	case serveFailed = <-serveErr:
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	stop()
	<-runDone
	if serveFailed != nil && !errors.Is(serveFailed, http.ErrServerClosed) {
		return fmt.Errorf("serve %s: %w", url, serveFailed)
	}
	return nil
}

func errNotImplemented(what string) error { return fmt.Errorf("%s: not implemented yet", what) }
