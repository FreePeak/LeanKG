// Package projectcfg is the Go port of the Rust engine's leankg.yaml project
// configuration surface (src/config/project.rs): the ProjectConfig shape and
// its defaults, the parse/absent postures its readers used, project-path
// resolution (nearest-config walk-up plus the project.project_path anchor),
// and the read-modify-write helpers that keep a regenerated config from
// dropping user fields.
//
// The Rust readers this port answers for:
//
//	db_config_from_cwd        -> DBConfigFromDir
//	MCPServer::resolve_project_root_raw -> ResolveProjectDBDir
//	main::find_project_root   -> FindProjectRoot
//	load_config / the `.unwrap_or_default()` sites -> Load / LoadOrDefault
//
// Divergence (deliberate, no serde equivalent): yaml.v3 leaves any field the
// document does not mention at its pre-seeded value, so a PRESENT-but-partial
// block (say `mcp:` without `port`) parses here while serde rejected it with a
// "missing field" error. Rust's own setup pipeline writes exactly such a block
// (setup/mod.rs write_project_config omits mcp.port), which serde-level
// strictness would then refuse to read back; the lenient posture is what the
// engine's other config reader (internal/lsp) already ships.
package projectcfg

import (
	"os"
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/lsp"
	"gopkg.in/yaml.v3"
)

// ConfigFileName is the project config file every reader looks for.
const ConfigFileName = "leankg.yaml"

// DefaultTypedResolve mirrors Rust's default_typed_resolve.
const DefaultTypedResolve = "off"

// DefaultMCPServerPort mirrors the built-in `mcp.port` default.
const DefaultMCPServerPort = 3000

// ProjectConfig mirrors Rust's ProjectConfig. Missing top-level keys keep the
// DefaultProjectConfig value (Rust: `#[serde(default)]` on the struct, which
// fills absent fields from the struct's own Default impl).
type ProjectConfig struct {
	Project       ProjectSettings              `yaml:"project"`
	Indexer       IndexerConfig                `yaml:"indexer"`
	MCP           MCPConfig                    `yaml:"mcp"`
	Documentation DocConfig                    `yaml:"documentation"`
	Microservice  *MicroserviceExtractorConfig `yaml:"microservice"`
	Auth          AuthSettings                 `yaml:"auth"`
	// LSP is the optional prefab / user LSP block. It is owned by
	// internal/lsp (which reads it directly); the field exists so a config
	// round-trips through this shape.
	LSP *lsp.Config `yaml:"lsp,omitempty"`
	// Source indexes from a non-local source; when set it overrides
	// project.root. CLI flags take precedence over these values.
	Source *SourceConfig `yaml:"source,omitempty"`
	// DB holds optional Postgres settings. When unset the backend uses
	// LEANKG_PG_URL or its built-in default, in that precedence order.
	DB *DBConfig `yaml:"db,omitempty"`
}

// ProjectSettings mirrors Rust's ProjectSettings. project_path is the schema
// identity anchor: readers and writers key the project's store on it.
type ProjectSettings struct {
	Name        string      `yaml:"name"`
	Root        string      `yaml:"root"`
	ProjectPath string      `yaml:"project_path,omitempty"`
	Languages   []string    `yaml:"languages"`
	Steer       SteerConfig `yaml:"steer"`
}

// SteerConfig mirrors Rust's config::steer::SteerConfig. Additive config: the
// index walk reads priority/ignore paths; everything else is user-facing.
type SteerConfig struct {
	PriorityPaths []string       `yaml:"priority_paths"`
	IgnorePaths   []string       `yaml:"ignore_paths"`
	Languages     SteerLanguages `yaml:"languages"`
	Notes         []ClusterNote  `yaml:"notes"`
}

// SteerLanguages is the language toggle map, kept as an explicit list of
// (language, enabled) pairs so the YAML round-trips deterministically.
type SteerLanguages struct {
	Items [][2]any `yaml:"items"`
}

