// Package registry ports the deleted Rust src/registry.rs: the global repo
// registry — a home-dir JSON file (~/.leankg/registry.json) listing named
// repositories with their last-indexed bookkeeping. The file location and
// schema mirror the Rust engine exactly, so a shared $HOME sees the same
// registry both engines wrote.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// RegistryVersion is the on-disk format version (Rust wrote 1).
const RegistryVersion = 1

// RepoEntry is one registered repository (Rust RepoEntry). last_indexed and
// element_count serialize as JSON null until an index run stamps them — the
// exact serde Option shape the Rust registry file carries.
type RepoEntry struct {
	Path         string  `json:"path"`
	LastIndexed  *string `json:"last_indexed"`
	ElementCount *int    `json:"element_count"`
}

// Registry is the loaded registry.json document (Rust Registry).
type Registry struct {
	Version int                   `json:"version"`
	Repos   map[string]*RepoEntry `json:"repos"`
}

// Default returns an empty registry at the current version.
func Default() *Registry {
	return &Registry{Version: RegistryVersion, Repos: map[string]*RepoEntry{}}
}

// Path returns the registry file location: $HOME/.leankg/registry.json
// (HOME unset or empty falls back to ./.leankg/registry.json, matching the
// Rust env::var("HOME").unwrap_or(".").to_string() fallback — "." joins as
// a literal segment the same way Rust's PathBuf::from(".").join() does).
func Path() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".leankg", "registry.json")
}

// Load reads the registry file. A missing file yields Default (Rust: same
// early return). A malformed file is an error, never a silent reset.
func Load() (*Registry, error) {
	path := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	var reg Registry
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("registry: parse %s: %w", path, err)
	}
	if reg.Repos == nil {
		reg.Repos = map[string]*RepoEntry{}
	}
	return &reg, nil
}

// Save writes the registry as pretty JSON, creating the parent directory.
func (r *Registry) Save() error {
	path := Path()
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("registry: create %s: %w", dir, err)
		}
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("registry: encode: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("registry: write %s: %w", path, err)
	}
	return nil
}

// Register records name at path (absolute; a relative path resolves against
// the process working directory) and saves. A re-register overwrites the
// entry, resetting last_indexed/element_count — the Rust insert semantics.
func (r *Registry) Register(name, path string) error {
	if !filepath.IsAbs(path) {
		if cwd, err := os.Getwd(); err == nil {
			path = filepath.Join(cwd, path)
		}
	}
	r.Repos[name] = &RepoEntry{Path: path}
	return r.Save()
}

// Unregister removes name and saves. An unknown name is a no-op save (the
// Rust remove on a missing key likewise does nothing) — callers that need
// must-report use Get first, as the Rust CLI did.
func (r *Registry) Unregister(name string) error {
	delete(r.Repos, name)
	return r.Save()
}

// Get returns the entry for name, or nil when not registered.
func (r *Registry) Get(name string) *RepoEntry {
	return r.Repos[name]
}

// List returns the registered entries sorted by name. The Rust list_repos
// returned HashMap order; sorted order is the deterministic Go equivalent.
func (r *Registry) List() []Entry {
	names := make([]string, 0, len(r.Repos))
	for name := range r.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Entry, 0, len(names))
	for _, name := range names {
		out = append(out, Entry{Name: name, Repo: r.Repos[name]})
	}
	return out
}

// Entry is one (name, entry) pair from List.
type Entry struct {
	Name string
	Repo *RepoEntry
}

// UpdateLastIndexed stamps name's entry with an index timestamp and element
// count and saves. An unknown name is a no-op (Rust: if-let on get_mut).
func (r *Registry) UpdateLastIndexed(name, timestamp string, elementCount int) error {
	if e := r.Repos[name]; e != nil {
		stamp := timestamp
		e.LastIndexed = &stamp
		n := elementCount
		e.ElementCount = &n
		return r.Save()
	}
	return nil
}

// ErrNotFound reports a status request for an unregistered name.
var ErrNotFound = errors.New("registry: repository not found")

// --- package-level surface (the Rust main.rs register/list/status verbs) ---

// Register adds the current working directory to the registry under name and
// prints nothing; use ConfirmRegister for the Rust CLI line.
func Register(name, path string) error {
	reg, err := Load()
	if err != nil {
		return err
	}
	return reg.Register(name, path)
}

// List returns the registered entries sorted by name.
func List() ([]Entry, error) {
	reg, err := Load()
	if err != nil {
		return nil, err
	}
	return reg.List(), nil
}

