// Package update implements `leankg update` (issue #73): replace the running
// binary with the newest GitHub Release for this platform.
//
// Flow: resolve the latest release (tags are `v<version>`, matching
// .github/workflows/release-go.yml) → compare against the embedded version →
// download the platform asset `leankg-<goos>-<goarch>.tgz` with a hard size
// ceiling → verify (SHA256 when the release publishes one; otherwise the tar
// contents: both binaries present, executable, stamped with the requested
// version, and the archive names nothing outside that set) → swap atomically
// with a rename, after backing the previous files up next to them as
// `leankg.old` / `leankg-embed.old`.
//
// Nothing is deleted-then-copied and nothing is mutated before the archive has
// been fully verified, so a corrupt or unexpected download provably leaves the
// working install untouched.
//
// The package reaches the network only through net/http with an injected
// Client and API base, so the tests are hermetic (httptest + t.TempDir).
package update

import (
	"fmt"
	"strconv"
	"strings"
)

// Compare orders two semver-ish version strings: -1 when a is older than b, 0
// when equal, +1 when newer. Leading `v`, surrounding space, and `+build`
// metadata are ignored; a missing numeric component reads as 0, so `1.2` and
// `1.2.0` compare equal. An unparseable version (empty, non-numeric core, more
// than three components) is an error rather than a guess — the caller must not
// download a replacement when it cannot tell which direction the change goes.
//
// Equal numeric cores rank a release above its own prereleases (1.0.0 >
// 1.0.0-rc1), then compare prerelease labels lexically.
func Compare(a, b string) (int, error) {
	av, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range 3 {
		switch {
		case av.core[i] < bv.core[i]:
			return -1, nil
		case av.core[i] > bv.core[i]:
			return 1, nil
		}
	}
	switch {
	case av.pre == "" && bv.pre != "":
		return 1, nil
	case av.pre != "" && bv.pre == "":
		return -1, nil
	case av.pre < bv.pre:
		return -1, nil
	case av.pre > bv.pre:
		return 1, nil
	}
	return 0, nil
}

type version struct {
	core [3]int
	pre  string
}

// parseVersion splits `v1.2.3-rc1+build7` into its numeric core and prerelease.
func parseVersion(s string) (version, error) {
	raw := strings.TrimSpace(s)
	v := strings.TrimPrefix(raw, "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	out := version{}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		out.pre = v[i+1:]
		v = v[:i]
	}
	if v == "" {
		return version{}, fmt.Errorf("invalid version %q", raw)
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return version{}, fmt.Errorf("invalid version %q", raw)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("invalid version %q", raw)
		}
		out.core[i] = n
	}
	return out, nil
}