// ClusterNote is a free-form note attached to a path prefix or cluster.
type ClusterNote struct {
	Path string `yaml:"path"`
	Note string `yaml:"note"`
}

// IndexerConfig mirrors Rust's IndexerConfig.
type IndexerConfig struct {
	Exclude []string `yaml:"exclude"`
	Include []string `yaml:"include"`
	// TypedResolve is the typed call resolution feature flag ("off", "all",
	// or a CSV of languages). internal/lsp owns its interpretation.
	TypedResolve string `yaml:"typed_resolve"`
}

// MCPConfig mirrors Rust's McpConfig: the auto-index gates the serving
// process consults on start.
type MCPConfig struct {
	Enabled                   bool   `yaml:"enabled"`
	Port                      int    `yaml:"port"`
	AuthToken                 string `yaml:"auth_token"`
	AutoIndexOnStart          bool   `yaml:"auto_index_on_start"`
	AutoIndexThresholdMinutes int    `yaml:"auto_index_threshold_minutes"`
	AutoIndexOnDBWrite        bool   `yaml:"auto_index_on_db_write"`
	RequireGitForAutoIndex    bool   `yaml:"require_git_for_auto_index"`
}

// DocConfig mirrors Rust's DocConfig.
type DocConfig struct {
	Output    string   `yaml:"output"`
	Templates []string `yaml:"templates"`
}

// MicroserviceExtractorConfig mirrors Rust's MicroserviceExtractorConfig.
type MicroserviceExtractorConfig struct {
	ClientDirs         []string `yaml:"client_dirs"`
	ConfigFiles        []string `yaml:"config_files"`
	GRPCAddressPattern string   `yaml:"grpc_address_pattern"`
	HTTPAddressPattern string   `yaml:"http_address_pattern"`
	TrackProtocols     []string `yaml:"track_protocols"`
}

// AuthProvider mirrors Rust's AuthProvider (serde rename_all = lowercase).
type AuthProvider string

// AuthProviderStatic is the only provider the Rust engine implemented.
const AuthProviderStatic AuthProvider = "static"

// AuthSettings mirrors Rust's AuthSettings.
type AuthSettings struct {
	Enabled  bool         `yaml:"enabled"`
	Provider AuthProvider `yaml:"provider"`
	Tokens   []TokenEntry `yaml:"tokens"`
}

// TokenEntry mirrors Rust's TokenEntry.
type TokenEntry struct {
	Token    string `yaml:"token"`
	Role     string `yaml:"role"`
	ClientID string `yaml:"client_id"`
}

// SourceConfig mirrors Rust's SourceConfig.
type SourceConfig struct {
	URI     string `yaml:"uri"`
	Auth    string `yaml:"auth,omitempty"`
	RefName string `yaml:"ref_name,omitempty"`
}

// DBConfig mirrors Rust's DbConfig. Every field is optional, so a minimal
// `db: {}` or a partial block is valid; the backend treats the block as the
// middle precedence tier: LEANKG_PG_URL env > `db:` yaml > built-in default.
type DBConfig struct {
	// URL is the connection string, e.g.
	// postgresql://postgres:postgres@localhost:5433/leankg.
	URL string `yaml:"url,omitempty"`
	// PoolSize is the lazy connection pool size (default 5, clamped >= 1).
	PoolSize *int `yaml:"pool_size,omitempty"`
	// Lock false disables the index advisory lock (default true).
	Lock *bool `yaml:"lock,omitempty"`
}

