// Package astgrep wraps the external ast-grep CLI (or its `sg` alias) for
// pattern search over a directory. The binary is never assumed present:
// probing lives in New, and the rest of the engine must treat the ast-grep
// tier as capability-gated (see langs.Tiers).
//
// Exec is argv-only (never a shell string) and always context-bounded with a
// 10s ceiling, so a wedged binary can never stall a query.
package astgrep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Match is one ast-grep hit, normalized.
//
// Line/Col are 1-based. ast-grep emits 0-based `range.start.{line,column}`
// (verified against 0.45.2); we add 1 to both. The alternate
// `position.{rowStart,colStart}` shape (both 0-based) gets the same +1. Hits
// carrying only byte offsets yield Line=Col=0 with Text filled.
type Match struct {
	File string
	Line int
	Col  int
	Text string
}

var (
	// ErrNotInstalled means neither ast-grep nor sg resolved on PATH.
	ErrNotInstalled = errors.New("astgrep: no ast-grep or sg binary on PATH")
	// ErrTimeout means the exec was cut short by its context (10s ceiling or
	// caller cancellation).
	ErrTimeout = errors.New("astgrep: command timed out or was canceled")
)

// runTimeout bounds every subprocess the wrapper spawns. ioGrace additionally
// caps the wait for output pipes after a kill: a wedged binary that spawned
// grandchildren inheriting our stdout would otherwise hold Wait open past the
// deadline (worst case runTimeout + ioGrace).
const (
	runTimeout = 10 * time.Second
	ioGrace    = 500 * time.Millisecond
)

// Runner wraps one resolved ast-grep binary.
type Runner struct {
	// Bin is the binary path. Empty means "not probed / absent" and every
	// call fails with ErrNotInstalled. New() fills it; tests may set it.
	Bin string
}

// New resolves the CLI on PATH: ast-grep first, then the sg alias.
func New() (*Runner, error) {
	for _, name := range []string{"ast-grep", "sg"} {
		if path, err := exec.LookPath(name); err == nil {
			return &Runner{Bin: path}, nil
		}
	}
	return nil, ErrNotInstalled
}

// RunPattern runs `<bin> run --pattern <pat> --lang <lang> <rootDir> --json`
// under ctx with a 10s ceiling. max <= 0 returns all matches; a positive max
// truncates.
func (r *Runner) RunPattern(ctx context.Context, lang, pattern, rootDir string, max int) ([]Match, error) {
	if r.Bin == "" {
		return nil, ErrNotInstalled
	}
	cctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, r.Bin, "run", "--pattern", pattern, "--lang", lang, rootDir, "--json")
	cmd.WaitDelay = ioGrace
	var out, errOut bytes.Buffer // stderr kept apart: warnings must not poison --json
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil || cctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", ErrTimeout, cctx.Err())
		}
		// ast-grep exits 1 when the scan found NO matches; the payload is
		// still a valid JSON array, so only other exits are failures.
		var exitErr *exec.ExitError
		if !(errors.As(err, &exitErr) && exitErr.ExitCode() == 1) {
			return nil, fmt.Errorf("astgrep: %s: %w", strings.TrimSpace(errOut.String()), err)
		}
	}
	matches, err := parse(out.Bytes())
	if err != nil {
		return nil, err
	}
	if max > 0 && len(matches) > max {
		matches = matches[:max]
	}
	return matches, nil
}

// Version returns the binary's --version output, trimmed.
func (r *Runner) Version(ctx context.Context) (string, error) {
	if r.Bin == "" {
		return "", ErrNotInstalled
	}
	cctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	versionCmd := exec.CommandContext(cctx, r.Bin, "--version")
	versionCmd.WaitDelay = ioGrace
	out, err := versionCmd.Output()
	if err != nil {
		if ctx.Err() != nil || cctx.Err() != nil {
			return "", fmt.Errorf("%w: %v", ErrTimeout, cctx.Err())
		}
		return "", fmt.Errorf("astgrep: version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// parse decodes ast-grep's --json output. The shape has drifted across
// releases ({file,range,text}, {path,...}, nested "matches", byte-offset
// variants), so decoding is generic and every field is best-effort.
func parse(data []byte) ([]Match, error) {
	var raw []map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &raw); err != nil {
		return nil, fmt.Errorf("astgrep: unexpected --json output: %w", err)
	}
	var out []Match
	for _, m := range raw {
		out = append(out, collect(m)...)
	}
	return out, nil
}

// collect maps one decoded JSON object to matches, recursing into any nested
// "matches" array (releases that emit one hit per capture use it).
func collect(m map[string]any) []Match {
	file := str(m, "file", "path")
	if nested, ok := m["matches"].([]any); ok && len(nested) > 0 {
		// Nested hits may omit file (it lives on the wrapper object).
		var out []Match
		for _, n := range nested {
			if nm, ok := n.(map[string]any); ok {
				for _, mt := range collect(nm) {
					if mt.File == "" {
						mt.File = file
					}
					out = append(out, mt)
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	mt := Match{File: file, Text: str(m, "text", "capture")}
	mt.Line, mt.Col = lineCol(m)
	return []Match{mt}
}

// lineCol extracts 1-based position from whichever shape is present; every
// source shape is 0-based on both axes.
func lineCol(m map[string]any) (int, int) {
	if r, ok := m["range"].(map[string]any); ok {
		if start, ok := r["start"].(map[string]any); ok {
			if _, hasLine := start["line"].(float64); hasLine {
				return int(num(start, "line")) + 1, int(num(start, "column")) + 1
			}
		}
	}
	if ranges, ok := m["ranges"].([]any); ok && len(ranges) > 0 {
		if first, ok := ranges[0].(map[string]any); ok {
			if pos, ok := first["position"].(map[string]any); ok {
				return int(num(pos, "rowStart")) + 1, int(num(pos, "colStart")) + 1
			}
		}
	}
	return 0, 0
}

// str returns the first present, string-typed key.
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return v
		}
	}
	return ""
}

// num returns the key as float64 (0 when absent or non-numeric).
func num(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}
