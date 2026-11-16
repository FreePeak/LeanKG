package core

import (
	"errors"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/errs"
	"github.com/FreePeak/LeanKG/internal/lsp"
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

// The proto wire sends "" for an unset number or boolean. That is absent, not a
// bad value: the field keeps its zero value (nil for a pointer) so the action's
// default applies. A string field still takes "" as a value.
type unsetArgsFixture struct {
	Count int    `json:"count"`
	Flag  bool   `json:"flag"`
	Depth *int   `json:"depth"`
	On    *bool  `json:"on"`
	Name  string `json:"name"`
}

func TestDecodeArgsEmptyStringIsAbsentForNumbersAndBooleans(t *testing.T) {
	got, err := decodeArgs[unsetArgsFixture](map[string]any{
		"count": "", "flag": "", "depth": "", "on": "", "name": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Count != 0 || got.Flag || got.Depth != nil || got.On != nil || got.Name != "" {
		t.Fatalf("decoded %+v, want every field at its zero value", got)
	}
}

// fresh is a boolean on the wire (JSON true) and on the proto wire ("true").
func TestDecodeArgsReadFreshIsABoolean(t *testing.T) {
	for _, raw := range []any{true, "true", false, "false"} {
		if _, err := decodeArgs[compressReadArgs](map[string]any{"fresh": raw}); err != nil {
			t.Fatalf("fresh=%v: %v", raw, err)
		}
	}
}

// limit is a whole number on pattern and lsp; the schema documents it.
func TestDecodeArgsFullScanLimit(t *testing.T) {
	if _, err := decodeArgs[patternArgs](map[string]any{"pattern": "func $F($A)", "limit": float64(5)}); err != nil {
		t.Fatalf("pattern limit: %v", err)
	}
	if _, err := decodeArgs[lspArgs](map[string]any{"lang": "go", "limit": "5"}); err != nil {
		t.Fatalf("lsp limit: %v", err)
	}
}

// args.limit caps the symbols a lsp query returns; a limit of 0 caps nothing.
func TestCapSymbolsHonoursLimit(t *testing.T) {
	syms := []lsp.Symbol{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	if got := capSymbols(syms, 2); len(got) != 2 || got[1].Name != "B" {
		t.Fatalf("limit 2: got %v", got)
	}
	if got := capSymbols(syms, 0); len(got) != 3 {
		t.Fatalf("limit 0 must cap nothing, got %d", len(got))
	}
	if got := capSymbols(syms, 9); len(got) != 3 {
		t.Fatalf("limit above the count must keep all, got %d", len(got))
	}
}
