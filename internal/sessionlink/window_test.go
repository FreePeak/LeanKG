package sessionlink

import (
	"errors"
	"testing"
	"time"

	"github.com/FreePeak/LeanKG/internal/telemetry"
)

func tcAt(id, name, args string, ts time.Time) ToolCall {
	return MakeToolCall(id, name, []byte(args), ts)
}

// synthetic chain: prompt -> assistant(grep) -> assistant(leankg query) ->
// assistant(read, edit) -> prompt2 -> assistant(bash)
func syntheticEvents(t0 time.Time) []Event {
	return []Event{
		{Prompt: true, Turn: Turn{Role: "user", Text: "first prompt", TS: t0}},
		{Turn: Turn{Role: "assistant", Text: "searching", TS: t0.Add(1 * time.Second),
			ToolCalls: []ToolCall{tcAt("tu-grep", "Grep", `{"pattern":"auth"}`, t0.Add(1*time.Second))}}},
		{Turn: Turn{Role: "assistant", Text: "asking leankg", TS: t0.Add(2 * time.Second),
			ToolCalls: []ToolCall{
				tcAt("tu-lk", "mcp__leankg__query", `{"q":"auth handler"}`, t0.Add(2*time.Second)),
				tcAt("tu-after1", "Read", `{"file_path":"/w/auth.go"}`, t0.Add(2*time.Second)),
			}}},
		{Turn: Turn{Role: "assistant", Text: "editing", TS: t0.Add(3 * time.Second),
			ToolCalls: []ToolCall{tcAt("tu-after2", "Edit", `{"file_path":"/w/auth.go"}`, t0.Add(3*time.Second))}}},
		{Prompt: true, Turn: Turn{Role: "user", Text: "second prompt", TS: t0.Add(4 * time.Second)}},
		{Turn: Turn{Role: "assistant", Text: "later", TS: t0.Add(5 * time.Second),
			ToolCalls: []ToolCall{tcAt("tu-next", "Bash", `{"command":"ls"}`, t0.Add(5*time.Second))}}},
	}
}

func TestBuildWindowPromptBeforeAfterFollowUps(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	ev := syntheticEvents(t0)
	c := CallRef{CallID: "c1", TS: t0.Add(2 * time.Second), Tool: "query", ArgsHash: telemetry.ArgsHash([]byte(`{"q":"auth handler"}`))}

	w, err := BuildWindow("claude-code", "s1", "/t.jsonl", ev, c, 6, ConfidenceUnique)
	if err != nil {
		t.Fatalf("BuildWindow: %v", err)
	}
	if w.Prompt != "first prompt" {
		t.Fatalf("Prompt = %q, want the last real prompt before the call", w.Prompt)
	}
	if w.Matched == nil || w.Matched.ID != "tu-lk" || w.Matched.Norm != "leankg.query" || !w.Matched.IsLeanKG {
		t.Fatalf("Matched = %+v, want tu-lk leankg.query", w.Matched)
	}
	if len(w.Before) != 2 || w.Before[0].Role != "user" || w.Before[1].Text != "searching" {
		t.Fatalf("Before = %+v, want [prompt, searching]", w.Before)
	}
	if len(w.After) != 3 || w.After[0].Text != "editing" || w.After[1].Role != "user" || w.After[2].Text != "later" {
		t.Fatalf("After = %+v, want [editing, second prompt, later]", w.After)
	}
	// Follow-ups: the same-turn Read, the Edit, and nothing past the next prompt.
	var names []string
	for _, f := range w.FollowUps {
		names = append(names, f.ID)
	}
	if got, want := names, []string{"tu-after1", "tu-after2"}; !equalStrings(got, want) {
		t.Fatalf("FollowUps = %v, want %v (stop at next prompt)", got, want)
	}
	if w.FollowUps[0].Target != "/w/auth.go" || w.FollowUps[0].Norm != "read" {
		t.Fatalf("follow-up target/norm = %q/%q", w.FollowUps[0].Target, w.FollowUps[0].Norm)
	}
	if w.FollowUps[0].Args != "" || w.FollowUps[0].Result != "" {
		t.Fatalf("follow-up bodies should be slimmed: %+v", w.FollowUps[0])
	}
}

