package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// hookVerbFixture seeds the memory of two repositories under a temp HOME and
// returns the home directory and the caller repository B, which has its own
// feedback. Repository A holds a feedback and a project memory.
func hookVerbFixture(t *testing.T) (home, callerB string) {
	t.Helper()
	central, projects := memorySyncEnv(t)
	home = filepath.Dir(filepath.Dir(filepath.Dir(central))) // central is <home>/.leankg/memory
	callerB = filepath.Join(home, "work", "repo-b")
	slugA := claudecode.Slug(filepath.Join(home, "work", "repo-a"))
	writeMemoryDoc(t, projects, claudecode.Slug(callerB), "b-feedback.md", "feedback", "Repository B own feedback.")
	writeMemoryDoc(t, projects, slugA, "a-feedback.md", "feedback", "Use tabs in deploy scripts.")
	writeMemoryDoc(t, projects, slugA, "a-project.md", "project", "Freeze on Friday.")
	return home, callerB
}

// setHookStdin replaces os.Stdin with a pipe holding content. keepOpen leaves
// the write end open, so a reader waits for input that never comes. The content
// is written by a goroutine, so content larger than the pipe buffer does not block.
func setHookStdin(t *testing.T, content string, keepOpen bool) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, _ = io.WriteString(w, content)
		if !keepOpen {
			_ = w.Close()
		}
	}()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		_ = w.Close()
		_ = r.Close()
	})
}

// runMemoryHook runs `leankg memory hook` in-process and returns its exit
// status, stdout and stderr.
func runMemoryHook(t *testing.T) (code int, stdout, stderr string) {
	t.Helper()
	stderr = captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			code = runVerb(context.Background(), []string{"memory", "hook"})
		})
	})
	return code, stdout, stderr
}

// assertHookBlockFor checks the block for repository B: the context line first,
// A's global memory present, B's own and A's project memory absent, and the
// hint line naming callerB last.
func assertHookBlockFor(t *testing.T, stdout, callerB string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if !strings.HasPrefix(lines[0], "LeanKG cross-repository memory: ") {
		t.Errorf("line 1 = %q, want the context line", lines[0])
	}
	if want := `args.cwd "` + callerB + `".`; !strings.HasSuffix(lines[len(lines)-1], want) {
		t.Errorf("last line = %q, want the hint naming %s", lines[len(lines)-1], callerB)
	}
	if !strings.Contains(stdout, "a-feedback") {
		t.Errorf("stdout lacks repository A's feedback:\n%s", stdout)
	}
	for _, leak := range []string{"b-feedback", "a-project"} {
		if strings.Contains(stdout, leak) {
			t.Errorf("stdout carries %q, which must not be injected:\n%s", leak, stdout)
		}
	}
}

func TestMemoryHookIsRegisteredAndListedInUsage(t *testing.T) {
	if _, ok := memorySubcommands["hook"]; !ok {
		t.Fatal(`memorySubcommands has no "hook" entry`)
	}
	if usage := memoryUsageText("  "); !strings.Contains(usage, "leankg memory hook") {
		t.Errorf("memory usage does not list the hook verb:\n%s", usage)
	}
}

func TestMemoryHookWritesBlockForCallerFromStdinJSON(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	setHookStdin(t, `{"session_id":"s-1","cwd":"`+callerB+`"}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and empty", code, stderr)
	}
	assertHookBlockFor(t, stdout, callerB)
}

// TestMemoryHookUnknownEventRunsAsSessionStart: an event the hook does not handle
// gets the SessionStart block, as an empty stdin does.
func TestMemoryHookUnknownEventRunsAsSessionStart(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	setHookStdin(t, `{"hook_event_name":"Notification","session_id":"s-1","cwd":"`+callerB+`"}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and empty", code, stderr)
	}
	assertHookBlockFor(t, stdout, callerB)
}

