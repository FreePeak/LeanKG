package ab_test

// FR-ZCP-08 harness hardening tests (issue #276). Hermetic: the judge is
// the scripted seam (JudgeFunc stub), no LLM calls, no files outside
// t.TempDir(), no network.

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	ab "github.com/FreePeak/LeanKG/benchmark/ab"
)

const (
	corpusSHA = "0123456789abcdef0123456789abcdef01234567"
	leankgSHA = "89abcdef0123456789abcdef0123456789abcdef"
	kiloSHA   = "fedcba9876543210fedcba9876543210fedcba98"
	promptSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func pins() ab.Pins {
	return ab.Pins{
		CorpusSHA:     corpusSHA,
		ToolSHAs:      map[string]string{"leankg": leankgSHA, "kilo": kiloSHA},
		PromptVersion: "kilo-ab-v1",
		PromptSHA256:  promptSHA,
	}
}

func trial(arm, task string, n, tokens int) ab.Trial {
	return ab.Trial{Arm: arm, Task: task, N: n, Tokens: tokens,
		Success: true, ToolCalls: 2, Pins: pins()}
}

func armTrials(arm, task string, n int, tokens ...int) []ab.Trial {
	out := make([]ab.Trial, 0, len(tokens))
	for i, tk := range tokens {
		t := trial(arm, task, n+i, tk)
		t.ToolCalls = 2
		out = append(out, t)
	}
	return out
}

// ---- clause 1: pinned SHAs/prompts — refused, not recorded ----

func TestAppendTrialRefusesUnpinned(t *testing.T) {
	cases := map[string]func(*ab.Pins){
		"no corpus sha":       func(p *ab.Pins) { p.CorpusSHA = "" },
		"short corpus sha":    func(p *ab.Pins) { p.CorpusSHA = "abc123" },
		"non-hex corpus sha":  func(p *ab.Pins) { p.CorpusSHA = strings.Repeat("g", 40) },
		"no tool shas":        func(p *ab.Pins) { p.ToolSHAs = nil },
		"tool sha not 40-hex": func(p *ab.Pins) { p.ToolSHAs = map[string]string{"kilo": "latest"} },
		"no prompt hash":      func(p *ab.Pins) { p.PromptSHA256 = "" },
		"short prompt hash":   func(p *ab.Pins) { p.PromptSHA256 = strings.Repeat("a", 63) },
		"no prompt version":   func(p *ab.Pins) { p.PromptVersion = "  " },
	}
	for name, breakPin := range cases {
		t.Run(name, func(t *testing.T) {
			tr := trial("leankg", "q1", 1, 100)
			p := tr.Pins
			breakPin(&p)
			tr.Pins = p
			var buf bytes.Buffer
			if err := ab.AppendTrial(&buf, tr); err == nil {
				t.Fatal("unpinned trial recorded — must be refused")
			}
			if buf.Len() != 0 {
				t.Errorf("refused trial still wrote %d bytes", buf.Len())
			}
		})
	}
	// Positive control: fully pinned goes through and the JSON row
	// carries the provenance fields.
	var buf bytes.Buffer
	if err := ab.AppendTrial(&buf, trial("leankg", "q1", 1, 100)); err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &row); err != nil {
		t.Fatal(err)
	}
	pm, ok := row["pins"].(map[string]any)
	if !ok {
		t.Fatal("recorded row has no pins object")
	}
	for _, field := range []string{"corpus_sha", "tool_shas", "prompt_sha256", "prompt_version"} {
		if pm[field] == nil || pm[field] == "" {
			t.Errorf("pins.%s missing in recorded row", field)
		}
	}
}

func TestParseTrialsRefusesUnpinnedRows(t *testing.T) {
	good, _ := json.Marshal(trial("leankg", "q1", 1, 100))
	bad := `{"arm":"baseline","task":"q1","trial":1,"tokens":200}`
	src := string(good) + "\n" + bad + "\n"
	if _, err := ab.ParseTrials(strings.NewReader(src)); err == nil {
		t.Fatal("loader accepted an unpinned row")
	}
	rows, err := ab.ParseTrials(strings.NewReader(string(good) + "\n"))
	if err != nil || len(rows) != 1 {
		t.Fatalf("pinned row rejected: %v %v", err, rows)
	}
}

// ---- clause 2: >=3 trials/arm enforced by the scorer + medians ----

func TestAggregateRefusesUnderThreeTrials(t *testing.T) {
	trials := []ab.Trial{trial("baseline", "q1", 1, 100), trial("baseline", "q1", 2, 110),
		trial("leankg", "q1", 1, 50), trial("leankg", "q1", 2, 55), trial("leankg", "q1", 3, 60)}
	_, err := ab.Aggregate(trials)
	if err == nil || !strings.Contains(err.Error(), "baseline") || !strings.Contains(err.Error(), ">=3") {
		t.Fatalf("scorer must fail naming the short arm: %v", err)
	}
}

