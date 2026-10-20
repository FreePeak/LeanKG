package telemetry

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// DefaultConfig is capture off with the DS-01 retention and body cap. It is
// what a missing telemetry.yaml means.
func DefaultConfig() Config {
	return Config{Capture: Off, RetentionDays: DefaultRetentionDays, MaxBodyBytes: DefaultMaxBodyBytes}
}

// LoadConfig reads ConfigPath(home). A missing file yields the defaults
// (capture off) and creates nothing (DS-01).
func LoadConfig(home string) (Config, error) {
	cfg := DefaultConfig()
	b, err := os.ReadFile(ConfigPath(home))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("telemetry: read config: %w", err)
	}
	// Unmarshal over the defaults so keys absent from the file keep them.
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("telemetry: parse %s: %w", ConfigPath(home), err)
	}
	return cfg, nil
}

// SaveConfig writes ConfigPath(home) (0600, creating home 0700). The write is
// a temp file plus rename, so a crash never leaves a half-written consent.
func SaveConfig(home string, cfg Config) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("telemetry: create home: %w", err)
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("telemetry: encode config: %w", err)
	}
	tmp, err := os.CreateTemp(home, "telemetry-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("telemetry: create temp config: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("telemetry: write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("telemetry: write config: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("telemetry: chmod config: %w", err)
	}
	if err := os.Rename(tmp.Name(), ConfigPath(home)); err != nil {
		return fmt.Errorf("telemetry: install config: %w", err)
	}
	return nil
}

// EffectiveLevel is the level a server records at: Off without a current
// consent; the env override (LEANKG_TELEMETRY) can only lower it (DS-01).
func EffectiveLevel(cfg Config, env string) Level {
	// Without a current consent nothing is recorded, whatever the file says.
	lvl := Off
	if cfg.Consent.Version >= ConsentVersion && !cfg.Consent.GrantedAt.IsZero() {
		if v := validLevel(cfg.Capture); v != "" {
			lvl = v
		}
	}
	// Only a known, lower level may override; an unknown value is ignored
	// rather than read as "off", so a typo cannot silently change behavior.
	if e := validLevel(Level(strings.ToLower(strings.TrimSpace(env)))); env != "" && e != "" && e.Rank() < lvl.Rank() {
		lvl = e
	}
	return lvl
}

// validLevel returns l when it names a known level, else "" (unknown).
func validLevel(l Level) Level {
	switch l {
	case Off, Metadata, Bodies:
		return l
	}
	return ""
}

// SessionsGranted reports whether transcripts of client may be read (DS-01).
func SessionsGranted(cfg Config, client string) bool {
	if !cfg.Sessions.Enabled {
		return false
	}
	for _, c := range cfg.Sessions.Clients {
		if c == client || c == "all" {
			return true
		}
	}
	return false
}
