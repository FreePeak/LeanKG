// Package setupcfg is the Go port of the Rust first-run setup contract
// (src/setup_config.rs, FR-ZCP-13): `<project>/.leankg/config.json` is the
// persisted auto/manual choice
//
//	{"setup": "auto" | "manual", "embed": bool}
//
// Read semantics (never panic / never error out the setup flow):
//
//   - missing file  -> Outcome Missing + the zero Config (NotConfigured: the
//     caller decides whether a prompt is possible);
//   - corrupt file  -> Outcome Corrupt + the zero Config (the caller warns).
//
// Mode resolution precedence (pure, unit-tested): `--auto`/`--manual` flag >
// `LEANKG_SETUP_MODE` env > stored config.json > interactive prompt (only when
// stdin AND stdout are TTYs) > manual default, so a script never blocks.
//
// Divergence (additive, no Rust equivalent): unknown JSON keys survive a
// rewrite. Rust's serde ignored them on read and dropped them on the next
// save; this port keeps the whole object so a `leankg setup --reset` or a
// re-answered prompt cannot silently delete a field another tool wrote.
package setupcfg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConfigName is the persisted file name under `<project>/.leankg`.
const ConfigName = "config.json"

// SetupMode is the chosen first-run setup mode.
type SetupMode string

// The two modes. Auto indexes (and optionally embeds) in the background right
// after registration; manual prints the next commands and lets the user run
// indexing explicitly.
const (
	ModeAuto   SetupMode = "auto"
	ModeManual SetupMode = "manual"
)

// ParseMode parses an env/CLI value ("auto" | "manual", case-insensitive).
// Anything else — including the empty string — yields ok=false.
func ParseMode(raw string) (mode SetupMode, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "auto":
		return ModeAuto, true
	case "manual":
		return ModeManual, true
	default:
		return "", false
	}
}

// ModeFromEnv parses LEANKG_SETUP_MODE: unset, empty, or unrecognized means
// "no env signal", exactly as Rust's `and_then(parse_env_value)` did.
func ModeFromEnv() (SetupMode, bool) {
	return ParseMode(os.Getenv("LEANKG_SETUP_MODE"))
}

// Source is the provenance of the resolved setup mode (surfaced in the
// registration summary).
type Source string

// The provenance values, in precedence order.
const (
	SourceFlag          Source = "flag"
	SourceEnv           Source = "env"
	SourceStored        Source = "stored"
	SourcePrompt        Source = "prompt"
	SourceManualDefault Source = "manual_default"
)

// Config is the persisted per-project setup contract. Setup == nil means
// NotConfigured (the next run re-asks); Embed says whether auto setup should
// chain an embedding pass after indexing.
//
// Raw is the whole decoded JSON object, so a rewrite preserves keys this
// package does not model. It is nil only for a Config that never came from
// Load/Parse.
type Config struct {
	Setup *SetupMode
	Embed bool
	Raw   map[string]json.RawMessage
}

// Outcome is the result of reading a setup config.
type Outcome int

const (
	// OutcomeFound: the file exists and parsed.
	OutcomeFound Outcome = iota
	// OutcomeMissing: no file at all (NotConfigured).
	OutcomeMissing
	// OutcomeCorrupt: the file exists but is not a valid setup config.
	OutcomeCorrupt
)

// PathFor returns the setup config path for a project root.
func PathFor(projectRoot string) string {
	return filepath.Join(projectRoot, ".leankg", ConfigName)
}

// rawConfig is the typed view of the known fields. Both pointers distinguish
// "absent/null" from a value, which is what decides NotConfigured and what
// lets a wrong-typed known field be reported as corrupt instead of silently
// defaulted.
type rawConfig struct {
	Setup *string `json:"setup"`
	Embed *bool   `json:"embed"`
}

// Parse decodes the config from raw JSON text. An unknown key is kept on
// Config.Raw; a wrong-typed known key (including an unknown `setup` mode
// string) is an error.
func Parse(raw []byte) (Config, error) {
	var typed rawConfig
	if err := json.Unmarshal(raw, &typed); err != nil {
		return Config{}, err
	}
	cfg := Config{}
	if typed.Setup != nil {
		mode, ok := ParseMode(*typed.Setup)
		if !ok {
			return Config{}, fmt.Errorf("unknown setup mode %q", *typed.Setup)
		}
		cfg.Setup = &mode
	}
	if typed.Embed != nil {
		cfg.Embed = *typed.Embed
	}
	rawMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return Config{}, err
	}
	if !rawConfigIsObject(raw) {
		return Config{}, fmt.Errorf("setup config must be a JSON object")
	}
	cfg.Raw = rawMap
	return cfg, nil
}