func TestAggregatePerArmMedians(t *testing.T) {
	// tokens: baseline 100/110/120 (med 110), leankg 50/60/70 (med 60)
	trials := append(armTrials("baseline", "q1", 1, 100, 110, 120),
		armTrials("leankg", "q1", 1, 50, 60, 70)...)
	rep, err := ab.Aggregate(trials)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Arms["baseline"].TokensMedian; got != 110 {
		t.Errorf("baseline median = %v, want 110", got)
	}
	if got := rep.Arms["leankg"].TokensMedian; got != 60 {
		t.Errorf("leankg median = %v, want 60", got)
	}
	// Savings: (110-60)/110*100
	if rep.SavingsPct == nil || math.Abs(*rep.SavingsPct-45.454545) > 0.01 {
		t.Errorf("savings = %v, want ~45.45", rep.SavingsPct)
	}
	// Provenance stamp lands in the report.
	if rep.Provenance.CorpusSHA != corpusSHA || rep.Provenance.PromptSHA256 != promptSHA {
		t.Errorf("report provenance = %+v", rep.Provenance)
	}
	if rep.MinTrials != 3 {
		t.Errorf("min_trials = %d, want 3", rep.MinTrials)
	}
}

func TestAggregateMultiTaskUsesMedianOfMedians(t *testing.T) {
	// q1: baseline {10,20,30} med 20 | leankg {5,6,7} med 6
	// q2: baseline {900,950,999} med 950 | leankg {8,9,10} med 9
	// Arm figure = median(20,950)=485 for baseline — the noisy q2 must
	// NOT dominate a pooled mean (methodology pin).
	trials := append(append(
		armTrials("baseline", "q1", 1, 10, 20, 30),
		armTrials("leankg", "q1", 1, 5, 6, 7)...),
		append(armTrials("baseline", "q2", 4, 900, 950, 999),
			armTrials("leankg", "q2", 4, 8, 9, 10)...)...)
	rep, err := ab.Aggregate(trials)
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Arms["baseline"].TokensMedian; got != 485 {
		t.Errorf("baseline arm median = %v, want median-of-task-medians 485", got)
	}
	// A task missing the floor on one arm fails even when the arm totals
	// clear it (baseline has 6 trials overall, q2 has 3 — leankg q2 too).
	broken := armTrials("baseline", "q1", 1, 1, 2, 3)
	broken = append(broken, armTrials("leankg", "q1", 1, 4, 5)...) // only 2 for leankg/q1
	broken = append(broken, armTrials("baseline", "q2", 4, 7)...)
	broken = append(broken, armTrials("leankg", "q2", 1, 8, 9, 10)...)
	if _, err := ab.Aggregate(broken); err == nil {
		t.Error("per-task trial floor must hold even when arm totals clear it")
	}
}

func TestAggregateRefusesMixedProvenance(t *testing.T) {
	trials := armTrials("baseline", "q1", 1, 1, 2, 3)
	drifted := armTrials("leankg", "q1", 1, 4, 5, 6)
	drifted[0].Pins.CorpusSHA = "1111111111111111111111111111111111111111"
	if _, err := ab.Aggregate(append(trials, drifted...)); err == nil {
		t.Fatal("report mixed two corpus pins")
	}
	// Prompt drift WITHIN an arm is broken beyond repair; drift ACROSS
	// arms is the instructed treatment — it must pass, with the
	// per-arm hash map exposing both templates.
	different := armTrials("leankg", "q1", 1, 4, 5, 6)
	different[0].Pins.PromptSHA256 = strings.Repeat("b", 64)
	different[1].Pins.PromptSHA256 = strings.Repeat("b", 64)
	if _, err := ab.Aggregate(append(armTrials("baseline", "q1", 1, 1, 2, 3), different...)); err == nil {
		t.Fatal("arm mixing two prompt templates scored")
	}
	different[2].Pins.PromptSHA256 = strings.Repeat("b", 64) // arm now uniform again
	rep, err := ab.Aggregate(append(armTrials("baseline", "q1", 1, 1, 2, 3), different...))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rep.PromptSHA256ByArm); got != 2 {
		t.Errorf("per-arm prompt map = %d entries, want 2", got)
	}
	if rep.Provenance.PromptSHA256 != "" {
		t.Errorf("shared prompt stamp = %q, want empty when arms differ", rep.Provenance.PromptSHA256)
	}
	// Identical templates collapse to one shared stamp.
	same := armTrials("leankg", "q1", 1, 4, 5, 6)
	rep, err = ab.Aggregate(append(armTrials("baseline", "q1", 1, 1, 2, 3), same...))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Provenance.PromptSHA256 != promptSHA {
		t.Errorf("shared prompt stamp = %q, want %q", rep.Provenance.PromptSHA256, promptSHA)
	}
}

