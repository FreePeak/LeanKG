//go:build dshusage

package dsusage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/internal/judge"
)

// Scorer scores steps through internal/judge (local sidecar over HTTP).
// Rules always stand; Laya can only add a score or raise to critical.
type Scorer struct {
	Backend judge.Judge
	Model   string
	Timeout time.Duration
}

// LayaClient is the historical name kept for Handler/Watcher wiring.
type LayaClient = Scorer

// DefaultLaya attaches to a configured Laya sidecar. Default is off: with no
// LAYA_URL / LEANKG_JUDGE_SIDECAR_URL the backend is nil and scoring is a
// no-op (rules still classify). Pass an explicit URL to enable.
func DefaultLaya() LayaClient {
	url := strings.TrimSpace(os.Getenv("LAYA_URL"))
	if url == "" {
		url = strings.TrimSpace(os.Getenv("LEANKG_JUDGE_SIDECAR_URL"))
	}
	if url == "" {
		return LayaClient{} // off
	}
	model := strings.TrimSpace(os.Getenv("LEANKG_JUDGE_MODEL"))
	if model == "" {
		model = "convaiinnovations/laya"
	}
	return LayaClient{
		// Cached: a judgment is a pure function of (state, questions) and the
		// watcher re-scores the same corpus on every poll, so repeats should
		// not pay for a forward pass. Unavailable results are never cached.
		Backend: judge.NewCached(judge.NewLocal(url, model, 20*time.Second), 4096),
		Model:   model,
		Timeout: 20 * time.Second,
	}
}

// NewLaya builds a client for an explicit sidecar URL. Empty url → off.
func NewLaya(url string) LayaClient {
	url = strings.TrimSpace(url)
	if url == "" {
		return LayaClient{}
	}
	model := strings.TrimSpace(os.Getenv("LEANKG_JUDGE_MODEL"))
	if model == "" {
		model = "convaiinnovations/laya"
	}
	return LayaClient{
		Backend: judge.NewCached(judge.NewLocal(url, model, 20*time.Second), 4096),
		Model:   model,
		Timeout: 20 * time.Second,
	}
}

type LayaVerdict struct {
	Available     bool    `json:"available"`
	Reason        string  `json:"reason,omitempty"`
	Critical      bool    `json:"critical"`
	Defect        bool    `json:"defect"`
	WrongProject  bool    `json:"wrong_project"`
	TransportDead bool    `json:"transport_dead"`
	Confidence    float64 `json:"confidence"`
	Score         float64 `json:"score,omitempty"`
	Noul          float64 `json:"noul,omitempty"`
}

// layaQuestions is the compiled battery for one MCP step.
//
// Decomposed on purpose. A single wide choice answers with a flat
// distribution (measured: a 5-way verdict scored confidence 0.47, while the
// same decision as binary questions scored 0.60–0.91), and Laya's confidence
// is normalized entropy — it is penalized by option count. Binary questions
// also keep each option's text well inside head_max_len.
func layaQuestions() map[string]judge.Question {
	return map[string]judge.Question{
		"is_defect": {
			Type:         judge.Noul,
			Instructions: "Does this LeanKG tool step show a defect that should be fixed in the product or the session wiring, rather than a normal miss?",
			Criteria:     map[string]string{"true": "a real defect to fix", "false": "an expected miss, a status probe, or a useful hit"},
		},
		"wrong_project": {
			Type:         judge.Noul,
			Instructions: "Was the step answered from a repository other than the one the session was working in?",
			Criteria:     map[string]string{"true": "another repository's data was returned", "false": "the right repository, or an explicit refusal instead of data"},
		},
		"transport_dead": {
			Type:         judge.Noul,
			Instructions: "Did the step fail because the MCP connection or session was lost?",
			Criteria:     map[string]string{"true": "the connection or session was lost", "false": "the call reached the server"},
		},
		"urgency": {
			Type:         judge.Score,
			Instructions: "How urgently should an engineer fix the underlying cause of this step?",
			Ladder:       []string{"ignore", "later", "this week", "blocking dogfood"},
		},
	}
}

// layaFloors are the per-question confidence floors. Below the floor the
// answer is not actionable and the native rule stands — Laya ships
// over-confident, so these are deliberately conservative defaults to be
// fitted by the recorded experiment, not guesses dressed as calibration.
var layaFloors = map[string]float64{
	"is_defect":      0.6,
	"wrong_project":  0.6,
	"transport_dead": 0.6,
}

