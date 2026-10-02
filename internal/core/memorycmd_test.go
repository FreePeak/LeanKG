package core

import (
	"context"
	"strings"
	"testing"
)

// TestMemoryReadRoutesEveryAdvertisedCommand pins the query tool's memory
// dispatch. The MCP schema and the tool guidance tell an agent to call
// `query action=memory` with args.command = session_recall | memories, but the
// router sent every OTHER command to the memory file search and answered
// `{"command":"search","hits":null}` — including a typo like "recal".
//
// The defect class is silent: a mistyped command is answered as a confident
// empty search, so the agent believes the recall returned nothing rather than
// that the command does not exist. MemoryRead's own error already names the
// valid set; the router just never reached it.
func TestMemoryReadRoutesEveryAdvertisedCommand(t *testing.T) {
	e, _ := newEngine(t) // newEngine wires the memory layer this path needs
	ctx := context.Background()

	t.Run("session reads", func(t *testing.T) {
		for _, cmd := range []string{"session_recall", "memories"} {
			out, err := e.Query(ctx, QueryRequest{
				Action: "memory", Query: "anything", Args: map[string]any{"command": cmd},
			})
			if err != nil {
				t.Fatalf("action=memory command=%s: %v", cmd, err)
			}
			if out["command"] != cmd {
				t.Fatalf("command=%s answered as command=%v — the router bypassed SessionMemoryRead", cmd, out["command"])
			}
		}
	})

	t.Run("an unknown command is an error", func(t *testing.T) {
		_, err := e.Query(ctx, QueryRequest{
			Action: "memory", Query: "anything", Args: map[string]any{"command": "recal"},
		})
		if err == nil {
			t.Fatal(`command="recal" must not answer as a search: a typo must not read as "no memories"`)
		}
		if !strings.Contains(err.Error(), "session_recall") {
			t.Fatalf("the error must name the valid commands, got %q", err)
		}
	})

	t.Run("the file-search shape stays reachable", func(t *testing.T) {
		// A bare search carries no command at all: the documented memory
		// search over the full-markdown memory files.
		out, err := e.Query(ctx, QueryRequest{Action: "memory", Query: "anything"})
		if err != nil {
			t.Fatalf("bare memory search: %v", err)
		}
		if out["command"] != "search" {
			t.Fatalf("bare memory search must answer command=search, got %v", out["command"])
		}
	})
}
