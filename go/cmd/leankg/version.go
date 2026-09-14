package main

import (
	_ "embed"
	"strings"
)

// embeddedVersion comes from go/cmd/leankg/VERSION, the one version source in
// the tree. The release pipeline owns it: release-please-config.json declares
// it as `version-file`, so a release rewrites the whole file to the new
// version. Parsed by field so trailing whitespace can never become a version.
//
//go:embed VERSION
var embeddedVersion string

// version is the ldflags-overridable engine version
// (-X main.version=<version> in .github/workflows/release.yml).
//
// It MUST be initialized with a constant expression: the Go linker only
// honours -X for a string variable whose initializer is constant, so the
// previous `var version = strings.TrimSpace(embeddedVersion)` silently ignored
// the release stamp (verified: -X main.version=9.9.9 produced "leankg 0.31.0",
// while cmd/leankg-embed's constant initializer stamped correctly).
var version = ""

// Version returns the engine version: the ldflags stamp when set, else the
// first field of the checked-in VERSION file (local and CI builds).
func Version() string {
	if v := strings.TrimSpace(version); v != "" {
		return v
	}
	if f := strings.Fields(embeddedVersion); len(f) > 0 {
		return f[0]
	}
	return "dev"
}
