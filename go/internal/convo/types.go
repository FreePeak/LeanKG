// Mined item model + classification (FR-MP-12), ported from the Rust
// `conversation_indexer::types` module.
package convo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Kind is the graph element type of a mined conversation item.
type Kind int

// KindGeneral is the classifier's no-signal fallback; items of that kind are
// never mined into the graph.
const (
	KindGeneral Kind = iota
	KindDecision
	KindPreference
	KindMilestone
	KindProblem
)

// String returns the element-type spelling of the kind.
func (k Kind) String() string {
	switch k {
	case KindDecision:
		return "decision"
	case KindPreference:
		return "preference"
	case KindMilestone:
		return "milestone"
	case KindProblem:
		return "problem"
	default:
		return "general"
	}
}

var (
	// labelPhrases are the explicit prefix labels; they win over keywords.
	labelPhrases = []struct {
		label string
		kind  Kind
	}{
		{"decision:", KindDecision},
		{"preference:", KindPreference},
		{"milestone:", KindMilestone},
		{"problem:", KindProblem},
	}
	// kindSignals are the keyword heuristics, evaluated in Rust's order:
	// problem first (most specific), then decision, milestone, preference.
	kindSignals = []struct {
		kind     Kind
		keywords []string
	}{
		{KindProblem, []string{"fails", "failure", "broken", "bug", "timeout", "crashes", "error", "timing out", "stuck", "keeps"}},
		{KindDecision, []string{"we decided", "decision", "we will", "we should", "we are going to", "let's use"}},
		{KindMilestone, []string{"milestone", "goal", "deadline", "by end of", "target date", "ship by", "by q"}},
		{KindPreference, []string{"prefer", "preference", "preferred", "rather use", "would like"}},
	}
)

// Classify is the deterministic keyword classifier over a raw message. A
// prefix label ("decision:", "preference:", ...) wins; otherwise problem
// phrases are tried first (most specific), then decision / milestone /
// preference keyword heuristics. Unmatched text falls back to KindGeneral.
func Classify(text string) Kind {
	lowered := strings.ToLower(strings.TrimSpace(text))

	for _, p := range labelPhrases {
		if strings.HasPrefix(lowered, p.label) {
			return p.kind
		}
	}
	for _, sig := range kindSignals {
		for _, kw := range sig.keywords {
			if strings.Contains(lowered, kw) {
				return sig.kind
			}
		}
	}
	return KindGeneral
}

// MinedItem is one mined conversation message. Raw verbatim is stored — no
// summarization.
type MinedItem struct {
	Kind         Kind     `json:"kind"`
	Verbatim     string   `json:"verbatim"`
	Source       string   `json:"source"`
	Participants []string `json:"participants"`
	Timestamp    string   `json:"timestamp"`
	Topic        string   `json:"topic"`
	CodeTargets  []string `json:"code_targets,omitempty"`
}

// topicStopwords are words too generic to name a conversation node.
var topicStopwords = map[string]bool{
	"decision": true, "preference": true, "milestone": true, "problem": true,
	"adopt": true, "use": true, "switch": true, "migrate": true, "prefer": true,
	"go": true, "pick": true, "choose": true, "we": true, "the": true,
	"our": true, "new": true, "for": true, "with": true, "and": true,
}

// extractTopic is the deterministic topic extractor: the first
// identifier-ish token (at least 3 chars, not a stopword), falling back to
// the first 40 characters of the text. Used for the node name.
func extractTopic(text string) string {
	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !isAlnum(r) && r != '_'
	}) {
		if len(token) >= 3 && !topicStopwords[strings.ToLower(token)] {
			return token
		}
	}
	return truncateRunes(text, 40)
}

// isAlnum mirrors Rust's char::is_alphanumeric (Unicode letter or digit).
func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func truncateRunes(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	count := 0
	for pos := range s {
		if count == n {
			return s[:pos]
		}
		count++
	}
	return s
}

var (
	// backtickQuoted matches `...` spans; the capture group is the inner
	// text, mirroring the Rust capture semantics (greedy across the line).
	backtickQuoted = regexp.MustCompile("`([^`]+)`")
	// fileSymbol matches inline `path/to/file.ext::symbol` references.
	fileSymbol = regexp.MustCompile(`\b([A-Za-z0-9_\-./]+\.(?:rs|go|ts|js|py|java|kt))::([A-Za-z0-9_]+)\b`)
	// srcPath matches bare `src/...` source paths.
	srcPath = regexp.MustCompile(`\bsrc/[A-Za-z0-9_\-./]+\.(?:rs|go|ts|js|py|java|kt)\b`)
)

