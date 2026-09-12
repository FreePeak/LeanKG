package lsp

// Bridge (Rust parity: src/lsp/bridge.rs). Caches one pooled client per
// (language, workspace root) pair via Manager, routes every request through
// the configured per-request timeout, and drops dead clients so the next call
// respawns. Workspace roots come from the config's workspace_root override or
// from the nearest manifest walking up from the file (nested-service
// monorepos each get their own server root).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/langs"
)

// Capability tiers of a resolved server (Rust parity: configured entry in
// leankg.yaml vs catalog auto-detection).
const (
	TierConfigured = "configured" // named in the project's lsp: block
	TierCatalog    = "catalog"    // catalog default filled by WithPrefabFallback
)

// Bridge is the manager-side facade over the client pool.
type Bridge struct {
	cfg          Config
	userSet      map[string]bool // catalog language ids named in leankg.yaml
	typedResolve string          // indexer.typed_resolve setting (default off)
	manager      *Manager
}

// NewBridge builds a bridge over an explicit config. Clients idle for 60s are
// reaped by the pool (idle shutdown); Shutdown MUST run before exit.
func NewBridge(cfg Config) *Bridge {
	cfg, userSet := cfg.normalized().WithPrefabFallback()
	return &Bridge{cfg: cfg, userSet: userSet, typedResolve: "off", manager: NewManager(defaultTTL)}
}

// FromLeanKGYAMLOrDefault loads <dir>/leankg.yaml's lsp block and
// indexer.typed_resolve setting, then fills every unconfigured catalog
// language (Rust parity: from_leankg_yaml_or_default). Missing file, missing
// block or unparsable block all degrade to the catalog plus typed_resolve
// "off".
func FromLeanKGYAMLOrDefault(dir string) *Bridge {
	cfg, typedResolve, _ := LoadProject(dir)
	b := NewBridge(cfg)
	b.typedResolve = typedResolve
	return b
}

// TypedResolve returns the effective indexer.typed_resolve setting ("off"
// when unset — Rust's default).
func (b *Bridge) TypedResolve() string { return b.typedResolve }

// Config returns the effective bridge configuration (after prefab fallback).
func (b *Bridge) Config() Config { return b.cfg }

// WorkspaceFor resolves the LSP workspace root for a file path: the config's
// explicit workspace_root wins, otherwise the nearest manifest parent.
func (b *Bridge) WorkspaceFor(path string) string {
	if b.cfg.WorkspaceRoot != "" {
		return b.cfg.WorkspaceRoot
	}
	return FindWorkspaceRoot(path)
}

// tagToCatalogID maps the Go engine's language tags (extension-without-dot,
// internal/index extLang values) to catalog language ids.
func tagToCatalogID(tag string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(tag))
	switch t {
	// Engine tags whose catalog id differs.
	case "ts", "tsx":
		return "typescript", true
	case "js", "jsx":
		return "javascript", true
	case "py":
		return "python", true
	case "md":
		return "markdown", true
	case "rs":
		return "rust", true
	// Engine tags that are catalog ids: the 13 original languages plus the
	// wave-1/wave-2 expansion (c/cpp, php, ruby, scala, lua, haskell, elixir,
	// crystal, elm, erlang, fsharp, nim, ocaml, sql, powershell, solidity,
	// zig) — every one of these has a Rust LSP registry row.
	case "go", "rust", "java", "kotlin", "swift", "objc", "dart",
		"c", "cpp", "php", "ruby", "scala", "lua", "haskell", "elixir",
		"crystal", "elm", "erlang", "fsharp", "nim", "ocaml", "sql",
		"powershell", "solidity", "zig":
		return t, true
	// Engine languages the Rust LSP registry never had a row for: map them so
	// the bridge reports a precise "no server for this language" instead of
	// treating the tag as unknown. Rust parity (registry.rs has no row for
	// csharp, perl, cuda, cypher, glsl, hlsl, qsharp, systemverilog, verilog).
	case "csharp", "perl", "cuda", "cypher", "glsl", "hlsl", "qsharp",
		"systemverilog", "verilog":
		return t, true
	}
	return "", false
}

// ServerFor resolves the effective server for a Go-engine language tag
// ("go", "ts", "tsx", "py", ...): the leankg.yaml entry when the user named
// the language (or a catalog alias of it), else the catalog default. tier
// reports which capability tier the server came from.
func (b *Bridge) ServerFor(tag string) (ServerConfig, string, bool) {
	id, ok := tagToCatalogID(tag)
	if !ok {
		spec, found := ForLanguage(tag)
		if !found {
			return ServerConfig{}, "", false
		}
		id = spec.Language
	}
	if sc, ok := b.cfg.Servers[id]; ok {
		tier := TierCatalog
		if b.userSet[id] {
			tier = TierConfigured
		}
		return sc, tier, true
	}
	return ServerConfig{}, "", false
}

