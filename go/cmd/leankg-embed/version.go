package main

// Version mirrors go/VERSION at build time; release builds override it via
// -X main.version=<tag> (see .github/workflows/release-go.yml).
var version = "dev"

// Version returns the engine version.
func Version() string { return version }
