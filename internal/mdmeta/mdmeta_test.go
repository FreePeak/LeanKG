package mdmeta

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	content := "---\r\nleankg_id: ./src/a.rs::A\r\nleankg_annotation: \"quoted: value\"\r\nleankg_relationships:\r\n---\r\n\r\n# Body\r\n"
	content = strings.ReplaceAll(content, "\r\n", "\n")
	meta, body := SplitFrontmatter(content)
	if meta["leankg_id"] != "./src/a.rs::A" {
		t.Fatalf("leankg_id = %q", meta["leankg_id"])
	}
	if meta["leankg_annotation"] != `"quoted: value"` {
		t.Fatalf("annotation value must keep its quotes, got %q", meta["leankg_annotation"])
	}
	if body != "# Body" {
		t.Fatalf("body = %q, want %q", body, "# Body")
	}

	// No frontmatter at all: everything is body, no keys.
	meta, body = SplitFrontmatter("# Plain\n\ntext\n")
	if len(meta) != 0 || body != "# Plain\n\ntext" {
		t.Fatalf("plain note parsed as meta=%v body=%q", meta, body)
	}
}

func TestSplitFrontmatterTopLevelType(t *testing.T) {
	meta, _ := SplitFrontmatter("---\ntype: feedback\n---\nbody\n")
	if meta["type"] != "feedback" {
		t.Fatalf("top-level type = %q, want %q (meta=%v)", meta["type"], "feedback", meta)
	}
}

func TestSplitFrontmatterIndentedTypeUnderMetadata(t *testing.T) {
	// The splitter cuts each line on the first colon and trims key and value,
	// so an indented key under a parent key still yields the bare key "type".
	meta, _ := SplitFrontmatter("---\nmetadata:\n  type: feedback\n---\nbody\n")
	if meta["type"] != "feedback" {
		t.Fatalf("indented type = %q, want %q (meta=%v)", meta["type"], "feedback", meta)
	}
	if _, ok := meta["metadata"]; !ok {
		t.Fatalf("parent key metadata missing (meta=%v)", meta)
	}
}

func TestWikiLinks(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []string
	}{
		{"plain", "see [[alpha]] now", []string{"alpha"}},
		{"alias stripped", "[[target|label]]", []string{"target"}},
		{"heading anchor stripped", "[[note#Heading]]", []string{"note"}},
		{"alias and anchor stripped", "[[note#Heading|shown]]", []string{"note"}},
		{"whitespace trimmed", "[[  spaced  ]]", []string{"spaced"}},
		{"empty target skipped", "[[|only alias]] [[#only-anchor]]", nil},
		{"duplicates removed in document order", "[[b]] [[a]] [[b]] [[c]] [[a]]", []string{"b", "a", "c"}},
		{"no links", "plain text", nil},
		{"nested brackets not matched", "[[[inner]]]", []string{"inner"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := WikiLinks(tc.content)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("WikiLinks(%q) = %#v, want %#v", tc.content, got, tc.want)
			}
		})
	}
}
