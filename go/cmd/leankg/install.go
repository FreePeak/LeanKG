// `leankg install --target <client>` (FR-ZCP-04): one-shot client setup —
// write the client's MCP config and optionally register the CWD hook.
// The stdio entry contract matches `leankg connect` projectless: command is
// the current executable + "mcp-stdio" with no --project flag (the server
// resolves the project from its process cwd); opts.Project is the escape
// hatch and the only way a --project flag is emitted.
package main

import "os"

// InstallOptions tunes Install.
type InstallOptions struct {
	HTTP        bool   // advertise a remote HTTP transport instead of stdio
	URL         string // remote MCP endpoint URL (required when HTTP)
	Project     string // optional --project escape hatch (stdio only)
	RegisterCWD bool   // also register the SessionStart hook (claude-code only)
}

// CurrentCommand returns the stdio command path: the current executable when
// resolvable, else bare "leankg" (relying on PATH).
func CurrentCommand() string {
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "leankg"
}

// Install writes the leankg MCP entry for target (one of Clients()) under
// homeDir and, when opts.RegisterCWD is set, registers the Claude Code
// SessionStart hook. Unknown targets are rejected with the valid set named.
func Install(homeDir, target string, opts InstallOptions) error {
	cfg := Config{Mode: "stdio", Exe: CurrentCommand(), Project: opts.Project}
	if opts.HTTP {
		cfg.Mode, cfg.Exe, cfg.URL = "http", "", opts.URL
	}
	if err := WriteClient(homeDir, target, cfg); err != nil {
		return err
	}
	if opts.RegisterCWD {
		return RegisterCWD(homeDir, target, "")
	}
	return nil
}
