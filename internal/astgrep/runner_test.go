package astgrep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeScript stands in for the ast-grep CLI. Each test picks its behaviour
// through FAKE_RUN (for `run`) or FAKE_VERSION (for `--version`), so every test
// execs this one file.
const fakeScript = "#!/bin/sh\ncase \"$1\" in\n" +
	"  --version) eval \"${FAKE_VERSION:-echo 'astgrep 0.0.0-fake'}\" ;;\n" +
	"  run) eval \"$FAKE_RUN\" ;;\n" +
	"  *) exit 2 ;;\nesac\n"

// sharedFake is the one executable every test in this package runs.
var sharedFake string

// TestMain writes sharedFake and execs it once before any test starts.
//
// Why: macOS verifies a newly written executable on its first exec, and under
// contention that verification queues behind other processes spawning binaries.
// Measured on this machine: a cold first exec took 0.2s idle and 5s to 8s with
// 16 to 24 concurrent fresh-binary spawners, while a re-exec of the same file
// stayed near 5ms. The old per-test fake paid that cold cost inside each test's
// 10s deadline, which is what flaked in the full-suite run (internal/astgrep
// tests reported "context deadline exceeded" at 10s). The warm-up below runs
// with no deadline, so the cost is paid once, outside every test budget.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "astgrep-fake-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "astgrep test setup:", err)
		os.Exit(1)
	}
	sharedFake = filepath.Join(dir, "ast-grep")
	if err := os.WriteFile(sharedFake, []byte(fakeScript), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "astgrep test setup:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := exec.Command(sharedFake, "--version").Run(); err != nil {
		fmt.Fprintln(os.Stderr, "astgrep test setup: warm-up exec failed:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeBin points the shared fake's `run` at runBody and returns its path.
func fakeBin(t *testing.T, runBody string) string {
	t.Helper()
	t.Setenv("FAKE_RUN", runBody)
	return sharedFake
}

// TestFakeBinReusesOneExecutable pins the cause of the load flake: each test
// must exec the same warmed file. A fresh file per test pays the OS's first-exec
// verification inside that test's 10s deadline (see TestMain).
func TestFakeBinReusesOneExecutable(t *testing.T) {
	first := fakeBin(t, "exit 0")
	second := fakeBin(t, "exit 1")
	if first != second {
		t.Errorf("fakeBin wrote a fresh executable per call (%q, %q): each one cold-starts inside its own deadline", first, second)
	}
}

// canned writes CANNED_JSON as the `run --json` payload.
func canned(t *testing.T, json string) string {
	t.Helper()
	t.Setenv("CANNED_JSON", json)
	return fakeBin(t, `printf '%s\n' "$CANNED_JSON"`)
}

// realShape is ast-grep 0.45.2's actual --json payload (0-based line AND
// column, byteOffset and metaVariables alongside), trimmed to the fields used.
const realShape = `[{"text":"func main()","range":{"byteOffset":{"start":4,"end":15},"start":{"line":4,"column":2},"end":{"line":4,"column":13}},"file":"a.go","lines":"func main() {}","language":"Go"}]`

func TestNewProbesPath(t *testing.T) {
	dir := t.TempDir()
	body := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "ast-grep"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if want := filepath.Join(dir, "ast-grep"); r.Bin != want {
		t.Errorf("Bin = %q, want %q", r.Bin, want)
	}

	os.Remove(filepath.Join(dir, "ast-grep"))
	if _, err := New(); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("New with empty PATH = %v, want ErrNotInstalled", err)
	}
}

// TestNewIgnoresSgAlias pins the Linux name collision fix: a bare `sg` on PATH
// is util-linux's set-group command there (upstream also deprecated the alias),
// so resolution must consider ONLY `ast-grep` — and must not execute the
// impostor to find out. Before the fix, New accepted it, pattern queries shelled
// out to it, got no JSON, and hard-failed instead of degrading to L2, while
// status advertised an ast-grep tier that could not answer.
func TestNewIgnoresSgAlias(t *testing.T) {
	dir := t.TempDir()
	executed := filepath.Join(dir, "executed")
	sg := "#!/bin/sh\ntouch \"" + executed + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sg"), []byte(sg), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if _, err := New(); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("New with only sg on PATH = %v, want ErrNotInstalled", err)
	}
	if _, err := os.Stat(executed); !os.IsNotExist(err) {
		t.Errorf("impostor sg was executed during resolution (marker %s exists)", executed)
	}
}

