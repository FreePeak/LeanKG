// Package ab implements the FR-ZCP-08 (issue #276) cross-tool harness
// hardening for the Go side: pinned provenance, a >=3-trials-per-arm
// gate, judge-blind answer scoring, per-arm median aggregation, and the
// zg pitfalls checklist. It is the library behind the benchmark/ab/abrun
// CLI that the kilo A/B runners (run_kilo_ab_final.sh,
// run_kilo_ab_test.sh) record through — a run without pins is refused,
// never recorded.
//
// Semantics mirror the established Rust-era cross_tool harness
// (benchmarks/cross_tool/score.py, aggregate.py): same rigor gates, same
// judge-blind structural property (the judge prompt is built only from
// question + shuffled anonymized answers, so arm identity cannot leak),
// and the same median methodology (per-task medians, arm figure = median
// of the per-task medians so one noisy query cannot dominate).
package ab

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
)

// MinTrialsPerArm is the stochasticity floor (FR-ZCP-08 clause 2).
const MinTrialsPerArm = 3

// RubricMax is the judge-blind 0-6 rubric ceiling (parity with
// score.py: correctness + grounding + depth, 0-2 each).
const RubricMax = 6

// Pins is the provenance stamp every recorded trial must carry.
// ToolSHAs maps every tool the run measured (agent CLI, engine binary)
// to the exact 40-hex git commit it was built from. CorpusSHA is the
// 40-hex commit of the indexed corpus; PromptSHA256 is the SHA-256 of
// the prompt template verbatim; PromptVersion is its human label.
type Pins struct {
	CorpusSHA     string            `json:"corpus_sha"`
	ToolSHAs      map[string]string `json:"tool_shas"`
	PromptVersion string            `json:"prompt_version"`
	PromptSHA256  string            `json:"prompt_sha256"`
	Model         string            `json:"model,omitempty"`
}

// Validate refuses incomplete pins: every field a defensible run needs
// must be present and well-formed.
func (p Pins) Validate() error {
	if !isHex(p.CorpusSHA, 40) {
		return errors.New("pins: corpus_sha must be a 40-hex git commit, got " + quote(p.CorpusSHA))
	}
	if len(p.ToolSHAs) == 0 {
		return errors.New("pins: at least one tool commit SHA must be pinned")
	}
	for tool, sha := range p.ToolSHAs {
		if !isHex(sha, 40) {
			return errors.New("pins: tool " + tool + " sha must be 40-hex, got " + quote(sha))
		}
	}
	if !isHex(p.PromptSHA256, 64) {
		return errors.New("pins: prompt_sha256 must be 64-hex, got " + quote(p.PromptSHA256))
	}
	if strings.TrimSpace(p.PromptVersion) == "" {
		return errors.New("pins: prompt_version must name the prompt template")
	}
	return nil
}

// SHA256Hex is the prompt-template hash form recorded in pins.
func SHA256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Trial is one recorded A/B measurement: an arm of a task, one trial.
// The runner captures Tokens from the CLI envelope, ToolCalls from the
// session events (>=1 reachable tool use for the candidate arm), and
// MCPAttached to expose a contaminated baseline (leakage). JudgeScore is
// filled in only by JudgeBlind after the report-time unblinding.
type Trial struct {
	Arm         string `json:"arm"`
	Task        string `json:"task"`
	N           int    `json:"trial"`
	Tokens      int    `json:"tokens"`
	Success     bool   `json:"success"`
	ToolCalls   int    `json:"mcp_tool_count"`
	MCPAttached bool   `json:"mcp_attached"`
	Question    string `json:"question,omitempty"`
	Answer      string `json:"answer,omitempty"`
	JudgeScore  int    `json:"judge_score,omitempty"`
	Judged      bool   `json:"judged,omitempty"`
	Pins        Pins   `json:"pins"`
}

