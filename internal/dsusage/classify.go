//go:build dshusage

package dsusage

import "strings"

// Severity is the fix priority a step needs. critical means the step is
// evidence of a defect that should be fixed before more dogfood; info is
// expected noise (a status probe, a miss on an unindexed name).
type Severity string

const (
	SevCritical Severity = "critical"
	SevHigh     Severity = "high"
	SevMedium   Severity = "medium"
	SevInfo     Severity = "info"
)

// Issue is one classified finding. Rule is the heuristic that fired so a
// later Laya pass can agree or override without hiding the evidence.
type Issue struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Fix      string   `json:"fix"`
}

// Step is one LeanKG tool call joined to the agent text around it.
type Step struct {
	SessionID   string   `json:"session_id"`
	Workspace   string   `json:"workspace"`
	TimeMS      int64    `json:"time_ms"`
	Turn        int      `json:"turn"`
	Step        int      `json:"step"`
	CallID      string   `json:"call_id"`
	Tool        string   `json:"tool"`
	Input       string   `json:"input"`
	Output      string   `json:"output"`
	IsError     bool     `json:"is_error"`
	AgentBefore string   `json:"agent_before"`
	AgentAfter  string   `json:"agent_after"`
	UserPrompt  string   `json:"user_prompt"`
	Issues      []Issue  `json:"issues"`
	TopSeverity Severity `json:"top_severity"`
}

// Classify applies the rules that the session scan already proved. A Laya
// pass may raise severity; it must not clear a critical rule hit.
func Classify(s Step) []Issue {
	in := strings.ToLower(s.Input)
	out := s.Output
	low := strings.ToLower(out)
	var issues []Issue

	if strings.Contains(low, "session not found") {
		issues = append(issues, Issue{
			Rule:     "mcp_session_lost",
			Severity: SevCritical,
			Title:    "MCP session died under the client",
			Detail:   "DSH kept an Mcp-Session-Id that the server no longer has. This happens when leankg-serve restarts (launchd) or when ?project= builds a second MCP handler with its own session map.",
			Fix:      "Serve MCP stateless (or share one session map across project handlers) so a restart or project switch cannot 404 an in-flight DSH client.",
		})
	}
	if strings.Contains(low, "ip address is not allowed") || strings.Contains(low, "error posting to endpoint") && strings.Contains(low, "not allowed") {
		issues = append(issues, Issue{
			Rule:     "remote_acl",
			Severity: SevHigh,
			Title:    "Remote knowledge-graph ACL rejected the call",
			Detail:   "The call reached a gateway that refused this IP. Local LeanKG would have answered.",
			Fix:      "Point the tool at the local server, or allow this IP on the gateway.",
		})
	}
	if s.IsError && !containsRule(issues, "mcp_session_lost") {
		issues = append(issues, Issue{
			Rule:     "tool_error",
			Severity: SevHigh,
			Title:    "Tool call returned an error",
			Detail:   clip(out, 240),
			Fix:      "Read the error text; if it is a transport failure, fix the server before tuning prompts.",
		})
	}
	if strings.Contains(low, "ambiguous project") {
		issues = append(issues, Issue{
			Rule:     "ambiguous_project",
			Severity: SevHigh,
			Title:    "Project name matched more than one registered repo",
			Detail:   "The agent passed a basename that exists twice. The call failed closed, which is correct, and the agent still has to guess which path to send.",
			Fix:      "Pass the absolute path, or register unique names. Do not fall back to the serve cwd.",
		})
	}
	if strings.Contains(low, "index a repository first") || strings.Contains(low, `"freshness":"cold"`) && strings.Contains(low, `"hits":[]`) {
		issues = append(issues, Issue{
			Rule:     "cold_store",
			Severity: SevHigh,
			Title:    "Query hit a cold or empty store",
			Detail:   "The selected project has no index, so the agent got guidance instead of code.",
			Fix:      "import the repo once (action=repo, absolute path, project=<basename>) before querying it. Do not re-import on every turn.",
		})
	}
	if strings.Contains(low, "omitted project") && strings.Contains(low, "multiple projects") {
		issues = append(issues, Issue{
			Rule:     "project_required",
			Severity: SevHigh,
			Title:    "Server now requires project under multi-project serving",
			Detail:   "The call omitted project and the server refused instead of answering from the wrong repo.",
			Fix:      "Pass project=<repo basename> or an absolute path.",
		})
	}
	if wrongProject(s) {
		issues = append(issues, Issue{
			Rule:     "project_not_passed",
			Severity: SevCritical,
			Title:    "Query omitted project and searched the wrong repo",
			Detail:   "With 279 registered projects the default handler is the serve process cwd (LeanKG). A menu/promo/restaurant question answered from LeanKG source is a routing bug, not a search miss.",
			Fix:      "Always pass project=<repo basename>. Teach the session prompt that an omitted project is not 'current directory'.",
		})
	}
	if strings.Contains(low, "no keyword match") || (strings.Contains(low, `"hits":[]`) && !strings.Contains(low, "cold")) {
		sev := SevMedium
		if strings.Contains(in, `"action":"exact"`) || strings.Contains(in, `"action": "exact"`) {
			sev = SevInfo
		}
		issues = append(issues, Issue{
			Rule:     "empty_hit",
			Severity: sev,
			Title:    "Query returned no hits",
			Detail:   "Either the name is not in the selected store, or L2/L1 was pinned when the question was semantic.",
			Fix:      "Confirm project= first. Drop action so the ladder can fall through to semantic, or import the repo if freshness is cold.",
		})
	}
	if strings.Contains(low, "truncated") && strings.Contains(low, "_token_budget") {
		issues = append(issues, Issue{
			Rule:     "token_budget_truncated",
			Severity: SevMedium,
			Title:    "Response was truncated by the tool token budget",
			Detail:   "The agent saw a slice of the hit list. Follow-up calls often re-query instead of reading the file.",
			Fix:      "Ask for one element (action=element) or lower limit. Do not raise the budget until truncation is the common case.",
		})
	}
	if len(issues) == 0 && strings.HasSuffix(s.Tool, "status") {
		issues = append(issues, Issue{
			Rule:     "status_probe",
			Severity: SevInfo,
			Title:    "Status probe",
			Detail:   "Health check, not a search. Fine as a first step; useless as the only LeanKG call in a coding session.",
			Fix:      "Follow status with a query against the same project.",
		})
	}
	if len(issues) == 0 {
		issues = append(issues, Issue{
			Rule:     "ok",
			Severity: SevInfo,
			Title:    "No defect in this step",
			Detail:   "The call returned a usable result and none of the known failure rules matched.",
			Fix:      "Leave it. Do not treat a successful import or an on-repo hit as a bug.",
		})
	}
	return issues
}

