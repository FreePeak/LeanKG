package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// auditLedgerCap bounds the ledger read for export/verify purposes. The Go
// store's AuditTail takes a row cap and has no time-window query, so the CLI
// tails the ledger and filters by `at`. ponytail: 1M rows covers any realistic
// ledger; the upgrade path is a store-side windowed iterator.
const auditLedgerCap = 1 << 20

// cmdAudit dispatches `leankg audit export|verify` (Rust FR-ENT-1:
// cli::AuditCommand + main.rs run_audit_command).
func cmdAudit(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "audit: expected a subcommand (export | verify)")
		os.Exit(2)
	}
	switch args[0] {
	case "export":
		cmdAuditExport(args[1:])
	case "verify":
		cmdAuditVerify(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "audit: unknown subcommand %q (valid: export | verify)\n", args[0])
		os.Exit(2)
	}
}

// cmdAuditExport prints the ledger window as JSONL, one entry per line, or
// writes it to --out. Ported from cli::AuditCommand::Export +
// cli::audit::export_ledger_jsonl.
func cmdAuditExport(args []string) {
	fs := flag.NewFlagSet("audit export", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	since := fs.String("since", "", "only entries at/after T (RFC3339 | epoch seconds | 90s|30m|24h|7d)")
	until := fs.String("until", "", "only entries at/before T")
	format := fs.String("format", "jsonl", "output format (only jsonl is wired, like the Rust AuditFormat enum)")
	out := fs.String("out", "", "write to FILE instead of stdout")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *format != "jsonl" {
		fmt.Fprintf(os.Stderr, "audit export: unknown format %q (valid: jsonl)\n", *format)
		os.Exit(2)
	}
	window, err := parseAuditWindow(*since, *until)
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit export:", err)
		os.Exit(2)
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	entries, err := auditEntries(engine.Store(), window)
	if err != nil {
		fatalJSON(err)
	}
	jsonl := auditJSONL(entries)
	if *out == "" {
		fmt.Print(jsonl)
		return
	}
	if err := os.WriteFile(*out, []byte(jsonl), 0o644); err != nil {
		fatalJSON(err)
	}
	// The Rust verb printed this note on stdout; keep stdout export-only.
	fmt.Fprintf(os.Stderr, "wrote %d audit entries to %s\n", len(entries), *out)
}

// cmdAuditVerify walks the hash chain and exits non-zero when it is broken.
// Ported from cli::AuditCommand::Verify + cli::audit::verify_ledger.
//
// Difference from Rust: the Go store's VerifyAuditChain always walks the whole
// ledger (there is no windowed chain query), so --since/--until only scope the
// count reported here — the integrity check itself is strictly stronger than
// the Rust window check.
func cmdAuditVerify(args []string) {
	fs := flag.NewFlagSet("audit verify", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	since := fs.String("since", "", "count only entries at/after T (RFC3339 | epoch seconds | 90s|30m|24h|7d)")
	until := fs.String("until", "", "count only entries at/before T")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	window, err := parseAuditWindow(*since, *until)
	if err != nil {
		fmt.Fprintln(os.Stderr, "audit verify:", err)
		os.Exit(2)
	}
	dir := resolveProjectDir(*project)
	engine, err := openEngine(dir, store.RO)
	if err != nil {
		fatalJSON(err)
	}
	defer engine.Store().Close()

	ok, brokenAt, err := engine.Store().VerifyAuditChain()
	if err != nil {
		fatalJSON(err)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "FAIL: audit chain broken at seq %d\n", brokenAt)
		os.Exit(1)
	}
	entries, err := auditEntries(engine.Store(), window)
	if err != nil {
		fatalJSON(err)
	}
	fmt.Printf("OK: audit chain intact (%d entries verified)\n", len(entries))
}

// auditWindow is the inclusive [since, until] time window; zero times mean
// unbounded.
type auditWindow struct {
	since, until time.Time
}

func parseAuditWindow(since, until string) (auditWindow, error) {
	var w auditWindow
	if since != "" {
		t, err := parseTimeFilter(since)
		if err != nil {
			return w, err
		}
		w.since = t
	}
	if until != "" {
		t, err := parseTimeFilter(until)
		if err != nil {
			return w, err
		}
		w.until = t
	}
	return w, nil
}

// contains reports whether an entry's epoch-second stamp falls in the window.
func (w auditWindow) contains(at int64) bool {
	if !w.since.IsZero() && at < w.since.Unix() {
		return false
	}
	if !w.until.IsZero() && at > w.until.Unix() {
		return false
	}
	return true
}

// auditEntries tails the ledger and keeps the window (Rust query_audit).
func auditEntries(st store.Backend, w auditWindow) ([]store.AuditEntry, error) {
	all, err := st.AuditTail(auditLedgerCap)
	if err != nil {
		return nil, err
	}
	out := make([]store.AuditEntry, 0, len(all))
	for _, e := range all {
		if w.contains(e.At) {
			out = append(out, e)
		}
	}
	return out, nil
}

// auditJSONL renders one JSON object per entry (Rust rows_to_jsonl). The Go
// ledger record carries different fields than the Rust one
// (seq/at/actor/action/target/details/prev_hash/hash), so the exporter emits
// the store's own field names.
func auditJSONL(entries []store.AuditEntry) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for i := range entries {
		if err := enc.Encode(&entries[i]); err != nil {
			fatalJSON(err)
		}
	}
	return b.String()
}

// parseTimeFilter ports cli::audit::parse_time_filter: a relative duration
// (<digits>s|m|h|d), unix epoch seconds, or an RFC 3339 / ISO-8601 timestamp.
func parseTimeFilter(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time filter")
	}

	// Relative duration: <digits><s|m|h|d>.
	if len(raw) > 1 && allDigits(raw[:len(raw)-1]) && strings.ContainsRune("smhd", rune(raw[len(raw)-1])) {
		n, err := strconv.ParseUint(raw[:len(raw)-1], 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time filter %q: %w", raw, err)
		}
		secs := n
		switch raw[len(raw)-1] {
		case 'm':
			secs = n * 60
		case 'h':
			secs = n * 3600
		case 'd':
			secs = n * 86400
		}
		if n > math.MaxUint64/86400 || secs > uint64(math.MaxInt64/int64(time.Second)) {
			return time.Time{}, fmt.Errorf("time filter %q underflows the clock", raw)
		}
		return time.Now().Add(-time.Duration(secs) * time.Second), nil
	}

	// Unix epoch seconds.
	if allDigits(raw) {
		if secs, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return time.Unix(int64(secs), 0), nil
		}
	}

	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("cannot parse time filter %q: %w", raw, err)
	}
	return ts, nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
