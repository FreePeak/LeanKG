package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
)

type argsFixture struct {
	envelopeArgs
	Depth *int     `json:"depth"`
	Name  string   `json:"name"`
	Flag  bool     `json:"flag"`
	Tags  []string `json:"tags"`
}

func requireInvalidParam(t *testing.T, err error, mention string) {
	t.Helper()
	var ce *errs.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a catalog error", err)
	}
	if ce.Entry.Code != errs.InvalidParam.Code {
		t.Fatalf("code = %s, want %s", ce.Entry.Code, errs.InvalidParam.Code)
	}
	if mention != "" && !strings.Contains(ce.Cause, mention) {
		t.Fatalf("cause %q does not mention %q", ce.Cause, mention)
	}
}

func TestDecodeArgsRejectsUnknownKey(t *testing.T) {
	_, err := decodeArgs[argsFixture](map[string]any{"nope": "x"})
	requireInvalidParam(t, err, `unknown args key "nope"`)
}

func TestDecodeArgsRejectsWrongTypes(t *testing.T) {
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"name": float64(3)}, "args.name must be a string"},
		{map[string]any{"depth": 2.5}, "args.depth must be a whole number"},
		{map[string]any{"depth": "banana"}, "args.depth must be a whole number"},
		{map[string]any{"flag": float64(1)}, "args.flag must be a boolean"},
		{map[string]any{"tags": []any{"a", float64(1)}}, "args.tags must be a list of strings"},
	}
	for _, c := range cases {
		_, err := decodeArgs[argsFixture](c.args)
		requireInvalidParam(t, err, c.want)
	}
}

func TestDecodeArgsAcceptsValidInput(t *testing.T) {
	got, err := decodeArgs[argsFixture](map[string]any{
		"scope": "code", "include_git": true, "depth": float64(3), "name": "alpha",
		"flag": true, "tags": []any{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "code" || !got.IncludeGit || got.Name != "alpha" || !got.Flag {
		t.Fatalf("decoded %+v", got)
	}
	if got.Depth == nil || *got.Depth != 3 {
		t.Fatalf("depth = %v, want 3", got.Depth)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "a" || got.Tags[1] != "b" {
		t.Fatalf("tags = %v", got.Tags)
	}
}

// The proto wire carries every arg as a string, so an int or bool field must
// keep accepting its string spelling, and an absent or nil key stays absent.
func TestDecodeArgsAcceptsStringSpellings(t *testing.T) {
	got, err := decodeArgs[argsFixture](map[string]any{
		"depth": "4", "flag": "true", "tags": "x, y", "name": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Depth == nil || *got.Depth != 4 || !got.Flag {
		t.Fatalf("decoded %+v", got)
	}
	if strings.Join(got.Tags, ",") != "x,y" {
		t.Fatalf("tags = %v, want x,y", got.Tags)
	}
	if got.Name != "" {
		t.Fatalf("nil name must stay absent, got %q", got.Name)
	}
}

func TestDecodeArgsAbsentPointerStaysNil(t *testing.T) {
	got, err := decodeArgs[argsFixture](nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Depth != nil {
		t.Fatalf("absent depth must stay nil, got %d", *got.Depth)
	}
}

func TestDecodeKnownArgsIgnoresKeysItDoesNotName(t *testing.T) {
	got, err := decodeKnownArgs[envelopeArgs](map[string]any{"scope": "prod", "depth": 9})
	if err != nil {
		t.Fatal(err)
	}
	if got.Scope != "prod" {
		t.Fatalf("scope = %q", got.Scope)
	}
	_, err = decodeKnownArgs[envelopeArgs](map[string]any{"scope": float64(1)})
	requireInvalidParam(t, err, "args.scope must be a string")
}

func TestDecodeArgsNoArgsActionRefusesAnyKey(t *testing.T) {
	if _, err := decodeArgs[noArgs](map[string]any{}); err != nil {
		t.Fatalf("empty args must pass: %v", err)
	}
	_, err := decodeArgs[noArgs](map[string]any{"path": "x"})
	requireInvalidParam(t, err, `unknown args key "path" (this action takes no args)`)
}
