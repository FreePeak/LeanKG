package projectcfg

import (
	"bytes"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ----------------------------------------------------------------------
// N1 (cycle-2 R2a): leankg.yaml writers must preserve user fields.
//
// Every writer of a project config (CLI `init`, the `mcp_init` tool, the
// setup pipeline) used to serialize a freshly generated ProjectConfig
// straight over the existing file, dropping `project.project_path` (the
// schema identity anchor) and every key serde does not model. The helpers
// below implement read-modify-write: EXISTING keys always win, missing keys
// are filled from the generated config.
// ----------------------------------------------------------------------

// FillMissingKeys recursively fills keys missing in target from source
// (Rust fill_missing_yaml_keys). Scalars and sequences present in target are
// never touched; mappings merge depth-first so nested user overrides survive.
// Either node may be a YAML document node (its mapping root is used); anything
// without a mapping root is a no-op.
func FillMissingKeys(target, source *yaml.Node) {
	t, s := mappingRoot(target), mappingRoot(source)
	if t == nil || s == nil {
		return
	}
	for i := 0; i+1 < len(s.Content); i += 2 {
		key, val := s.Content[i], s.Content[i+1]
		if got := mappingValue(t, key.Value); got != nil {
			FillMissingKeys(got, val)
			continue
		}
		t.Content = append(t.Content, cloneNode(key), cloneNode(val))
	}
}

// MergePreservingExisting merges a freshly generated config UNDER an existing
// leankg.yaml document: existing keys — including fields this package does not
// model — win, missing keys are filled from fresh. An unparseable document, or
// an empty one, falls back to the fresh serialization (nothing recoverable to
// preserve); a parseable document of any other shape is returned verbatim.
func MergePreservingExisting(existing string, fresh ProjectConfig) string {
	freshDoc, err := nodeOf(fresh)
	if err != nil {
		return ""
	}
	freshText := func() string {
		out, merr := marshalNode(freshDoc)
		if merr != nil {
			return ""
		}
		return string(out)
	}
	merged, ok := parseYAML(existing)
	if !ok {
		return freshText()
	}
	FillMissingKeys(merged, freshDoc)
	out, merr := marshalNode(merged)
	if merr != nil {
		return freshText()
	}
	return string(out)
}

// WriteConfigPreservingExisting writes fresh to path, preserving user fields
// when the file already exists and parses. Parent directories are created as
// needed; a missing file is simply created with the fresh content.
func WriteConfigPreservingExisting(path string, fresh ProjectConfig) error {
	if parent := filepath.Dir(path); parent != "" && parent != "." {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	content, err := Marshal(fresh)
	if err != nil {
		return err
	}
	if existing, rerr := os.ReadFile(path); rerr == nil {
		content = []byte(MergePreservingExisting(string(existing), fresh))
	}
	return os.WriteFile(path, content, 0o666)
}

// EnsureIdentityFields refills a MISSING project.project_path anchor in an
// existing config using identityHint — the canonical path the caller is about
// to key on. Every other key, including unmodelled custom fields, is preserved
// verbatim; an anchor already present is never touched, and missing files are
// skipped. A document that is not a top-level mapping, or whose `project` key
// is not a mapping, is left alone.
func EnsureIdentityFields(configPath, identityHint string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil
	}
	doc, ok := parseYAML(string(data))
	if !ok {
		return nil
	}
	project := mappingRoot(mappingValue(mappingRoot(doc), "project"))
	if project == nil {
		return nil
	}
	if mappingValue(project, "project_path") != nil {
		return nil
	}
	project.Content = append(project.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "project_path"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: identityHint},
	)
	out, err := marshalNode(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, out, 0o666)
}

