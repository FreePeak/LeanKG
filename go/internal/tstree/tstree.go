//go:build tstree

// Package tstree is the CGO tree-sitter tier (build tag `tstree`). The
// default build excludes it entirely; `go build -tags tstree` compiles the
// bundled grammars (smacker/go-tree-sitter vendors the C sources).
//
// Grammar coverage vs the 13 default languages: go, rust, ts, tsx, js/jsx,
// py, java, kotlin, swift are bundled. objective-c, dart and markdown are
// NOT available in this grammar set — those languages stay on the regex
// tier (markdown via docindex; objc/dart + LSP when a server resolves),
// which is an honest tier gap, not a silent fallback.
package tstree

import (
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	tsswift "github.com/smacker/go-tree-sitter/swift"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"
)

// Grammar returns the tree-sitter language for a registry language id, or nil
// when no grammar is bundled for it (regex tier only).
func Grammar(lang string) *sitter.Language {
	switch lang {
	case "go":
		return golang.GetLanguage()
	case "rust":
		return rust.GetLanguage()
	case "ts":
		return typescript.GetLanguage()
	case "tsx":
		return tsx.GetLanguage()
	case "js", "jsx":
		return javascript.GetLanguage()
	case "py":
		return python.GetLanguage()
	case "java":
		return java.GetLanguage()
	case "kotlin":
		return kotlin.GetLanguage()
	case "swift":
		return tsswift.GetLanguage()
	// md: no GetLanguage exported by this grammar version — markdown stays
	// on the regex/docindex tier.
	}
	return nil // objc, dart: no bundled grammar
}