// Validate checks the record-level contract: identity + provenance.
func (t *Trial) Validate() error {
	if strings.TrimSpace(t.Arm) == "" {
		return errors.New("trial: arm must be set")
	}
	if strings.TrimSpace(t.Task) == "" {
		return errors.New("trial: task must be set")
	}
	if t.N < 1 {
		return fmt.Errorf("trial: trial index must be >=1, got %d", t.N)
	}
	return t.Pins.Validate()
}

// AppendTrial writes one trial as a JSONL row — or refuses and writes
// nothing when the trial is unpinned/invalid. This is the hard gate: a
// run without pins is refused, not recorded.
func AppendTrial(w io.Writer, t Trial) error {
	if err := t.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ParseTrials reads JSONL trial rows, refusing the whole load when any
// row is unpinned or malformed — pinned provenance is not optional at
// read time either.
func ParseTrials(r io.Reader) ([]Trial, error) {
	var out []Trial
	dec := json.NewDecoder(r)
	for i := 0; ; i++ {
		var t Trial
		if err := dec.Decode(&t); err == io.EOF {
			return out, nil
		} else if err != nil {
			return nil, fmt.Errorf("trial %d: %w", i+1, err)
		}
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("trial %d: %w", i+1, err)
		}
		out = append(out, t)
	}
}

// Median of ints (even count = mean of the middle pair). Empty input is
// NaN so JSON reports surface "not measured" rather than a fake zero.
func Median(xs []int) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	v := make([]float64, len(xs))
	for i, x := range xs {
		v[i] = float64(x)
	}
	return medianF(v)
}

// MedianF is the float median; exported for report consumers.
func MedianF(xs []float64) float64 { return medianF(xs) }