func TestBuildWindowNoMatchIsNotFound(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	c := CallRef{TS: t0, Tool: "query", ArgsHash: "deadbeef"}
	_, err := BuildWindow("claude-code", "s1", "/t", syntheticEvents(t0), c, 6, ConfidenceUnique)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (no false link)", err)
	}
}

func TestBuildWindowPicksClosestCandidate(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	args := `{"q":"same"}`
	ev := []Event{
		{Turn: Turn{Role: "assistant", ToolCalls: []ToolCall{tcAt("early", "mcp__leankg__query", args, t0)}}},
		{Turn: Turn{Role: "assistant", ToolCalls: []ToolCall{tcAt("near", "mcp__leankg__query", args, t0.Add(50*time.Second))}}},
	}
	c := CallRef{TS: t0.Add(52 * time.Second), Tool: "query", ArgsHash: telemetry.ArgsHash([]byte(args))}
	w, err := BuildWindow("claude-code", "s1", "/t", ev, c, 6, ConfidenceAmbiguous)
	if err != nil {
		t.Fatalf("BuildWindow: %v", err)
	}
	if w.Matched.ID != "near" {
		t.Fatalf("Matched = %s, want the candidate closest to the call (near)", w.Matched.ID)
	}
}

func TestBuildWindowSidechainIsIsolated(t *testing.T) {
	t0 := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	args := `{"q":"sub"}`
	ev := []Event{
		{Prompt: true, Turn: Turn{Role: "user", Text: "main prompt"}},
		{Turn: Turn{Role: "assistant", Text: "main text"}},
		{Sidechain: true, Prompt: true, Turn: Turn{Role: "user", Text: "subagent task"}},
		{Sidechain: true, Turn: Turn{Role: "assistant", Text: "sub text",
			ToolCalls: []ToolCall{tcAt("sub-lk", "mcp__leankg__query", args, t0)}}},
		{Sidechain: true, Turn: Turn{Role: "assistant", Text: "sub after",
			ToolCalls: []ToolCall{tcAt("sub-next", "Grep", `{"pattern":"x"}`, t0.Add(time.Second))}}},
	}
	c := CallRef{TS: t0, Tool: "query", ArgsHash: telemetry.ArgsHash([]byte(args))}
	w, err := BuildWindow("claude-code", "s1", "/t", ev, c, 6, ConfidenceUnique)
	if err != nil {
		t.Fatalf("BuildWindow: %v", err)
	}
	if w.Prompt != "subagent task" {
		t.Fatalf("Prompt = %q, want the subagent's task, not the main prompt", w.Prompt)
	}
	for _, turn := range append(append([]Turn{}, w.Before...), w.After...) {
		if turn.Text == "main text" || turn.Text == "main prompt" {
			t.Fatalf("main-chain turn %q leaked into a sidechain window", turn.Text)
		}
	}
	if len(w.FollowUps) != 1 || w.FollowUps[0].ID != "sub-next" {
		t.Fatalf("FollowUps = %+v, want only the subagent's next call", w.FollowUps)
	}
}

func TestTurnsOfCapsAndKeepsOrder(t *testing.T) {
	var ev []Event
	for i := 0; i < 5; i++ {
		ev = append(ev, Event{Turn: Turn{Text: string(rune('a' + i))}})
	}
	got := TurnsOf(ev, 3)
	if len(got) != 3 || got[0].Text != "a" || got[2].Text != "c" {
		t.Fatalf("TurnsOf(3) = %+v", got)
	}
	if len(TurnsOf(ev, 0)) != 5 {
		t.Fatal("maxTurns 0 must mean no cap")
	}
}

func TestClipCapsAndRedacts(t *testing.T) {
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'y'
	}
	if got := Clip(string(long), 10); len(got) > 10+len("...[truncated]") {
		t.Fatalf("Clip did not cap: %d bytes", len(got))
	}
}

func TestValidSessionID(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../x", "a/b", `a\b`, "a\x00b"} {
		if ValidSessionID(bad) {
			t.Errorf("ValidSessionID(%q) = true, want false", bad)
		}
	}
	if !ValidSessionID("3f2c0e1a-8b7d-4c1e-9a2b-111122223333") {
		t.Error("a UUID session id must be valid")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
