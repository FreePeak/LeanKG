//go:build dshusage

package dsusage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Handler serves the dashboard JSON + optional watch APIs. When findings is
// non-nil, API reads are cache-only; the watcher owns the background scan.
// The optional store argument keeps the old three-argument call shape usable
// for embedders that do not need persistence.
func Handler(roots []string, laya LayaClient, watch *Watcher, stores ...*FindingsStore) http.Handler {
	var findings *FindingsStore
	if len(stores) > 0 {
		findings = stores[0]
	}
	mux := http.NewServeMux()
	readSnapshot := func() (Snapshot, error) {
		if findings != nil {
			return findings.Snapshot(), nil
		}
		steps, issues, err := ScanRoots(roots)
		if err != nil {
			return Snapshot{}, err
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		findings := buildFindings(steps, issues)
		return Snapshot{
			Steps: steps, SessionIssues: issues, Findings: findings,
			Scan: ScanStatus{State: "ready", UpdatedAt: now, Steps: len(steps), SessionIssues: len(issues), Findings: len(findings)},
		}, nil
	}
	mux.HandleFunc("GET /api/steps", func(w http.ResponseWriter, r *http.Request) {
		snap, err := readSnapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		steps := snap.Steps
		for i := range steps {
			steps[i].Issues = Classify(steps[i])
			steps[i].TopSeverity = Top(steps[i].Issues)
		}
		if r.URL.Query().Get("laya") == "1" {
			ApplyLaya(steps, laya, 8)
		}
		summary := summarize(steps)
		if l, ok := summary["laya"].(map[string]int); ok {
			l["configured"] = 0
			if laya.Backend != nil {
				l["configured"] = 1
			}
		}
		writeJSON(w, map[string]any{
			"steps":          steps,
			"session_issues": snap.SessionIssues,
			"summary":        summary,
			"causes":         knownCauses(),
			"scan":           snap.Scan,
		})
	})
	mux.HandleFunc("GET /api/findings", func(w http.ResponseWriter, r *http.Request) {
		minSeverity, err := parseMinSeverity(r.URL.Query().Get("min_severity"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		snap, err := readSnapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		findingsOut := make([]Finding, 0)
		for _, finding := range snap.Findings {
			if finding.Status == "open" && severityAtLeast(finding.Severity, minSeverity) {
				findingsOut = append(findingsOut, finding)
			}
		}
		path := ""
		if findings != nil {
			path = findings.Path()
		}
		writeJSON(w, map[string]any{
			"findings":      findingsOut,
			"min_severity":  minSeverity,
			"scan":          snap.Scan,
			"findings_file": path,
		})
	})
	mux.HandleFunc("GET /api/asks", func(w http.ResponseWriter, r *http.Request) {
		if watch == nil {
			out := map[string]any{"asks": []any{}, "stats": map[string]any{}, "watch": false}
			if findings != nil {
				out["scan"] = findings.Snapshot().Scan
			}
			writeJSON(w, out)
			return
		}
		out := watch.Snapshot()
		out["watch"] = true
		if findings != nil {
			out["scan"] = findings.Snapshot().Scan
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("POST /api/asks/", func(w http.ResponseWriter, r *http.Request) {
		if watch == nil {
			http.Error(w, "watch not enabled", http.StatusBadRequest)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/asks/")
		id = strings.Trim(id, "/")
		if id == "" {
			http.Error(w, "missing ask id", http.StatusBadRequest)
			return
		}
		var body struct {
			Help bool `json:"help"`
		}
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		_ = json.Unmarshal(b, &body)
		// also accept ?help=1
		if r.URL.Query().Get("help") == "1" {
			body.Help = true
		}
		if r.URL.Query().Get("help") == "0" {
			body.Help = false
		}
		a, err := watch.AnswerAsk(id, body.Help)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, a)
	})
	mux.HandleFunc("GET /api/causes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, knownCauses())
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboardHTML)
	})
	return mux
}

func parseMinSeverity(value string) (Severity, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return SevMedium, nil
	}
	severity := Severity(value)
	switch severity {
	case SevCritical, SevHigh, SevMedium, SevInfo:
		return severity, nil
	default:
		return "", fmt.Errorf("invalid min_severity %q (want critical, high, medium, or info)", value)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func summarize(steps []Step) map[string]any {
	sev := map[string]int{}
	tools := map[string]int{}
	rungs := map[string]int{}
	sessions := map[string]int{}
	scored, unavailable, skippedInfo := 0, 0, 0
	for _, s := range steps {
		sev[string(s.TopSeverity)]++
		tools[s.Tool]++
		sessions[s.SessionID]++
		if s.Rung != "" {
			rungs[s.Rung]++
		}
		// Laya engagement is derived from the tags ApplyLaya already writes
		// onto each step — no new counters, no new judge calls.
		for _, is := range s.Issues {
			switch is.Rule {
			case "laya_score", "laya_critical":
				scored++
			case "laya_unavailable":
				unavailable++
			}
		}
		if Top(s.Issues) == SevInfo {
			skippedInfo++
		}
	}
	return map[string]any{
		"steps": len(steps), "sessions": len(sessions),
		"by_severity": sev, "by_tool": tools, "by_rung": rungs,
		"laya": map[string]int{"scored": scored, "unavailable": unavailable, "skipped_info": skippedInfo},
	}
}

func knownCauses() []Issue {
	return []Issue{
		{Rule: "meter_wrong", Severity: SevInfo, Title: "The April posttooluse.log is not a DSH meter",
			Detail: "That file is written only by the Claude plugin hook, and only for Rust-era tool names. DSH never runs it, so a stale April mtime does not mean DSH is idle.",
			Fix:    "Read ~/.dsh/sessions/*/session.v4.jsonl(.zstd), or this dashboard."},
		{Rule: "no_dsh_hook", Severity: SevHigh, Title: "DSH has no LeanKG nudge",
			Detail: "Claude and Cursor inject a bootstrap hook. DSH only has a passive paragraph in ~/.dsh/AGENTS.md. Across 152 sessions, 8 called LeanKG (24 calls) while bash/read/grep ran thousands of times.",
			Fix:    "Add a DSH session-start hook that says: query LeanKG before grep, and always pass project."},
		{Rule: "project_default", Severity: SevCritical, Title: "Omitted project= searches the serve cwd",
			Detail: "Multi-project MCP routes only on ?project= or the tool argument. The tool argument is what agents have. When they omit it, the handler is the leankg-serve working directory, not the session cwd.",
			Fix:    "Default the tool argument from the client roots, or reject a query that omits project when more than one project is registered."},
		{Rule: "session_sticky", Severity: SevCritical, Title: "Streamable HTTP sessions do not survive restart",
			Detail: "DSH keeps Mcp-Session-Id. leankg-serve is launchd-restarted and also builds a fresh MCP handler per project, each with its own session map. Result: session not found.",
			Fix:    "NewStreamableHTTPHandler(..., &mcp.StreamableHTTPOptions{Stateless: true})."},
		{Rule: "cursor_unwired", Severity: SevHigh, Title: "Cursor mcp.json has no local LeanKG",
			Detail: "User Cursor config lists be-knowledge-graph, db-mcp-server, qa-agent. The Claude plugin still documents deleted tool names (get_clusters, search_code).",
			Fix:    "Add leankg url http://127.0.0.1:9699/mcp to ~/.cursor/mcp.json and point hooks at import/query/status."},
		{Rule: "laya_not_a_chat_model", Severity: SevInfo, Title: "Local Laya cannot write the agent reply",
			Detail: "The cached convaiinnovations/laya checkpoint is a non-autoregressive classifier (ModernBERT). Port 9101 is bge-small embeddings, not a chat server. There is no local llama chat model on disk.",
			Fix:    "Use Laya to score steps (this page + --watch). Do not point a chat client at :9101."},
	}
}
