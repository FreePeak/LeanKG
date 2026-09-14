package main

import "testing"

func TestBindPositional(t *testing.T) {
	// A bare positional selects the project (the `leankg index <dir>` convention).
	p := ""
	bindPositional("run", []string{"/abs/child"}, &p)
	if p != "/abs/child" {
		t.Fatalf("positional ignored: project=%q — the silent-cwd-store footgun", p)
	}

	// --project wins over the positional.
	p = "--set"
	bindPositional("run", []string{"/abs/child"}, &p)
	if p != "--set" {
		t.Fatalf("explicit project overwritten: %q", p)
	}

	// Flag-only args leave project untouched.
	p = ""
	bindPositional("run", []string{"--model", "m"}, &p)
	if p != "" {
		t.Fatalf("flag value consumed as positional: %q", p)
	}
}
