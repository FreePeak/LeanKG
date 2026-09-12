// Package pack exports portable context packs: a deterministic, relative-path,
// content-hashed bundle of a graph slice.
//
//	out/
//	  snapshot.json   # deterministic graph slice (elements + relationships)
//	  manifest.json   # schema version, counts, content hash, scope, revision
//
// A pack is a distribution artifact, never a live serving store. The serving
// database remains authoritative; a pack can be diffed, committed, or shipped
// to a cold-start consumer.
//
// Determinism contract: identical graphs produce byte-identical snapshot.json
// AND byte-identical manifest.json across runs and across checkouts — no
// wall-clock timestamps, no absolute paths. The manifest content hash covers
// the snapshot bytes. Oversize slices are refused, never silently truncated.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// DefaultMaxNodes is the element ceiling for a pack when Options.MaxNodes is
// not set. The Rust CLI default is the same value.
const DefaultMaxNodes = 5000

// PackKind names a manifest; PackSnapshotKind names the snapshot it describes.
const (
	packKind         = "leankg.context.pack"
	packSnapshotKind = "leankg.context.pack.snapshot"
	packSchemaVer    = 1
)

// allRelationshipsLimit caps the relationship read well above any realistic
// graph. store.Backend.RelationshipsAll treats limit <= 0 as 1000 rows, so the
// full edge set must be requested explicitly (same convention as web/api.go).
const allRelationshipsLimit = 1 << 20

// Options scope a pack. Path empty packs the whole graph (refusing on graphs
// over MaxNodes); MaxNodes <= 0 means DefaultMaxNodes.
type Options struct {
	Path           string // path scope prefix; empty = whole graph
	MaxNodes       int    // <=0 -> DefaultMaxNodes
	SourceRevision string // recorded verbatim in the manifest
}

// Manifest describes a written pack.
type Manifest struct {
	SchemaVersion  int     `json:"schema_version"`
	Kind           string  `json:"kind"`
	Elements       int     `json:"elements"`
	Relationships  int     `json:"relationships"`
	ContentHash    string  `json:"content_hash"`
	SourceRevision *string `json:"source_revision"`
	PathScope      *string `json:"path_scope"`
}

