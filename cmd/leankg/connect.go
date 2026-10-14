// Client-integration writers (FR-ZCP-04; Rust src/connect parity): write or
// merge the LeanKG MCP server entry into a supported AI client's config file
// so agents can talk to LeanKG without hand-editing JSON/TOML. Idempotent:
// re-running merges rather than duplicates and preserves every sibling key
// and unknown field of the existing config.
//
// All writers take an explicit homeDir so tests stay hermetic — the CLI
// layer resolves the real home (os.UserHomeDir) and passes it in; the
// writers never touch the environment for it.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// serverKey is the key this tool owns inside a client's MCP server map.
const serverKey = "leankg"

// ErrUnsupportedClient is returned when a client has no hook mechanism for
// automatic CWD registration (only claude-code does today). Use errors.Is.
var ErrUnsupportedClient = errors.New("client has no hook mechanism; register the project manually")

// Supported client names (order matches the CLI listing).
const (
	ClientClaudeCode = "claude-code"
	ClientCursor     = "cursor"
	ClientCodex      = "codex"
	ClientGemini     = "gemini"
	ClientOpencode   = "opencode"
	ClientOmp        = "omp"
	ClientXdev       = "xdev"
)

// Clients returns the supported client names.
func Clients() []string {
	return []string{ClientClaudeCode, ClientCursor, ClientCodex, ClientGemini, ClientOpencode, ClientOmp, ClientXdev}
}

// Config describes the leankg entry written into a client config.
type Config struct {
	Mode    string // "stdio" (spawn Exe) or "http" (remote URL)
	Exe     string // stdio executable path; "" falls back to bare "leankg" (PATH)
	URL     string // remote MCP endpoint URL (bare); required in http mode
	Project string // optional --project escape hatch; stdio only, ignored for http
}

func (c Config) validate() error {
	switch c.Mode {
	case "stdio":
		return nil
	case "http":
		if c.URL == "" {
			return errors.New("http mode requires a URL")
		}
		return nil
	default:
		return fmt.Errorf(`config mode must be "stdio" or "http", got %q`, c.Mode)
	}
}

// clientConfigPath returns the config file the writer owns for client.
func clientConfigPath(homeDir, client string) (string, error) {
	switch client {
	case ClientClaudeCode:
		return filepath.Join(homeDir, ".claude.json"), nil
	case ClientCursor:
		return filepath.Join(homeDir, ".cursor", "mcp.json"), nil
	case ClientCodex:
		return filepath.Join(homeDir, ".codex", "config.toml"), nil
	case ClientGemini:
		return filepath.Join(homeDir, ".gemini", "settings.json"), nil
	case ClientOpencode:
		return filepath.Join(homeDir, ".config", "opencode", "opencode.json"), nil
	case ClientOmp:
		return filepath.Join(homeDir, ".omp", "agent", "mcp.json"), nil
	case ClientXdev:
		// xdev reads one MCP config, a YAML file, and has no `xdev mcp`
		// subcommand of its own (docs/decisions/leankg-memory-backend K9).
		return filepath.Join(homeDir, ".xdev", "agent", "mcp.yml"), nil
	default:
		return "", fmt.Errorf("unknown client %q (valid: %s)", client, strings.Join(Clients(), ", "))
	}
}

// jsonContainer returns the map key holding MCP servers in client's JSON
// config: "mcpServers" everywhere except opencode, which uses "mcp".
func jsonContainer(client string) string {
	if client == ClientOpencode {
		return "mcp"
	}
	return "mcpServers"
}

