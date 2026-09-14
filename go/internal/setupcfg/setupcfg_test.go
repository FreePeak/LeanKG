package setupcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mode(m SetupMode) *SetupMode { return &m }

func TestRoundTripAutoAndManual(t *testing.T) {
	dir := t.TempDir()
	for _, m := range []SetupMode{ModeAuto, ModeManual} {
		cfg := Config{Setup: mode(m), Embed: true}
		if err := Save(dir, cfg); err != nil {
			t.Fatalf("save %s: %v", m, err)
		}
		loaded, outcome := Load(dir)
		if outcome != OutcomeFound {
			t.Fatalf("save(%s) + load: outcome = %v, want Found", m, outcome)
		}
		if loaded.Setup == nil || *loaded.Setup != m {
			t.Fatalf("round-trip %s: setup = %v, want %s", m, loaded.Setup, m)
		}
		if !loaded.Embed {
			t.Fatalf("round-trip %s: embed lost", m)
		}
	}
}

func TestSaveWritesSetupOmittedWhenUnset(t *testing.T) {
	// Rust `#[serde(skip_serializing_if = "Option::is_none")]`: a NotConfigured
	// config file must not carry a `"setup": null` pair.
	dir := t.TempDir()
	if err := Save(dir, Config{Embed: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(PathFor(dir))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("saved file is not JSON: %v\n%s", err, raw)
	}
	if _, has := doc["setup"]; has {
		t.Fatalf("unset setup must be omitted, got %s", raw)
	}
	var embed bool
	if err := json.Unmarshal(doc["embed"], &embed); err != nil || !embed {
		t.Fatalf("embed must round-trip true, got %s", doc["embed"])
	}
}

func TestMissingFileIsNotConfigured(t *testing.T) {
	dir := t.TempDir()
	cfg, outcome := Load(dir)
	if outcome != OutcomeMissing {
		t.Fatalf("outcome = %v, want Missing", outcome)
	}
	if cfg.Setup != nil || cfg.Embed {
		t.Fatalf("missing file must yield the zero config, got %+v", cfg)
	}
}

func TestCorruptJSONDegradesToDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".leankg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PathFor(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, outcome := Load(dir)
	if outcome != OutcomeCorrupt {
		t.Fatalf("outcome = %v, want Corrupt", outcome)
	}
	if cfg.Setup != nil || cfg.Embed {
		t.Fatalf("corrupt file must yield the zero config, got %+v", cfg)
	}
}

func TestWrongTypedAndShapedFieldsAreCorrupt(t *testing.T) {
	corrupt := []string{
		`{"setup": "bogus", "embed": true}`, // unknown mode string
		`{"setup": "auto", "embed": "yes"}`, // embed must be a bool
		`{"setup": 1}`,                      // setup must be a string
		`["auto"]`,                          // not an object
		`"auto"`,                            // not an object
		`{"setup": ["auto"]}`,               // array where a scalar belongs
		``,                                  // empty file
	}
	for _, raw := range corrupt {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("should be corrupt: %q", raw)
		}
	}
}

func TestUnknownFieldsAreKeptNotDropped(t *testing.T) {
	// Rust ignored unknown fields on read (forward compatibility) — and then
	// dropped them on the next save. This port keeps them through a rewrite.
	raw := `{"setup":"manual","embed":false,"future":1,"nested":{"a":true}}`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Setup == nil || *cfg.Setup != ModeManual {
		t.Fatalf("setup = %v, want manual", cfg.Setup)
	}
	if cfg.Embed {
		t.Fatal("embed must be false")
	}
	dir := t.TempDir()
	if err := Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := os.ReadFile(PathFor(dir))
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(onDisk, &doc); err != nil {
		t.Fatalf("saved doc: %v\n%s", err, onDisk)
	}
	if string(doc["future"]) != "1" {
		t.Fatalf("unknown key future must survive, doc = %s", onDisk)
	}
	var nested struct {
		A bool `json:"a"`
	}
	if err := json.Unmarshal(doc["nested"], &nested); err != nil || !nested.A {
		t.Fatalf("unknown key nested must survive intact, doc = %s", onDisk)
	}
}

