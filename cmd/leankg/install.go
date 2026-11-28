// `leankg install --target <client>` (FR-ZCP-04): one-shot client setup —
// write the client's MCP config and optionally register the CWD hook.
// The stdio entry contract matches `leankg connect` projectless: command is
// the current executable + ["serve", "--stdio"] with no --project flag (the
// server resolves the project from its process cwd); opts.Project is the
// escape hatch and the only way a --project flag is emitted.
package main

import (
	"errors"
	"os"

	"github.com/FreePeak/LeanKG/internal/cliexit"
)

// errHookOnlyScope refuses --hook-only with a flag it cannot honour: it writes
// only the memory hook, so --register-cwd, --http, --url and --project have
// nothing to write. cmdInstall and Install share this one message.
var errHookOnlyScope = errors.New("--hook-only needs --register-memory and no --register-cwd, --http, --url or --project")

// checkHookOnlyScope returns the usage refusal (exit 2) when opts sets HookOnly
// with a flag outside its scope, and nil otherwise.
func checkHookOnlyScope(opts InstallOptions) error {
	if opts.HookOnly && (!opts.RegisterMemory || opts.RegisterCWD || opts.HTTP || opts.URL != "" || opts.Project != "") {
		return cliexit.Usage(errHookOnlyScope)
	}
	return nil
}

// InstallOptions tunes Install.
type InstallOptions struct {
	HTTP           bool   // advertise a remote HTTP transport instead of stdio
	URL            string // remote MCP endpoint URL (required when HTTP)
	Project        string // optional --project escape hatch (stdio only)
	RegisterCWD    bool   // also register the SessionStart index hook (claude-code only)
	RegisterMemory bool   // also register the SessionStart memory hook (claude-code only)
	HookOnly       bool   // with RegisterMemory: register only that hook and write no MCP entry
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
// homeDir and, when opts.RegisterCWD or opts.RegisterMemory is set, registers
// the matching Claude Code SessionStart hook. With opts.HookOnly it writes only
// the memory hook. Unknown targets are rejected with the valid set named.
func Install(homeDir, target string, opts InstallOptions) error {
	if err := checkHookOnlyScope(opts); err != nil {
		return err
	}
	if opts.HookOnly {
		return RegisterMemory(homeDir, target, CurrentCommand())
	}
	cfg := Config{Mode: "stdio", Exe: CurrentCommand(), Project: opts.Project}
	if opts.HTTP {
		cfg.Mode, cfg.Exe, cfg.URL = "http", "", opts.URL
	}
	if err := WriteClient(homeDir, target, cfg); err != nil {
		return err
	}
	if opts.RegisterCWD {
		if err := RegisterCWD(homeDir, target, cfg.Exe); err != nil {
			return err
		}
	}
	if opts.RegisterMemory {
		return RegisterMemory(homeDir, target, cfg.Exe)
	}
	return nil
}
