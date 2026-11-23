package telemetry

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Redaction runs before anything is queued (DS-02). Each rule replaces the
// secret and keeps enough shape to show that something was there.
var redactRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// PEM private key blocks: a terminated block, or a truncated one whose
	// base64 body follows its BEGIN line. The marker must start a line, so a
	// source line that quotes it is left alone.
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`), "[REDACTED:pem]"},
	{regexp.MustCompile(`(?m)^-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----(?:\n[A-Za-z0-9+/=]+)*`), "[REDACTED:pem]"},
	// Bearer credentials in headers or pasted logs. A token must carry a digit,
	// so prose such as "bearer authentication" is kept.
	{regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]*[0-9][A-Za-z0-9._~+/=-]*`), "${1}[REDACTED]"},
	// Basic credentials only in an Authorization header.
	{regexp.MustCompile(`(?i)(\bauthorization:[ \t]*basic[ \t]+)[A-Za-z0-9+/=]{4,}`), "${1}[REDACTED]"},
	// Password in a URL's userinfo: scheme://user:password@host keeps the user.
	{regexp.MustCompile(`(://[^\s:/@]+:)[^\s/@]+@`), "${1}[REDACTED]@"},
	// JSON Web Tokens: three base64url segments, the first a JSON header.
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`), "[REDACTED:jwt]"},
	// Provider keys. The Anthropic form is listed before the generic sk- form.
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{10,}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bsk_live_[A-Za-z0-9]{16,}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`), "[REDACTED:key]"},
	{regexp.MustCompile(`\bxox[bpaors]-[A-Za-z0-9-]{10,}`), "[REDACTED:key]"},
	// LeanKG bearer tokens: lkg_<32hex>_<8hex> (internal/auth.NewSecret), or the
	// bare lkg_<32hex> key id a log line may carry without its suffix.
	{regexp.MustCompile(`\blkg_[0-9a-f]{32}(?:_[0-9a-f]{8})?`), "[REDACTED:key]"},
	// key=value and JSON "key": "value" pairs whose key names a secret. The
	// separator stays on one line, and the value must not start with an
	// operator (so `token := x` and `token == x` are code, not secrets) and
	// must be at least 6 characters (so `token: 5` is kept). The prefix may
	// carry a qualifier (db_password), but "max_tokens" does not match because
	// "token" must be followed by the separator.
	{regexp.MustCompile(`(?i)\b([a-z0-9_-]*(?:password|passwd|secret|token|api[_-]?key|secret[_-]access[_-]key)"?[ \t]*[=:][ \t]*"?)[^\s"'=,;&}][^\s"',;&}]{5,}`), "${1}[REDACTED]"},
}

// Redact masks secrets (bearer/JWT, sk-/ghp_/AKIA keys, password=, PEM
// blocks) and rewrites absolute paths under $HOME to "~" (DS-02).
func Redact(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return redactHome(s)
}

// redactHome rewrites the real home directory to "~" so a shared ledger
// does not name the user's account. Only a whole path element matches, so a
// sibling such as /Users/x2 is left alone.
func redactHome(s string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || home == "/" {
		return s
	}
	s = strings.ReplaceAll(s, home+"/", "~/")
	if i := strings.LastIndex(s, home); i >= 0 {
		re := regexp.MustCompile(regexp.QuoteMeta(home) + `($|[\s"',)\]:])`)
		s = re.ReplaceAllString(s, "~${1}")
	}
	return s
}

// Cap truncates s to at most max bytes (on a rune boundary) with a marker.
// max <= 0 means no cap.
func Cap(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "...[truncated]"
}

// ExpandHome reverses the "~" rewrite Redact applies to paths: "~" and "~/x"
// resolve against the real home directory; anything else is returned as is.
// The dashboard needs it to reopen a transcript whose path was stored redacted.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