func TestLegacyShapes(t *testing.T) {
	// Legacy shape 1: the pre-embed era wrote only the mode (embed defaults
	// false, exactly as Rust's #[serde(default)] did).
	// Legacy shape 2: an explicit null is NotConfigured, not corrupt.
	// Legacy shape 3: a bare "embed" file with no stored choice.
	legacy := map[string]struct {
		setup *SetupMode
		embed bool
	}{
		`{"setup":"auto"}`:                 {mode(ModeAuto), false},
		`{"setup":null,"embed":false}`:     {nil, false},
		`{"embed":true}`:                   {nil, true},
		`{"setup":"manual","embed":false}`: {mode(ModeManual), false},
	}
	for raw, want := range legacy {
		cfg, err := Parse([]byte(raw))
		if err != nil {
			t.Fatalf("legacy shape %q: %v", raw, err)
		}
		if !reflect.DeepEqual(cfg.Setup, want.setup) {
			t.Fatalf("legacy %q: setup = %v, want %v", raw, cfg.Setup, want.setup)
		}
		if cfg.Embed != want.embed {
			t.Fatalf("legacy %q: embed = %v, want %v", raw, cfg.Embed, want.embed)
		}
	}
}

func TestResetClearsSetupKeepsEmbed(t *testing.T) {
	dir := t.TempDir()
	// No file yet -> false, and still no file.
	reset, err := ResetSetupChoice(dir)
	if err != nil || reset {
		t.Fatalf("reset on missing file = (%v, %v), want (false, nil)", reset, err)
	}
	if _, statErr := os.Stat(PathFor(dir)); statErr == nil {
		t.Fatal("reset on a missing file must not create one")
	}

	if err := Save(dir, Config{Setup: mode(ModeAuto), Embed: true}); err != nil {
		t.Fatal(err)
	}
	reset, err = ResetSetupChoice(dir)
	if err != nil || !reset {
		t.Fatalf("reset on stored file = (%v, %v), want (true, nil)", reset, err)
	}
	cfg, outcome := Load(dir)
	if outcome != OutcomeFound {
		t.Fatalf("outcome = %v, want Found (file kept)", outcome)
	}
	if cfg.Setup != nil {
		t.Fatalf("setup choice must be cleared, got %v", *cfg.Setup)
	}
	if !cfg.Embed {
		t.Fatal("embed must survive the reset")
	}
}

func TestParseMode(t *testing.T) {
	for raw, want := range map[string]*SetupMode{
		"auto":    mode(ModeAuto),
		"AUTO":    mode(ModeAuto),
		" Manual": mode(ModeManual),
		"":        nil,
		"bogus":   nil,
		"both":    nil,
	} {
		got, ok := ParseMode(raw)
		if want == nil {
			if ok {
				t.Fatalf("ParseMode(%q) = (%s, true), want not-ok", raw, got)
			}
			continue
		}
		if !ok || got != *want {
			t.Fatalf("ParseMode(%q) = (%s, %v), want (%s, true)", raw, got, ok, *want)
		}
	}
}

func TestModeFromEnv(t *testing.T) {
	cases := map[string]*SetupMode{
		"":       nil,
		"auto":   mode(ModeAuto),
		"MANUAL": mode(ModeManual),
		"junk":   nil,
	}
	for env, want := range cases {
		t.Setenv("LEANKG_SETUP_MODE", env)
		got, ok := ModeFromEnv()
		if want == nil {
			if ok {
				t.Fatalf("ModeFromEnv() with %q = (%s, true), want not-ok", env, got)
			}
			continue
		}
		if !ok || got != *want {
			t.Fatalf("ModeFromEnv() with %q = (%s, %v), want (%s, true)", env, got, ok, *want)
		}
	}
}