// requestTimeout bounds every bridge request (Rust parity: timeout_ms,
// default 5000).
func (b *Bridge) requestTimeout() time.Duration {
	ms := b.cfg.TimeoutMS
	if ms <= 0 {
		ms = defaultTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// Resolve sends a position request (definition/references/hover) through the
// language server for filePath. found=false means no server is configured or
// spawnable for the language — the caller falls back (never an error by
// itself). err reports spawn/protocol failures. Rust parity: LspBridge.resolve.
func (b *Bridge) Resolve(ctx context.Context, language, filePath string, line, character int, req RequestKind) (ResolveResult, bool, error) {
	sc, _, ok := b.ServerFor(language)
	if !ok || !commandOnPath(sc.Command) {
		return ResolveResult{}, false, nil
	}
	root := b.WorkspaceFor(filePath)
	c, err := b.manager.GetCommand(ctx, langs.Language(language), sc.Command, sc.Args, root, sc.InitializationOptions)
	if err != nil {
		return ResolveResult{}, false, err
	}
	res, err := sendResolveRequest(ctx, c, req, filePath, line, character)
	if err != nil {
		// Drop the dead client so the next call respawns (Rust: entry = None).
		b.manager.Evict(langs.Language(language), root)
		return ResolveResult{}, false, err
	}
	return res, true, nil
}

// Shutdown closes every pooled client and stops the reaper. Idempotent.
func (b *Bridge) Shutdown() {
	b.manager.Shutdown()
}

// RequestKind selects the position request verb (Rust parity: LspRequest).
type RequestKind int

const (
	ReqDefinition RequestKind = iota
	ReqReferences
	ReqHover
)

func (r RequestKind) method() string {
	switch r {
	case ReqReferences:
		return "textDocument/references"
	case ReqHover:
		return "textDocument/hover"
	default:
		return "textDocument/definition"
	}
}

// ResolveResult carries either the resolved locations (definition/references)
// or the hover documentation payload.
type ResolveResult struct {
	Locations []Location
	Hover     string
}

// Location is one resolved LSP location; lines are 1-based (package
// convention), characters 0-based (Rust parity: LspLocation).
type Location struct {
	URI          string `json:"uri"`
	Line         int    `json:"line"`
	Character    int    `json:"character"`
	EndLine      int    `json:"end_line"`
	EndCharacter int    `json:"end_character"`
}

// sendResolveRequest issues the request and normalizes the response shape.
func sendResolveRequest(ctx context.Context, c *Client, req RequestKind, filePath string, line, character int) (ResolveResult, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return ResolveResult{}, err
	}
	if req == ReqHover {
		doc, err := c.Hover(ctx, abs, line, character)
		if err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{Hover: doc}, nil
	}
	raw, err := c.request(ctx, req.method(), map[string]any{
		"textDocument": map[string]any{"uri": uriOf(abs)},
		"position":     map[string]any{"line": line, "character": character},
	})
	if err != nil {
		return ResolveResult{}, err
	}
	locs, err := decodeLocations(raw)
	if err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Locations: locs}, nil
}

type rawLocation struct {
	URI   string   `json:"uri"`
	Range rawRange `json:"range"`
}

type rawLocationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange rawRange `json:"targetRange"`
}

// decodeLocations accepts every documented definition/references response
// shape: Location | []Location | []LocationLink | null (Rust parity:
// client.rs request()).
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single rawLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []Location{single.toLocation()}, nil
	}
	var many []rawLocation
	if err := json.Unmarshal(raw, &many); err == nil {
		out := make([]Location, 0, len(many))
		for _, l := range many {
			out = append(out, l.toLocation())
		}
		return out, nil
	}
	var links []rawLocationLink
	if err := json.Unmarshal(raw, &links); err == nil {
		out := make([]Location, 0, len(links))
		for _, l := range links {
			if l.TargetURI == "" {
				continue
			}
			out = append(out, Location{
				URI:          l.TargetURI,
				Line:         l.TargetRange.Start.Line + 1,
				Character:    l.TargetRange.Start.Char,
				EndLine:      l.TargetRange.End.Line + 1,
				EndCharacter: l.TargetRange.End.Char,
			})
		}
		return out, nil
	}
	return nil, ErrProtocol
}

func (l rawLocation) toLocation() Location {
	return Location{
		URI:          l.URI,
		Line:         l.Range.Start.Line + 1,
		Character:    l.Range.Start.Char,
		EndLine:      l.Range.End.Line + 1,
		EndCharacter: l.Range.End.Char,
	}
}

// FindWorkspaceRoot walks up from start until a directory contains a known
// manifest. The walk never crosses the user's home directory (Rust parity:
// bridge.rs find_workspace_root).
func FindWorkspaceRoot(start string) string {
	const manifests = ".git leankg.yaml go.mod Cargo.toml package.json pyproject.toml" +
		" pom.xml build.gradle build.gradle.kts tsconfig.json Gemfile mix.exs" +
		" pubspec.yaml Project.toml Package.swift"
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = string(filepath.Separator)
	}
	cur, err := filepath.Abs(start)
	if err != nil {
		return start
	}
	if fi, serr := os.Stat(cur); serr == nil && !fi.IsDir() {
		cur = filepath.Dir(cur)
	}
	for {
		for m := range strings.SplitSeq(manifests, " ") {
			if _, err := os.Stat(filepath.Join(cur, m)); err == nil {
				return cur
			}
		}
		if cur == home || filepath.Dir(cur) == cur {
			return cur
		}
		cur = filepath.Dir(cur)
	}
}
