package summarize

import "testing"

// A URL's port may contain the digits 402 (an httptest server on :54021 once
// turned a transient transport error into a "quota exhausted" terminal stop in
// CI). Bare digits only count when bracketed as a status code.
func TestTerminalReasonIgnoresPortDigits(t *testing.T) {
	if got := terminalReason(`Post "http://127.0.0.1:54021/v1/chat/completions": read: connection reset`); got != "" {
		t.Errorf("port digits must not read as quota: %q", got)
	}
	if got := terminalReason("provider returned 402 for this key"); got == "" {
		t.Error("status 402 must stay terminal")
	}
}
