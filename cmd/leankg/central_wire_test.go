package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
)

// TestCentralMemoryWired pins when serve hands agent_recall the central memory.
// With the gate on every caller must authenticate, so the central memory is
// wired whatever the bind. With the gate off every caller resolves to Admin, so
// the central memory is wired only while no listener binds beyond loopback. A
// stdio session starts no listener, so it is wired with the gate off.
func TestCentralMemoryWired(t *testing.T) {
	cases := []struct {
		name   string
		gateOn bool
		binds  []string
		want   bool
	}{
		{"loopback listeners, gate off", false, []string{"127.0.0.1:9699", "127.0.0.1:9700", "localhost:8081"}, true},
		{"no listener (stdio), gate off", false, nil, true},
		{"public dashboard, gate off", false, []string{"127.0.0.1:9699", ":8081"}, false},
		{"public REST, gate off", false, []string{"0.0.0.0:9700"}, false},
		{"public dashboard, gate on", true, []string{"127.0.0.1:9699", ":8081"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := centralMemoryWired(c.gateOn, c.binds); got != c.want {
				t.Errorf("centralMemoryWired(gateOn=%v, binds=%v) = %v, want %v", c.gateOn, c.binds, got, c.want)
			}
		})
	}
}

// resetDenyList puts the process-wide deny-list back to the empty state of a
// fresh process, now and when the test ends.
func resetDenyList(t *testing.T) {
	t.Helper()
	_, empty, err := memory.LoadScrubFile(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	memory.SetScrubPatterns(nil, empty)
	t.Cleanup(func() { memory.SetScrubPatterns(nil, empty) })
}

// TestInstallServeDenyList pins the serve startup load of <central>/scrub.txt.
// Placeholders only; HOME and CLAUDE_CONFIG_DIR point at temp dirs.
func TestInstallServeDenyList(t *testing.T) {
	const denied = "PLACEHOLDER_DENIED_TERM"
	const text = "note mentions PLACEHOLDER_DENIED_TERM here"

	writeScrub := func(t *testing.T, home, content string) {
		t.Helper()
		central := memory.GlobalRoot(home)
		if err := os.MkdirAll(central, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(central, "scrub.txt"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(t *testing.T, home string) []string {
		t.Helper()
		var lines []string
		installServeDenyList(home, func(format string, args ...any) {
			lines = append(lines, fmt.Sprintf(format, args...))
		})
		return lines
	}

	t.Run("patterns installed and applied by Redact", func(t *testing.T) {
		resetDenyList(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		writeScrub(t, home, denied+"\n")
		if lines := run(t, home); len(lines) != 0 {
			t.Fatalf("logged %q for a valid file, want nothing", lines)
		}
		if got := memory.Redact(text); strings.Contains(got, denied) || !strings.Contains(got, "[SCRUBBED]") {
			t.Fatalf("Redact(%q) = %q, want the term masked", text, got)
		}
	})

	t.Run("missing file is a no-op", func(t *testing.T) {
		resetDenyList(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		if lines := run(t, home); len(lines) != 0 {
			t.Fatalf("logged %q for a missing file, want nothing", lines)
		}
		if got := memory.Redact(text); got != text {
			t.Fatalf("Redact(%q) = %q, want the text unchanged", text, got)
		}
	})

	t.Run("bad line logs one line and installs nothing", func(t *testing.T) {
		resetDenyList(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		writeScrub(t, home, denied+"\nPLACEHOLDER_BAD_TERM(\n")
		lines := run(t, home)
		if len(lines) != 1 {
			t.Fatalf("logged %d lines %q, want exactly one", len(lines), lines)
		}
		line := lines[0]
		if !strings.Contains(line, "scrub.txt") || !strings.Contains(line, "line 2") {
			t.Fatalf("log line %q, want the file basename and line 2", line)
		}
		if strings.Contains(line, "PLACEHOLDER_BAD_TERM") || strings.Contains(line, denied) || strings.Contains(line, home) {
			t.Fatalf("log line %q leaks a pattern or the home path", line)
		}
		if got := memory.Redact(text); got != text {
			t.Fatalf("Redact(%q) = %q, want no list installed", text, got)
		}
	})

	t.Run("unreadable file logs one line without the home path", func(t *testing.T) {
		resetDenyList(t)
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
		// A directory where the file should be: reading it fails with a path error.
		if err := os.MkdirAll(filepath.Join(memory.GlobalRoot(home), "scrub.txt"), 0o755); err != nil {
			t.Fatal(err)
		}
		lines := run(t, home)
		if len(lines) != 1 {
			t.Fatalf("logged %d lines %q, want exactly one", len(lines), lines)
		}
		if !strings.Contains(lines[0], "scrub.txt") || strings.Contains(lines[0], home) {
			t.Fatalf("log line %q, want the basename and no home path", lines[0])
		}
	})
}
