package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/FreePeak/LeanKG/internal/sessionlink/all"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// knownSessionClients are the agents whose transcripts LeanKG can read
// (DS-01); the adapter registry is the one home of the list.
var knownSessionClients = all.Clients

// telemetryEnableOpts are the parsed flags of `leankg telemetry enable`.
type telemetryEnableOpts struct {
	bodies   bool
	sessions string
	yes      bool
}

// cmdTelemetry implements `leankg telemetry status|enable|disable|purge|link|import-ab`.
func cmdTelemetry(args []string) {
	if len(args) == 0 {
		telemetryUsage()
	}
	home := telemetry.Home()
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("telemetry status", flag.ExitOnError)
		jsonOut := fs.Bool("json", false, "print the status as JSON")
		parseInterspersed("telemetry status", fs, args[1:], 0)
		if err := telemetryStatus(home, *jsonOut, os.Stdout); err != nil {
			fatalJSON(err)
		}
	case "enable":
		var opts telemetryEnableOpts
		fs := flag.NewFlagSet("telemetry enable", flag.ExitOnError)
		fs.BoolVar(&opts.bodies, "bodies", false, "also record redacted, capped args and response bodies")
		fs.StringVar(&opts.sessions, "sessions", "", "comma list of agents whose transcripts may be read, or 'all'")
		fs.BoolVar(&opts.yes, "yes", false, "grant without the interactive confirmation")
		parseInterspersed("telemetry enable", fs, args[1:], 0)
		if err := telemetryEnable(home, opts, os.Stdin, stdinIsTTY(), os.Stdout); err != nil {
			fatalJSON(err)
		}
	case "disable":
		parseInterspersed("telemetry disable", flag.NewFlagSet("telemetry disable", flag.ExitOnError), args[1:], 0)
		if err := telemetryDisable(home, os.Stdout); err != nil {
			fatalJSON(err)
		}
	case "purge":
		fs := flag.NewFlagSet("telemetry purge", flag.ExitOnError)
		before := fs.String("before", "", "delete rows older than this: 30d, or a date like 2026-09-01")
		all := fs.Bool("all", false, "delete every row")
		parseInterspersed("telemetry purge", fs, args[1:], 0)
		now := time.Now()
		var cutoff time.Time
		switch {
		case *all && *before != "":
			fatalJSON(errors.New("telemetry purge: use --before or --all, not both"))
		case *all:
			cutoff = now.Add(24 * time.Hour) // every row written so far
		case *before != "":
			t, err := parseBefore(*before, now)
			if err != nil {
				fatalJSON(err)
			}
			cutoff = t
		default:
			fatalJSON(errors.New("telemetry purge: pass --before <30d|date> or --all"))
		}
		if err := telemetryPurge(home, cutoff, os.Stdout); err != nil {
			fatalJSON(err)
		}
	case "link":
		cmdTelemetryLink(args[1:])
	case "import-ab":
		cmdTelemetryImportAB(args[1:])
	default:
		telemetryUsage()
	}
}

func telemetryUsage() {
	fmt.Fprintln(os.Stderr, "usage: leankg telemetry status [--json] | enable [--bodies] [--sessions <list|all>] [--yes] | disable | purge (--before <30d|date> | --all) | link | import-ab <dir>")
	os.Exit(2)
}

