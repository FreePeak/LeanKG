// Package annot persists business-logic annotations: one free-text
// description per element, optionally linked to a user story or a feature.
//
// The Rust engine kept these in a Cozo relation (business_logic, keyed by
// element_qualified). The Go store has no such table, so the whole
// annotation set lives as a single JSON document in the namespaced KV,
// sorted by Element so the document is stable and deterministic.
package annot

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

const (
	// kvNamespace and kvKey locate the annotation document. The namespace is
	// distinct from every other KV user (e.g. obsidian's note annotations).
	kvNamespace = "annotation"
	kvKey       = "records"
)

// Record is one business-logic annotation, keyed by Element (the element's
// qualified name). UserStoryID and FeatureID are optional links; empty means
// unset (encoded as JSON null/skip, matching the Rust Option<String>).
type Record struct {
	Element     string `json:"element"`
	Description string `json:"description"`
	UserStoryID string `json:"user_story_id,omitempty"`
	FeatureID   string `json:"feature_id,omitempty"`
}

// load reads the whole annotation set. A missing KV document is an empty set.
func load(st store.Backend) ([]Record, error) {
	raw, ok, err := st.KVGet(kvNamespace, kvKey)
	if err != nil {
		return nil, fmt.Errorf("annot: read records: %w", err)
	}
	if !ok {
		return []Record{}, nil
	}
	var recs []Record
	if err := json.Unmarshal([]byte(raw), &recs); err != nil {
		return nil, fmt.Errorf("annot: decode records: %w", err)
	}
	sortRecords(recs)
	return recs, nil
}

// sortRecords orders annotations by Element, the document's stable key order.
func sortRecords(recs []Record) {
	sort.Slice(recs, func(i, j int) bool { return recs[i].Element < recs[j].Element })
}

// save writes the whole annotation set back, sorted by Element.
//
// ponytail: read-modify-write of a single KV document — two concurrent
// writers race and the later save silently drops the earlier one (lost
// update). Fine at the CLI/watcher cadence this package serves; the upgrade
// path is a dedicated store table (or a store-level compare-and-swap).
func save(st store.Backend, recs []Record) error {
	if recs == nil {
		recs = []Record{}
	}
	sortRecords(recs)
	b, err := json.Marshal(recs)
	if err != nil {
		return fmt.Errorf("annot: encode records: %w", err)
	}
	if err := st.KVSet(kvNamespace, kvKey, string(b)); err != nil {
		return fmt.Errorf("annot: write records: %w", err)
	}
	return nil
}

// Get returns the annotation for element, or nil when the element has none.
func Get(st store.Backend, element string) (*Record, error) {
	recs, err := load(st)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if recs[i].Element == element {
			r := recs[i]
			return &r, nil
		}
	}
	return nil, nil
}

// Put upserts r keyed by r.Element (Rust :put semantics: an existing row for
// the same element is replaced whole, so unset links are cleared).
func Put(st store.Backend, r Record) error {
	recs, err := load(st)
	if err != nil {
		return err
	}
	replaced := false
	for i := range recs {
		if recs[i].Element == r.Element {
			recs[i] = r
			replaced = true
			break
		}
	}
	if !replaced {
		recs = append(recs, r)
	}
	return save(st, recs)
}

// Delete removes the annotation for element. Deleting an element that has no
// annotation is a no-op, like the Rust :rm over zero rows.
func Delete(st store.Backend, element string) error {
	recs, err := load(st)
	if err != nil {
		return err
	}
	kept := recs[:0]
	for _, r := range recs {
		if r.Element != element {
			kept = append(kept, r)
		}
	}
	return save(st, kept)
}

// All returns every annotation, sorted by Element. An empty store yields an
// empty slice, not an error.
func All(st store.Backend) ([]Record, error) {
	return load(st)
}

// ByUserStory returns the annotations linked to userStoryID.
func ByUserStory(st store.Backend, userStoryID string) ([]Record, error) {
	return filter(st, func(r Record) bool { return r.UserStoryID == userStoryID })
}

// ByFeature returns the annotations linked to featureID.
func ByFeature(st store.Backend, featureID string) ([]Record, error) {
	return filter(st, func(r Record) bool { return r.FeatureID == featureID })
}

func filter(st store.Backend, keep func(Record) bool) ([]Record, error) {
	recs, err := load(st)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Search ports the Rust search_business_logic: the query is lowercased and
// embedded in a ".*<query>.*" regex which is matched against the lowercased
// DESCRIPTION only — never against element names. An invalid regex (or one
// the query makes invalid) is an error, as the Cozo query error was in Rust.
func Search(st store.Backend, query string) ([]Record, error) {
	re, err := regexp.Compile(".*" + strings.ToLower(query) + ".*")
	if err != nil {
		return nil, fmt.Errorf("annot: search pattern: %w", err)
	}
	recs, err := load(st)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(recs))
	for _, r := range recs {
		if re.MatchString(strings.ToLower(r.Description)) {
			out = append(out, r)
		}
	}
	return out, nil
}

// Annotate ports the Rust annotate_element CLI verb: create the annotation
// when the element has none, update (replace) it when it does. created
// reports which happened; a nil userStory/feature clears that link.
func Annotate(st store.Backend, element, description string, userStory, feature *string) (bool, error) {
	existing, err := Get(st, element)
	if err != nil {
		return false, err
	}
	r := Record{Element: element, Description: description}
	if userStory != nil {
		r.UserStoryID = *userStory
	}
	if feature != nil {
		r.FeatureID = *feature
	}
	if err := Put(st, r); err != nil {
		return false, err
	}
	return existing == nil, nil
}

// Link ports the Rust link_element CLI verb. On an existing annotation it
// appends " | Linked to <kind> <id>" to the description unless the
// description already starts with "Linked to" (no duplicated suffix), sets
// the story or feature link, and keeps the other one. On an element with no
// annotation it creates one described "Linked to <kind> <id>". Any kind
// other than "story" is treated as a feature link.
func Link(st store.Backend, element, id, kind string) error {
	existing, err := Get(st, element)
	if err != nil {
		return err
	}
	if existing == nil {
		r := Record{Element: element, Description: "Linked to " + kind + " " + id}
		if kind == "story" {
			r.UserStoryID = id
		} else {
			r.FeatureID = id
		}
		return Put(st, r)
	}
	r := *existing
	if kind == "story" {
		r.Description = withLinkSuffix(r.Description, "story", id)
		r.UserStoryID = id
	} else {
		r.Description = withLinkSuffix(r.Description, "feature", id)
		r.FeatureID = id
	}
	return Put(st, r)
}

// withLinkSuffix appends " | Linked to <kind> <id>" unless the description
// already starts with "Linked to", in which case it is left untouched.
func withLinkSuffix(description, kind, id string) string {
	if strings.HasPrefix(description, "Linked to") {
		return description
	}
	return description + " | Linked to " + kind + " " + id
}