func wrongProject(s Step) bool {
	if !strings.HasSuffix(s.Tool, "query") {
		return false
	}
	in := strings.ToLower(s.Input)
	if strings.Contains(in, `"project"`) {
		return false
	}
	ws := strings.ToLower(s.Workspace)
	// The leankg checkout asking about leankg is the one case an omitted
	// project is correct.
	if strings.Contains(ws, "leankg") && !strings.Contains(ws, "freepeak--") {
		return false
	}
	blob := strings.ToLower(s.UserPrompt + " " + s.AgentBefore + " " + s.Input)
	for _, hint := range []string{"menu", "promo", "restaurant", "merchant", "cadence", "be-", "onegw", "xdev"} {
		if strings.Contains(blob, hint) && !strings.Contains(ws, "leankg") {
			return true
		}
	}
	// A non-leankg workspace that did not pass project is still wrong: the
	// server default is the serve cwd, not the session cwd.
	return !strings.Contains(ws, "leankg")
}

func containsRule(issues []Issue, rule string) bool {
	for _, i := range issues {
		if i.Rule == rule {
			return true
		}
	}
	return false
}

// Top returns the highest severity in issues, or info when empty.
func Top(issues []Issue) Severity {
	order := map[Severity]int{SevInfo: 0, SevMedium: 1, SevHigh: 2, SevCritical: 3}
	best := SevInfo
	for _, i := range issues {
		if order[i.Severity] > order[best] {
			best = i.Severity
		}
	}
	return best
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
