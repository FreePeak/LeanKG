package store

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// StandalonePathEnv names the environment variable that overrides the SQLite
// store location (FR-P2): the project directory becomes optional and the user
// owns the store.
const StandalonePathEnv = "LEANKG_DB_PATH"

// StandaloneDBPath is the configured SQLite store path for one project
// directory: LEANKG_DB_PATH > the nearest leankg.yaml
// `db.standalone_db_path` > "" (the caller then uses its own default,
// <project>/.leankg/leankg.db).
//
// It lives in store, not projectcfg, because OpenBackend is the ONE place every
// verb opens a store and store cannot import projectcfg (projectcfg → lsp →
// store). projectcfg.StandaloneDBPath delegates here, so the precedence is
// defined once and both the CLI and the store agree.
func StandaloneDBPath(dir string) string {
	if v := os.Getenv(StandalonePathEnv); v != "" {
		return v
	}
	// The FIRST leankg.yaml walking up is authoritative: a missing or
	// unreadable file stops the walk rather than continuing to a parent that
	// is a different project.
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for d := abs; ; {
		data, err := os.ReadFile(filepath.Join(d, "leankg.yaml"))
		if err == nil {
			var cfg struct {
				DB struct {
					StandaloneDBPath string `yaml:"standalone_db_path"`
				} `yaml:"db"`
			}
			if yaml.Unmarshal(data, &cfg) != nil {
				return "" // malformed document: no override, never a guess
			}
			return cfg.DB.StandaloneDBPath
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