func TestRunPatternMapsRealShape(t *testing.T) {
	r := &Runner{Bin: canned(t, realShape)}

	matches, err := r.RunPattern(context.Background(), "go", "$A(...)", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("RunPattern: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	got := matches[0]
	if got.File != "a.go" || got.Line != 5 || got.Col != 3 || got.Text != "func main()" {
		t.Errorf("match = %+v, want {a.go 5 3 func main()} (0-based both axes → 1-based)", got)
	}
}

// TestRunPatternNoMatches pins the real CLI's zero-hit behavior: `run --json`
// prints [] and exits 1, which must not surface as an error.
func TestRunPatternNoMatches(t *testing.T) {
	t.Setenv("CANNED_JSON", "[]")
	r := &Runner{Bin: fakeBin(t, `printf '%s\n' "$CANNED_JSON"; exit 1`)}

	matches, err := r.RunPattern(context.Background(), "go", "$A", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("RunPattern with zero hits: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("matches = %+v, want none", matches)
	}
}

// A genuine CLI failure (bad pattern, exit != 1) still reports an error.
func TestRunPatternBinaryError(t *testing.T) {
	r := &Runner{Bin: fakeBin(t, `echo 'Error: cannot parse pattern' >&2; exit 2`)}
	if _, err := r.RunPattern(context.Background(), "go", "$A(", t.TempDir(), 0); err == nil {
		t.Fatal("RunPattern with failing binary = nil error, want failure")
	} else if !strings.Contains(err.Error(), "cannot parse pattern") {
		t.Errorf("error = %v, want it to carry the CLI's message", err)
	}
}

// The CLI may chatter on stderr while succeeding; that must not corrupt the
// JSON payload read from stdout.
func TestRunPatternIgnoresStderrNoise(t *testing.T) {
	t.Setenv("CANNED_JSON", realShape)
	r := &Runner{Bin: fakeBin(t,
		`echo 'Warning: skipped 3 ignored dirs' >&2; printf '%s\n' "$CANNED_JSON"`)}

	matches, err := r.RunPattern(context.Background(), "go", "$A", t.TempDir(), 0)
	if err != nil {
		t.Fatalf("RunPattern: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
}

func TestRunPatternToleratesShapeDrift(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Match
	}{
		{
			name: "byteOffset/position with capture",
			json: `[{"path":"b.py","ranges":[{"byteOffset":{"start":10,"end":20},"position":{"rowStart":3,"colStart":7,"rowEnd":3,"colEnd":17}}],"capture":"widget"}]`,
			want: Match{File: "b.py", Line: 4, Col: 8, Text: "widget"},
		},
		{
			name: "nested matches array",
			json: `[{"file":"c.rs","matches":[{"text":"m1","range":{"start":{"line":0,"column":1},"end":{"line":0,"column":3}}},{"text":"m2","range":{"start":{"line":7,"column":4},"end":{"line":7,"column":9}}}]}]`,
			want: Match{File: "c.rs", Line: 1, Col: 2, Text: "m1"},
		},
		{
			name: "offsets only still yields file and text",
			json: `[{"file":"d.ts","ranges":[{"byteOffset":{"start":3,"end":9}}],"text":"const"}]`,
			want: Match{File: "d.ts", Text: "const"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{Bin: canned(t, tc.json)}
			matches, err := r.RunPattern(context.Background(), "rs", "$X", t.TempDir(), 0)
			if err != nil {
				t.Fatalf("RunPattern: %v", err)
			}
			if len(matches) == 0 {
				t.Fatal("no matches")
			}
			if matches[0] != tc.want {
				t.Errorf("match = %+v, want %+v", matches[0], tc.want)
			}
		})
	}
}

func TestRunPatternMaxTruncates(t *testing.T) {
	const two = `[{"file":"a.go","range":{"start":{"line":4,"column":2},"end":{"line":4,"column":12}},"text":"one"},` +
		`{"file":"b.go","range":{"start":{"line":9,"column":1},"end":{"line":9,"column":5}},"text":"two"}]`
	r := &Runner{Bin: canned(t, two)}

	matches, err := r.RunPattern(context.Background(), "go", "$A", t.TempDir(), 1)
	if err != nil {
		t.Fatalf("RunPattern: %v", err)
	}
	if len(matches) != 1 || matches[0].File != "a.go" {
		t.Fatalf("matches = %+v, want just the first hit (max=1)", matches)
	}
}

func TestRunPatternPassesArgvVerbatim(t *testing.T) {
	// The fake dumps its own argv, one element per line: any other shape means
	// the pattern went through a shell instead of exec.
	argsFile := filepath.Join(t.TempDir(), "argv")
	t.Setenv("ARGS_OUT", argsFile)
	bin := fakeBin(t,
		`printf '%s\n' "$@" > "$ARGS_OUT"; `+
			`printf '[{"file":"f.go","range":{"start":{"line":0,"column":1},"end":{"line":0,"column":2}},"text":"hit"}]'`)
	r := &Runner{Bin: bin}

	const hostile = `a; touch $(pwned).out # "quote" 'sq'`
	root := t.TempDir()
	matches, err := r.RunPattern(context.Background(), "go", hostile, root, 0)
	if err != nil {
		t.Fatalf("RunPattern: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read argv dump: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	want := []string{"run", "--pattern", hostile, "--lang", "go", root, "--json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %q,\nwant     %q", got, want)
	}
}

func TestRunPatternTimeout(t *testing.T) {
	// `sleep 5 &` leaves a grandchild holding the stdout pipe: only WaitDelay
	// keeps the call inside its bound.
	r := &Runner{Bin: fakeBin(t, "sleep 5 & sleep 5")}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := r.RunPattern(ctx, "go", "$A", t.TempDir(), 0)
	if !errors.Is(err, ErrTimeout) {
		t.Errorf("RunPattern = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("RunPattern returned after %v, want promptly after cancel", d)
	}
}

func TestVersionTimeout(t *testing.T) {
	// The shared fake stalls on --version.
	t.Setenv("FAKE_VERSION", "sleep 5")
	r := &Runner{Bin: sharedFake}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := r.Version(ctx); !errors.Is(err, ErrTimeout) {
		t.Errorf("Version = %v, want ErrTimeout", err)
	}
}

func TestRunPatternBadOutput(t *testing.T) {
	r := &Runner{Bin: canned(t, "not json at all")}
	if _, err := r.RunPattern(context.Background(), "go", "$A", t.TempDir(), 0); err == nil {
		t.Fatal("RunPattern with garbage output = nil error, want parse failure")
	}
}

func TestRunPatternEmptyBin(t *testing.T) {
	r := &Runner{}
	if _, err := r.RunPattern(context.Background(), "go", "$A", ".", 0); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("RunPattern = %v, want ErrNotInstalled", err)
	}
	if _, err := r.Version(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("Version = %v, want ErrNotInstalled", err)
	}
}

func TestVersion(t *testing.T) {
	r := &Runner{Bin: fakeBin(t, "exit 0")}
	got, err := r.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "astgrep 0.0.0-fake" {
		t.Errorf("Version = %q, want %q", got, "astgrep 0.0.0-fake")
	}
}