// WritePack writes outDir/snapshot.json + outDir/manifest.json and returns the
// manifest. It errors out (instead of truncating) if the selected slice exceeds
// the node ceiling: the contract advertises "refuses to truncate", and silent
// row-drop would make that a lie.
func WritePack(st store.Backend, outDir string, opts Options) (Manifest, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Manifest{}, fmt.Errorf("create pack dir: %w", err)
	}

	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}

	allElements, err := st.Elements()
	if err != nil {
		return Manifest{}, fmt.Errorf("read elements: %w", err)
	}
	allRels, err := st.RelationshipsAll(allRelationshipsLimit)
	if err != nil {
		return Manifest{}, fmt.Errorf("read relationships: %w", err)
	}

	elements, relationships := selectSlice(allElements, allRels, opts.Path)

	if len(elements) > maxNodes {
		return Manifest{}, fmt.Errorf(
			"pack slice %d elements exceeds max_nodes=%d (refusing to truncate)",
			len(elements), maxNodes)
	}

	snapshot, err := renderSnapshot(elements, relationships)
	if err != nil {
		return Manifest{}, fmt.Errorf("render snapshot: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "snapshot.json"), snapshot, 0o644); err != nil {
		return Manifest{}, fmt.Errorf("write snapshot: %w", err)
	}

	sum := sha256.Sum256(snapshot)
	manifest := Manifest{
		SchemaVersion:  packSchemaVer,
		Kind:           packKind,
		Elements:       len(elements),
		Relationships:  len(relationships),
		ContentHash:    hex.EncodeToString(sum[:]),
		SourceRevision: optionalString(opts.SourceRevision),
		PathScope:      optionalString(opts.Path),
	}
	manifestBytes, err := marshalJSON(manifest)
	if err != nil {
		return Manifest{}, fmt.Errorf("render manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return Manifest{}, fmt.Errorf("write manifest: %w", err)
	}
	return manifest, nil
}

// selectSlice picks the element/relationship slice for a pack. Oversize is
// reported by the caller; nothing is truncated here. Relationships survive only
// when both endpoints survive the scope filter.
func selectSlice(allElements []store.Element, allRels []store.Relationship, scope string) ([]store.Element, []store.Relationship) {
	elements := allElements
	if scope != "" {
		p := trimDotslash(scope)
		elements = make([]store.Element, 0, len(allElements))
		for _, e := range allElements {
			if strings.HasPrefix(trimDotslash(e.FilePath), p) || strings.HasPrefix(e.QualifiedName, p) {
				elements = append(elements, e)
			}
		}
	}
	sort.SliceStable(elements, func(i, j int) bool {
		return elements[i].QualifiedName < elements[j].QualifiedName
	})

	inScope := make(map[string]struct{}, len(elements))
	for _, e := range elements {
		inScope[e.QualifiedName] = struct{}{}
	}
	relationships := make([]store.Relationship, 0, len(allRels))
	for _, r := range allRels {
		if _, ok := inScope[r.Source]; !ok {
			continue
		}
		if _, ok := inScope[r.Target]; !ok {
			continue
		}
		relationships = append(relationships, r)
	}
	return elements, relationships
}

// renderSnapshot renders deterministic snapshot JSON: sorted, relative paths,
// no absolute paths, no timestamps. Objects marshal through map[string]any so
// keys are emitted alphabetically, matching the Rust serde_json::Map output.
func renderSnapshot(elements []store.Element, relationships []store.Relationship) ([]byte, error) {
	elems := make([]map[string]any, 0, len(elements))
	for _, e := range elements {
		elems = append(elems, map[string]any{
			"qualified_name": e.QualifiedName,
			"element_type":   e.ElementType,
			"name":           e.Name,
			"file_path":      relativize(e.FilePath),
			"line_start":     e.LineStart,
			"line_end":       e.LineEnd,
			"language":       e.Language,
			// The store has no cluster columns; keep the keys for shape parity
			// with the Rust snapshot (where they were always None here too).
			"cluster_id":       nil,
			"cluster_label":    nil,
			"parent_qualified": nullable(e.ParentQualified),
		})
	}
	sort.SliceStable(elems, func(i, j int) bool {
		return elems[i]["qualified_name"].(string) < elems[j]["qualified_name"].(string)
	})

	rels := make([]map[string]any, 0, len(relationships))
	for _, r := range relationships {
		rels = append(rels, map[string]any{
			"source":     r.Source,
			"target":     r.Target,
			"rel_type":   r.RelType,
			"confidence": r.Confidence,
		})
	}
	sort.SliceStable(rels, func(i, j int) bool {
		a, b := rels[i], rels[j]
		if a["source"].(string) != b["source"].(string) {
			return a["source"].(string) < b["source"].(string)
		}
		if a["rel_type"].(string) != b["rel_type"].(string) {
			return a["rel_type"].(string) < b["rel_type"].(string)
		}
		return a["target"].(string) < b["target"].(string)
	})

	// Confidence is a float in [0,1], where Go's shortest-round-trip float
	// formatting and serde_json's ryu agree digit-for-digit.
	doc := map[string]any{
		"version":       packSchemaVer,
		"kind":          packSnapshotKind,
		"elements":      elems,
		"relationships": rels,
	}
	return marshalJSON(doc)
}

// marshalJSON renders v like serde_json::to_vec_pretty: two-space indent, no
// HTML escaping, no trailing newline.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// relativize normalizes a stored path to repo-relative form: backslashes become
// slashes, leading slashes and repeated "./" prefixes are stripped, so a
// snapshot stays portable across checkouts.
func relativize(filePath string) string {
	norm := strings.ReplaceAll(filePath, "\\", "/")
	return trimDotslash(strings.TrimLeft(norm, "/"))
}

// trimDotslash removes every leading "./", mirroring Rust
// str::trim_start_matches("./") (repeated removal, not path.Clean).
func trimDotslash(s string) string {
	for strings.HasPrefix(s, "./") {
		s = s[2:]
	}
	return s
}

// nullable maps the store's empty-string sentinel to JSON null (the store has
// no distinction between NULL and "").
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// optionalString returns nil for an unset option so the manifest carries an
// explicit null rather than an empty string.
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