// ---- clause 3: judge-blind scorer ----

func TestJudgeBlindInputViewIsLabelErased(t *testing.T) {
	mk := func(arm, task, answer string, n int) ab.Trial {
		tt := trial(arm, task, n, 100)
		tt.Question = "How does the import pipeline batch writes?"
		tt.Answer = answer
		return tt
	}
	trials := []ab.Trial{
		mk("baseline", "q1", "alpha answer citing handler.rs", 1),
		mk("leankg", "q1", "beta answer citing batch.rs", 2),
		mk("baseline", "q1", "gamma answer", 3),
		mk("leankg", "q1", "delta answer", 4),
	}
	var prompts []string
	judge := func(prompt string) (map[string]int, error) {
		prompts = append(prompts, prompt)
		return map[string]int{"A": 6, "B": 4, "C": 2, "D": 0}, nil
	}
	if _, err := ab.JudgeBlind(trials, ab.NewSeededRand(7), judge); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 1 {
		t.Fatalf("got %d prompts, want one per task", len(prompts))
	}
	p := prompts[0]
	// Blind property: the scorer's input view is label-erased. Arm names
	// (case-insensitive) and any arm metadata must be structurally
	// absent — the only answer headers are A/B/C/D.
	for _, leaky := range []string{"baseline", "leankg", `"arm"`, "mcp_attached", "tokens"} {
		if strings.Contains(strings.ToLower(p), leaky) {
			t.Errorf("judge view leaks arm metadata: %q found in prompt", leaky)
		}
	}
	for _, label := range []string{"=== ANSWER A ===", "=== ANSWER B ===", "=== ANSWER C ===", "=== ANSWER D ==="} {
		if !strings.Contains(p, label) {
			t.Errorf("blind prompt missing %q", label)
		}
	}
	// Unblind round-trip: label D scored 0 — exactly one trial carries it
	// and it maps to a real arm; scores must land on the right answers.
	scoreOf := func(answer string) (int, bool) {
		for _, tr := range trials {
			if tr.Answer == answer {
				return tr.JudgeScore, tr.Judged
			}
		}
		t.Fatalf("answer %q not in trials", answer)
		return 0, false
	}
	if n := len(ab.BlindGroups(trials, ab.NewSeededRand(7))[0].Labels); n != 4 {
		t.Errorf("group labels = %d, want 4", n)
	}
	// Every trial got a score from the 6/4/2/0 pool (all judged).
	for _, tr := range trials {
		if !tr.Judged {
			t.Errorf("trial %s#%d not judged", tr.Arm, tr.N)
		}
	}
	if s, ok := scoreOf(trials[0].Answer); !ok || s < 0 || s > ab.RubricMax {
		t.Errorf("unblinded score out of rubric: %d", s)
	}
}

func TestJudgeBlindShufflePermutesAndIsSeeded(t *testing.T) {
	trials := []ab.Trial{}
	for i := 0; i < 6; i++ {
		arm := "baseline"
		if i%2 == 0 {
			arm = "leankg"
		}
		tr := trial(arm, "q1", i+1, 100)
		tr.Question = "Q?"
		tr.Answer = strings.Repeat("x", i+2) + strings.Repeat("y", 6-i)
		trials = append(trials, tr)
	}
	g1 := ab.BlindGroups(trials, ab.NewSeededRand(11))[0]
	g2 := ab.BlindGroups(trials, ab.NewSeededRand(11))[0]
	if strings.Join(g1.Texts, "|") != strings.Join(g2.Texts, "|") {
		t.Error("same seed must reproduce the same shuffle (reproducible reports)")
	}
	// With 6 slots and seed 11 the naive input order must be broken.
	if g1.Texts[0] == trials[0].Answer && g1.Texts[1] == trials[1].Answer {
		t.Error("shuffle left the first two answers in input order")
	}
	// De-anon integrity: label i maps back to the trial owning the text.
	for i := range g1.Labels {
		if trials[g1.DeAnon[i]].Answer != g1.Texts[i] {
			t.Errorf("de-anon mismatch at %s", g1.Labels[i])
		}
	}
	if arms := g1.ArmLabels(trials); len(arms) == 0 {
		t.Error("ArmLabels unavailable at report time")
	}
}

