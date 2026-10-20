package core

import (
	"context"
	"strings"
	"testing"
)

// TestSessionReadNamesItsCommands pins the read-side posture of the session
// verbs against the memory verbs, which wave 1 already fixed.
//
// The defect: `query action=session` with no args.command failed the tool call
// with `unknown session read command "" (valid: recall, canvas)`. That is the
// right error for a MISSPELLED command, but a session read carries no text
// query — the command IS the request — so the no-command case is an agent that
// wants "what do I have for this session", not an agent that made a mistake.
//
// Concretely: `action=memory` with no command now answers the file search, and
// a bad memory command errors naming the valid set. `action=session` with no
// command errors, which means the one verb whose whole argument IS a command
// is the one that cannot be called to discover what it supports.
func TestSessionReadNamesItsCommands(t *testing.T) {
	e, _ := newEngine(t)
	dir := t.TempDir()
	e.SetProjectDir(dir)
	ctx := context.Background()

	// No command: an answer listing what the verb supports, not a failed call.
	out, err := e.Query(ctx, QueryRequest{Action: "session"})
	if err != nil {
		t.Fatalf("session read with no command must answer, not error: %v", err)
	}
	cmds, _ := out["commands"].([]string)
	if len(cmds) == 0 {
		t.Fatalf("the answer must name the valid commands: %+v", out)
	}
	joined := strings.Join(cmds, ",")
	for _, want := range []string{"recall", "canvas"} {
		if !strings.Contains(joined, want) {
			t.Errorf("commands %v must include %q", cmds, want)
		}
	}

	// A misspelled command still errors, naming the valid set.
	_, err = e.Query(ctx, QueryRequest{
		Action: "session", Query: "s1", Args: map[string]any{"command": "recalll"},
	})
	if err == nil {
		t.Fatal(`command="recalll" must not silently answer: a typo must not read as "no session state"`)
	}
	if !strings.Contains(err.Error(), "recall") {
		t.Fatalf("the error must name the valid commands, got %q", err)
	}
}
