// Package convo implements conversation mining (US-MP-03 / FR-MP-09..13),
// ported from the Rust `conversation_indexer` module.
//
// Parsers stay format-local and produce a flat list of RawMessage; a single
// shared classifier + keyword extractor turns messages into MinedItems, so
// all three export formats get identical mining semantics. Mined items are
// persisted as decision / preference / milestone / problem elements with a
// `decided_about` edge from decision nodes to code targets. Raw verbatim
// text is stored in element metadata — no summarization.
package convo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Format selects the export shape of `leankg mine-conversations --format`.
type Format int

// UnknownFormat is an unrecognized --format value; mining errors out.
// FormatClaude, FormatChatGPT and FormatSlack are the three supported
// export shapes.
const (
	UnknownFormat Format = iota
	FormatClaude
	FormatChatGPT
	FormatSlack
)

// String returns the CLI spelling of the format ("unknown" when unparsed).
func (f Format) String() string {
	switch f {
	case FormatClaude:
		return "claude"
	case FormatChatGPT:
		return "chatgpt"
	case FormatSlack:
		return "slack"
	default:
		return "unknown"
	}
}

// ParseFormat maps a --format value onto a Format ("claude" | "chatgpt" |
// "slack", case-insensitive); anything else yields UnknownFormat.
func ParseFormat(s string) Format {
	switch strings.ToLower(s) {
	case "claude":
		return FormatClaude
	case "chatgpt":
		return FormatChatGPT
	case "slack":
		return FormatSlack
	default:
		return UnknownFormat
	}
}

// parseFile extracts the raw messages of one export file in the given format.
func parseFile(path string, format Format) ([]RawMessage, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var (
		raw  []RawMessage
		perr error
	)
	switch format {
	case FormatClaude:
		raw, perr = parseClaudeExport(content)
	case FormatChatGPT:
		raw, perr = parseChatGPTExport(content)
	case FormatSlack:
		raw, perr = parseSlackExport(content)
	default:
		return nil, fmt.Errorf("unknown format; use --format claude|chatgpt|slack")
	}
	if perr != nil {
		return nil, fmt.Errorf("%s: %w", path, perr)
	}
	return raw, nil
}

// MineFile mines a single export file (CLI --input <file>).
func MineFile(path string, format Format) ([]MinedItem, error) {
	messages, err := parseFile(path, format)
	if err != nil {
		return nil, err
	}
	items := make([]MinedItem, 0, len(messages))
	for _, m := range messages {
		if item, ok := FromMessage(m); ok {
			items = append(items, item)
		}
	}
	return items, nil
}

// MineDir mines a file or directory of exports (CLI --input <file-or-dir>).
// Directory entries are read in sorted order and only *.json files are
// attempted; files whose shape does not match the format are skipped with a
// warning on stderr. Sources counts parsed files.
func MineDir(ctx context.Context, input string, format Format) (MiningResult, error) {
	info, err := os.Stat(input)
	if err != nil {
		return MiningResult{}, fmt.Errorf("input path not found: %s", input)
	}
	if !info.IsDir() {
		items, err := MineFile(input, format)
		if err != nil {
			return MiningResult{}, err
		}
		return MiningResult{Items: items, Sources: 1}, nil
	}

	entries, err := os.ReadDir(input)
	if err != nil {
		return MiningResult{}, fmt.Errorf("cannot read %s: %w", input, err)
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		paths = append(paths, filepath.Join(input, e.Name()))
	}
	sort.Strings(paths)

	result := MiningResult{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		items, err := MineFile(path, format)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mine-conversations] skipping %s: %v\n", path, err)
			continue
		}
		result.Sources++
		result.Items = append(result.Items, items...)
	}
	return result, nil
}

// MineIntoProject mines input and persists the items into the project's
// graph (CLI entry point). The project store is opened read-write and
// migrated; nothing is written when no message classifies.
func MineIntoProject(ctx context.Context, project, input string, format Format) (MiningResult, error) {
	result, err := MineDir(ctx, input, format)
	if err != nil {
		return result, err
	}
	if len(result.Items) == 0 {
		return result, nil
	}
	st, err := store.OpenBackend(ctx, project, os.Getenv("LEANKG_DB_ENGINE"), os.Getenv("LEANKG_PG_URL"), store.RW)
	if err != nil {
		return result, fmt.Errorf("open store: %w", err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return result, fmt.Errorf("migrate: %w", err)
	}
	indexed, err := IndexItems(st, project, result.Items)
	if err != nil {
		return result, err
	}
	result.ElementsIndexed = indexed.ElementsIndexed
	result.RelationshipsCreated = indexed.RelationshipsCreated
	return result, nil
}

// IndexItems persists mined items into an open store (public seam, mirrors
// the Rust `index_items`). It is idempotent: elements are keyed on
// qualified_name and relationships on (source, target, rel_type), so a
// second run over the same items collapses instead of duplicating. The
// batch's conversation file paths are cleared first so a re-mine that no
// longer classifies an item drops its stale node.
//
// All clears run before any write: store.DeleteByFile cascades to the
// relationships sourced at the cleared elements, so interleaving the clears
// with the inserts would wipe the `decided_about` edges of every element that
// shares a conversation file path. (The Rust original interleaved them and
// lost all but the last decision's edges; this port fixes that, which the
// decided_about count assertion in TestIndexItemsPersistsNodesAndEdges pins.)
func IndexItems(st store.Backend, project string, items []MinedItem) (MiningResult, error) {
	var (
		elements      []store.Element
		relationships []store.Relationship
	)
	for i := range items {
		el, rels := items[i].GraphElements(project)
		elements = append(elements, el)
		relationships = append(relationships, rels...)
	}

	cleared := make(map[string]bool, len(elements))
	for _, el := range elements {
		if cleared[el.FilePath] {
			continue
		}
		cleared[el.FilePath] = true
		if err := st.DeleteByFile(el.FilePath); err != nil {
			return MiningResult{}, fmt.Errorf("clear prior mined nodes for %s: %w", el.FilePath, err)
		}
	}
	if len(elements) > 0 {
		if err := st.UpsertElements(elements); err != nil {
			return MiningResult{}, fmt.Errorf("insert mined elements: %w", err)
		}
	}
	if len(relationships) > 0 {
		if err := st.UpsertRelationships(relationships); err != nil {
			return MiningResult{}, fmt.Errorf("insert mined relationships: %w", err)
		}
	}

	return MiningResult{
		Items:                items,
		ElementsIndexed:      len(elements),
		RelationshipsCreated: len(relationships),
	}, nil
}