// DefaultProjectConfig mirrors `impl Default for ProjectConfig`, literals and
// all. Callers must not rely on the slices being shared: every call builds a
// fresh config.
func DefaultProjectConfig() ProjectConfig {
	return ProjectConfig{
		Project: ProjectSettings{
			Name: "my-project",
			Root: ".",
			Languages: []string{
				"go", "typescript", "python", "java", "kotlin", "rust", "dart",
				"swift", "objc", "c", "cpp", "ruby", "php", "perl", "r",
				"elixir", "bash", "lua", "scala", "zig", "solidity", "csharp",
			},
			Steer: SteerConfig{},
		},
		Indexer: IndexerConfig{
			Exclude: []string{"**/node_modules/**", "**/vendor/**"},
			Include: []string{
				"*.go", "*.ts", "*.py", "*.java", "*.kt", "*.xml", "*.rs",
				"*.dart", "*.swift", "*.m", "*.mm", "*.c", "*.h", "*.cpp",
				"*.hpp", "*.rb", "*.php", "*.pl", "*.pm", "*.r", "*.ex",
				"*.exs", "*.sh", "*.lua", "*.scala", "*.zig", "*.sol", "*.cs",
			},
			TypedResolve: DefaultTypedResolve,
		},
		MCP: MCPConfig{
			Enabled:                   true,
			Port:                      DefaultMCPServerPort,
			AuthToken:                 "",
			AutoIndexOnStart:          true,
			AutoIndexThresholdMinutes: 5,
			// Re-indexing on every external DB write can create CPU/memory
			// storms in large workspaces; users opt in explicitly.
			AutoIndexOnDBWrite:     false,
			RequireGitForAutoIndex: true,
		},
		Documentation: DocConfig{
			Output:    "./docs",
			Templates: []string{"agents", "claude"},
		},
		Microservice: nil,
		Auth:         AuthSettings{Provider: AuthProviderStatic},
	}
}

// DefaultMicroserviceExtractorConfig mirrors the Rust Default impl for the
// microservice extractor (used when a project config omits the block).
func DefaultMicroserviceExtractorConfig() MicroserviceExtractorConfig {
	return MicroserviceExtractorConfig{
		ClientDirs:         []string{"internal/external"},
		ConfigFiles:        []string{"config/config.go", "config/*.yaml", "config/*.yml"},
		GRPCAddressPattern: `dns:///{service}\.default\.svc\.cluster\.local\.::{port}`,
		HTTPAddressPattern: `http://{service}\.default\.svc\.cluster\.local\.`,
		TrackProtocols:     []string{"grpc"},
	}
}

// ConfigPath is the config file path for a project directory.
func ConfigPath(dir string) string {
	return filepath.Join(dir, ConfigFileName)
}

// Parse decodes a leankg.yaml document. Absent top-level keys keep their
// DefaultProjectConfig value; a syntax error is returned, never swallowed, so
// each caller can pick the posture it had in Rust (hard error versus
// unwrap_or_default). An empty document parses to the defaults.
func Parse(data []byte) (ProjectConfig, error) {
	cfg := DefaultProjectConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultProjectConfig(), err
	}
	return cfg, nil
}

// Load reads <dir>/leankg.yaml. Both a missing file and a malformed document
// are errors (Rust: `read_to_string(...)?` then `from_str(...)?`).
func Load(dir string) (ProjectConfig, error) {
	data, err := os.ReadFile(ConfigPath(dir))
	if err != nil {
		return DefaultProjectConfig(), err
	}
	return Parse(data)
}

// LoadOrDefault is the Rust `... .unwrap_or_default()` posture shared by the
// index and auto-index paths: an absent or malformed config yields the
// built-in defaults instead of failing the run.
func LoadOrDefault(dir string) ProjectConfig {
	cfg, err := Load(dir)
	if err != nil {
		return DefaultProjectConfig()
	}
	return cfg
}

// FindProjectRoot walks up from start to the nearest directory that owns a
// .leankg entry or a leankg.yaml (Rust main.rs::find_project_root). start
// itself is returned when nothing matches, so callers always get a usable dir.
func FindProjectRoot(start string) string {
	if start == "" {
		start = "."
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		abs = start
	}
	for dir := abs; ; {
		if pathExists(filepath.Join(dir, ".leankg")) || pathExists(ConfigPath(dir)) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		dir = parent
	}
}