func medianF(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	v := make([]float64, len(xs))
	copy(v, xs)
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// ArmTask is one (task, arm) cell of the report.
type ArmTask struct {
	N            int     `json:"n"`
	TokensMedian float64 `json:"tokens_median"`
	JudgeMedian  float64 `json:"judge_median,omitempty"`
}

// TaskRow is one task across arms, with per-arm medians.
type TaskRow struct {
	Task string             `json:"task"`
	Arms map[string]ArmTask `json:"arms"`
}

// ArmSummary is the arm figure: median of the per-task medians
// (judge-segmented median precedent — never pool raw trials across
// tasks, one noisy query must not dominate).
type ArmSummary struct {
	Trials       int     `json:"trials"`
	TokensMedian float64 `json:"tokens_median"`
	JudgeMedian  float64 `json:"judge_median,omitempty"`
}

// Report is the results JSON the runners emit: provenance stamp,
// aggregated medians, and the computed zg pitfalls checklist.
// Provenance carries the fields shared by every arm (corpus, tools,
// prompt_version); PromptSHA256 holds the single hash when all arms used
// one template (cross_tool-style identical prompts) and is empty when
// the arms legitimately differ (kilo-style instructed treatment) — the
// per-arm hashes are then in PromptSHA256ByArm.
type Report struct {
	Provenance        Pins                   `json:"provenance"`
	PromptSHA256ByArm map[string]string      `json:"prompt_sha256_by_arm"`
	MinTrials         int                    `json:"min_trials_per_arm"`
	Arms              map[string]*ArmSummary `json:"arms"`
	Tasks             []TaskRow              `json:"tasks"`
	Checklist         []Check                `json:"pitfalls_checklist"`
	SavingsPct        *float64               `json:"savings_pct,omitempty"`
}

// Aggregate enforces the >=3-trials-per-arm gate and computes medians.
// Fails when any arm (overall or for any single task) has fewer than
// MinTrialsPerArm trials, when fewer than two arms are present, or when
// provenance is not uniform: corpus, tools and prompt_version must match
// across the whole report, and each arm must carry exactly one prompt
// template hash (drift inside an arm breaks like-for-like beyond
// repair). Different arms MAY use different templates — that is the
// kilo-style instructed treatment; identical is verified via the
// per-arm map at report time.
func Aggregate(trials []Trial) (Report, error) {
	byArm := map[string][]Trial{}
	byTaskArm := map[string]map[string][]Trial{}
	var prov Pins
	for i := range trials {
		t := trials[i]
		if err := t.Validate(); err != nil {
			return Report{}, fmt.Errorf("trial %d (%s/%s): %w", i+1, t.Arm, t.Task, err)
		}
		if i == 0 {
			prov = t.Pins
		} else if t.Pins.CorpusSHA != prov.CorpusSHA ||
			t.Pins.PromptVersion != prov.PromptVersion || !sameTools(t.Pins.ToolSHAs, prov.ToolSHAs) {
			return Report{}, fmt.Errorf("trial %d pins diverge from the run provenance — one report covers one pinned run", i+1)
		}
		byArm[t.Arm] = append(byArm[t.Arm], t)
		if byTaskArm[t.Task] == nil {
			byTaskArm[t.Task] = map[string][]Trial{}
		}
		byTaskArm[t.Task][t.Arm] = append(byTaskArm[t.Task][t.Arm], t)
	}
	if len(byArm) < 2 {
		return Report{}, fmt.Errorf("need at least two arms for an A/B report, got %d", len(byArm))
	}
	promptByArm := map[string]string{}
	for _, arm := range slices.Sorted(maps.Keys(byArm)) {
		n := len(byArm[arm])
		if n < MinTrialsPerArm {
			return Report{}, fmt.Errorf("arm %q has %d trial(s), scorer requires >=%d per arm", arm, n, MinTrialsPerArm)
		}
		sha := byArm[arm][0].Pins.PromptSHA256
		for _, t := range byArm[arm][1:] {
			if t.Pins.PromptSHA256 != sha {
				return Report{}, fmt.Errorf("arm %q mixes prompt templates (%s vs %s) — like-for-like broken", arm, sha[:12], t.Pins.PromptSHA256[:12])
			}
		}
		promptByArm[arm] = sha
	}
	if len(uniqueValues(promptByArm)) == 1 {
		prov.PromptSHA256 = promptByArm[slices.Sorted(maps.Keys(promptByArm))[0]]
	} else {
		prov.PromptSHA256 = "" // arms differ legitimately; per-arm map is the stamp
	}
	report := Report{Provenance: prov, PromptSHA256ByArm: promptByArm, MinTrials: MinTrialsPerArm, Arms: map[string]*ArmSummary{}}
	arms := slices.Sorted(maps.Keys(byArm))
	for _, arm := range arms {
		report.Arms[arm] = &ArmSummary{Trials: len(byArm[arm])}
	}
	taskNames := slices.Sorted(maps.Keys(byTaskArm))
	perArmTaskMeds := map[string][]float64{}
	perArmTaskJudge := map[string][]int{}
	for _, task := range taskNames {
		row := TaskRow{Task: task, Arms: map[string]ArmTask{}}
		for _, arm := range slices.Sorted(maps.Keys(byTaskArm[task])) {
			ts := byTaskArm[task][arm]
			if len(ts) < MinTrialsPerArm {
				return Report{}, fmt.Errorf("arm %q has %d trial(s) for task %q, scorer requires >=%d per arm", arm, len(ts), task, MinTrialsPerArm)
			}
			toks := make([]int, len(ts))
			js := make([]int, 0, len(ts))
			for i, t := range ts {
				toks[i] = t.Tokens
				if t.Judged {
					js = append(js, t.JudgeScore)
				}
			}
			med := Median(toks)
			at := ArmTask{N: len(ts), TokensMedian: med}
			if len(js) > 0 {
				jm := Median(js)
				at.JudgeMedian = jm
				perArmTaskJudge[arm] = append(perArmTaskJudge[arm], int(jm))
			}
			row.Arms[arm] = at
			perArmTaskMeds[arm] = append(perArmTaskMeds[arm], med)
		}
		report.Tasks = append(report.Tasks, row)
	}
	for _, arm := range arms {
		report.Arms[arm].TokensMedian = MedianF(perArmTaskMeds[arm])
		if js := perArmTaskJudge[arm]; len(js) > 0 {
			report.Arms[arm].JudgeMedian = Median(js)
		}
	}
	// Savings: with exactly two arms the alphabetically-last arm is the
	// candidate against the reference (baseline first, leankg second —
	// matches the Checklist convention).
	if len(arms) == 2 {
		ref, cand := report.Arms[arms[0]], report.Arms[arms[1]]
		if !math.IsNaN(ref.TokensMedian) && !math.IsNaN(cand.TokensMedian) && ref.TokensMedian != 0 {
			s := (ref.TokensMedian - cand.TokensMedian) / ref.TokensMedian * 100
			report.SavingsPct = &s
		}
	}
	return report, nil
}

// ---------- judge-blind scoring ----------

// BlindGroup is one task's anonymized judge view: labels A, B, C...
// mapped to shuffled answer texts. The mapping (DeAnon) lives only in
// this process and is applied AFTER scoring — that is the blind
// property, structural not conventional: JudgePrompt accepts only the
// group, whose fields carry no arm identity, so an arm name cannot
// enter the judge view.
type BlindGroup struct {
	Task     string
	Question string
	Labels   []string
	Texts    []string
	DeAnon   []int // Labels[i] -> index into the trials slice
}

// ArmLabels returns the arm identities behind this group's labels. It is
// deliberately a SEPARATE accessor from what JudgePrompt consumes: the
// judge view (Labels/Texts/Question) never carries them.
func (g BlindGroup) ArmLabels(trials []Trial) []string {
	arms := make([]string, len(g.Labels))
	for i, idx := range g.DeAnon {
		arms[i] = trials[idx].Arm
	}
	return arms
}

// Rubric is the fixed 0-6 judge rubric (parity with score.py).
const Rubric = `You are grading anonymized answers to one software-architecture
question about a specific codebase. Score EACH answer on three criteria,
0-2 points each (integers only):
  correctness: describes the real data/control flow of the asked mechanism
  grounding:   cites concrete files/modules/symbols that plausibly exist
  depth:       explains mechanism detail beyond a surface summary
Total 0-6 per answer. Be strict: vague or generic prose scores low on
grounding and depth.`

// MaxAnswerChars keeps the judge prompt bounded (parity with score.py).
const MaxAnswerChars = 8000

// NewSeededRand builds the deterministic shuffle source (v2 PCG).
func NewSeededRand(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// BlindGroups collects the answerable trials per task, shuffles them
// with rng, and returns one anonymized view per task. Trials without an
// answer or question are skipped (a dropped answer must not fake a
// blind slot).
func BlindGroups(trials []Trial, rng *rand.Rand) []BlindGroup {
	type slot struct {
		text  string
		index int
	}
	byTask := map[string][]slot{}
	questions := map[string]string{}
	for i, t := range trials {
		if t.Answer == "" || t.Question == "" {
			continue
		}
		byTask[t.Task] = append(byTask[t.Task], slot{t.Answer, i})
		questions[t.Task] = t.Question
	}
	tasks := slices.Sorted(maps.Keys(byTask))
	groups := make([]BlindGroup, 0, len(tasks))
	for _, task := range tasks {
		slots := byTask[task]
		cp := make([]slot, len(slots))
		copy(cp, slots)
		rng.Shuffle(len(cp), func(i, j int) { cp[i], cp[j] = cp[j], cp[i] })
		g := BlindGroup{Task: task, Question: questions[task]}
		for i, s := range cp {
			if i >= 26 {
				break // labels A..Z ceiling; more arms+trials per task is out of scorer scope
			}
			g.Labels = append(g.Labels, string(rune('A'+i)))
			g.Texts = append(g.Texts, s.text)
			g.DeAnon = append(g.DeAnon, s.index)
		}
		groups = append(groups, g)
	}
	return groups
}

// JudgePrompt renders the judge view for one group: rubric, question,
// labeled answers only. No arm name is expressible — the function has no
// access to it.
func JudgePrompt(g BlindGroup) string {
	parts := []string{Rubric, "", "QUESTION:\n" + g.Question, ""}
	for i, label := range g.Labels {
		text := g.Texts[i]
		if len(text) > MaxAnswerChars {
			text = text[:MaxAnswerChars]
		}
		parts = append(parts, "=== ANSWER "+label+" ===\n"+text+"\n")
	}
	parts = append(parts, "Grade every answer. JSON array only:")
	parts = append(parts, `[{"label": "A", "correctness": 0, "grounding": 0, "depth": 0}, ...]`)
	return strings.Join(parts, "\n")
}

// JudgeFunc is the scripted/fake-able judge seam: prompt in, per-label
// totals (0..RubricMax) out. Tests stub it deterministically; the live
// runner execs a real judge CLI.
type JudgeFunc func(prompt string) (map[string]int, error)

// JudgeBlind scores every group through the judge seam and unblinds the
// scores onto the trials ONLY here, at report time. Returns the number
// of trials judged.
func JudgeBlind(trials []Trial, rng *rand.Rand, judge JudgeFunc) (int, error) {
	groups := BlindGroups(trials, rng)
	done := 0
	for _, g := range groups {
		scores, err := judge(JudgePrompt(g))
		if err != nil {
			return done, fmt.Errorf("judge task %q: %w", g.Task, err)
		}
		for i, label := range g.Labels {
			total, ok := scores[label]
			if !ok {
				return done, fmt.Errorf("judge task %q: missing score for label %s", g.Task, label)
			}
			if total < 0 || total > RubricMax {
				return done, fmt.Errorf("judge task %q: label %s score %d outside 0..%d", g.Task, label, total, RubricMax)
			}
			trials[g.DeAnon[i]].Judged = true
			trials[g.DeAnon[i]].JudgeScore = total
			done++
		}
	}
	return done, nil
}

// ---------- zg pitfalls checklist ----------

// Check is one rigor gate computed from run metadata (parity with the
// cross_tool aggregate.py "Rigor Checklist": >=3 trials/arm, prompt
// identical, model uniform, corpus pinned, tools reachable — plus the
// leakage guard that voids a contaminated baseline).
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"` // PASS | FAIL | n/a
	Detail string `json:"detail"`
}

// Checklist computes the zg-pitfall gates from the recorded trials. It
// never fails: an unpinned or short run must still produce the artifact
// that says so.
func Checklist(trials []Trial) []Check {
	pass := func(ok bool) string {
		if ok {
			return "PASS"
		}
		return "FAIL"
	}
	if len(trials) == 0 {
		return []Check{{ID: "trials_min3", Status: "FAIL", Detail: "no trials recorded"}}
	}
	byArmTask := map[string]map[string]int{}
	byArm := map[string]int{}
	promptByArm := map[string]map[string]bool{}
	corpuses := map[string]bool{}
	models := map[string]bool{}
	unpinned := 0
	for _, t := range trials {
		if t.Pins.Validate() != nil {
			unpinned++
		}
		if byArmTask[t.Arm] == nil {
			byArmTask[t.Arm] = map[string]int{}
			promptByArm[t.Arm] = map[string]bool{}
		}
		byArmTask[t.Arm][t.Task]++
		byArm[t.Arm]++
		promptByArm[t.Arm][t.Pins.PromptSHA256] = true
		corpuses[t.Pins.CorpusSHA] = true
		if t.Pins.Model != "" {
			models[t.Pins.Model] = true
		}
	}
	min3 := true
	short := []string{}
	for arm, tasks := range byArmTask {
		for task, n := range tasks {
			if n < MinTrialsPerArm {
				min3 = false
				short = append(short, fmt.Sprintf("%s/%s=%d", arm, task, n))
			}
		}
	}
	sort.Strings(short)
	checks := []Check{
		{ID: "trials_min3", Status: pass(min3),
			Detail: fmt.Sprintf("%d trial(s) across %d arm(s); shortfalls: %s", len(trials), len(byArm), joinOr(short, "none"))},
	}
	// Prompt drift (like-for-like): one template hash per arm. The two
	// arms legitimately differ (the treatment IS the extra instruction),
	// but mixing hashes within an arm breaks like-for-like.
	promptOK := true
	for _, shas := range promptByArm {
		if len(shas) > 1 {
			promptOK = false
			break
		}
	}
	checks = append(checks, Check{ID: "prompt_identical", Status: pass(promptOK),
		Detail: fmt.Sprintf("%d arm(s), one prompt hash each", len(promptByArm))})
	// Corpus drift = leakage of a "different codebase" into the
	// comparison: one 40-hex corpus SHA.
	corpusOK := len(corpuses) == 1
	for c := range corpuses {
		if !isHex(c, 40) {
			corpusOK = false
		}
	}
	checks = append(checks, Check{ID: "corpus_pinned", Status: pass(corpusOK),
		Detail: fmt.Sprintf("%d distinct corpus SHA(s)", len(corpuses))})
	// Stochasticity guard beyond the count: model uniformity.
	checks = append(checks, Check{ID: "model_uniform", Status: pass(len(models) <= 1),
		Detail: fmt.Sprintf("%d distinct model(s)", len(models))})
	// Pins recorded on every row.
	checks = append(checks, Check{ID: "pins_recorded", Status: pass(unpinned == 0),
		Detail: fmt.Sprintf("%d unpinned row(s)", unpinned)})
	// Tool-access smoke + leakage guard. Candidate arm = last in sorted
	// arm order when >=2 arms (mirrors Aggregate's savings convention:
	// baseline first alphabetically, leankg second).
	arms := slices.Sorted(maps.Keys(byArm))
	if len(arms) >= 2 {
		cand, refArm := arms[len(arms)-1], arms[0]
		candTrials, refTrials := 0, 0
		candCalls, leaky := 0, 0
		for _, t := range trials {
			switch t.Arm {
			case cand:
				candTrials++
				if t.ToolCalls > 0 {
					candCalls++
				}
			case refArm:
				refTrials++
				if t.MCPAttached || t.ToolCalls > 0 {
					leaky++
				}
			}
		}
		checks = append(checks,
			Check{ID: "tool_access_smoke", Status: pass(candCalls == candTrials && candTrials > 0),
				Detail: fmt.Sprintf("candidate arm %q: %d/%d trials used >=1 tool", cand, candCalls, candTrials)},
			Check{ID: "no_leakage", Status: pass(leaky == 0),
				Detail: fmt.Sprintf("reference arm %q: %d/%d trials saw MCP tooling (must be 0)", refArm, leaky, refTrials)},
		)
	} else {
		checks = append(checks,
			Check{ID: "tool_access_smoke", Status: "n/a", Detail: "single-arm run"},
			Check{ID: "no_leakage", Status: "n/a", Detail: "single-arm run"},
		)
	}
	return checks
}

// ---------- report emission ----------

// WriteReport emits the results JSON: provenance stamp + medians +
// checklist. NaN medians marshal as null.
func WriteReport(w io.Writer, r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// ---------- helpers ----------

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func quote(s string) string { return `"` + s + `"` }

func sameTools(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func uniqueValues(m map[string]string) map[string]bool {
	out := map[string]bool{}
	for _, v := range m {
		out[v] = true
	}
	return out
}

func joinOr(xs []string, empty string) string {
	if len(xs) == 0 {
		return empty
	}
	return strings.Join(xs, ",")
}