// TestMemoryHookPostToolUseTakesFilePathFromStdin: a PostToolUse event for a
// memory file syncs that project and prints nothing. The same stdin as
// SessionStart would print the block.
func TestMemoryHookPostToolUseTakesFilePathFromStdin(t *testing.T) {
	home, callerB := hookVerbFixture(t)
	file := filepath.Join(claudecode.ProjectsRoot(home), claudecode.Slug(filepath.Join(home, "work", "repo-a")), "memory", "a-feedback.md")
	setHookStdin(t, `{"hook_event_name":"PostToolUse","session_id":"s-1","cwd":"`+callerB+`","tool_name":"Edit","tool_input":{"file_path":"`+file+`"}}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0 and both empty", code, stdout, stderr)
	}
}

// assertHookDoesNothing runs the hook on the stdin already set and asserts it
// exits 0 with no stdout and no stderr: no sync, no injected block.
func assertHookDoesNothing(t *testing.T) {
	t.Helper()
	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0 and both empty", code, stdout, stderr)
	}
}

// TestMemoryHookInvalidJSONDoesNothing: stdin that is not valid JSON is not a
// SessionStart event, so the hook neither syncs nor injects, even with
// CLAUDE_PROJECT_DIR set.
func TestMemoryHookInvalidJSONDoesNothing(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	t.Setenv("CLAUDE_PROJECT_DIR", callerB)
	setHookStdin(t, "not json", false)
	assertHookDoesNothing(t)
}

// TestMemoryHookTruncatedStdinDoesNothing: a PostToolUse event whose JSON is cut
// off does not fall through to SessionStart.
func TestMemoryHookTruncatedStdinDoesNothing(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	setHookStdin(t, `{"hook_event_name":"PostToolUse","tool_input":{"file_path":"`+filepath.Join(callerB, "big.go"), false)
	assertHookDoesNothing(t)
}

// TestMemoryHookOverCapStdinDoesNothing: a PostToolUse event for a large
// non-memory file, whose stdin is longer than the 1 MiB cap, does not run a sync
// or print a block.
func TestMemoryHookOverCapStdinDoesNothing(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	big := `{"hook_event_name":"PostToolUse","session_id":"s-1","cwd":"` + callerB +
		`","tool_name":"Write","tool_input":{"file_path":"` + filepath.Join(callerB, "big.go") +
		`"},"tool_response":"` + strings.Repeat("x", hookStdinLimit) + `"}`
	setHookStdin(t, big, false)
	assertHookDoesNothing(t)
}

// TestMemoryHookStdinWithoutEOFDoesNothing: stdin that holds no data and never
// closes is read to the bounded wait, and then the hook does nothing: it cannot
// tell an empty input from a late one.
func TestMemoryHookStdinWithoutEOFDoesNothing(t *testing.T) {
	hookVerbFixture(t)
	setHookStdin(t, "", true)
	assertHookDoesNothing(t)
}

func TestMemoryHookFallsBackToWorkingDirectoryOnEmptyStdin(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	if err := os.MkdirAll(callerB, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(callerB)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if claudecode.Slug(wd) != claudecode.Slug(callerB) {
		t.Fatalf("working directory %s does not map to the fixture slug of %s", wd, callerB)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	setHookStdin(t, "", false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and empty", code, stderr)
	}
	assertHookBlockFor(t, stdout, wd)
}

func TestMemoryHookExitsZeroAndPrintsNothingWhenNothingToInject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	setHookStdin(t, `{"cwd":"`+filepath.Join(home, "work", "repo-b")+`"}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stdout != "" || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q; want 0 and both empty", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(home, ".leankg")); !os.IsNotExist(err) {
		t.Errorf("central tree was created (stat err = %v)", err)
	}
}

func TestMemoryHookUnreadableProjectsRootExitsZeroWithOneStderrLine(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission checks do not apply to root")
	}
	home, callerB := hookVerbFixture(t)
	setHookStdin(t, `{"cwd":"`+callerB+`"}`, false)
	if code, _, _ := runMemoryHook(t); code != 0 {
		t.Fatalf("first run exit %d, want 0", code)
	}

	projects := claudecode.ProjectsRoot(home)
	if err := os.Chmod(projects, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(projects, 0o755) })
	setHookStdin(t, `{"cwd":"`+callerB+`"}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	assertHookBlockFor(t, stdout, callerB)
	if n := strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") + 1; stderr == "" || n != 1 {
		t.Errorf("stderr has %d lines, want exactly one: %q", n, stderr)
	}
}

func TestMemoryHookDoesNotWaitForOpenStdin(t *testing.T) {
	hookVerbFixture(t)
	setHookStdin(t, "", true)

	start := time.Now()
	code, _, _ := runMemoryHook(t)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("hook took %v with stdin left open, want the bounded wait to end it", elapsed)
	}
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
}

// enableTelemetryForTest grants consent under a fresh LEANKG_HOME, so the ledger
// lands in a scratch directory, and returns that directory.
func enableTelemetryForTest(t *testing.T) string {
	t.Helper()
	lk := filepath.Join(t.TempDir(), "lk")
	t.Setenv("LEANKG_HOME", lk)
	t.Setenv("LEANKG_TELEMETRY", "")
	cfg := telemetry.Config{
		Capture: telemetry.Metadata,
		Consent: telemetry.Consent{Version: telemetry.ConsentVersion, GrantedAt: time.Now(), GrantedBy: "cli"},
	}
	if err := telemetry.SaveConfig(lk, cfg); err != nil {
		t.Fatal(err)
	}
	resetTelemetryProcess(t)
	return lk
}

// resetTelemetryProcess empties the process recorder and the memory sink now and
// again when the test ends. The recorder is a process singleton, so without this
// a test would reuse the closed ledger of an earlier test.
func resetTelemetryProcess(t *testing.T) {
	t.Helper()
	reset := func() {
		telemetryOnce = sync.Once{}
		telemetryBuildOnce = sync.Once{}
		telemetrySinks.install(telemetry.Nop{})
		memory.SetRecorder(nil)
	}
	reset()
	t.Cleanup(reset)
}

// ledgerMemoryEvents reads every memory event in the ledger under lk.
func ledgerMemoryEvents(t *testing.T, lk string) []telemetry.MemoryEvent {
	t.Helper()
	ctx := context.Background()
	st, err := telemetry.OpenStore(ctx, lk, true)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer st.Close()
	evs, err := st.MemoryEvents(ctx, telemetry.CallFilter{})
	if err != nil {
		t.Fatalf("read memory events: %v", err)
	}
	return evs
}

// ledgerEventsByVerb keeps the events whose verb is verb.
func ledgerEventsByVerb(evs []telemetry.MemoryEvent, verb string) []telemetry.MemoryEvent {
	var out []telemetry.MemoryEvent
	for _, e := range evs {
		if e.Verb == verb {
			out = append(out, e)
		}
	}
	return out
}

// ledgerBytes is every byte written under the ledger directory lk, including the
// sqlite journal files, so a body string written anywhere in it is found.
func ledgerBytes(t *testing.T, lk string) string {
	t.Helper()
	entries, err := os.ReadDir(lk)
	if err != nil {
		t.Fatalf("read ledger dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(lk, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}

const (
	hookCAM18Body    = "PLANTED-BODY-CAM18 must never reach the ledger"
	hookCAM18Session = "sess-cam18"
)

func TestMemoryHookTelemetryRecordsRetainPerBankAndInjectWithSessionID(t *testing.T) {
	lk := enableTelemetryForTest(t)
	home, callerB := hookVerbFixture(t)
	slugA := claudecode.Slug(filepath.Join(home, "work", "repo-a"))
	writeMemoryDoc(t, claudecode.ProjectsRoot(home), slugA, "planted.md", "feedback", hookCAM18Body)
	setHookStdin(t, `{"session_id":"`+hookCAM18Session+`","cwd":"`+callerB+`"}`, false)

	if code, _, stderr := runMemoryHook(t); code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and empty", code, stderr)
	}
	evs := ledgerMemoryEvents(t, lk)

	// One retain per changed bank: the global bank (two feedback notes from A and
	// B, plus the planted note) and repository A's project bank.
	retains := ledgerEventsByVerb(evs, "retain")
	wantWritten := map[string]int{ccmemory.GlobalBank: 3, ccmemory.ProjectBank(slugA): 1}
	if len(retains) != len(wantWritten) {
		t.Fatalf("retain events = %d, want one per changed bank (%d): %+v", len(retains), len(wantWritten), retains)
	}
	for _, e := range retains {
		want, ok := wantWritten[e.Banks]
		if !ok {
			t.Errorf("retain names bank %q, which the fixture did not change", e.Banks)
			continue
		}
		if e.Written != want {
			t.Errorf("retain for %s wrote %d rows, want %d", e.Banks, e.Written, want)
		}
	}

	// One inject naming the global bank.
	injects := ledgerEventsByVerb(evs, "inject")
	if len(injects) != 1 || injects[0].Banks != ccmemory.GlobalBank {
		t.Fatalf("inject events = %+v, want one naming %s", injects, ccmemory.GlobalBank)
	}

	// Every hook event carries the session id from stdin.
	for _, e := range append(retains, injects...) {
		if e.ClientSessionID != hookCAM18Session {
			t.Errorf("%s event session id = %q, want %q", e.Verb, e.ClientSessionID, hookCAM18Session)
		}
	}

	// The ledger holds ids, counts and hashes, never the memory text.
	if got := ledgerBytes(t, lk); strings.Contains(got, "PLANTED-BODY-CAM18") {
		t.Error("ledger bytes contain the planted memory body")
	}
}

func TestMemoryHookTelemetrySecondUnchangedRunRecordsInjectOnly(t *testing.T) {
	lk := enableTelemetryForTest(t)
	_, callerB := hookVerbFixture(t)
	stdin := `{"session_id":"` + hookCAM18Session + `","cwd":"` + callerB + `"}`

	setHookStdin(t, stdin, false)
	runMemoryHook(t)
	first := ledgerMemoryEvents(t, lk)

	// Each hook run is its own process; reset the in-process recorder so the
	// second run builds a fresh one instead of reusing the first run's closed ledger.
	resetTelemetryProcess(t)
	setHookStdin(t, stdin, false)
	runMemoryHook(t)
	second := ledgerMemoryEvents(t, lk)

	if n := len(ledgerEventsByVerb(second, "retain")) - len(ledgerEventsByVerb(first, "retain")); n != 0 {
		t.Errorf("unchanged second run recorded %d new retain events, want 0", n)
	}
	if n := len(ledgerEventsByVerb(second, "inject")) - len(ledgerEventsByVerb(first, "inject")); n != 1 {
		t.Errorf("second run recorded %d new inject events, want 1", n)
	}
}

func TestMemoryHookTelemetryConsentOffOpensAndWritesNothing(t *testing.T) {
	resetTelemetryProcess(t)
	lk := filepath.Join(t.TempDir(), "lk")
	t.Setenv("LEANKG_HOME", lk)
	t.Setenv("LEANKG_TELEMETRY", "")
	_, callerB := hookVerbFixture(t)
	setHookStdin(t, `{"session_id":"`+hookCAM18Session+`","cwd":"`+callerB+`"}`, false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 0 and empty", code, stderr)
	}
	assertHookBlockFor(t, stdout, callerB)
	if _, err := os.Stat(lk); !os.IsNotExist(err) {
		t.Errorf("consent off created the telemetry directory %s (stat err = %v)", lk, err)
	}
}

// hookFlushSink is the process telemetry sink for the deadline tests. Its first
// memory event sleeps work, which is how the hook's own work spends its budget,
// because emitMemory calls RecordMemory on the work path. Its Close is the
// blockingRecorder one: it never returns until block closes, so the flush always
// runs out whatever budget it is given.
type hookFlushSink struct {
	blockingRecorder
	work time.Duration
	once sync.Once
}

func (s *hookFlushSink) Level() telemetry.Level { return telemetry.Metadata }

func (s *hookFlushSink) RecordMemory(telemetry.MemoryEvent) {
	s.once.Do(func() { time.Sleep(s.work) })
}

// installBlockedHookSink grants consent and makes the hook's recorder a
// hookFlushSink that spends work on its first memory event.
func installBlockedHookSink(t *testing.T, work time.Duration) {
	t.Helper()
	enableTelemetryForTest(t)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	telemetryOnce.Do(func() {}) // the lazy build must not replace the sink below
	telemetrySinks.install(&hookFlushSink{blockingRecorder: blockingRecorder{block: block}, work: work})
}

// TestMemoryHookFastWorkWithBlockedFlushEndsAtDeadline pins the shared budget
// when the work is quick: a flush that never returns is cut at the hook
// deadline, so the run ends within 3.2 s rather than after a further close budget.
func TestMemoryHookFastWorkWithBlockedFlushEndsAtDeadline(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	installBlockedHookSink(t, 0)
	setHookStdin(t, `{"session_id":"`+hookCAM18Session+`","cwd":"`+callerB+`"}`, false)

	start := time.Now()
	code, _, _ := runMemoryHook(t)
	elapsed := time.Since(start)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	if limit := ccmemory.HookDeadline + 500*time.Millisecond; elapsed > limit {
		t.Errorf("hook took %v with a blocked flush, want at most %v (the hook deadline plus slack)", elapsed, limit)
	}
}

// TestMemoryHookWorkUsingFullBudgetAddsOnlyTheFlushFloor pins the shared budget
// when the work uses all of it: the flush then gets only the floor (100 ms), so
// the run ends within the deadline plus the floor, not the deadline plus a full
// close budget.
func TestMemoryHookWorkUsingFullBudgetAddsOnlyTheFlushFloor(t *testing.T) {
	_, callerB := hookVerbFixture(t)
	installBlockedHookSink(t, ccmemory.HookDeadline)
	setHookStdin(t, `{"session_id":"`+hookCAM18Session+`","cwd":"`+callerB+`"}`, false)

	start := time.Now()
	code, _, _ := runMemoryHook(t)
	elapsed := time.Since(start)
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	// Deadline, plus the 100 ms floor, plus 400 ms of scheduling slack.
	if limit := ccmemory.HookDeadline + 500*time.Millisecond; elapsed > limit {
		t.Errorf("hook took %v when the work used the full budget, want at most %v", elapsed, limit)
	}
}

// TestMemoryHookPanicPrintsOneLineAndExitsZero: a panic in the hook run is one
// stderr line and a nil return, so the process exits 0.
func TestMemoryHookPanicPrintsOneLineAndExitsZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	setHookStdin(t, `{"cwd":"`+filepath.Join(home, "work", "repo-b")+`"}`, false)
	old := memoryHookRun
	memoryHookRun = func(context.Context, ccmemory.HookOptions) { panic("boom") }
	t.Cleanup(func() { memoryHookRun = old })

	var err error
	stderr := captureStderr(t, func() { err = cmdMemoryHook(context.Background(), nil) })
	if err != nil {
		t.Errorf("cmdMemoryHook = %v, want nil (exit 0)", err)
	}
	if n := strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") + 1; stderr == "" || n != 1 {
		t.Errorf("stderr has %d lines, want exactly one: %q", n, stderr)
	}
}

// TestMemoryHookWithoutHomeExitsZeroWithOneStderrLine: with HOME and
// CLAUDE_PROJECT_DIR unset the hook exits 0, writes nothing to stdout and
// writes one stderr line.
func TestMemoryHookWithoutHomeExitsZeroWithOneStderrLine(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	setHookStdin(t, "", false)

	code, stdout, stderr := runMemoryHook(t)
	if code != 0 || stdout != "" {
		t.Errorf("exit %d, stdout %q; want 0 and empty", code, stdout)
	}
	if n := strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") + 1; stderr == "" || n != 1 {
		t.Errorf("stderr has %d lines, want exactly one: %q", n, stderr)
	}
}