// ResolveProjectRoot resolves and canonicalizes the .leankg directory a project
// actually serves from (Rust MCPServer::resolve_project_root). Canonicalizing
// matters: a RELATIVE project_path/root must not re-key the project schema
// against whatever directory the caller was launched from (Rust N3).
func ResolveProjectRoot(dbDir string) string {
	chosen := ResolveProjectDBDir(dbDir)
	if abs, err := filepath.Abs(chosen); err == nil {
		chosen = abs
	}
	if resolved, err := filepath.EvalSymlinks(chosen); err == nil {
		return resolved
	}
	return chosen
}

// AuthFromDir loads the `auth:` block from the nearest leankg.yaml walking up
// from dbDir (Rust mcp::server::load_auth_settings, which started at the
// `.leankg` directory). Unlike DBConfigFromDir this walk CONTINUES past a
// config it cannot read or parse — only a readable, parsable config ends it.
// The fallback is auth disabled with the static provider.
func AuthFromDir(dbDir string) AuthSettings {
	for dir := dbDir; ; {
		if data, err := os.ReadFile(ConfigPath(dir)); err == nil {
			if cfg, err := Parse(data); err == nil {
				return cfg.Auth
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return AuthSettings{Provider: AuthProviderStatic}
		}
		dir = parent
	}
}

// ResolveProjectDBDir resolves which .leankg directory a project actually uses,
// honoring the config at <dbDir>/leankg.yaml (Rust
// MCPServer::resolve_project_root_raw).
//
// Order: a live `project.project_path` anchor wins, then a non-"." project.root
// (or its parent), then dbDir itself. An absent or malformed config, or an
// anchor pointing at a directory with no .leankg, leaves dbDir unchanged.
func ResolveProjectDBDir(dbDir string) string {
	data, err := os.ReadFile(ConfigPath(dbDir))
	if err != nil {
		return dbDir
	}
	cfg, err := Parse(data)
	if err != nil {
		return dbDir
	}
	// 1. project_path is the absolute identity anchor stored at init time.
	if pp := cfg.Project.ProjectPath; pp != "" {
		if at := filepath.Join(pp, ".leankg"); pathIsDir(at) {
			return at
		}
	}
	// 2. A configured root other than "." may own its own .leankg (the
	// `index ./src` layout), as may its parent.
	root := cfg.Project.Root
	if root != "" && root != "." {
		projectRoot := filepath.Dir(dbDir)
		resolved := root
		if !filepath.IsAbs(root) {
			resolved = filepath.Join(projectRoot, root)
		}
		if at := filepath.Join(resolved, ".leankg"); pathIsDir(at) && !samePath(at, dbDir) {
			return at
		}
		if parent := filepath.Dir(resolved); parent != resolved {
			if at := filepath.Join(parent, ".leankg"); pathIsDir(at) && !samePath(at, dbDir) {
				return at
			}
		}
	}
	return dbDir
}

// DBConfigFromDir loads the `db:` block from the nearest leankg.yaml walking up
// from dir (Rust config::project::db_config_from_cwd, parameterized on the
// starting directory instead of the process cwd). The FIRST leankg.yaml found
// is authoritative: if it cannot be read or parsed, or has no db block, the
// result is nil — the walk never continues past it. Callers apply Rust's
// precedence: LEANKG_PG_URL env > this block > built-in default.
func DBConfigFromDir(dir string) *DBConfig {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for d := abs; ; {
		p := ConfigPath(d)
		if pathIsFile(p) {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			cfg, err := Parse(data)
			if err != nil {
				return nil
			}
			return cfg.DB
		}
		parent := filepath.Dir(d)
		if parent == d {
			return nil
		}
		d = parent
	}
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func pathIsDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func pathIsFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// samePath compares two paths by their cleaned spelling, which is what Rust's
// PathBuf equality checked (no canonicalization).
func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}
