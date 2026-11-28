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
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
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

// tokenEnvPattern is the only shape --token-env accepts: a bare variable name.
var tokenEnvPattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// authHeaderSyntax is each client's documented env-reference form for the
// Authorization header of a remote entry; %s is the variable name. The client
// expands it at launch, so the token value never lands in the config file.
// Codex is not here: it takes bearer_token_env_var instead (see codexSectionLines).
var authHeaderSyntax = map[string]string{
	ClientClaudeCode: "Bearer ${%s}",
	ClientCursor:     "Bearer ${env:%s}",
	ClientOpencode:   "Bearer {env:%s}",
}

// supportsTokenEnv reports whether client has a verified env-reference form.
func supportsTokenEnv(client string) bool {
	_, ok := authHeaderSyntax[client]
	return ok || client == ClientCodex
}

// Config describes the leankg entry written into a client config.
type Config struct {
	Mode     string // "stdio" (spawn Exe) or "http" (remote URL)
	Exe      string // stdio executable path; "" falls back to bare "leankg" (PATH)
	URL      string // remote MCP endpoint URL (bare); required in http mode
	Project  string // optional --project escape hatch; stdio only, ignored for http
	TokenEnv string // optional env var name the remote entry reads its bearer from; http only
}

func (c Config) validate() error {
	if c.TokenEnv != "" {
		if c.Mode != "http" {
			return errors.New("--token-env applies to remote (--http) entries only")
		}
		if !tokenEnvPattern.MatchString(c.TokenEnv) {
			return fmt.Errorf("--token-env %q: want letters, digits and underscore only", c.TokenEnv)
		}
	}
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

// bareURL drops any query/fragment from a remote URL. Stdio resolves the
// project from the process cwd (FR-ZCP-01 clause 1); HTTP callers name the
// project as the `project` tool argument (roots/list dropped 2026-11-18, see
// docs/mcp-multi-project-scoping.md section 4). An absolute path
// baked into a shared config is stale the moment that config leaves the
// machine it was written on. Measured on this machine 2026-10-11: three
// configs carrying `?project=/workspace-other` — a Docker mount path that
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
	if cfg.TokenEnv != "" && !supportsTokenEnv(client) {
		return fmt.Errorf("--token-env: %s has no documented env-reference header syntax; write it without --token-env", client)
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
		var entry map[string]any
		switch client {
		case ClientOpencode:
			entry = map[string]any{"type": "remote", "url": cfg.mergedURL(), "enabled": true}
		case ClientOmp:
			entry = map[string]any{"type": "http", "url": cfg.mergedURL(), "enabled": true}
		default: // claude-code, cursor, gemini
			entry = map[string]any{"type": "http", "url": cfg.mergedURL()}
		}
		if cfg.TokenEnv != "" {
			entry["headers"] = map[string]any{"Authorization": fmt.Sprintf(authHeaderSyntax[client], cfg.TokenEnv)}
		}
		return entry
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
// is an error — a broken user config is never clobbered silently — and so is
// valid JSON that is not an object (null, an array, a scalar).
func readJSONConfig(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("%s: not valid JSON: %w", path, err)
	}
	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: settings file is not a JSON object", path)
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
	data, err := encodeJSON(root)
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

// encodeJSON renders root as pretty-printed JSON (two-space indent, trailing newline).
func encodeJSON(root map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// resolveSymlink returns the file a symlink at path points to, so a write through
// the link replaces the target and keeps the link. A path that is not a symlink,
// or does not exist, is returned unchanged.
func resolveSymlink(path string) (string, error) {
	fi, err := os.Lstat(path)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return path, nil
	}
	return filepath.EvalSymlinks(path)
}

// atomicWrite writes data via a temp sibling then renames over path. A symlink at
// path is written through (the temp sibling goes beside the target). A file that
// already exists keeps its mode; a new file gets 0644.
func atomicWrite(path string, data []byte) error {
	path, err := resolveSymlink(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
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
	if err := os.Chmod(name, mode); err != nil {
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
		lines := []string{"url = " + strconv.Quote(cfg.mergedURL())}
		if cfg.TokenEnv != "" {
			lines = append(lines, "bearer_token_env_var = "+strconv.Quote(cfg.TokenEnv))
		}
		return lines
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
// Claude Code SessionStart hooks (register-cwd, register-memory)
// ---------------------------------------------------------------------------

// sessionHook describes one SessionStart hook the merge code owns. suffixes
// are the argument tails of the command: suffixes[0] is the current form and
// the rest are generated legacy forms that are replaced in place. The other
// fields shape the entry written for the hook.
type sessionHook struct {
	event    string // hook event key under "hooks", e.g. SessionStart
	suffixes []string
	matcher  string // "" writes no matcher key
	async    bool
	timeout  int // seconds; 0 writes no timeout key
}

// indexHook is the hook RegisterCWD owns. The suffix quotes $CLAUDE_PROJECT_DIR,
// which Claude Code expands at hook runtime, so project paths with spaces
// survive the shell. --hook keeps the run cheap and safe to fire on every
// session start (see index's hook mode). The legacy suffix is the pre-async form.
var indexHook = sessionHook{
	event:    "SessionStart",
	suffixes: []string{` index --hook "$CLAUDE_PROJECT_DIR"`, ` index "$CLAUDE_PROJECT_DIR"`},
	matcher:  "startup|resume",
	async:    true,
}

// memoryHooks are the hooks RegisterMemory owns, all running `memory hook`:
// a synchronous run with a 10 second budget at session start, an async run
// after each Write or Edit, and a synchronous run at session end.
var memoryHooks = []sessionHook{
	{event: "SessionStart", suffixes: []string{" memory hook"}, timeout: 10},
	{event: "PostToolUse", suffixes: []string{" memory hook"}, matcher: "Write|Edit", async: true},
	{event: "SessionEnd", suffixes: []string{" memory hook"}, timeout: 10},
}

// command renders the hook command for exe. exe is the resolved current
// executable (CurrentCommand()); bare "leankg" would depend on the hook's PATH
// and silently no-op when it differs. An empty exe falls back to "leankg". The
// shell runs the command, so an exe with a space or other special character is
// quoted (shellQuote).
func (h sessionHook) command(exe string) string {
	if exe == "" {
		exe = "leankg"
	}
	return shellQuote(exe) + h.suffixes[0]
}

// shellQuote returns s as one POSIX shell word. A word of plain path characters
// is returned unchanged, so ordinary paths keep their command text; any other
// word is single-quoted.
func shellQuote(s string) string {
	if !strings.ContainsFunc(s, needsShellQuote) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsShellQuote reports whether r would not be read literally in an unquoted
// shell word.
func needsShellQuote(r rune) bool {
	return !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("/._-+:,@%", r))
}

// shellUnquote returns the word that a shellQuote form stands for, and whether s
// was single-quoted. Any other s is returned unchanged with false.
func shellUnquote(s string) (string, bool) {
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return s, false
	}
	return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'"), true
}

// hookCommand renders the index hook command for exe (see indexHook).
func hookCommand(exe string) string {
	return indexHook.command(exe)
}

// owns reports whether cmd is a hook this spec manages: the command it writes
// now, or a generated command in any suffix form. A generated command names the
// leankg binary, either as a single-quoted word (any path) or as an unquoted
// whitespace-free word whose base name is leankg (or leankg.exe). An earlier
// release wrote an unquoted command for an executable path with whitespace, so
// that unquoted form is owned when it names the executable command writes.
// Anything else, including another tool's ` memory hook`, is the user's own
// wiring and is never matched.
func (h sessionHook) owns(cmd, command string) bool {
	if cmd == command {
		return true
	}
	own, _ := shellUnquote(strings.TrimSuffix(command, h.suffixes[0]))
	for _, suffix := range h.suffixes {
		word, ok := strings.CutSuffix(cmd, suffix)
		if !ok {
			continue
		}
		if exe, quoted := shellUnquote(word); quoted {
			if isLeankgBinary(exe) {
				return true
			}
		} else if word == own || (isLeankgBinary(word) && !strings.ContainsAny(word, " \t\r\n")) {
			return true
		}
	}
	return false
}

// isLeankgBinary reports whether the base name of exe is leankg or leankg.exe.
func isLeankgBinary(exe string) bool {
	base := filepath.Base(exe)
	return base == "leankg" || base == "leankg.exe"
}

// entry is the SessionStart entry the spec writes for exe.
func (h sessionHook) entry(exe string) map[string]any {
	hook := map[string]any{"type": "command", "command": h.command(exe)}
	if h.async {
		hook["async"] = true
	}
	if h.timeout > 0 {
		hook["timeout"] = h.timeout
	}
	entry := map[string]any{"hooks": []any{hook}}
	if h.matcher != "" {
		entry["matcher"] = h.matcher
	}
	return entry
}

// current reports whether obj/hook is the form entry(exe) writes for command:
// the same matcher (or none), the same async flag, and the same timeout when
// the spec sets one.
func (h sessionHook) current(obj, hook map[string]any, command string) bool {
	cmd, _ := hook["command"].(string)
	if cmd != command || (hook["async"] == true) != h.async {
		return false
	}
	matcher, has := obj["matcher"]
	if has != (h.matcher != "") || (has && matcher != h.matcher) {
		return false
	}
	return h.timeout == 0 || hook["timeout"] == float64(h.timeout)
}

// onlyCurrent reports whether sessionStart holds exactly one hook the spec
// manages and it is the form entry(exe) writes. Nothing else needs changing then.
func (h sessionHook) onlyCurrent(sessionStart []any, command string) bool {
	owned, current := 0, false
	for _, item := range sessionStart {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := obj["hooks"].([]any)
		for _, x := range inner {
			hook, ok := x.(map[string]any)
			if !ok {
				continue
			}
			cmd, _ := hook["command"].(string)
			if !h.owns(cmd, command) {
				continue
			}
			owned++
			current = h.current(obj, hook, command)
		}
	}
	return owned == 1 && current
}

// withoutOwned reports whether obj holds a hook the spec manages and, when it
// does, returns the entry with those hooks removed. The returned entry is nil
// when no other hook remains, so the caller drops it entirely.
func (h sessionHook) withoutOwned(obj map[string]any, command string) (map[string]any, bool) {
	inner, ok := obj["hooks"].([]any)
	if !ok {
		return nil, false
	}
	var foreign []any
	owned := false
	for _, x := range inner {
		if hook, ok := x.(map[string]any); ok {
			if cmd, _ := hook["command"].(string); h.owns(cmd, command) {
				owned = true
				continue
			}
		}
		foreign = append(foreign, x)
	}
	if !owned {
		return nil, false
	}
	if len(foreign) == 0 {
		return nil, true
	}
	kept := maps.Clone(obj)
	kept["hooks"] = foreign
	return kept, true
}

// settingsZone is GMT+7 (Asia/Ho_Chi_Minh). A fixed offset is exact here
// (Vietnam observes no DST) and needs no tzdata at runtime.
var settingsZone = time.FixedZone("GMT+7", 7*60*60)

// backupStamp renders now for a settings backup name, in GMT+7.
func backupStamp(now time.Time) string {
	return now.In(settingsZone).Format("20060102-150405")
}

// claudeSettingsPath is the Claude Code settings.json: $CLAUDE_CONFIG_DIR, else
// <homeDir>/.claude (claudecode.ConfigDir).
func claudeSettingsPath(homeDir string) string {
	return filepath.Join(claudecode.ConfigDir(homeDir), "settings.json")
}

// hookEvents returns root["hooks"] as a map, creating it when absent. A
// non-object value is an error naming path.
func hookEvents(root map[string]any, path string) (map[string]any, error) {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		if root["hooks"] != nil {
			return nil, fmt.Errorf(`"hooks" in %s is not a JSON object`, path)
		}
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	return hooks, nil
}

// hookEventList returns hooks[event] as a list (nil when absent). A non-array
// value is an error naming path.
func hookEventList(hooks map[string]any, event, path string) ([]any, error) {
	list, ok := hooks[event].([]any)
	if !ok && hooks[event] != nil {
		return nil, fmt.Errorf(`"hooks.%s" in %s is not a JSON array`, event, path)
	}
	return list, nil
}

// writeSettings writes root to path. When the file exists and its bytes change,
// the current bytes are first copied to path.leankg-bak-<stamp> so the change
// can be rolled back. An unchanged file is not rewritten.
func writeSettings(path string, root map[string]any) error {
	data, err := encodeJSON(root)
	if err != nil {
		return err
	}
	// The backup and the write both go to the file a settings symlink points to.
	path, err = resolveSymlink(path)
	if err != nil {
		return err
	}
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case bytes.Equal(old, data):
		return nil
	default:
		if err := backupSettings(path, old); err != nil {
			return err
		}
	}
	return atomicWrite(path, data)
}

// backupSettings copies old to a new backup beside path, with path's mode. A
// name already taken in the same second gets a -N suffix, so an earlier backup
// is never overwritten.
func backupSettings(path string, old []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	base := path + ".leankg-bak-" + backupStamp(time.Now())
	dst := base
	for n := 2; ; n++ {
		if _, err := os.Stat(dst); err != nil {
			break
		}
		dst = fmt.Sprintf("%s-%d", base, n)
	}
	return os.WriteFile(dst, old, mode)
}

// registerSessionHook is the one hook merge every register flag uses. For each
// spec it merges-or-creates the hook under hooks[spec.event] in the Claude Code
// settings.json (claudecode.ConfigDir: $CLAUDE_CONFIG_DIR, else <homeDir>/.claude),
// preserving every other hook and setting. Idempotent: when every entry is
// already the only managed hook present the file is left untouched. Generated
// entries (the current or a legacy command form, duplicates included) are
// replaced in place by one current entry; a customised command is user wiring
// and is left alone. Only claude-code supports hooks today; other clients
// return an error wrapping ErrUnsupportedClient. label names the caller in
// that error. exe is the resolved binary path to embed (empty falls back to
// "leankg"); the project itself is resolved at hook runtime.
func registerSessionHook(homeDir, client, label string, specs []sessionHook, exe string) error {
	if client != ClientClaudeCode {
		return fmt.Errorf("%s for %s: %w", label, client, ErrUnsupportedClient)
	}
	path := claudeSettingsPath(homeDir)
	root, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	hooks, err := hookEvents(root, path)
	if err != nil {
		return err
	}
	changed := false
	for _, h := range specs {
		list, err := hookEventList(hooks, h.event, path)
		if err != nil {
			return err
		}
		command := h.command(exe)
		if h.onlyCurrent(list, command) {
			continue // identical hook already present, and no duplicate or legacy copy
		}
		next := make([]any, 0, len(list)+1)
		placed := false
		for _, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				next = append(next, item)
				continue
			}
			kept, owned := h.withoutOwned(obj, command)
			if !owned {
				next = append(next, item)
				continue
			}
			if kept != nil {
				next = append(next, kept) // its foreign hooks survive in place
			}
			if !placed {
				next = append(next, h.entry(exe))
				placed = true
			}
		}
		if !placed {
			next = append(next, h.entry(exe))
		}
		hooks[h.event] = next
		changed = true
	}
	if !changed {
		return nil
	}
	return writeSettings(path, root)
}

// unregisterSessionHooks removes every hook the specs own from the Claude Code
// settings.json, leaving foreign hooks and settings in place. An event left
// empty is dropped, and so is an emptied hooks object. A file with nothing to
// remove is not rewritten. Client and error rules match registerSessionHook.
func unregisterSessionHooks(homeDir, client, label string, specs []sessionHook, exe string) error {
	if client != ClientClaudeCode {
		return fmt.Errorf("%s for %s: %w", label, client, ErrUnsupportedClient)
	}
	path := claudeSettingsPath(homeDir)
	root, err := readJSONConfig(path)
	if err != nil {
		return err
	}
	hooks, err := hookEvents(root, path)
	if err != nil {
		return err
	}
	changed := false
	for _, h := range specs {
		list, err := hookEventList(hooks, h.event, path)
		if err != nil {
			return err
		}
		command := h.command(exe)
		var next []any
		owned := false
		for _, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				next = append(next, item)
				continue
			}
			kept, found := h.withoutOwned(obj, command)
			if !found {
				next = append(next, item)
				continue
			}
			owned = true
			if kept != nil {
				next = append(next, kept)
			}
		}
		if !owned {
			continue
		}
		changed = true
		if len(next) == 0 {
			delete(hooks, h.event)
		} else {
			hooks[h.event] = next
		}
	}
	if !changed {
		return nil
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	}
	return writeSettings(path, root)
}

// RegisterCWD merges-or-creates the Claude Code SessionStart hook that
// attaches the project on session start:
//
//	{"matcher": "startup|resume", "hooks": [{"type": "command", "command": exe + ` index --hook "$CLAUDE_PROJECT_DIR"`, "async": true}]}
//
// See registerSessionHook for the merge rules. Only claude-code supports it.
func RegisterCWD(homeDir, client, exe string) error {
	return registerSessionHook(homeDir, client, "register-cwd", []sessionHook{indexHook}, exe)
}

// RegisterMemory merges-or-creates the three memory hooks (see memoryHooks):
// SessionStart and SessionEnd with timeout 10, and PostToolUse on Write|Edit
// (async). Each runs exe + " memory hook". See registerSessionHook for the
// merge rules. Only claude-code supports it.
func RegisterMemory(homeDir, client, exe string) error {
	return registerSessionHook(homeDir, client, "register-memory", memoryHooks, exe)
}

// UnregisterMemory removes the memory hooks RegisterMemory writes, and only
// those. See unregisterSessionHooks. Only claude-code supports it.
func UnregisterMemory(homeDir, client, exe string) error {
	return unregisterSessionHooks(homeDir, client, "unregister-memory", memoryHooks, exe)
}
