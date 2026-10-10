package metrics

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/FreePeak/LeanKG/benchmark/ab"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// ABImport is the outcome of scanning a directory for A/B results.
type ABImport struct {
	Runs     []telemetry.ABRun
	Files    int      // data files (.json, .jsonl) read
	Warnings []string // unreadable files and unrecognised records
}

// crossRun is one row of benchmarks/cross_tool/run_one.sh output (JSONL).
type crossRun struct {
	Repo          string  `json:"repo"`
	Arm           string  `json:"arm"`
	RunIdx        int     `json:"run_idx"`
	RepoSHA       string  `json:"repo_sha"`
	PromptSHA     string  `json:"prompt_sha256"`
	Valid         bool    `json:"valid"`
	InvalidReason string  `json:"invalid_reason"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	Turns         int     `json:"num_turns"`
	DurationS     float64 `json:"duration_s"`
	CostUSD       float64 `json:"total_cost_usd"`
	ToolCalls     int     `json:"tool_calls"`
	FileReads     int     `json:"file_reads"`
}

// scoreRow is one judge-blind score from benchmarks/cross_tool/score.py.
type scoreRow struct {
	Repo   string  `json:"repo"`
	Arm    string  `json:"arm"`
	RunIdx int     `json:"run_idx"`
	Judge  string  `json:"judge_model"`
	Score  float64 `json:"score"`
}

type record struct {
	file  string // slash-separated path relative to the scanned dir
	index int    // 1-based line (JSONL) or array position (JSON)
	raw   []byte
}

// ParseABDir reads every .json and .jsonl file under dir and maps the
// cross_tool run rows and benchmark/ab trials to telemetry.ABRun. A run is
// Valid only when its pins are present and its leak checks pass; invalid runs
// are kept (with Valid=false) so the paper trail survives. ids are derived
// from the file and record bytes, so re-importing the same directory upserts
// instead of duplicating. A missing dir is an error.
//
// The judge score of a cross_tool run is the score from the lexically first
// judge model that scored it, so scores from different judges never blend.
func ParseABDir(dir string, now time.Time) (ABImport, error) {
	out := ABImport{Runs: []telemetry.ABRun{}, Warnings: []string{}}
	if _, err := os.Stat(dir); err != nil {
		return out, err
	}
	var recs []record
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s: %v", p, err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := filepath.Ext(p)
		if ext != ".json" && ext != ".jsonl" {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		body, rerr := os.ReadFile(p)
		if rerr != nil {
			out.Warnings = append(out.Warnings, rel+": "+rerr.Error())
			return nil
		}
		out.Files++
		recs = append(recs, splitRecords(rel, ext, body, &out.Warnings)...)
		return nil
	})
	if err != nil {
		return out, err
	}

	scores := map[string]scoreRow{}
	var crossRecs, trialRecs []record
	for _, r := range recs {
		var probe map[string]json.RawMessage
		if json.Unmarshal(r.raw, &probe) != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s#%d: not a JSON object", r.file, r.index))
			continue
		}
		_, hasJudge := probe["judge_model"]
		_, hasScore := probe["score"]
		_, hasTask := probe["task"]
		_, hasPins := probe["pins"]
		_, hasArm := probe["arm"]
		_, hasRepo := probe["repo"]
		switch {
		case hasJudge && hasScore:
			var s scoreRow
			if json.Unmarshal(r.raw, &s) == nil {
				k := scoreKey(s.Repo, s.Arm, s.RunIdx)
				if cur, ok := scores[k]; !ok || s.Judge < cur.Judge {
					scores[k] = s
				}
			}
		case hasTask && hasPins:
			trialRecs = append(trialRecs, r)
		case hasArm && hasRepo:
			crossRecs = append(crossRecs, r)
		default:
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s#%d: unrecognised record skipped", r.file, r.index))
		}
	}

	for _, r := range crossRecs {
		var c crossRun
		if err := json.Unmarshal(r.raw, &c); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s#%d: %v", r.file, r.index, err))
			continue
		}
		run := telemetry.ABRun{
			ID:         recordID("cross_tool", r),
			Source:     "cross_tool",
			Task:       c.Repo,
			Arm:        c.Arm,
			Repo:       c.Repo,
			Tokens:     c.InputTokens + c.OutputTokens,
			Turns:      c.Turns,
			DurationS:  c.DurationS,
			ToolCalls:  c.ToolCalls,
			FileReads:  c.FileReads,
			CostUSD:    c.CostUSD,
			Valid:      c.Valid && c.RepoSHA != "" && c.PromptSHA != "" && armKnown(c.Arm),
			ImportedAt: now,
		}
		if s, ok := scores[scoreKey(c.Repo, c.Arm, c.RunIdx)]; ok {
			run.JudgeScore = s.Score
		}
		out.Runs = append(out.Runs, run)
	}

	for _, r := range trialRecs {
		var t ab.Trial
		if err := json.Unmarshal(r.raw, &t); err != nil {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s#%d: %v", r.file, r.index, err))
			continue
		}
		leak := t.Arm == "without" && t.MCPAttached
		out.Runs = append(out.Runs, telemetry.ABRun{
			ID:         recordID("ab_harness", r),
			Source:     "ab_harness",
			Task:       t.Task,
			Arm:        t.Arm,
			Tokens:     int64(t.Tokens),
			ToolCalls:  t.ToolCalls,
			JudgeScore: float64(t.JudgeScore),
			Valid:      t.Validate() == nil && !leak && armKnown(t.Arm),
			ImportedAt: now,
		})
	}
	return out, nil
}

func armKnown(arm string) bool { return arm == "with" || arm == "without" }

func scoreKey(repo, arm string, run int) string {
	return repo + "|" + arm + "|" + strconv.Itoa(run)
}

func recordID(source string, r record) string {
	h := sha256.New()
	h.Write([]byte(r.file))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(r.index)))
	h.Write([]byte{0})
	h.Write(r.raw)
	return source + "-" + hex.EncodeToString(h.Sum(nil))[:24]
}

// splitRecords turns one file into records: each non-blank line of a JSONL
// file, or each element of a top-level JSON array, or the whole object.
func splitRecords(rel, ext string, body []byte, warnings *[]string) []record {
	if ext == ".jsonl" {
		var out []record
		sc := bufio.NewScanner(bytes.NewReader(body))
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		line := 0
		for sc.Scan() {
			line++
			b := bytes.TrimSpace(sc.Bytes())
			if len(b) == 0 {
				continue
			}
			out = append(out, record{file: rel, index: line, raw: append([]byte(nil), b...)})
		}
		if err := sc.Err(); err != nil {
			*warnings = append(*warnings, rel+": "+err.Error())
		}
		return out
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}
	if trimmed[0] == '[' {
		var elems []json.RawMessage
		if err := json.Unmarshal(trimmed, &elems); err != nil {
			*warnings = append(*warnings, rel+": "+err.Error())
			return nil
		}
		out := make([]record, 0, len(elems))
		for i, e := range elems {
			out = append(out, record{file: rel, index: i + 1, raw: []byte(e)})
		}
		return out
	}
	return []record{{file: rel, index: 1, raw: append([]byte(nil), trimmed...)}}
}