// bareURL drops any query/fragment from a remote URL. Per-request project
// resolution (FR-ZCP-01: stdio process cwd; HTTP roots/list) makes a
// `?project=` suffix unnecessary on the happy path, and an absolute path
// baked into a shared config is stale the moment that config leaves the
// machine it was written on. Measured on this machine 2026-10-11: three
// configs carrying `?project=/workspace-freepeak` — a Docker mount path that
// exists nowhere on the host — failed every initialize with `unknown
// project`, so the clients reading them dropped leankg from their tool list.
// Stripping here means the one caller that can still pass such a URL cannot
// reproduce that failure.
func bareURL(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u // not a URL we can inspect: hand it through untouched
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// mergedURL returns the URL every writer emits: bare, per FR-ZCP-04.
func (c Config) mergedURL() string { return bareURL(c.URL) }

// WriteClient writes or merges the leankg entry for client under homeDir.
// JSON configs are read-modify-write (every sibling key and unknown field is
// preserved); missing files and directories are created. Re-running with the
// same Config produces byte-identical output.
func WriteClient(homeDir, client string, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	path, err := clientConfigPath(homeDir, client)
	if err != nil {
		return err
	}
	switch client {
	case ClientCodex:
		return writeCodexTOML(path, cfg)
	case ClientXdev:
		return writeXdevYAML(path, cfg)
	}
	return writeJSONEntry(path, jsonContainer(client), jsonEntry(client, cfg))
}

// jsonEntry builds the client-specific JSON value for the leankg entry.
// Stdio entries carry NO --project flag unless Project is set (FR-ZCP-04
// URL contract: the server resolves the project from its process cwd; an
// explicit project path is the escape hatch and the only flag source).
func jsonEntry(client string, cfg Config) map[string]any {
	if cfg.Mode == "http" {
		switch client {
		case ClientOpencode:
			return map[string]any{"type": "remote", "url": cfg.mergedURL(), "enabled": true}
		case ClientOmp:
			return map[string]any{"type": "http", "url": cfg.mergedURL(), "enabled": true}
		default: // claude-code, cursor, gemini
			return map[string]any{"type": "http", "url": cfg.mergedURL()}
		}
	}
	exe := cfg.Exe
	if exe == "" {
		exe = "leankg"
	}
	args := stdioArgs(cfg.Project, "")
	switch client {
	case ClientOpencode:
		return map[string]any{"type": "local", "command": append([]string{exe}, args...), "enabled": true}
	case ClientOmp:
		return map[string]any{"type": "stdio", "command": exe, "args": args, "enabled": true}
	default: // claude-code, cursor, gemini: no type key on stdio
		return map[string]any{"command": exe, "args": args}
	}
}

// stdioArgs returns the spawn args after the entry command: `serve --stdio
// --memory`, this binary's MCP-over-stdio mode. --memory is unconditional: the
// markdown memory layer is part of the product, and leaving it off made every
// agent's memory write fail with "memory not initialized" (internal/core).
// Relative project paths are made absolute against the current directory
// (Rust parity). When dbPath is non-empty (standalone-DB mode, no checkout
// required), the project directory is not resolved — the user owns the DB path
// via LEANKG_DB_PATH/env.
func stdioArgs(project, dbPath string) []string {
	args := []string{"serve", "--stdio", "--memory"}
	if dbPath != "" {
		args = append(args, "--db", dbPath)
	}
	if project == "" {
		return args
	}
	return append(args, "--project", absolutize(project))
}

// absolutize makes p absolute against the process working directory.
func absolutize(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, p)
	}
	return p
}

// ---------------------------------------------------------------------------
// Shared JSON plumbing (claude-code / cursor / gemini / opencode / omp)
// ---------------------------------------------------------------------------

// readJSONConfig reads a JSON config; a missing file reads as an empty map
// so a fresh machine gets a minimal valid config. Existing-but-invalid JSON
// is an error — a broken user config is never clobbered silently.
func readJSONConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON: %w", path, err)
	}
	return root, nil
}

// writeJSONEntry merges the leankg entry into the container map at path and
// writes the config back atomically with two-space indentation.
func writeJSONEntry(path, container string, entry map[string]any) error {
	root, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	servers, ok := root[container].(map[string]any)
	if !ok {
		if root[container] != nil {
			return fmt.Errorf("%q in %s is not a JSON object", container, path)
		}
		servers = map[string]any{}
		root[container] = servers
	}
	servers[serverKey] = entry
	return writeJSONFile(path, root)
}

// writeJSONFile creates parent directories and atomically writes root as
// pretty-printed JSON (two-space indent, trailing newline).
func writeJSONFile(path string, root map[string]any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return err
	}
	return atomicWrite(path, buf.Bytes())
}

// atomicWrite writes data via a temp sibling then renames over path.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".leankg-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Codex TOML
// ---------------------------------------------------------------------------

// codexSectionHeader is the TOML table the writer owns in the Codex config.
const codexSectionHeader = "[mcp_servers.leankg]"