func TestResolveModePrecedence(t *testing.T) {
	boom := func() *SetupMode {
		t.Fatal("prompt must not run when an earlier signal resolves")
		return nil
	}
	// Flag wins over everything.
	d := ResolveMode(mode(ModeManual), mode(ModeAuto), mode(ModeManual), true, boom)
	if d.Mode != ModeManual || d.Source != SourceFlag {
		t.Fatalf("flag precedence: %+v", d)
	}
	// Env beats stored.
	d = ResolveMode(nil, mode(ModeAuto), mode(ModeManual), true, boom)
	if d.Mode != ModeAuto || d.Source != SourceEnv {
		t.Fatalf("env precedence: %+v", d)
	}
	// Stored beats prompt.
	d = ResolveMode(nil, nil, mode(ModeAuto), true, boom)
	if d.Mode != ModeAuto || d.Source != SourceStored {
		t.Fatalf("stored precedence: %+v", d)
	}
}

func TestResolveModePromptOnlyWhenInteractive(t *testing.T) {
	boom := func() *SetupMode {
		t.Fatal("prompt ran while non-interactive")
		return nil
	}
	// Non-interactive: the prompt is never consulted; manual default.
	d := ResolveMode(nil, nil, nil, false, boom)
	if d.Mode != ModeManual || d.Source != SourceManualDefault {
		t.Fatalf("non-interactive: %+v", d)
	}
	// Interactive + EOF (prompt yields nothing) -> manual default.
	eof := func() *SetupMode { return nil }
	d = ResolveMode(nil, nil, nil, true, eof)
	if d.Mode != ModeManual || d.Source != SourceManualDefault {
		t.Fatalf("interactive EOF: %+v", d)
	}
	// Interactive + answer -> prompt provenance.
	answers := func() *SetupMode { return mode(ModeManual) }
	d = ResolveMode(nil, nil, nil, true, answers)
	if d.Mode != ModeManual || d.Source != SourcePrompt {
		t.Fatalf("interactive answer: %+v", d)
	}
	// A nil prompt closure is an immediate manual default, not a panic.
	d = ResolveMode(nil, nil, nil, true, nil)
	if d.Mode != ModeManual || d.Source != SourceManualDefault {
		t.Fatalf("nil prompt: %+v", d)
	}
}

func TestMergeProjectRoots(t *testing.T) {
	dir := t.TempDir()
	twin := filepath.Join(dir, "twin")
	if err := os.Mkdir(twin, 0o755); err != nil {
		t.Fatal(err)
	}
	// On macOS a TempDir lives under /var -> /private/var; the symlinked and
	// resolved spellings must collapse to ONE entry.
	merged := MergeProjectRoots([]string{
		twin + "/",
		twin,
		dir,
		"/does/not/exist",
	})
	if len(merged) != 3 {
		t.Fatalf("merged = %v, want 3 entries", merged)
	}
	if merged[0] == merged[1] {
		t.Fatalf("twin spellings did not dedupe: %v", merged)
	}
	if merged[len(merged)-1] != "/does/not/exist" {
		t.Fatalf("missing path must be kept as written: %v", merged)
	}
}

func TestFreshnessLadder(t *testing.T) {
	i := func(v int64) *int64 { return &v }
	cases := []struct {
		elements int
		computed *int64
		commit   *int64
		want     string
	}{
		{0, nil, nil, "cold"},
		{100, i(1000), i(500), "fresh"},
		{100, i(1000), i(1000), "fresh"}, // commit == snapshot: not newer
		{100, i(1000), i(2000), "possibly_stale"},
		{100, nil, i(500), "possibly_stale"},
		{100, i(1000), nil, "possibly_stale"},
	}
	for _, tc := range cases {
		if got := FreshnessLabel(tc.elements, tc.computed, tc.commit); got != tc.want {
			t.Errorf("FreshnessLabel(%d, %v, %v) = %s, want %s",
				tc.elements, tc.computed, tc.commit, got, tc.want)
		}
	}
}

func TestPathFor(t *testing.T) {
	if got := PathFor("/proj"); got != filepath.Join("/proj", ".leankg", "config.json") {
		t.Fatalf("PathFor = %s", got)
	}
}

func TestInteractiveMatchesTerminals(t *testing.T) {
	// In `go test`, stdin/stdout are pipes (or /dev/null), never a terminal:
	// Interactive must be false so a test-suite binary never blocks on a
	// prompt.
	if Interactive() {
		t.Fatal("Interactive() must be false under the test harness")
	}
}
