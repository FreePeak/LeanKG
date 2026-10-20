package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/core"
)

// TestTheAdvertisedArgsAreTheOnesTheEngineReads pins the schema against the
// engine, which is the contract an agent actually programs against.
//
// Wave 23 enumerated every advertised surface and found the mismatch here: the
// query schema advertises `args.limit` for the `pattern` action, and the engine
// reads it (the full-scan tools are the ones the budget and the cap act on) —
// but the top-level `limit` is what `search` honours, and when both are sent the
// top-level wins:
//
//	{"action":"search","query":"rank","limit":2}                      -> 2 hits
//	{"action":"search","query":"rank","limit":2,"args":{"limit":1}}   -> 2 hits
//
// An agent that reads "args carries limit" and sends both gets the other one.
// The answer is not wrong — the schema does advertise a top-level `limit`, and
// its description is one line away — but the schema presents TWO names for one
// value with no statement of which wins, and every other transport reads the
// top-level. That is a defect in the ADVERTISEMENT rather than in the engine:
// the fix is to say which field is which, not to change precedence and break
// every caller.
//
// The same sweep found the ontology sub-commands (`trace`, `status`,
// `concept_search`, `feature_flow`, `traceability`) all work and are
// advertised NOWHERE: `args.cmd` is a sentence of prose, not a list, so an
// agent cannot enumerate what it may ask for. Both halves are the same
// requirement — **an advertised contract must be discoverable and unambiguous**,
// which is the read-side twin of the write-verb rules in waves 15-17: a call
// the agent cannot form correctly is a call the product has not really
// published.
func TestTheAdvertisedArgsAreTheOnesTheEngineReads(t *testing.T) {
	schema := queryInputSchema(t)
	props := schema.map_("properties")
	args, _ := props.map_("args").string_("description")

	// 1. The top-level `limit` must be described as the one `search` honours,
	// so an agent sending both knows which wins.
	limitDesc, ok := props.map_("limit").string_("description")
	if !ok || strings.TrimSpace(limitDesc) == "" {
		t.Fatal("query.limit must carry a description — it is the field search honours")
	}
	if !strings.Contains(limitDesc, "search") {
		t.Errorf("query.limit must say it is what search honours, got %q", limitDesc)
	}

	// 2. `args.cmd` for ontology must be an ENUM, not prose: the engine accepts
	// six values and advertises none of them.
	if !strings.Contains(args, "ontology") {
		t.Errorf("the args description must cover the ontology cmd, got:\n%s", args)
	}
	for _, cmd := range []string{"matches", "trace", "status", "concept_search", "feature_flow", "traceability"} {
		if !strings.Contains(args, cmd) {
			t.Errorf("the args description must name the ontology cmd %q the engine accepts; got:\n%s", cmd, args)
		}
	}

	// 3. The advertised actions that need a mandatory sub-arg must say so where
	// the agent reads it, not only in the error it gets back. `path` without
	// args.to is the canonical case: the schema advertises `path` as an action
	// and never mentions that it is unusable without another argument.
	desc, _ := props.map_("action").string_("description")
	for _, need := range []struct{ action, arg string }{
		{"path", "args.to"},
		{"pattern", "args.pattern"},
		{"lsp", "args.lang"},
	} {
		if !strings.Contains(desc, need.arg) && !strings.Contains(args, need.arg) {
			t.Errorf("action=%s needs %s; the schema must say so where the agent reads the action list, got:\n  action: %s\n  args: %s",
				need.action, need.arg, desc, args)
		}
	}

	// 4. The knobs waves 14 and 20 added must stay discoverable in the schema,
	// or an agent cannot reach them at all.
	if !strings.Contains(desc, "scope") && !strings.Contains(args, "scope") {
		t.Errorf("args.scope is honoured by search and must be discoverable in the schema")
	}
	if !strings.Contains(args, "weight") {
		t.Errorf("args.weight is honoured by search and must be discoverable in the schema")
	}
}

// --- tiny JSON-shaped accessors so the assertions read like the schema ---

type jsonNode map[string]any

// map_ returns the object at key; with key "" it is the node's own object,
// which is how the test reaches properties/limit without repeating the cast.
func (n jsonNode) map_(key string) jsonNode {
	if key == "" {
		return n
	}
	v, _ := n[key].(map[string]any)
	return jsonNode(v)
}

func (n jsonNode) string_(key string) (string, bool) {
	v, ok := n[key].(string)
	return v, ok
}

// queryInputSchema reads the schema an AGENT reads: over the wire, from
// tools/list, off a live session. Reading the registered Go value instead
// would test the source rather than the advertisement, and the two are exactly
// what wave 23 found disagreeing.
func queryInputSchema(t *testing.T) jsonNode {
	t.Helper()
	session := newTestServer(t)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != core.ToolQuery {
			continue
		}
		raw, err := json.Marshal(tl.InputSchema)
		if err != nil {
			t.Fatalf("query schema: %v", err)
		}
		var node jsonNode
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatalf("query input schema is not JSON: %v\n%s", err, raw)
		}
		return node
	}
	t.Fatal("query tool is not advertised at all")
	return nil
}
