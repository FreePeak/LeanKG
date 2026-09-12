package main

import (
	_ "embed"
	"strings"
)

// embeddedVersion comes from go/cmd/leankg/VERSION (checked in, mirrors go/VERSION).
//
//go:embed VERSION
var embeddedVersion string

// version is the ldflags-overridable engine version
// (-X main.version=<tag> in .github/workflows/release-go.yml).
var version = strings.TrimSpace(embeddedVersion)

// Version returns the engine version.
func Version() string { return version }
