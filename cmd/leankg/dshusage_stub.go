//go:build !dshusage

package main

import (
	"fmt"
	"os"
)

// cmdDSHUsage is a stub when LeanKG is built without -tags dshusage.
// The DSH usage dashboard is an optional dogfood feature, default off.
func cmdDSHUsage(args []string) {
	fmt.Fprintln(os.Stderr, `leankg dsh-usage is an optional feature (default off).

Rebuild with the tag to enable:
  go build -tags dshusage -o bin/leankg ./cmd/leankg
  # or: make go-build-dshusage

Then:
  leankg dsh-usage [--addr 127.0.0.1:9710]
  leankg dsh-usage --watch                 # continuous classify + asks
  # Laya scoring is also off unless LAYA_URL / LEANKG_JUDGE_SIDECAR_URL is set,
  # or you pass --laya-url. Dashboard: ?laya=1 only scores when a judge is configured.

Docs: docs/dsh-root-causes.md, docs/laya-shared-service.md, docs/judge-use-cases.md (UC-9)`)
	os.Exit(2)
}
