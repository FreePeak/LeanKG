package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"regexp/syntax"
	"strings"
	"sync/atomic"
)

// User deny-list (CAM-06). The operator lists extra patterns in a scrub file,
// one Go regexp per line. Redact applies them after the fixed rules, so every
// write and every read that goes through Redact is masked the same way.

// scrubbedMarker replaces each match of a user pattern.
const scrubbedMarker = "[SCRUBBED]"

// emptyScrubDigest is the digest of a scrub list with no file behind it: the
// hex sha256 of zero bytes. A missing file and an empty file both mean "no
// patterns", so both report this value and a sync that hashes the list sees
// the same source_hash either way.
var emptyScrubDigest = sha256Hex(nil)

type scrubState struct {
	patterns []*regexp.Regexp
	digest   string
}

var activeScrub atomic.Pointer[scrubState]

// LoadScrubFile reads a scrub file. Blank lines and lines whose first
// non-space character is '#' are skipped; every other line is compiled as a
// regexp (surrounding space trimmed). A missing file yields an empty list and
// a nil error. digest is the hex sha256 of the file bytes, or emptyScrubDigest
// when the file is missing. A line that does not compile fails the load with
// its 1-based line number. The regexp text is left out of the error so a
// deny-list entry is not echoed into logs.
func LoadScrubFile(path string) (patterns []*regexp.Regexp, digest string, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, emptyScrubDigest, nil
	}
	if err != nil {
		return nil, "", err
	}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, cerr := regexp.Compile(line)
		if cerr != nil {
			var se *syntax.Error
			if errors.As(cerr, &se) {
				return nil, "", fmt.Errorf("scrub file line %d: invalid pattern: %s", i+1, se.Code)
			}
			return nil, "", fmt.Errorf("scrub file line %d: invalid pattern", i+1)
		}
		patterns = append(patterns, re)
	}
	return patterns, sha256Hex(data), nil
}

// SetScrubPatterns installs the deny-list process-wide. digest is the value
// LoadScrubFile returned for the same list. It is safe to call while other
// goroutines redact; a call replaces the whole list atomically.
func SetScrubPatterns(patterns []*regexp.Regexp, digest string) {
	activeScrub.Store(&scrubState{
		patterns: append([]*regexp.Regexp(nil), patterns...),
		digest:   digest,
	})
}

// ScrubDigest returns the digest of the installed deny-list, or
// emptyScrubDigest when none has been installed.
func ScrubDigest() string {
	if st := activeScrub.Load(); st != nil {
		return st.digest
	}
	return emptyScrubDigest
}

// applyScrub masks s with the installed deny-list. With no list installed it
// returns s unchanged.
func applyScrub(s string) string {
	st := activeScrub.Load()
	if st == nil {
		return s
	}
	return scrubWith(s, st.patterns)
}

// scrubWith replaces each match of each pattern with scrubbedMarker. The text
// is split on the marker first, so a pattern never matches inside an earlier
// mask: a second pass leaves masked text unchanged.
func scrubWith(s string, patterns []*regexp.Regexp) string {
	for _, re := range patterns {
		parts := strings.Split(s, scrubbedMarker)
		for i, p := range parts {
			parts[i] = re.ReplaceAllLiteralString(p, scrubbedMarker)
		}
		s = strings.Join(parts, scrubbedMarker)
	}
	return s
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
