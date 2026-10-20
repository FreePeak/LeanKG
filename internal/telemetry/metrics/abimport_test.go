package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

const (
	sha40 = "0123456789abcdef0123456789abcdef01234567"
	sha64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseABDirMapsCrossToolAndHarnessRuns(t *testing.T) {
	dir := t.TempDir()
	cross := strings.Join([]string{
		`{"repo":"gin","arm":"with","run_idx":1,"repo_sha":"` + sha40 + `","prompt_sha256":"` + sha64 + `","valid":true,"input_tokens":1000,"output_tokens":200,"cache_read_tokens":9999,"num_turns":4,"duration_s":31.5,"total_cost_usd":0.4,"tool_calls":6,"file_reads":1}`,
		`{"repo":"gin","arm":"without","run_idx":1,"repo_sha":"` + sha40 + `","prompt_sha256":"` + sha64 + `","valid":false,"invalid_reason":"mcp_leaked_into_without_arm","input_tokens":2000,"output_tokens":100,"num_turns":9,"duration_s":50,"total_cost_usd":0.9,"tool_calls":12,"file_reads":7}`,
		`{"repo":"gin","arm":"with","run_idx":2,"repo_sha":"","prompt_sha256":"","valid":true,"input_tokens":5,"output_tokens":5}`,
		`{"hello":"world"}`,
	}, "\n")
	writeFile(t, filepath.Join(dir, "cross_tool", "results", "gin.jsonl"), cross+"\n")
	writeFile(t, filepath.Join(dir, "cross_tool", "results", "scores", "scores.jsonl"),
		`{"repo":"gin","arm":"with","run_idx":1,"judge_model":"sonnet","score":5}`+"\n"+
			`{"repo":"gin","arm":"without","run_idx":1,"judge_model":"sonnet","score":3}`+"\n")
	trial := `{"arm":"with","task":"find-auth","trial":1,"tokens":700,"success":true,"mcp_tool_count":3,"mcp_attached":true,"judge_score":4,"pins":{"corpus_sha":"` + sha40 + `","tool_shas":{"leankg":"` + sha40 + `"},"prompt_version":"v2","prompt_sha256":"` + sha64 + `"}}`
	leak := `{"arm":"without","task":"find-auth","trial":2,"tokens":900,"mcp_attached":true,"pins":{"corpus_sha":"` + sha40 + `","tool_shas":{"leankg":"` + sha40 + `"},"prompt_version":"v2","prompt_sha256":"` + sha64 + `"}}`
	unpinned := `{"arm":"with","task":"find-auth","trial":3,"tokens":700,"pins":{}}`
	writeFile(t, filepath.Join(dir, "ab", "trials.jsonl"), trial+"\n"+leak+"\n"+unpinned+"\n")
	writeFile(t, filepath.Join(dir, "ab", "notes.txt"), "ignored entirely")

	imp, err := ParseABDir(dir, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(imp.Runs) != 6 {
		t.Fatalf("runs=%d want 6 (3 cross_tool + 3 harness; unpinned rows parsed but invalid; unrecognised row skipped)", len(imp.Runs))
	}
	var cw, cwo telemetry.ABRun
	for _, r := range imp.Runs {
		if r.Source == "cross_tool" && r.Arm == "with" && r.DurationS == 31.5 {
			cw = r
		}
		if r.Source == "cross_tool" && r.Arm == "without" {
			cwo = r
		}
	}
	if cw.Tokens != 1200 || cw.Turns != 4 || cw.ToolCalls != 6 || cw.FileReads != 1 || cw.CostUSD != 0.4 {
		t.Fatalf("cross_tool with run = %+v", cw)
	}
	if cw.JudgeScore != 5 || cw.Task != "gin" || cw.Repo != "gin" || !cw.Valid || cw.ImportedAt != t0 {
		t.Fatalf("cross_tool with run judge/task/valid/imported = %+v", cw)
	}
	if cwo.Valid {
		t.Fatal("leak-flagged without run must be invalid")
	}
	if cwo.JudgeScore != 3 {
		t.Fatalf("judge score for without run=%v want 3", cwo.JudgeScore)
	}
	unpinnedRow := 0
	for _, r := range imp.Runs {
		if r.Source == "cross_tool" && r.Valid && r.Tokens == 10 {
			unpinnedRow++
		}
	}
	if unpinnedRow != 0 {
		t.Fatal("cross_tool row without pins must be invalid")
	}
	var h telemetry.ABRun
	for _, r := range imp.Runs {
		if r.Source == "ab_harness" && r.Arm == "with" && r.Valid {
			h = r
		}
	}
	if h.Tokens != 700 || h.JudgeScore != 4 || h.ToolCalls != 3 || h.Task != "find-auth" {
		t.Fatalf("harness trial = %+v", h)
	}
	var leakRun, unpinnedRun telemetry.ABRun
	for _, r := range imp.Runs {
		if r.Source != "ab_harness" {
			continue
		}
		if r.Arm == "without" {
			leakRun = r
		} else {
			if r.Valid == false && r.Tokens == 700 {
				unpinnedRun = r
			}
		}
	}
	if leakRun.Valid {
		t.Fatal("harness without-arm trial with MCP attached must be invalid (leak)")
	}
	if unpinnedRun.ID == "" {
		t.Fatal("unpinned harness trial must be parsed and marked invalid")
	}
	seen := map[string]bool{}
	for _, r := range imp.Runs {
		if r.ID == "" || seen[r.ID] {
			t.Fatalf("ids must be non-empty and unique: %q", r.ID)
		}
		seen[r.ID] = true
	}
	if imp.Files != 3 {
		t.Fatalf("files=%d want 3 scanned data files", imp.Files)
	}
	if len(imp.Warnings) == 0 {
		t.Fatal("unrecognised files must produce a warning, not a silent skip")
	}
}

func TestParseABDirIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "r.jsonl"), `{"repo":"x","arm":"with","run_idx":1,"repo_sha":"`+sha40+`","prompt_sha256":"`+sha64+`","valid":true,"input_tokens":1}`+"\n")
	a, err := ParseABDir(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseABDir(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Runs) != 1 || len(b.Runs) != 1 || a.Runs[0].ID != b.Runs[0].ID {
		t.Fatal("re-import must produce the same ids so the store upserts instead of duplicating")
	}
}

func TestParseABDirMissingDir(t *testing.T) {
	if _, err := ParseABDir(filepath.Join(t.TempDir(), "absent"), t0); err == nil {
		t.Fatal("missing dir must be an error")
	}
}