// extractCodeTargets is the deterministic code-target extractor:
// backtick-quoted paths, inline `file::symbol` patterns, and bare `src/...`
// paths inside the text, in that order, de-duplicated preserving order.
func extractCodeTargets(text string) []string {
	var targets []string
	for _, m := range backtickQuoted.FindAllStringSubmatch(text, -1) {
		targets = append(targets, m[1])
	}
	for _, m := range fileSymbol.FindAllStringSubmatch(text, -1) {
		targets = appendUnique(targets, m[1]+"::"+m[2])
	}
	for _, m := range srcPath.FindAllString(text, -1) {
		targets = appendUnique(targets, m)
	}
	return targets
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// FromMessage builds a mined item from one raw message. ok is false when the
// message is empty or carries no classifiable signal (KindGeneral).
func FromMessage(m RawMessage) (MinedItem, bool) {
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return MinedItem{}, false
	}
	kind := Classify(text)
	if kind == KindGeneral {
		return MinedItem{}, false
	}
	return MinedItem{
		Kind:         kind,
		Verbatim:     text,
		Source:       m.Source,
		Participants: []string{m.Participant},
		Timestamp:    m.Timestamp,
		Topic:        extractTopic(text),
		CodeTargets:  extractCodeTargets(text),
	}, true
}

// QualifiedName is the graph node name of this item:
// conversations/<project>/<kind>/<topic-slug>.
//
// The slug keeps ASCII [a-z0-9_], folds A-Z to lowercase and maps every
// other rune to '_', matching the Rust rule byte-for-byte.
func (m MinedItem) QualifiedName(project string) string {
	projectName := projectName(project)
	var b strings.Builder
	for _, r := range m.Topic {
		switch {
		case isSlugKeep(r):
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteByte('_')
		}
	}
	return "conversations/" + projectName + "/" + m.Kind.String() + "/" + b.String()
}

// isSlugKeep reports the runes a slug keeps verbatim: ASCII alphanumerics
// and underscore. Every other rune (including non-ASCII letters) maps to '_',
// which is exactly the Rust slug rule.
func isSlugKeep(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
}

// projectName is the project directory's base name, "project" when the path
// has none (mirrors the Rust file_name().unwrap_or("project")).
func projectName(project string) string {
	base := filepath.Base(project)
	if base == "" || base == "." || base == ".." || base == string(filepath.Separator) {
		return "project"
	}
	return base
}

// GraphElements returns the element + relationships for the graph. Decision
// nodes link to code targets via `decided_about`; other kinds carry no
// outgoing edges. Raw verbatim, source, participants, timestamp and topic
// live in element metadata; relationships carry the verbatim and an
// EXTRACTED confidence label.
func (m MinedItem) GraphElements(project string) (store.Element, []store.Relationship) {
	qn := m.QualifiedName(project)
	participants := m.Participants
	if participants == nil {
		participants = []string{}
	}
	el := store.Element{
		QualifiedName: qn,
		ElementType:   m.Kind.String(),
		Name:          m.Topic,
		FilePath:      "conversations/" + m.Source + "/" + m.Kind.String(),
		LineStart:     1,
		LineEnd:       1,
		Language:      "conversation",
		Metadata: map[string]any{
			"kind":         m.Kind.String(),
			"verbatim":     m.Verbatim,
			"source":       m.Source,
			"participants": participants,
			"timestamp":    m.Timestamp,
			"topic":        m.Topic,
		},
	}

	var relationships []store.Relationship
	if m.Kind == KindDecision {
		for _, target := range m.CodeTargets {
			relationships = append(relationships, store.Relationship{
				Source:     qn,
				Target:     target,
				RelType:    "decided_about",
				Confidence: 1.0,
				Metadata: map[string]any{
					"verbatim":         m.Verbatim,
					"confidence_label": "EXTRACTED",
				},
			})
		}
	}
	return el, relationships
}

// MiningResult is the aggregated result of a mining run.
type MiningResult struct {
	Items                []MinedItem `json:"items"`
	Sources              int         `json:"sources"`
	ElementsIndexed      int         `json:"elements_indexed"`
	RelationshipsCreated int         `json:"relationships_created"`
}

// Summary renders the one-line CLI report of a mining run.
func (r MiningResult) Summary() string {
	kinds := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		kinds = append(kinds, it.Kind.String())
	}
	sort.Strings(kinds)
	kinds = dedupeSorted(kinds)
	word := "items"
	if len(r.Items) == 1 {
		word = "item"
	}
	return fmt.Sprintf("Mined %d %s from %d source(s) [%s]: %d elements, %d relationships",
		len(r.Items), word, r.Sources, strings.Join(kinds, ", "),
		r.ElementsIndexed, r.RelationshipsCreated)
}

// dedupeSorted collapses equal neighbours of a sorted slice.
func dedupeSorted(xs []string) []string {
	out := xs[:0]
	for i, x := range xs {
		if i == 0 || x != xs[i-1] {
			out = append(out, x)
		}
	}
	return out
}
