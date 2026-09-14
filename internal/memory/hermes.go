package memory

import (
	"fmt"
	"os"
	"strings"
)

// Add appends text as a new "§ "-delimited entry to a memory file
// (Hermes append sugar). Core-file bounds still apply: a push past
// CoreFileBytes fails with ErrOverflow, leaving the file untouched.
func (m *Memory) Add(file, text string) error {
	full, key, err := m.resolve(file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	cur := strings.TrimRight(string(data), "\n")
	var out string
	if cur == "" {
		out = "§ " + text + "\n"
	} else {
		out = cur + "\n\n§ " + text + "\n"
	}
	if err := bound(key, out); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return err
	}
	return m.reindex(key)
}

// Replace replaces the single occurrence of old with new in a memory file
// (Hermes variant of StrReplace). Zero or multiple occurrences fail with
// ErrAmbiguousMatch.
func (m *Memory) Replace(file, old, new string) error {
	return m.StrReplace(file, old, new)
}

// Remove deletes the single occurrence of substr from the file, keeping the
// rest of the containing line. Zero or multiple occurrences fail with
// ErrAmbiguousMatch.
func (m *Memory) Remove(file, substr string) error {
	full, key, err := m.resolve(file)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	content := string(data)
	n := strings.Count(content, substr)
	if n != 1 {
		return fmt.Errorf("%w: %d occurrences of %q", ErrAmbiguousMatch, n, substr)
	}
	i := strings.Index(content, substr)
	out := content[:i] + content[i+len(substr):]
	if err := bound(key, out); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
		return err
	}
	return m.reindex(key)
}