// stdinIsTTY reports whether stdin is a character device, so a y/N prompt
// can be shown. /dev/null also passes; it then reads EOF and declines.
func stdinIsTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// telemetryEnable records consent (DS-01). It prints what will be recorded
// and where, then asks y/N on a TTY. Without a TTY it refuses unless --yes.
func telemetryEnable(home string, opts telemetryEnableOpts, in io.Reader, tty bool, out io.Writer) error {
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		return err
	}
	level := telemetry.Metadata
	if opts.bodies {
		level = telemetry.Bodies
	}
	sessionsSet := strings.TrimSpace(opts.sessions) != ""
	var clients []string
	if sessionsSet {
		if clients, err = parseSessionClients(opts.sessions); err != nil {
			return err
		}
	}
	sessionsOn := cfg.Sessions.Enabled
	granted := cfg.Sessions.Clients
	if sessionsSet {
		sessionsOn = true
		granted = clients
	}

	describeTelemetryGrant(out, home, level, cfg.RetentionDays, sessionsOn, granted, sessionsSet)
	if !tty && !opts.yes {
		return errors.New("telemetry enable: no terminal to confirm on; re-run with --yes to grant consent")
	}
	if tty && !opts.yes {
		fmt.Fprint(out, "Record as described above? [y/N]: ")
		answer, _ := bufio.NewReader(in).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "telemetry not changed.")
			return nil
		}
	}

	cfg.Capture = level
	if sessionsSet {
		cfg.Sessions = telemetry.SessionsConfig{Enabled: len(clients) > 0, Clients: clients}
	}
	cfg.Consent = telemetry.Consent{GrantedAt: time.Now().UTC(), GrantedBy: "cli", Version: telemetry.ConsentVersion}
	if err := telemetry.SaveConfig(home, cfg); err != nil {
		return err
	}
	fmt.Fprintf(out, "telemetry enabled at %s. Servers read this setting when they start.\n", level)
	if env := os.Getenv("LEANKG_TELEMETRY"); env != "" {
		if eff := telemetry.EffectiveLevel(cfg, env); eff != level {
			fmt.Fprintf(out, "note: LEANKG_TELEMETRY=%s lowers the effective level to %s.\n", env, eff)
		}
	}
	return nil
}

// describeTelemetryGrant prints the recording scope before consent.
func describeTelemetryGrant(out io.Writer, home string, level telemetry.Level, retentionDays int, sessionsOn bool, granted []string, sessionsSet bool) {
	if retentionDays <= 0 {
		retentionDays = telemetry.DefaultRetentionDays
	}
	fmt.Fprintf(out, "Telemetry level: %s\n", level)
	switch level {
	case telemetry.Bodies:
		fmt.Fprintln(out, "  records: outcome, sizes, latency, client identity, arg keys and hashes,")
		fmt.Fprintln(out, "           and redacted, size-capped request args and response bodies")
	default:
		fmt.Fprintln(out, "  records: outcome, sizes, latency, client identity, arg keys and hashes")
	}
	fmt.Fprintf(out, "  kept for: %d days\n", retentionDays)
	fmt.Fprintf(out, "  config:   %s\n", telemetry.ConfigPath(home))
	fmt.Fprintf(out, "  ledger:   %s\n", telemetry.DBPath(home))
	fmt.Fprintln(out, "  network:  none; everything stays on this machine")
	if !sessionsOn || len(granted) == 0 {
		if sessionsSet {
			fmt.Fprintln(out, "  transcripts: none (session reading disabled)")
		} else {
			fmt.Fprintln(out, "  transcripts: unchanged (not granted)")
		}
		return
	}
	fmt.Fprintln(out, "  transcripts that may be read:")
	for _, c := range granted {
		if c == "all" {
			for _, k := range knownSessionClients {
				fmt.Fprintf(out, "    %-12s %s\n", k, transcriptPath(k))
			}
			continue
		}
		fmt.Fprintf(out, "    %-12s %s\n", c, transcriptPath(c))
	}
}

// transcriptPath is where a client keeps its transcripts, as its adapter
// resolves it (honoring CLAUDE_CONFIG_DIR, PI_CODING_AGENT_DIR, ...), so the
// grant names exactly the paths the linker will read.
func transcriptPath(client string) string {
	a, ok := all.ByClient(client)
	if !ok {
		return "(unknown client)"
	}
	home, _ := os.UserHomeDir()
	return strings.Join(a.Roots(home), ", ")
}

// telemetryDisable turns capture off and revokes session reading. The client
// list is kept so a later enable can reuse it.
func telemetryDisable(home string, out io.Writer) error {
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		return err
	}
	cfg.Capture = telemetry.Off
	cfg.Sessions.Enabled = false
	if err := telemetry.SaveConfig(home, cfg); err != nil {
		return err
	}
	fmt.Fprintln(out, "telemetry disabled: capture off, session reading off. Existing rows remain until purged.")
	return nil
}

// telemetryPurge deletes rows older than before. It never creates a ledger.
func telemetryPurge(home string, before time.Time, out io.Writer) error {
	if _, err := os.Stat(telemetry.DBPath(home)); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(out, "no telemetry ledger at %s; nothing to purge\n", telemetry.DBPath(home))
		return nil
	}
	st, err := telemetry.OpenStore(home, false)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.Purge(context.Background(), before)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "purged %d rows older than %s\n", n, before.Format(time.RFC3339))
	return nil
}

