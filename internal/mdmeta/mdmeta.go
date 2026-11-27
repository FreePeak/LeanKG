// Package mdmeta holds the markdown note helpers shared by the Obsidian vault
// sync and the memory path: the frontmatter splitter and the wiki-link
// extractor. It is a leaf package and imports nothing from this repository, so
// callers that need only these two functions do not pull in the store.
package mdmeta

import (
	"regexp"
	"strings"
)

// wikiLinkRE matches [[target]], [[target|alias]], [[target#heading]], and the
// inner target of ![[embed]].
var wikiLinkRE = regexp.MustCompile(`\[\[([^\[\]]+)\]\]`)

// SplitFrontmatter ports the Rust parse_frontmatter and additionally returns
// the note body: the leading --- fence is scanned to the closing fence, every
// line splits on the FIRST ':' with both sides trimmed, and values keep any
// surrounding quotes. Lines before the opening fence are ignored.
func SplitFrontmatter(content string) (meta map[string]string, body string) {
	meta = map[string]string{}
	lines := strings.Split(content, "\n")
	in, end := false, 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			if in {
				end = i + 1
				break
			}
			in = true
			continue
		}
		if !in {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			meta[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if end > 0 {
		return meta, strings.Trim(strings.Join(lines[end:], "\n"), "\n")
	}
	return meta, strings.Trim(content, "\n")
}

// WikiLinks extracts [[target]] targets from note text, stripping Obsidian
// aliases (|label) and heading or block anchors (#heading). Targets keep
// document order with duplicates removed.
//
// ponytail: links inside fenced code blocks are picked up too; a
// fenced-region pre-pass is the upgrade path if that ever matters.
func WikiLinks(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range wikiLinkRE.FindAllStringSubmatch(content, -1) {
		target := m[1]
		if i := strings.IndexByte(target, '|'); i >= 0 {
			target = target[:i]
		}
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	return out
}
