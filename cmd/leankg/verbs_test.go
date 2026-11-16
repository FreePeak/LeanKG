package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/FreePeak/LeanKG/internal/cliexit"
)

// Every verb the dispatcher routed before CX-29 is registered, and each handler
// has the one shape func(ctx, args) error.
func TestEveryVerbHandlerTakesContextAndReturnsError(t *testing.T) {
	names := []string{
		"serve", "index", "query", "impact", "writer", "connect", "install",
		"version", "obsidian", "setup", "status", "doctor", "run",
		"detect-clusters", "report", "gods", "ctags", "cost", "migrate", "audit",
		"metrics", "dashboard", "telemetry", "auth", "tunnels", "quality",
		"reflect", "refresh", "gc", "vacuum", "register", "unregister", "list",
		"status-repo", "export", "pack", "generate", "annotate", "prd", "prd-trace",
		"link", "delete-annotation", "search-annotations", "show-annotations",
		"mine-conversations", "incident", "note", "env-conflicts", "service-context",
		"team-map", "push", "pull", "update", "summarize", "register-project",
		"projects", "help", "-h", "--help",
	}
	want := reflect.TypeOf(verbFunc(nil))
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			fn, ok := verbs[name]
			if !ok || fn == nil {
				t.Fatalf("verb %q is not registered", name)
			}
			if got := reflect.TypeOf(fn); got != want {
				t.Fatalf("verb %q has type %s, want %s", name, got, want)
			}
		})
	}
}

// runVerb hands the verb's error to cliexit.Finish, so the status comes from one
// place: 0 success, 1 a plain error, 2 a usage or flag error, 130 a cancelled
// context, and the code a verb sets (run passes its child's status through).
func TestRunVerbMapsVerbErrorToExitStatus(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	missing := filepath.Join(t.TempDir(), "absent")

	fake := func(err error) verbFunc {
		return func(context.Context, []string) error { return err }
	}
	cases := []struct {
		name string
		ctx  context.Context
		argv []string
		want int
	}{
		{"fake success", context.Background(), []string{"fake"}, 0},
		{"fake plain error", context.Background(), []string{"fake"}, 1},
		{"fake cancelled", context.Background(), []string{"fake"}, 130},
		{"fake exit code", context.Background(), []string{"fake"}, 3},
		{"unknown verb", context.Background(), []string{"no-such-verb"}, 2},
		{"bad flag", context.Background(), []string{"status", "--no-such-flag"}, 2},
		{"index of a missing path", context.Background(), []string{"index", "--project", missing, missing}, 1},
		{"index interrupted", cancelled, []string{"index", "--no-embed", "--project", "DIR", "DIR"}, 130},
		{"index interrupted under hook", cancelled, []string{"index", "--hook", "--no-embed", "--project", "DIR", "DIR"}, 130},
	}
	fakes := map[string]verbFunc{
		"fake success":     fake(nil),
		"fake plain error": fake(errors.New("boom")),
		"fake cancelled":   fake(fmt.Errorf("stopped: %w", context.Canceled)),
		"fake exit code":   fake(cliexit.Code(3)),
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if fn, ok := fakes[tc.name]; ok {
				verbs["fake"] = fn
				t.Cleanup(func() { delete(verbs, "fake") })
			}
			dir := writeSourceTree(t, 5)
			argv := make([]string, len(tc.argv))
			for i, a := range tc.argv {
				argv[i] = a
				if a == "DIR" {
					argv[i] = dir
				}
			}
			if got := runVerb(tc.ctx, argv); got != tc.want {
				t.Fatalf("runVerb(%v) = %d, want %d", argv, got, tc.want)
			}
		})
	}
}
