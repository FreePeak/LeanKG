package main

import (
	_ "embed"
	"strings"
)

// embeddedVersion comes from go/cmd/leankg/VERSION (checked in; release-please
// bumps it on release — the trailing marker comment is not part of the version).
//
//go:embed VERSION
var embeddedVersion string

// version is the ldflags-overridable engine version
// (-X main.version=<tag> in .github/workflows/release-go.yml).
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
