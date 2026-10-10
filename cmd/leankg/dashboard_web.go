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
	"strings"
	"syscall"
	"time"

	"github.com/FreePeak/LeanKG/internal/dashboard"
	"github.com/FreePeak/LeanKG/internal/sessionlink/all"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

const defaultDashboardAddr = "127.0.0.1:9701"

// cmdDashboardWeb starts the loopback dashboard server and opens the browser.
func cmdDashboardWeb(args []string) {
	if err := runDashboardWeb(context.Background(), args, os.Stdout, dashboard.OpenBrowser); err != nil {
		fmt.Fprintln(os.Stderr, "dashboard:", err)
		os.Exit(1)
	}
}

// runDashboardWeb serves the dashboard until ctx is done or SIGINT/SIGTERM
// arrives. opener is called with the URL unless --no-open is given.
func runDashboardWeb(ctx context.Context, args []string, out io.Writer, opener func(url string) error) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	addr := fs.String("addr", defaultDashboardAddr, "listen address (loopback unless --allow-remote)")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	project := fs.String("project", "", "default project filter for the reports")
	since := fs.String("since", "", "default window: <N>h, <N>d or <N>w (e.g. 7d); default all time")
	allowRemote := fs.Bool("allow-remote", false, "serve beyond loopback; every request needs --token")
	token := fs.String("token", "", "bearer token required with --allow-remote")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if err := dashboard.CheckListen(*addr, *allowRemote, *token); err != nil {
		return err
	}
	if *token != "" && !*allowRemote {
		fmt.Fprintln(os.Stderr, "dashboard: --token is ignored without --allow-remote (loopback needs no token)")
	}
	start, err := dashboard.ParseSince(*since, time.Now())
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	home := telemetry.Home()
	srv, err := dashboard.New(dashboard.Options{
		Home:        home,
		Since:       start,
		Project:     *project,
		Adapters:    all.Adapters(),
		Token:       *token,
		AllowRemote: *allowRemote,
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", *addr, err)
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
	if *allowRemote {
		fmt.Fprintln(out, "remote mode: every request needs 'Authorization: Bearer <token>'")
	}
	if !*noOpen && opener != nil {
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

// dashboardWantsText reports whether args carry --format. Only then does
// `leankg dashboard` keep its text/JSON output (DS-20).
func dashboardWantsText(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if name == "format" || strings.HasPrefix(name, "format=") {
			return true
		}
	}
	return false
}

func errNotImplemented(what string) error { return fmt.Errorf("%s: not implemented yet", what) }