// EnsureIdentityFieldsForDB runs EnsureIdentityFields over every conventional
// location of a project config around a .leankg dir: <root>/.leankg/leankg.yaml,
// <root>/leankg.yaml, and the same pair one level up (the `index ./src`
// invocation style anchors .leankg inside the source dir while the config lives
// at the repo root). Only files that already exist are touched.
func EnsureIdentityFieldsForDB(dbPath, identityHint string) {
	var roots []string
	if r := filepath.Dir(dbPath); r != "" {
		roots = append(roots, r)
		if g := filepath.Dir(r); g != r {
			roots = append(roots, g)
		}
	}
	seen := make(map[string]bool, len(roots))
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, cfg := range []string{
			filepath.Join(root, ".leankg", ConfigFileName),
			ConfigPath(root),
		} {
			_ = EnsureIdentityFields(cfg, identityHint)
		}
	}
}

// Marshal renders a config the way the Rust writers did
// (`serde_yaml::to_string(&config)`): unset optional blocks stay absent and an
// unset project_path emits no key at all.
func Marshal(cfg ProjectConfig) ([]byte, error) {
	doc, err := nodeOf(cfg)
	if err != nil {
		return nil, err
	}
	return marshalNode(doc)
}

// parseYAML parses src into a document node tree. ok=false covers a syntax
// error and an empty/comment-only document (there is nothing to preserve).
func parseYAML(src string) (*yaml.Node, bool) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		return nil, false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, false
	}
	return &doc, true
}

// IsMappingDocument reports whether src parses as a YAML document whose root
// is a mapping — the only shape a leankg.yaml can be. A syntax error, an
// empty/comment-only document, and a scalar/sequence root all report false.
//
// Writers use this to decide between read-modify-write and leaving the file
// alone: an unparseable or non-mapping file is not project config, and
// rewriting it would discard whatever the user actually wrote there.
func IsMappingDocument(src string) bool {
	doc, ok := parseYAML(src)
	if !ok {
		return false
	}
	root := doc.Content[0]
	return root.Kind == yaml.MappingNode
}

// MergePreservingExistingUnder merges a template DOCUMENT (raw YAML text)
// under an existing leankg.yaml: existing keys — including fields this
// package does not model — win, missing template keys are filled in, and the
// result is rendered. It reports ok=false when the existing document is not a
// mapping (nothing recoverable to merge, so the caller keeps what is there).
//
// This is the raw-text sibling of MergePreservingExisting, for writers whose
// fresh document is a literal template rather than a ProjectConfig value.
// Serializing a ProjectConfig emits every field of the Go shape (an empty
// `steer:` block, `microservice: null`, `documentation: ./docs`, a zero
// `mcp.port`); merging the template text directly keeps the generated file
// byte-shaped like the template plus whatever the user had.
//
// The rendered text is "" only on an internal encoder failure — callers
// treat that as "leave the file alone".
func MergePreservingExistingUnder(existing, template string) (string, bool) {
	merged, ok := parseYAML(existing)
	if !ok || mappingRoot(merged) == nil {
		return "", false
	}
	tmpl, tok := parseYAML(template)
	if !tok || mappingRoot(tmpl) == nil {
		return "", false
	}
	FillMissingKeys(merged, tmpl)
	out, err := marshalNode(merged)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// nodeOf renders v to YAML and re-parses it into a document node, so callers
// can merge node trees instead of Go values.
func nodeOf(v any) (*yaml.Node, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// marshalNode renders a node tree with serde_yaml's two-space indentation.
func marshalNode(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// mappingRoot descends a document node to its mapping root and follows aliases.
// It returns nil when the node does not resolve to a mapping.
func mappingRoot(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n != nil && n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		n = n.Content[0]
		for n != nil && n.Kind == yaml.AliasNode {
			n = n.Alias
		}
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// mappingValue returns the value node stored under key, or nil when the
// mapping has no such key.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Kind == yaml.ScalarNode && m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// cloneNode deep-copies a node tree so a merged document never shares nodes
// with the fresh config it was filled from. Anchors are dropped: the fresh
// serialization has none, and re-emitting a clone under the original anchor
// name would duplicate the definition.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Anchor = ""
	c.Content = nil
	for _, child := range n.Content {
		c.Content = append(c.Content, cloneNode(child))
	}
	return &c
}
