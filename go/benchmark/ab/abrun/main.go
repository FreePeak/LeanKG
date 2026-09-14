// Command abrun is the FR-ZCP-08 harness gate the kilo A/B runners
// record through (see run_kilo_ab_final.sh / run_kilo_ab_test.sh).
//
//	abrun record -out runs.jsonl            # one trial JSON on stdin
//	abrun score  -in runs.jsonl -out report.json [-judge <cmd>] [-seed N]
//
// `record` refuses (non-zero exit, nothing written) any trial without
// complete pins. `score` fails (non-zero) when any arm has fewer than 3
// trials and otherwise emits the results JSON: provenance stamp, per-arm
// medians, judge-blind quality when -judge is given, and the computed zg
// pitfalls checklist. -judge names an executable that receives the
// blind judge prompt on stdin and answers with a JSON object
// {"A": 6, "B": 3, ...} of 0-6 rubric totals — any deterministic script
// or CLI plugs in.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	ab "github.com/FreePeak/LeanKG/go/benchmark/ab"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "record":
		err = record(os.Args[2:])
	case "score":
		err = score(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "abrun:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: abrun record -out FILE | abrun score -in FILE -out FILE [-judge CMD] [-seed N]")
}

func record(args []string) error {
	fs := flag.NewFlagSet("record", flag.ExitOnError)
	out := fs.String("out", "", "JSONL trials file to append to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return fmt.Errorf("record: -out required")
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	var t ab.Trial
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("stdin is not one trial JSON object: %w", err)
	}
	f, err := os.OpenFile(*out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return ab.AppendTrial(f, t)
}

func score(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	in := fs.String("in", "", "JSONL trials file")
	out := fs.String("out", "", "report JSON to write")
	judge := fs.String("judge", "", "judge executable (blind prompt on stdin, {\"A\":n,...} on stdout)")
	seed := fs.Uint64("seed", 20260913, "shuffle seed (record it for reproducibility)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return fmt.Errorf("score: -in and -out required")
	}
	f, err := os.Open(*in)
	if err != nil {
		return err
	}
	defer f.Close()
	trials, err := ab.ParseTrials(f)
	if err != nil {
		return err
	}
	if *judge != "" {
		jf := execJudge(*judge)
		n, err := ab.JudgeBlind(trials, ab.NewSeededRand(*seed), jf)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "abrun: judge-blind scored %d trial(s)\n", n)
	}
	report, err := ab.Aggregate(trials)
	if err != nil {
		return err
	}
	report.Checklist = ab.Checklist(trials)
	w, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer w.Close()
	if err := ab.WriteReport(w, report); err != nil {
		return err
	}
	for _, c := range report.Checklist {
		fmt.Fprintf(os.Stderr, "abrun: [%s] %s — %s\n", c.Status, c.ID, c.Detail)
	}
	return nil
}

func execJudge(name string) ab.JudgeFunc {
	return func(prompt string) (map[string]int, error) {
		cmd := exec.Command(name)
		cmd.Stdin = strings.NewReader(prompt)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		scores := map[string]int{}
		if err := json.Unmarshal([]byte(stdout.String()), &scores); err != nil {
			return nil, fmt.Errorf("judge output is not a JSON label->score object: %w", err)
		}
		return scores, nil
	}
}
