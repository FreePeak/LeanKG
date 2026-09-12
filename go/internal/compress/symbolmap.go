package compress

import (
	"regexp"
	"sort"
	"strings"
)

const (
	// minIdentLength is the shortest identifier worth mapping (Rust
	// MIN_IDENT_LENGTH).
	minIdentLength = 6
	// mapEntryOverhead approximates the token cost of one "[MAP]" entry
	// (Rust MAP_ENTRY_OVERHEAD).
	mapEntryOverhead = 2
)

// identifierRe matches source identifiers.
var identifierRe = regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]*\b`)

// AnchorGenerator yields bijective base-52 anchors: A..Z, a..z, AA, AB, ...
// (Rust symbol_map::AnchorGenerator).
type AnchorGenerator struct {
	index int
}

// Next returns the next anchor.
func (g *AnchorGenerator) Next() string {
	num := g.index
	g.index++
	var result []byte
	for {
		rem := num % 52
		var chr byte
		if rem < 26 {
			chr = 'A' + byte(rem)
		} else {
			chr = 'a' + byte(rem-26)
		}
		result = append([]byte{chr}, result...)
		if num < 52 {
			break
		}
		num = num/52 - 1
	}
	return string(result)
}

// SymbolMap maps long identifiers to short anchors (Rust
// symbol_map::SymbolMap).
type SymbolMap struct {
	forward       map[string]string
	existingWords map[string]struct{}
}

// NewSymbolMap reserves every identifier already present in content so
// anchors never collide with source text.
func NewSymbolMap(content string) *SymbolMap {
	existingWords := make(map[string]struct{})
	for _, m := range identifierRe.FindAllString(content, -1) {
		existingWords[m] = struct{}{}
	}
	return &SymbolMap{forward: map[string]string{}, existingWords: existingWords}
}

// Register assigns an anchor to identifier; ok is false for identifiers
// shorter than minIdentLength. Already-mapped identifiers return their
// existing anchor.
func (m *SymbolMap) Register(identifier string) (short string, ok bool) {
	if len(identifier) < minIdentLength {
		return "", false
	}
	if existing, mapped := m.forward[identifier]; mapped {
		return existing, true
	}
	gen := &AnchorGenerator{}
	for {
		short := gen.Next()
		if _, used := m.existingWords[short]; !used {
			m.forward[identifier] = short
			m.existingWords[short] = struct{}{}
			return short, true
		}
	}
}

// Apply replaces registered identifiers in text, longest first (plain
// substring replacement, matching the Rust str::replace behavior).
func (m *SymbolMap) Apply(text string) string {
	if len(m.forward) == 0 {
		return text
	}
	type entry struct{ long, short string }
	entries := make([]entry, 0, len(m.forward))
	for l, s := range m.forward {
		entries = append(entries, entry{l, s})
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].long) != len(entries[j].long) {
			return len(entries[i].long) > len(entries[j].long)
		}
		return entries[i].long < entries[j].long
	})
	for _, e := range entries {
		text = strings.ReplaceAll(text, e.long, e.short)
	}
	return text
}

// FormatTable renders the "\n[MAP]:" legend (Rust SymbolMap::format_table).
func (m *SymbolMap) FormatTable() string {
	if len(m.forward) == 0 {
		return ""
	}
	type entry struct{ long, short string }
	entries := make([]entry, 0, len(m.forward))
	for l, s := range m.forward {
		entries = append(entries, entry{l, s})
	}
	sort.Slice(entries, func(i, j int) bool {
		if len(entries[i].short) != len(entries[j].short) {
			return len(entries[i].short) < len(entries[j].short)
		}
		return entries[i].short < entries[j].short
	})
	var b strings.Builder
	b.WriteString("\n[MAP]:")
	for _, e := range entries {
		b.WriteString("\n  " + e.short + "=" + e.long)
	}
	return b.String()
}

// Len returns the number of registered mappings.
func (m *SymbolMap) Len() int { return len(m.forward) }

// IsEmpty reports whether nothing is registered.
func (m *SymbolMap) IsEmpty() bool { return len(m.forward) == 0 }

// ShouldRegister reports whether mapping identifier for occurrences uses is
// net-positive in tokens (Rust symbol_map::should_register).
func ShouldRegister(identifier string, occurrences int) bool {
	if len(identifier) < minIdentLength {
		return false
	}
	identTokens := EstimateTokens(identifier)
	shortTokens := 1 // pure alphabets are 1 token
	savingPerUse := identTokens - shortTokens
	if savingPerUse < 0 {
		savingPerUse = 0
	}
	if savingPerUse == 0 {
		return false
	}
	totalSavings := occurrences * savingPerUse
	entryCost := identTokens + shortTokens + mapEntryOverhead
	return totalSavings > entryCost
}

func isKeyword(word, ext string) bool {
	switch ext {
	case "rs":
		switch word {
		case "continue", "default", "return", "struct", "unsafe", "where", "match", "impl":
			return true
		}
	case "ts", "tsx", "js", "jsx":
		switch word {
		case "constructor", "arguments", "undefined", "prototype", "instanceof", "function":
			return true
		}
	case "py":
		switch word {
		case "continue", "lambda", "return", "import", "class", "def":
			return true
		}
	case "go":
		switch word {
		case "continue", "default", "return", "struct", "interface", "func":
			return true
		}
	}
	return false
}

// ExtractIdentifiers returns identifiers worth mapping, sorted by estimated
// savings. Ties are broken lexicographically for determinism (the Rust
// HashMap iteration made tie order random).
func ExtractIdentifiers(content, ext string) []string {
	counts := map[string]int{}
	for _, w := range identifierRe.FindAllString(content, -1) {
		if len(w) >= minIdentLength && !isKeyword(w, ext) {
			counts[w]++
		}
	}
	type item struct {
		ident string
		count int
	}
	var items []item
	for ident, count := range counts {
		if ShouldRegister(ident, count) {
			items = append(items, item{ident, count})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		si, sj := len(items[i].ident)*items[i].count, len(items[j].ident)*items[j].count
		if si != sj {
			return si > sj
		}
		return items[i].ident < items[j].ident
	})
	idents := make([]string, 0, len(items))
	for _, it := range items {
		idents = append(idents, it.ident)
	}
	return idents
}
