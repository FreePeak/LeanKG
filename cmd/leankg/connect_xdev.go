// xdev YAML plumbing (FR-ZCP-14 K9)
//
// xdev is the one target that reads a YAML config, and it reads exactly one
// file — `$XDEV_AGENT_DIR/mcp.yml` — with no subcommand of its own to write
// it. Its schema differs from the JSON clients in two ways that matter: the
// server map is under `servers:`, and a server entry carries an `autoStart:`
// block (command/args/env/cwd/healthUrl/healthTimeoutSec) that restarts the
// server when the health probe fails. Omitting it wires a client that never
// heals a crashed server, which is the whole reason xdev has the block.
//
// Read-modify-write like the JSON path: every sibling server and every
// comment-free sibling key is preserved, a missing file is created, and a file
// that does not parse is refused rather than clobbered.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// writeXdevYAML merges the leankg entry into xdev's mcp.yml.
func writeXdevYAML(path string, cfg Config) error {
	root, err := readYAMLConfig(path)
	if err != nil {
		return err
	}
	servers, ok := root["servers"].(map[string]any)
	if !ok {
		if root["servers"] != nil {
			return fmt.Errorf("%q in %s is not a mapping", "servers", path)
		}
		servers = map[string]any{}
		root["servers"] = servers
	}
	servers["leankg"] = xdevEntry(cfg)
	return writeYAMLFile(path, root)
}

// readYAMLConfig reads a YAML config; a missing file reads as an empty map so
// a fresh machine gets a minimal valid config. Existing-but-invalid YAML is an
// error — a broken user config is never clobbered silently.
func readYAMLConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: not valid YAML: %w", path, err)
	}
	if root == nil { // an empty or comment-only file
		root = map[string]any{}
	}
	return root, nil
}

// writeYAMLFile creates parent directories and atomically writes root as YAML
// (two-space indent, trailing newline).
func writeYAMLFile(path string, root map[string]any) error {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes())
}

// xdevEntry builds xdev's server value. Both modes carry an autoStart block:
// a stdio entry is spawned directly by xdev, but the same entry is what a
// remote (HTTP) session points at when it finds the server down, so the block
// is what turns "leankg is not running" into a self-healing session.
func xdevEntry(cfg Config) map[string]any {
	exe := cfg.Exe
	if exe == "" {
		exe = "leankg"
	}
	if cfg.Mode != "http" {
		args := stdioArgs(cfg.Project, "")
		return map[string]any{
			"type":      "stdio",
			"command":   exe,
			"args":      args,
			"enabled":   true,
			"autoStart": xdevAutoStart(exe, args, cfg, healthURLForStdio(cfg)),
		}
	}
	// A remote entry never spawns a stdio command: the URL is the server. The
	// autoStart block is the local fallback for when that server is down, so it
	// carries the full serve argv instead.
	return map[string]any{
		"type":    "remote",
		"url":     cfg.URL,
		"enabled": true,
		"autoStart": xdevAutoStart(
			currentCommandForAutoStart(cfg), []string{"serve", "--http", ":9699", "--rest", ":9700", "--memory"},
			cfg, healthURLFor(cfg.URL),
		),
	}
}

// xdevAutoStart builds the autoStart block. Its field set is xdev's
// AutoStartConfig exactly — command, args, cwd, env, healthUrl,
// healthTimeoutSec, pidFile (verified against the consumer: xdev's
// internal/mcpclient/mcp.go AutoStartConfig, and the shipped binary's own
// `yaml:"..."` struct tags).
//
// Writing a key xdev does not define is worse than omitting one: yaml.Unmarshal
// drops it silently, so the file reads as configured while the loader never
// sees it. #467 shipped five such knobs (startupWaitSecs, logFile,
// restartOnExit, restartBackoffSec, maxRestarts) — dead config that promised
// restart supervision xdev does not have. TestXdevAutoStartSchema pins this key
// set so drift fails a test instead of a session.
//
// What each field actually does, measured against the shipped xdev binary
// instead of assumed: StartAuto is reachable (all four of its error strings
// are present) and reads command, args, cwd, env, healthUrl and
// healthTimeoutSec. pidFile is declared and NOT yet read — no runtime
// reference exists in the binary. It is written so the config is ready for the
// day xdev acts on it, and it is labelled as such rather than counted as a
// fix.
func xdevAutoStart(command string, args []string, cfg Config, healthURL string) map[string]any {
	return map[string]any{
		"command":          command,
		"args":             args,
		"cwd":              autoStartCWD(cfg),
		"healthUrl":        healthURL,
		"healthTimeoutSec": 30,
		"pidFile":          pidFilePath(cfg),
		"env":              sidecarPortEnv(),
	}
}

// pidFilePath is where xdev's AutoStartConfig.PidFile points: the path xdev
// would record the detached daemon at so a second xdev run waits for the
// existing one instead of starting a duplicate. xdev declares the field but
// does not read it yet, and leankg does not write the file either, so this is
// a ready path — not a live behaviour, and not something to claim as fixed.
func pidFilePath(cfg Config) string {
	project := cfg.Project
	if project == "" {
		if cwd, err := os.Getwd(); err == nil {
			project = cwd
		}
	}
	if project == "" {
		return ""
	}
	return filepath.Join(project, ".leankg", "leankg.pid")
}

// autoStartCWD is the directory the respawned server runs in: the configured
// project when one is pinned, else the process cwd.
func autoStartCWD(cfg Config) string {
	if cfg.Project != "" {
		return absolutize(cfg.Project)
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}

// healthURLFor derives the health probe from a remote URL. xdev derives
// <scheme://host/health> itself, so this is belt-and-braces: it keeps the
// answer identical when the URL carries a sub-path (a reverse proxy prefix).
func healthURLFor(url string) string {
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, "/mcp")
	return url + "/health"
}

// healthURLForStdio is the probe for a stdio entry: there is no URL, so the
// default loopback MCP address is what the server binds when spawned without
// --http.
func healthURLForStdio(cfg Config) string {
	return "http://127.0.0.1:9699/health"
}

// sidecarPortEnv pins the local embedding sidecar off port 8080, which a
// developer machine commonly already holds: without it llama-server dies
// during startup with "couldn't bind HTTP server socket" and the whole server
// exits before answering its health probe. Defaults to the port the wrapper
// and the live self-host use (9101).
func sidecarPortEnv() map[string]any {
	return map[string]any{"LEANKG_EMBED_SIDECAR_PORT": "9101"}
}

// currentCommandForAutoStart resolves the binary an autoStart block should
// respawn: the same resolution `leankg` itself uses (this binary's path).
func currentCommandForAutoStart(cfg Config) string {
	if cfg.Exe != "" {
		return cfg.Exe
	}
	return CurrentCommand()
}
