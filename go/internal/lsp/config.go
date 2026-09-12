package lsp

// Bridge configuration (Rust parity: src/lsp/config.rs). A project names its
// language servers in the `lsp:` block of leankg.yaml; everything not named
// falls back to the catalog (with_prefab_fallback). A missing file, a missing
// block or an unparsable block all degrade to catalog-only configuration —
// the Rust loader ignored parse errors and used prefab defaults, so the
// bridge never hard-fails on config problems.

import (
	"encoding/json"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ServerConfig is one configured language server.
type ServerConfig struct {
	Command               string   `json:"command" yaml:"command"`
	Args                  []string `json:"args,omitempty" yaml:"args,omitempty"`
	Extensions            []string `json:"extensions,omitempty" yaml:"extensions,omitempty"`
	InitializationOptions any      `json:"initialization_options,omitempty" yaml:"initialization_options,omitempty"`
}

// Config is the bridge configuration. Servers are keyed by canonical catalog
// language id ("go", "typescript", "python", ...).
type Config struct {
	Servers       map[string]ServerConfig `json:"servers" yaml:"servers"`
	WorkspaceRoot string                  `json:"workspace_root,omitempty" yaml:"workspace_root,omitempty"`
	TimeoutMS     int                     `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

// defaultTimeoutMS mirrors Rust's default_lsp_timeout: every bridge request
// is bounded by this unless the project config overrides it.
const defaultTimeoutMS = 5000

// defaultTypedResolve mirrors Rust's default_typed_resolve.
const defaultTypedResolve = "off"

// normalized returns the config with defaults applied (nil maps and
// non-positive timeouts).
func (c Config) normalized() Config {
	if c.Servers == nil {
		c.Servers = map[string]ServerConfig{}
	}
	if c.TimeoutMS <= 0 {
		c.TimeoutMS = defaultTimeoutMS
	}
	return c
}

// WithPrefabFallback fills languages missing from the user config from the
// catalog (Rust parity: LspConfig::with_prefab_fallback). Returns the filled
// config and the set of language ids that came from the user config (used for
// capability-tier reporting).
func (c Config) WithPrefabFallback() (Config, map[string]bool) {
	c = c.normalized()
	user := make(map[string]bool, len(c.Servers))
	for id := range c.Servers {
		user[id] = true
	}
	for _, spec := range catalog {
		if _, ok := c.Servers[spec.Language]; !ok {
			c.Servers[spec.Language] = ServerConfig{
				Command:    spec.Command,
				Args:       append([]string(nil), spec.Args...),
				Extensions: append([]string(nil), spec.Extensions...),
			}
		}
	}
	return c, user
}

// LoadConfig reads the `lsp:` block from <dir>/leankg.yaml. ok=false means
// the file or the block is absent; a parse error is reported as ok=false as
// well (Rust parity: from_leankg_yaml_or_default falls back to prefab).
func LoadConfig(dir string) (Config, bool) {
	cfg, _, ok := LoadProject(dir)
	return cfg, ok
}

// LoadProject reads <dir>/leankg.yaml once: the `lsp:` block (ok=false when
// the file, block or YAML is unusable) and the `indexer.typed_resolve`
// feature flag (Rust default "off").
func LoadProject(dir string) (cfg Config, typedResolve string, ok bool) {
	typedResolve = defaultTypedResolve
	raw, err := os.ReadFile(filepath.Join(dir, "leankg.yaml"))
	if err != nil {
		return Config{}, typedResolve, false
	}
	doc, err := parseYAMLSubset(string(raw))
	if err != nil {
		return Config{}, typedResolve, false
	}
	if v, found := nestedString(doc, "indexer", "typed_resolve"); found {
		typedResolve = v
	}
	cfg, ok = lspFromDoc(doc)
	return cfg, typedResolve, ok
}

// parseLSPBlock extracts the top-level `lsp:` mapping from YAML text and
// decodes it into a Config.
func parseLSPBlock(data []byte) (Config, bool) {
	doc, err := parseYAMLSubset(string(data))
	if err != nil {
		return Config{}, false
	}
	return lspFromDoc(doc)
}

// lspFromDoc decodes the doc's `lsp:` mapping into a Config (via a JSON
// round-trip so YAML scalar mapping and struct tags stay in one place).
func lspFromDoc(doc map[string]any) (Config, bool) {
	blk, ok := doc["lsp"]
	if !ok {
		return Config{}, false
	}
	blob, err := json.Marshal(blk)
	if err != nil {
		return Config{}, false
	}
	var cfg Config
	if err := json.Unmarshal(blob, &cfg); err != nil {
		return Config{}, false
	}
	return cfg, true
}

// parseYAMLSubset decodes a document into generic maps/slices/scalars.
func parseYAMLSubset(src string) (map[string]any, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// nestedString reads doc[section][key] as a string.
func nestedString(doc map[string]any, section, key string) (string, bool) {
	blk, ok := doc[section].(map[string]any)
	if !ok {
		return "", false
	}
	s, ok := blk[key].(string)
	return s, ok
}
