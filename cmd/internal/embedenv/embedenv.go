// Package embedenv reads the LEANKG_EMBED_* environment into an embed.Config.
// It is the only place the embed provider's environment is read: the embed
// package takes the explicit Config, so the same provider can be configured
// from a flag, a test or an operator's shell alike.
package embedenv

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/FreePeak/LeanKG/internal/embed"
)

// Load builds the embed configuration from lookup (os.Getenv in production).
// A malformed value is an error that names the variable, so a typo cannot
// silently choose a value. An unset variable leaves the field at its zero
// value, which the embed package resolves to its default.
func Load(lookup func(string) string) (embed.Config, error) {
	var cfg embed.Config
	cfg.Provider = lookup("LEANKG_EMBED_PROVIDER")
	cfg.Model = lookup("LEANKG_EMBED_MODEL")
	cfg.BaseURL = lookup("LEANKG_EMBED_BASE_URL")
	cfg.APIKey = lookup("LEANKG_EMBED_API_KEY")
	cfg.Revision = lookup("LEANKG_EMBED_REVISION")

	var err error
	if cfg.Dims, err = positiveInt(lookup, "LEANKG_EMBED_DIMS"); err != nil {
		return cfg, err
	}
	if cfg.TextCap, err = textCap(lookup); err != nil {
		return cfg, err
	}
	if cfg.RequestTimeout, err = seconds(lookup, "LEANKG_EMBED_REQUEST_TIMEOUT_SECS"); err != nil {
		return cfg, err
	}
	if cfg.Sidecar, err = sidecar(lookup); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// positiveInt reads an optional positive integer; 0 when unset.
func positiveInt(lookup func(string) string, key string) (int, error) {
	raw := lookup(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("embed: invalid %s %q: want a positive integer", key, raw)
	}
	return n, nil
}

// textCap reads LEANKG_EMBED_TEXT_CAP, the operator's write-cap pin in runes.
// Unset is 0 (no pin); a value outside [256, 8000] is an error.
func textCap(lookup func(string) string) (int, error) {
	const key = "LEANKG_EMBED_TEXT_CAP"
	raw := lookup(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 256 || n > 8000 {
		return 0, fmt.Errorf("embed: %s=%q: want an integer in [256, 8000]", key, raw)
	}
	return n, nil
}

// seconds reads an optional whole number of seconds as a duration; 0 when unset.
func seconds(lookup func(string) string, key string) (time.Duration, error) {
	raw := lookup(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("embed: invalid %s %q: want a positive number of seconds", key, raw)
	}
	return time.Duration(n) * time.Second, nil
}

// sidecar reads the llama.cpp sidecar spawn settings. The argv defaults to the
// pinned GGUF (embed.DefaultSidecarArgs) when LEANKG_EMBED_SIDECAR_ARGS is unset.
func sidecar(lookup func(string) string) (embed.SidecarConfig, error) {
	var sc embed.SidecarConfig
	sc.Command = lookup("LEANKG_EMBED_SIDECAR_CMD")
	if raw := lookup("LEANKG_EMBED_SIDECAR_ARGS"); raw != "" {
		args, err := embed.SplitArgs(raw)
		if err != nil {
			return sc, fmt.Errorf("embed: invalid LEANKG_EMBED_SIDECAR_ARGS: %w", err)
		}
		sc.Args = args
	} else {
		home, _ := os.UserHomeDir()
		sc.Args = embed.DefaultSidecarArgs(home)
	}
	if raw := lookup("LEANKG_EMBED_SIDECAR_PORT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 65535 {
			return sc, fmt.Errorf("embed: invalid LEANKG_EMBED_SIDECAR_PORT %q", raw)
		}
		sc.Port = n
	}
	ready, err := seconds(lookup, "LEANKG_EMBED_SIDECAR_READY_SECS")
	if err != nil {
		return sc, err
	}
	sc.ReadyTimeout = ready
	return sc, nil
}