// Status reports name's registry bookkeeping plus the live store counts when
// the repository has been indexed. ErrNotFound when name is unregistered.
func Status(name string) (RepoStatus, error) {
	reg, err := Load()
	if err != nil {
		return RepoStatus{}, err
	}
	entry := reg.Get(name)
	if entry == nil {
		return RepoStatus{}, ErrNotFound
	}
	st := RepoStatus{
		Name:         name,
		Path:         entry.Path,
		LastIndexed:  entry.LastIndexed,
		ElementCount: entry.ElementCount,
	}
	// Rust gated the live read on the .leankg directory existing; a repo that
	// exists but cannot be opened reports nothing beyond bookkeeping, exactly
	// like the Rust `if let Ok(db) = init_db(...)`.
	if info, statErr := os.Stat(filepath.Join(entry.Path, ".leankg")); statErr == nil && info.IsDir() {
		st.Indexed = true
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		backend, openErr := store.OpenBackend(ctx, entry.Path, os.Getenv("LEANKG_DB_ENGINE"), os.Getenv("LEANKG_PG_URL"), store.RO)
		if openErr == nil {
			defer backend.Close()
			if n, err := backend.ElementCount(); err == nil {
				st.CurrentElements = &n
			}
			if n, err := backend.RelationshipCount(); err == nil {
				st.CurrentRelationships = &n
			}
		}
	}
	return st, nil
}

// RepoStatus is the status-repo payload.
type RepoStatus struct {
	Name         string
	Path         string
	LastIndexed  *string
	ElementCount *int
	// Indexed reports that <Path>/.leankg exists.
	Indexed bool
	// live counts; nil when the store could not be read (or is absent).
	CurrentElements      *int
	CurrentRelationships *int
}

// --- CLI rendering (Rust main.rs printed these lines verbatim) ---

// ConfirmRegister is the Rust register_repo output line.
func ConfirmRegister(name, path string) string {
	return fmt.Sprintf("Registered repository '%s' at %s\n", name, path)
}

// ConfirmUnregister is the Rust unregister_repo output line; a name that was
// not registered prints the Rust "not found" line instead.
func ConfirmUnregister(name string, wasRegistered bool) string {
	if !wasRegistered {
		return fmt.Sprintf("Repository '%s' not found in registry\n", name)
	}
	return fmt.Sprintf("Unregistered repository '%s'\n", name)
}

// Get loads the registry and returns name's entry, or nil when unregistered.
func Get(name string) (*RepoEntry, error) {
	reg, err := Load()
	if err != nil {
		return nil, err
	}
	return reg.Get(name), nil
}

// Unregister removes name, reporting whether it was registered (the Rust CLI
// checked get_repo before calling unregister and printed accordingly).
func Unregister(name string) (wasRegistered bool, err error) {
	reg, err := Load()
	if err != nil {
		return false, err
	}
	if reg.Get(name) == nil {
		return false, nil
	}
	if err := reg.Unregister(name); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateLastIndexed loads the registry, stamps name's index bookkeeping and
// saves. An unregistered name is a no-op.
func UpdateLastIndexed(name, timestamp string, elementCount int) error {
	reg, err := Load()
	if err != nil {
		return err
	}
	return reg.UpdateLastIndexed(name, timestamp, elementCount)
}

// RenderList renders the Rust list_repos output.
func RenderList(entries []Entry) string {
	if len(entries) == 0 {
		return "No repositories registered. Run 'leankg register <name>' to add one.\n"
	}
	var b strings.Builder
	b.WriteString("Registered repositories:\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "  - %s: %s (indexed: %s)\n", e.Name, e.Repo.Path, debugOptStr(e.Repo.LastIndexed))
	}
	return b.String()
}

// Render renders the Rust status_repo output.
func (s RepoStatus) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Repository: %s\n", s.Name)
	fmt.Fprintf(&b, "  Path: %s\n", s.Path)
	fmt.Fprintf(&b, "  Last indexed: %s\n", debugOptStr(s.LastIndexed))
	fmt.Fprintf(&b, "  Element count: %s\n", debugOptInt(s.ElementCount))
	if s.CurrentElements != nil {
		fmt.Fprintf(&b, "  Current elements: %d\n", *s.CurrentElements)
	}
	if s.CurrentRelationships != nil {
		fmt.Fprintf(&b, "  Current relationships: %d\n", *s.CurrentRelationships)
	}
	if !s.Indexed {
		b.WriteString("  Status: Not indexed (no .leankg directory found)\n")
	}
	return b.String()
}

// debugOptStr renders an Option<String> the way the Rust CLI's `{:?}` did:
// None / Some("value").
func debugOptStr(v *string) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%q)", *v)
}

// debugOptInt renders an Option<usize> the way the Rust CLI's `{:?}` did.
func debugOptInt(v *int) string {
	if v == nil {
		return "None"
	}
	return fmt.Sprintf("Some(%d)", *v)
}