// Judge scores one step. A nil/unreachable judge returns unavailable; it must
// never clear a rule hit.
func (c LayaClient) Judge(step Step) LayaVerdict {
	if c.Backend == nil {
		return LayaVerdict{Reason: "no judge configured (set LAYA_URL / LEANKG_JUDGE_SIDECAR_URL or --laya-url)"}
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	answers, err := c.Backend.Ask(ctx, stepState(step), layaQuestions())
	if err != nil {
		return LayaVerdict{Reason: clip(err.Error(), 300)}
	}
	if judge.Unavailable(answers) {
		return LayaVerdict{Reason: "sidecar unreachable"}
	}
	v := LayaVerdict{Available: true}
	// Confidence = the LEAST certain judgment in the call, never the product:
	// one shaky answer spoils the verdict, and a product would fall with
	// question count even when every judgment is sound.
	v.Confidence = 1.0
	v.Defect = floorAnswer(answers, "is_defect", layaFloors["is_defect"], &v.Confidence)
	v.WrongProject = floorAnswer(answers, "wrong_project", layaFloors["wrong_project"], &v.Confidence)
	v.TransportDead = floorAnswer(answers, "transport_dead", layaFloors["transport_dead"], &v.Confidence)
	if s, ok := parseScore(answers["urgency"].Value); ok {
		v.Score = s
	}
	if v.Confidence == 1.0 {
		v.Confidence = 0 // no question cleared its floor
	}
	// A specific, well-evidenced failure mode escalates on its own. Measured:
	// on a dead-session step, transport_dead answered 0.644 (above floor)
	// while the general is_defect question answered 0.452 — requiring
	// is_defect to agree would suppress exactly the failure we can see.
	// Rules still own the final severity; this only lets Laya corroborate.
	v.Critical = v.TransportDead || (v.Defect && v.WrongProject)
	return v
}

// floorAnswer reads one noul answer and folds its confidence into the call's
// least-certain aggregate. An answer below its floor is not actionable: it
// reports false and still lowers the aggregate, because "I am unsure" is
// itself information the caller must not discard.
func floorAnswer(answers map[string]judge.Answer, id string, floor float64, agg *float64) bool {
	a, ok := answers[id]
	if !ok {
		*agg = 0
		return false
	}
	if a.Confidence < *agg {
		*agg = a.Confidence
	}
	p, ok := parseScore(a.Value)
	if !ok || a.Confidence < floor {
		return false
	}
	return p >= 0.5
}

func parseScore(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal([]byte(s), &f); err == nil {
		return f, true
	}
	// score answers carry the level index as a string ("0".."3").
	for _, tok := range strings.Fields(s) {
		var g float64
		if err := json.Unmarshal([]byte(tok), &g); err == nil {
			return g, true
		}
	}
	return 0, false
}

func stepState(s Step) string {
	b, _ := json.Marshal(map[string]string{
		"tool": s.Tool, "workspace": s.Workspace, "input": clip(s.Input, 400),
		"output": clip(s.Output, 400), "agent": clip(s.AgentBefore+"\n"+s.AgentAfter, 400),
		"rules": ruleList(s),
	})
	return string(b)
}

func ruleList(s Step) string {
	var b strings.Builder
	for _, i := range s.Issues {
		b.WriteString(i.Rule)
		b.WriteByte(' ')
	}
	return b.String()
}

// ApplyLaya attaches a verdict onto issues when the model is up. A model
// failure never removes a rule hit.
func ApplyLaya(steps []Step, c LayaClient, limit int) {
	n := 0
	for i := range steps {
		if Top(steps[i].Issues) == SevInfo && steps[i].Issues != nil {
			continue
		}
		if limit > 0 && n >= limit {
			break
		}
		n++
		v := c.Judge(steps[i])
		if !v.Available {
			steps[i].Issues = append(steps[i].Issues, Issue{
				Rule: "laya_unavailable", Severity: SevInfo,
				Title: "Laya did not score this step", Detail: v.Reason,
				Fix: "Start the local sidecar: ~/venvs/laya/bin/python scripts/laya-sidecar.py --port 8091",
			})
			return
		}
		steps[i].Issues = append(steps[i].Issues, Issue{
			Rule: "laya_score", Severity: SevInfo,
			Title: "Laya: " + verdictSummary(v),
			Detail: "defect=" + boolStr(v.Defect) + " wrong_project=" + boolStr(v.WrongProject) +
				" transport_dead=" + boolStr(v.TransportDead) +
				" urgency=" + formatFloat(v.Score) + " confidence=" + formatFloat(v.Confidence),
			Fix: "Rule hits still win; Laya raises to critical only above its confidence floor.",
		})
		if v.Critical && Top(steps[i].Issues) != SevCritical {
			steps[i].Issues = append(steps[i].Issues, Issue{
				Rule: "laya_critical", Severity: SevCritical,
				Title:  "Laya marked this step critical",
				Detail: verdictSummary(v) + " (confidence " + formatFloat(v.Confidence) + ")",
				Fix:    "Treat the rule hits on this step as a fix, not a prompt miss.",
			})
			steps[i].TopSeverity = SevCritical
		}
	}
}

// verdictSummary renders a decomposed verdict as one short phrase.
func verdictSummary(v LayaVerdict) string {
	if !v.Defect {
		return "no defect"
	}
	switch {
	case v.WrongProject && v.TransportDead:
		return "defect: wrong project + dead transport"
	case v.WrongProject:
		return "defect: wrong project"
	case v.TransportDead:
		return "defect: dead transport"
	default:
		return "defect"
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func formatFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
