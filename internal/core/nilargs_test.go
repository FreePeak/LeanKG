package core

// Args and Tags are optional on the wire, so a request whose maps are nil is a
// normal input. Every action must answer or refuse it; none may panic.

import (
	"context"
	"fmt"
	"testing"
)

// assertNoPanic runs call and reports a panic as a test failure, so one bad
// action does not stop the table from reaching the others.
func assertNoPanic(t *testing.T, name string, call func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", name, r)
		}
	}()
	call()
}

func TestNilArgsNeverPanic(t *testing.T) {
	ctx := context.Background()
	e := New(seededStore(t), Options{})
	dir := t.TempDir()
	tags := []string{"alpha", "beta"}

	// Path "" matters: a set Path is folded into Args by withFlatArgs, which
	// hides the nil map from the tags write.
	for action := range importActions {
		for _, path := range []string{"", dir} {
			for _, withTags := range []bool{false, true} {
				req := ImportRequest{Action: action, Path: path}
				if withTags {
					req.Tags = tags
				}
				assertNoPanic(t, fmt.Sprintf("import action=%q path=%q tags=%v", action, path, withTags), func() {
					_, _ = e.Import(ctx, req)
				})
			}
		}
	}

	queryKeys := []string{""}
	for action := range queryActions {
		queryKeys = append(queryKeys, action)
	}
	for _, action := range queryKeys {
		assertNoPanic(t, fmt.Sprintf("query action=%q", action), func() {
			_, _ = e.Query(ctx, QueryRequest{Action: action, Query: "Alpha"})
		})
	}
}