// Load reads the setup config for projectRoot. Missing/corrupt degrade to the
// zero Config with the outcome reported — never an error.
func Load(projectRoot string) (Config, Outcome) {
	raw, err := os.ReadFile(PathFor(projectRoot))
	if err != nil {
		return Config{}, OutcomeMissing
	}
	cfg, perr := Parse(raw)
	if perr != nil {
		return Config{}, OutcomeCorrupt
	}
	return cfg, OutcomeFound
}

// Save persists the config (creating `.leankg/` when missing). Every key the
// document already carried is written back; `setup` is omitted entirely when
// no choice is stored, matching the Rust `skip_serializing_if`. `embed` is
// always written, mirroring the Rust `#[serde(default)]` field.
func Save(projectRoot string, cfg Config) error {
	path := PathFor(projectRoot)
	if parent := filepath.Dir(path); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	out := map[string]json.RawMessage{}
	for k, v := range cfg.Raw {
		out[k] = v
	}
	// The typed fields are authoritative: Raw may predate this Config (a
	// caller that built one by hand carries no Raw at all), and a rewrite
	// must never lose the decision it was asked to store.
	embed, err := json.Marshal(cfg.Embed)
	if err != nil {
		return err
	}
	out["embed"] = embed
	if cfg.Setup != nil {
		encoded, merr := json.Marshal(string(*cfg.Setup))
		if merr != nil {
			return merr
		}
		out["setup"] = encoded
	} else {
		delete(out, "setup")
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	return os.WriteFile(path, pretty.Bytes(), 0o644)
}

// ResetSetupChoice clears the stored setup choice so the next registration
// re-asks. Every other field (notably `embed`) is kept. Reports false when no
// config file exists yet.
func ResetSetupChoice(projectRoot string) (bool, error) {
	if _, err := os.Stat(PathFor(projectRoot)); err != nil {
		return false, nil
	}
	cfg, _ := Load(projectRoot)
	cfg.Setup = nil
	if err := Save(projectRoot, cfg); err != nil {
		return false, err
	}
	return true, nil
}

// Decision is a resolved setup mode plus where it came from.
type Decision struct {
	Mode   SetupMode
	Source Source
}

// ResolveMode resolves the setup mode with the Rust precedence: flag > env >
// stored > prompt (TTY-gated) > manual default. prompt is only consulted when
// interactive is true and no earlier signal resolved; a nil prompt is the
// same as a prompt that returned no answer.
func ResolveMode(flag, env, stored *SetupMode, interactive bool, prompt func() *SetupMode) Decision {
	if flag != nil {
		return Decision{Mode: *flag, Source: SourceFlag}
	}
	if env != nil {
		return Decision{Mode: *env, Source: SourceEnv}
	}
	if stored != nil {
		return Decision{Mode: *stored, Source: SourceStored}
	}
	if interactive && prompt != nil {
		if mode := prompt(); mode != nil {
			return Decision{Mode: *mode, Source: SourcePrompt}
		}
	}
	return Decision{Mode: ModeManual, Source: SourceManualDefault}
}

// Interactive reports whether the prompt may be used: stdin AND stdout are
// both terminals (Rust: `stdin.is_terminal() && stdout.is_terminal()`).
func Interactive() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

// MergeProjectRoots dedupes project roots for a `status` listing:
// canonicalize best-effort (a missing path is kept as written), first
// occurrence wins.
func MergeProjectRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := map[string]bool{}
	for _, root := range roots {
		canon, err := filepath.EvalSymlinks(root)
		if err != nil {
			canon = root
		}
		if seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	return out
}

// Freshness label vocabulary (FR-ZCP-06).
const (
	FreshnessFresh         = "fresh"
	FreshnessPossiblyStale = "possibly_stale"
	FreshnessCold          = "cold"
)

// FreshnessLabel derives the freshness label from cheap local facts only:
//
//   - no elements yet -> cold (initialized, index empty or still building);
//   - an inventory snapshot taken at/after the last commit -> fresh;
//   - everything else (commit newer than the snapshot, no git context, or no
//     inventory to prove freshness) -> possibly_stale.
func FreshnessLabel(elements int, inventoryComputedAt, lastCommitTime *int64) string {
	if elements == 0 {
		return FreshnessCold
	}
	if inventoryComputedAt == nil {
		return FreshnessPossiblyStale
	}
	if lastCommitTime == nil {
		// No git context — freshness cannot be proven.
		return FreshnessPossiblyStale
	}
	if *lastCommitTime > *inventoryComputedAt {
		return FreshnessPossiblyStale
	}
	return FreshnessFresh
}

// rawConfigIsObject reports whether raw decodes to a JSON object (the only
// shape a setup config can have). Parse has already rejected a syntax error by
// the time this runs, so a false result means a JSON array/string/number.
func rawConfigIsObject(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