// writeCodexTOML best-effort replaces or appends the [mcp_servers.leankg]
// section in the Codex TOML config.
//
// ponytail: naive line-based TOML handling per contract — comments, key
// order, and sibling tables outside the leankg section survive; the section
// itself is regenerated wholesale so re-runs are byte-identical. Ceiling: a
// multi-line value inside the section (or a table header embedded in a
// string) confuses the scan — upgrade to a TOML editor if Codex configs
// ever need structure-aware edits.
func writeCodexTOML(path string, cfg Config) error {
	var text string
	if data, err := os.ReadFile(path); err == nil {
		text = string(data)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	out := replaceTOMLSection(text, codexSectionHeader, codexSectionLines(cfg))
	return atomicWrite(path, []byte(out))
}

// codexSectionLines builds the key = value lines under the leankg header.
// Codex takes the full spawn command as a single TOML string array.
func codexSectionLines(cfg Config) []string {
	if cfg.Mode == "http" {
		return []string{"url = " + strconv.Quote(cfg.mergedURL())}
	}
	// Codex spawns the full command array: exe first, then args.
	// Empty Exe falls back to bare "leankg" (PATH), matching jsonEntry.
	exe := cfg.Exe
	if exe == "" {
		exe = "leankg"
	}
	args := append([]string{exe}, stdioArgs(cfg.Project, "")...)
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	return []string{"command = [" + strings.Join(quoted, ", ") + "]"}
}

// replaceTOMLSection replaces the table at header (up to the next table
// header or EOF) with the given lines, or appends the section. Output ends
// with exactly one newline; re-running is byte-identical.
func replaceTOMLSection(text, header string, lines []string) string {
	section := header + "\n" + strings.Join(lines, "\n") + "\n"
	raw := strings.Split(text, "\n")
	start := -1
	for i, line := range raw {
		if strings.TrimSpace(line) == header {
			start = i
			break
		}
	}
	if start < 0 {
		for len(raw) > 0 && strings.TrimSpace(raw[len(raw)-1]) == "" {
			raw = raw[:len(raw)-1]
		}
		if len(raw) == 0 {
			return section
		}
		return strings.Join(raw, "\n") + "\n\n" + section
	}
	end := len(raw)
	for i := start + 1; i < len(raw); i++ {
		t := strings.TrimSpace(raw[i])
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			end = i
			break
		}
	}
	head := strings.TrimRight(strings.Join(raw[:start], "\n"), "\n")
	tail := strings.TrimLeft(strings.Join(raw[end:], "\n"), "\n")
	if head != "" {
		section = head + "\n\n" + section
	}
	if tail != "" {
		section += "\n" + tail
	}
	return section
}

// ---------------------------------------------------------------------------
// Claude Code SessionStart hook (register-cwd)
// ---------------------------------------------------------------------------

// hookMatcher limits the SessionStart hook to a fresh start or a resume.
const hookMatcher = "startup|resume"

// hookSuffix and legacyHookSuffix are the argument tails of the hook command.
// legacyHookSuffix is the pre-async form; RegisterCWD replaces it in place.
const (
	hookSuffix       = ` index --hook "$CLAUDE_PROJECT_DIR"`
	legacyHookSuffix = ` index "$CLAUDE_PROJECT_DIR"`
)

// hookCommand renders the SessionStart hook the writer owns. exe is the
// resolved current executable (CurrentCommand()); bare "leankg" would depend
// on the hook's PATH and silently no-op when it differs. $CLAUDE_PROJECT_DIR
// is expanded by Claude Code at hook runtime and quoted, so project paths
// with spaces survive the shell. --hook keeps the run cheap and safe to fire
// on every session start (see index's hook mode).
func hookCommand(exe string) string {
	if exe == "" {
		exe = "leankg"
	}
	return exe + hookSuffix
}

// isGeneratedCommand reports whether cmd is a hook RegisterCWD wrote: an
// executable token with no whitespace followed by either suffix. Anything
// with extra tokens is user-customised and never matched.
func isGeneratedCommand(cmd string) bool {
	for _, suffix := range []string{hookSuffix, legacyHookSuffix} {
		if exe, ok := strings.CutSuffix(cmd, suffix); ok && exe != "" && !strings.ContainsAny(exe, " \t\r\n") {
			return true
		}
	}
	return false
}

