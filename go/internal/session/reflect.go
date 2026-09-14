// Query-outcome reflection (Rust US-GF-09, GraphEngine::report_query_outcome —
// the `reflect` CLI verb and the report_query_outcome MCP tool): append one
// Markdown entry per reflection to <project>/.leankg/reflections/LESSONS.md.
// The Rust engine kept these reflections in the same LESSONS.md file its
// session recall index ranked as a lesson source (session/mod.rs
// lesson_rank("LESSONS.md", 1.0, …)), which is why the port lives in the
// session package: reflections and session lessons share one artifact tree.
//
// Rust appended unconditionally, with no dedup — the dedup'd lessons.json
// (Store.AddLesson) is a different, session-scoped artifact (US-SM-02), so
// this port writes LESSONS.md only.
package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LessonsFile is the reflections artifact, relative to the project directory.
const LessonsFile = ".leankg/reflections/LESSONS.md"

// ReflectOutcome appends one query-outcome entry to
// <projectDir>/.leankg/reflections/LESSONS.md, creating the directory and file
// as needed, and returns the file path.
//
// question is what was asked; outcome is the classification (useful |
// dead_end | corrected — any non-empty string is accepted, matching the Rust
// verb which passed it through unchecked); nodes are the optional qualified
// names that were returned; note is optional free-form context.
func ReflectOutcome(projectDir, question string, nodes []string, outcome, note string) (string, error) {
	path := filepath.Join(projectDir, filepath.FromSlash(LessonsFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("session: create reflections dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("session: open reflections: %w", err)
	}
	defer f.Close()

	nodeStr := "(none)"
	if len(nodes) > 0 {
		nodeStr = strings.Join(nodes, ", ")
	}
	noteLine := ""
	if note != "" {
		noteLine = fmt.Sprintf("- Note: %s\n", note)
	}
	entry := fmt.Sprintf("\n## %d — %s\n\n- Question: %s\n- Nodes: %s\n- Outcome: %s\n%s\n",
		time.Now().Unix(), outcome, question, nodeStr, outcome, noteLine)
	if _, err := f.WriteString(entry); err != nil {
		return "", fmt.Errorf("session: append reflection: %w", err)
	}
	return path, nil
}