func TestJudgeRejectsOutOfRangeScores(t *testing.T) {
	trials := []ab.Trial{}
	for i := 0; i < 4; i++ {
		tr := trial([]string{"baseline", "leankg"}[i%2], "q1", i+1, 1)
		tr.Question = "Q?"
		tr.Answer = "ans" + string(rune('a'+i))
		trials = append(trials, tr)
	}
	_, err := ab.JudgeBlind(trials, ab.NewSeededRand(3),
		func(string) (map[string]int, error) { return map[string]int{"A": 7, "B": 0, "C": 1, "D": 2}, nil })
	if err == nil || !strings.Contains(err.Error(), "0..6") {
		t.Fatalf("rubric bound not enforced: %v", err)
	}
}

// ---- clause 4: zg pitfalls checklist ----

func validTrials() []ab.Trial {
	out := []ab.Trial{}
	for i, tk := range []int{100, 110, 120} {
		tr := trial("baseline", "q1", i+1, tk)
		tr.ToolCalls = 0 // baseline has no MCP server; a nonzero count here WOULD be leakage
		out = append(out, tr)
	}
	for i, tk := range []int{50, 60, 70} {
		out = append(out, trial("leankg", "q1", i+1, tk)) // ToolCalls=2 from trial()
	}
	return out
}

func find(t *testing.T, checks []ab.Check, id string) ab.Check {
	t.Helper()
	for _, c := range checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("check %q missing from %+v", id, checks)
	return ab.Check{}
}

func TestChecklistAllGreenOnValidRun(t *testing.T) {
	checks := ab.Checklist(validTrials())
	for _, id := range []string{"trials_min3", "prompt_identical", "corpus_pinned", "model_uniform", "pins_recorded", "tool_access_smoke", "no_leakage"} {
		if got := find(t, checks, id).Status; got != "PASS" {
			t.Errorf("%s = %s, want PASS", id, got)
		}
	}
}

func TestChecklistFlagsEachPitfall(t *testing.T) {
	t.Run("stochasticity", func(t *testing.T) {
		ts := validTrials()
		ts = ts[:4] // leankg down to 1 trial
		if got := find(t, ab.Checklist(ts), "trials_min3").Status; got != "FAIL" {
			t.Errorf("trials_min3 = %s, want FAIL", got)
		}
	})
	t.Run("prompt drift", func(t *testing.T) {
		ts := validTrials()
		ts[3].Pins.PromptSHA256 = strings.Repeat("b", 64)
		if got := find(t, ab.Checklist(ts), "prompt_identical").Status; got != "FAIL" {
			t.Errorf("prompt_identical = %s, want FAIL", got)
		}
	})
	t.Run("corpus drift", func(t *testing.T) {
		ts := validTrials()
		ts[0].Pins.CorpusSHA = strings.Repeat("c", 40)
		if got := find(t, ab.Checklist(ts), "corpus_pinned").Status; got != "FAIL" {
			t.Errorf("corpus_pinned = %s, want FAIL", got)
		}
	})
	t.Run("tool-access smoke", func(t *testing.T) {
		ts := validTrials()
		for i := range ts {
			if ts[i].Arm == "leankg" {
				ts[i].ToolCalls = 0 // candidate arm never touched a tool
			}
		}
		if got := find(t, ab.Checklist(ts), "tool_access_smoke").Status; got != "FAIL" {
			t.Errorf("tool_access_smoke = %s, want FAIL", got)
		}
	})
	t.Run("leakage", func(t *testing.T) {
		ts := validTrials()
		ts[0].MCPAttached = true // baseline saw an MCP server
		if got := find(t, ab.Checklist(ts), "no_leakage").Status; got != "FAIL" {
			t.Errorf("no_leakage = %s, want FAIL", got)
		}
	})
	t.Run("unpinned", func(t *testing.T) {
		ts := validTrials()
		ts[1].Pins.CorpusSHA = ""
		if got := find(t, ab.Checklist(ts), "pins_recorded").Status; got != "FAIL" {
			t.Errorf("pins_recorded = %s, want FAIL", got)
		}
	})
}

// ---- report emission ----

func TestWriteReportCarriesProvenanceAndChecklist(t *testing.T) {
	trials := validTrials()
	rep, err := ab.Aggregate(trials)
	if err != nil {
		t.Fatal(err)
	}
	rep.Checklist = ab.Checklist(trials)
	var buf bytes.Buffer
	if err := ab.WriteReport(&buf, rep); err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back["provenance"] == nil {
		t.Error("results JSON has no provenance stamp")
	}
	cl, ok := back["pitfalls_checklist"].([]any)
	if !ok || len(cl) != len(rep.Checklist) {
		t.Errorf("pitfalls_checklist not emitted: %v", back["pitfalls_checklist"])
	}
}

func TestSHA256HexKnownVector(t *testing.T) {
	// echo -n "" | sha256sum
	if got := ab.SHA256Hex(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("SHA256Hex(\"\") = %s", got)
	}
}