// telemetryStatusView is the status report, text or JSON.
type telemetryStatusView struct {
	Home            string           `json:"home"`
	ConfigPath      string           `json:"config_path"`
	ConfigPresent   bool             `json:"config_present"`
	DBPath          string           `json:"db_path"`
	DBPresent       bool             `json:"db_present"`
	Capture         telemetry.Level  `json:"capture"`
	EffectiveLevel  telemetry.Level  `json:"effective_level"`
	EnvOverride     string           `json:"env_override,omitempty"`
	ConsentGranted  bool             `json:"consent_granted"`
	ConsentAt       time.Time        `json:"consent_at,omitzero"`
	ConsentBy       string           `json:"consent_by,omitempty"`
	ConsentVersion  int              `json:"consent_version"`
	CurrentVersion  int              `json:"current_consent_version"`
	SessionsEnabled bool             `json:"sessions_enabled"`
	SessionClients  []string         `json:"session_clients"`
	RetentionDays   int              `json:"retention_days"`
	MaxBodyBytes    int              `json:"max_body_bytes"`
	Stats           *telemetry.Stats `json:"stats,omitempty"`
	StatsError      string           `json:"stats_error,omitempty"`
}

// telemetryStatus reports the config, effective level, grants and, when the
// ledger exists, its Stats. It opens the ledger read-only and never creates it.
func telemetryStatus(home string, asJSON bool, out io.Writer) error {
	cfg, err := telemetry.LoadConfig(home)
	if err != nil {
		return err
	}
	env := os.Getenv("LEANKG_TELEMETRY")
	v := telemetryStatusView{
		Home:            home,
		ConfigPath:      telemetry.ConfigPath(home),
		ConfigPresent:   fileExists(telemetry.ConfigPath(home)),
		DBPath:          telemetry.DBPath(home),
		DBPresent:       fileExists(telemetry.DBPath(home)),
		Capture:         cfg.Capture,
		EffectiveLevel:  telemetry.EffectiveLevel(cfg, env),
		EnvOverride:     env,
		ConsentGranted:  !cfg.Consent.GrantedAt.IsZero(),
		ConsentAt:       cfg.Consent.GrantedAt,
		ConsentBy:       cfg.Consent.GrantedBy,
		ConsentVersion:  cfg.Consent.Version,
		CurrentVersion:  telemetry.ConsentVersion,
		SessionsEnabled: cfg.Sessions.Enabled,
		SessionClients:  cfg.Sessions.Clients,
		RetentionDays:   cfg.RetentionDays,
		MaxBodyBytes:    cfg.MaxBodyBytes,
	}
	if v.DBPresent {
		if st, err := telemetry.OpenStore(home, true); err != nil {
			v.StatsError = err.Error()
		} else {
			stats, err := st.Stats(context.Background())
			_ = st.Close()
			if err != nil {
				v.StatsError = err.Error()
			} else {
				v.Stats = &stats
			}
		}
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	printTelemetryStatus(out, v)
	return nil
}

func printTelemetryStatus(out io.Writer, v telemetryStatusView) {
	fmt.Fprintf(out, "capture (configured): %s\n", v.Capture)
	fmt.Fprintf(out, "effective level:      %s\n", v.EffectiveLevel)
	if v.EnvOverride != "" {
		fmt.Fprintf(out, "env LEANKG_TELEMETRY: %s\n", v.EnvOverride)
	}
	switch {
	case !v.ConsentGranted:
		fmt.Fprintln(out, "consent:              none (capture stays off)")
	case v.ConsentVersion < v.CurrentVersion:
		fmt.Fprintf(out, "consent:              version %d is older than %d; run `leankg telemetry enable` again\n", v.ConsentVersion, v.CurrentVersion)
	default:
		fmt.Fprintf(out, "consent:              granted %s by %s (version %d)\n", v.ConsentAt.Format(time.RFC3339), v.ConsentBy, v.ConsentVersion)
	}
	if v.SessionsEnabled && len(v.SessionClients) > 0 {
		fmt.Fprintf(out, "sessions:             enabled for %s\n", strings.Join(v.SessionClients, ", "))
	} else {
		fmt.Fprintln(out, "sessions:             disabled")
	}
	fmt.Fprintf(out, "retention:            %d days, body cap %d bytes\n", v.RetentionDays, v.MaxBodyBytes)
	fmt.Fprintf(out, "config:               %s (present: %v)\n", v.ConfigPath, v.ConfigPresent)
	fmt.Fprintf(out, "ledger:               %s (present: %v)\n", v.DBPath, v.DBPresent)
	if v.StatsError != "" {
		fmt.Fprintf(out, "ledger stats:         unavailable: %s\n", v.StatsError)
	}
	if s := v.Stats; s != nil {
		fmt.Fprintf(out, "calls:                %d\n", s.Calls)
		fmt.Fprintf(out, "memory events:        %d\n", s.MemoryEvents)
		fmt.Fprintf(out, "sessions:             %d (links: %d)\n", s.Sessions, s.Links)
		fmt.Fprintf(out, "dropped events:       %d\n", s.DroppedEvents)
		fmt.Fprintf(out, "size:                 %d bytes\n", s.SizeBytes)
		if !s.OldestTS.IsZero() {
			fmt.Fprintf(out, "range:                %s .. %s\n", s.OldestTS.Format(time.RFC3339), s.NewestTS.Format(time.RFC3339))
		}
	}
}

// parseSessionClients validates a --sessions list. "all" stands alone.
func parseSessionClients(s string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		c := strings.ToLower(strings.TrimSpace(part))
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if c == "all" {
			return []string{"all"}, nil
		}
		if !telemetryHasClient(c) {
			return nil, fmt.Errorf("telemetry: unknown client %q (known: %s, all)", c, strings.Join(knownSessionClients, ", "))
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("telemetry: --sessions names no client")
	}
	return out, nil
}

func telemetryHasClient(c string) bool {
	for _, k := range knownSessionClients {
		if k == c {
			return true
		}
	}
	return false
}

var beforeDaysRe = regexp.MustCompile(`^(\d+)d$`)

// parseBefore reads a --before value: "<n>d" or a date (YYYY-MM-DD or RFC3339).
func parseBefore(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if m := beforeDaysRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("telemetry: --before %q: %w", s, err)
		}
		return now.AddDate(0, 0, -n), nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("telemetry: --before %q: want <n>d, YYYY-MM-DD or RFC3339", s)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// telemetryOnce and telemetryStop make the process Recorder a singleton.
var (
	telemetryOnce sync.Once
	// telemetryBuildOnce guards the lazy build (ReloadTelemetry). It is
	// separate from telemetryOnce because serve consumes that one at startup
	// with a Nop when capture is off — reusing it would make the reload a
	// silent no-op, which is exactly the "first grant after startup does
	// nothing" bug this reload exists to prevent.
	telemetryBuildOnce sync.Once
	telemetryRec       telemetry.Recorder = telemetry.Nop{}
	// telemetryStop holds the retention sweep's cancel for a lazily built
	// recorder (ReloadTelemetry), so a close stops the sweep before the store.
	telemetryStop   func()
	telemetryStopMu sync.Mutex
)

// telemetryRecorder returns the process-wide capture Recorder: telemetry.Nop
// unless the user granted consent in $LEANKG_HOME/telemetry.yaml.
func telemetryRecorder() telemetry.Recorder {
	telemetryOnce.Do(func() {
		rec, err := telemetry.Open(telemetry.Home())
		if err != nil {
			fmt.Fprintf(os.Stderr, "telemetry: capture disabled: %v\n", err)
			return
		}
		telemetryRec = rec
	})
	return telemetryRec
}

// closeTelemetry stops the retention sweep then flushes the process Recorder.
func closeTelemetry() {
	telemetryStopMu.Lock()
	stop := telemetryStop
	telemetryStop = nil
	telemetryStopMu.Unlock()
	if stop != nil {
		stop()
	}
	_ = telemetryRec.Close()
}

// ReloadTelemetry re-reads the consent file and applies the level to the live
// recorder, so granting or withdrawing consent mid-session takes effect without
// a server restart (the plan-dashboard §8 deviation this closes).
//
// The subtle half: a server that started with capture Off has NO recorder at
// all — telemetry.Open returns a Nop and creates no file, which is the "off
// means off" contract. Reloading on such a server therefore has to build the
// recorder on demand, or a first-time grant after startup would still capture
// nothing and the operator would conclude the reload is broken.
func ReloadTelemetry() error {
	cfg, err := telemetry.LoadConfig(telemetry.Home())
	if err != nil {
		return fmt.Errorf("telemetry reload: %w", err)
	}
	level := telemetry.EffectiveLevel(cfg, os.Getenv("LEANKG_TELEMETRY"))
	if level == telemetry.Off {
		// Nothing to raise. Lowering a live recorder to Off is still applied,
		// so a mid-session withdrawal stops capture immediately.
		if rec := telemetryRecorder(); rec != nil {
			if err := rec.SetLevel(telemetry.Off); err != nil {
				return fmt.Errorf("telemetry reload: %w", err)
			}
		}
		return nil
	}
	// Build the recorder once for this process if startup found capture off.
	telemetryBuildOnce.Do(func() {
		st, oerr := telemetry.OpenStore(telemetry.Home(), false)
		if oerr != nil {
			fmt.Fprintf(os.Stderr, "telemetry reload: open ledger: %v\n", oerr)
			return
		}
		stop, serr := telemetry.StartRetention(st, cfg.RetentionDays)
		if serr != nil {
			fmt.Fprintf(os.Stderr, "telemetry reload: retention: %v\n", serr)
			return
		}
		telemetryStopMu.Lock()
		telemetryStop = stop
		telemetryStopMu.Unlock()
		rec := telemetry.NewRecorder(st, level, cfg.MaxBodyBytes)
		telemetryRec = rec
	})
	rec := telemetryRecorder()
	if rec == nil {
		return fmt.Errorf("telemetry reload: no recorder available")
	}
	if err := rec.SetLevel(level); err != nil {
		return fmt.Errorf("telemetry reload: %w", err)
	}
	// Re-point the consumers at the (possibly new) recorder.
	installTelemetrySinks(rec)
	log.Printf("telemetry: consent reloaded at %s", level)
	return nil
}

// telemetrySinks re-installs the process recorder into every consumer after a
// live build. Each consumer holds the Recorder VALUE it was given at startup
// (mcp.SetRecorder, memory.SetRecorder, session.SetRecorder, the REST and
// ConnectRPC handlers), so raising a level on a freshly built recorder reaches
// nobody until the sinks are pointed at it. Without this the reload logs
// success and captures nothing — the exact "it does not work" report the reload
// exists to prevent.
var telemetrySinks []func(telemetry.Recorder)

// AddTelemetrySink registers a consumer to be re-pointed when a reload builds
// a recorder (serve registers its four sinks once, at startup).
func AddTelemetrySink(fn func(telemetry.Recorder)) { telemetrySinks = append(telemetrySinks, fn) }

// watchConsentReload reloads consent on SIGHUP until ctx is done. SIGHUP is the
// conventional "re-read your config" signal, so it is what an operator already
// knows to send; the dashboard's consent screen writes the same file and takes
// effect on the next SIGHUP (or restart).
func watchConsentReload(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if err := ReloadTelemetry(); err != nil {
					log.Printf("telemetry reload: %v", err)
				}
			}
		}
	}()
}

// registerMCPSink records one live MCP server so a consent reload can re-point
// it. MCP servers are created per listener and lazily per project, so the sink
// list cannot be known at startup; each New(engine) registers itself here as
// it is built. The pointer is kept, not the value, because every consumer holds
// the Recorder it was given at start.
var (
	mcpSinkMu sync.Mutex
	mcpSinks  []interface{ SetRecorder(telemetry.Recorder) }
)

func registerMCPSink(srv interface{ SetRecorder(telemetry.Recorder) }) {
	mcpSinkMu.Lock()
	defer mcpSinkMu.Unlock()
	mcpSinks = append(mcpSinks, srv)
}

// installTelemetrySinks re-points every consumer at rec.
func installTelemetrySinks(rec telemetry.Recorder) {
	for _, fn := range telemetrySinks {
		fn(rec)
	}
	mcpSinkMu.Lock()
	defer mcpSinkMu.Unlock()
	for _, srv := range mcpSinks {
		srv.SetRecorder(rec)
	}
}