// hookEntry is the SessionStart entry RegisterCWD writes for exe.
func hookEntry(exe string) map[string]any {
	return map[string]any{
		"matcher": hookMatcher,
		"hooks":   []any{map[string]any{"type": "command", "command": hookCommand(exe), "async": true}},
	}
}

// ownsCommand reports whether cmd is a hook RegisterCWD manages: the command it
// writes now (also when the executable path holds whitespace), or a generated
// command in either form.
func ownsCommand(cmd, command string) bool {
	return cmd == command || isGeneratedCommand(cmd)
}

// onlyCurrentHook reports whether sessionStart holds exactly one hook
// RegisterCWD manages and it is the hook hookEntry(exe) would write: same
// matcher, and running command asynchronously. Nothing else needs changing then.
func onlyCurrentHook(sessionStart []any, command string) bool {
	owned, current := 0, false
	for _, item := range sessionStart {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := obj["hooks"].([]any)
		for _, h := range inner {
			hook, ok := h.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hook["command"].(string)
			if !ownsCommand(cmd, command) {
				continue
			}
			owned++
			current = cmd == command && hook["async"] == true && obj["matcher"] == hookMatcher
		}
	}
	return owned == 1 && current
}

// withoutGeneratedHooks reports whether obj holds a hook RegisterCWD manages
// and, when it does, returns the entry with those hooks removed. The returned
// entry is nil when no other hook remains, so the caller drops it entirely.
func withoutGeneratedHooks(obj map[string]any, command string) (map[string]any, bool) {
	inner, ok := obj["hooks"].([]any)
	if !ok {
		return nil, false
	}
	var foreign []any
	generated := false
	for _, h := range inner {
		if hook, ok := h.(map[string]any); ok {
			if cmd, _ := hook["command"].(string); ownsCommand(cmd, command) {
				generated = true
				continue
			}
		}
		foreign = append(foreign, h)
	}
	if !generated {
		return nil, false
	}
	if len(foreign) == 0 {
		return nil, true
	}
	kept := maps.Clone(obj)
	kept["hooks"] = foreign
	return kept, true
}

// RegisterCWD merges-or-creates the Claude Code SessionStart hook that
// attaches the project on session start:
//
//	{"matcher": "startup|resume", "hooks": [{"type": "command", "command": exe + ` index --hook "$CLAUDE_PROJECT_DIR"`, "async": true}]}
//
// under hooks.SessionStart in <homeDir>/.claude/settings.json, preserving
// every other hook and setting. Idempotent: when that entry is already the only
// leankg hook present the file is left untouched. Generated leankg entries (the
// current or the legacy command form, duplicates included) are replaced in
// place by one current entry; a
// customised leankg command is user wiring and is left alone. Only claude-code
// supports hooks today; other clients return an error wrapping
// ErrUnsupportedClient. exe is the resolved binary path to embed (empty falls
// back to "leankg"); the project itself is resolved at hook runtime via
// $CLAUDE_PROJECT_DIR.
func RegisterCWD(homeDir, client, exe string) error {
	if client != ClientClaudeCode {
		return fmt.Errorf("register-cwd for %s: %w", client, ErrUnsupportedClient)
	}
	path := filepath.Join(homeDir, ".claude", "settings.json")
	root, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		if root["hooks"] != nil {
			return fmt.Errorf(`"hooks" in %s is not a JSON object`, path)
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	sessionStart, ok := hooks["SessionStart"].([]any)
	if !ok {
		if hooks["SessionStart"] != nil {
			return fmt.Errorf(`"hooks.SessionStart" in %s is not a JSON array`, path)
		}
		sessionStart = nil
	}
	command := hookCommand(exe)
	if onlyCurrentHook(sessionStart, command) {
		return nil // identical hook already present, and no duplicate or legacy copy
	}
	next := make([]any, 0, len(sessionStart)+1)
	placed := false
	for _, item := range sessionStart {
		obj, ok := item.(map[string]any)
		if !ok {
			next = append(next, item)
			continue
		}
		kept, generated := withoutGeneratedHooks(obj, command)
		if !generated {
			next = append(next, item)
			continue
		}
		if kept != nil {
			next = append(next, kept) // its non-leankg hooks survive in place
		}
		if !placed {
			next = append(next, hookEntry(exe))
			placed = true
		}
	}
	if !placed {
		next = append(next, hookEntry(exe))
	}
	hooks["SessionStart"] = next
	return writeJSONFile(path, root)
}
