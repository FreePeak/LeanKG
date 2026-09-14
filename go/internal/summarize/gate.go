package summarize

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// MaxConsecutiveFailures is the cutoff that ends a pass (graft failure.ts:23).
// One flaky file is normal; five in a row is a provider that is not going to
// start working, and every further call is spend with no chance of a result —
// that is graft #127's 1,617 doomed calls, and the reason this gate exists.
const MaxConsecutiveFailures = 5

// Terminal-error patterns (graft failure.ts:29-36), matched on the message
// because the message is all a provider-neutral transport carries. Graft's own
// asymmetry is kept: over-matching costs a run that was going to fail anyway,
// under-matching falls back to the consecutive cutoff.
var (
	quotaRe = regexp.MustCompile(`quota|insufficient[_ ]funds|insufficient[_ ]quota|billing|payment required|\b402\b`)
	authRe  = regexp.MustCompile(`\b401\b|\b403\b|unauthorized|invalid api key|invalid_api_key|authentication`)
)

// terminalReason names why no amount of retrying fixes this error, or "" when
// the error may be transient.
func terminalReason(message string) string {
	m := strings.ToLower(message)
	switch {
	case quotaRe.MatchString(m):
		return "the provider reports the quota/credit for this key is exhausted"
	case authRe.MatchString(m):
		return "the provider rejected the API key"
	}
	return ""
}

// gate is graft's LlmFailureGate: the failure bookkeeping of one run, shared by
// both passes so they agree on "the provider stopped serving" and the caller
// reports it once. Mutex-guarded here because pass 1 runs concurrently; graft
// needed no lock only because its workers are single-threaded between awaits.
type gate struct {
	mu          sync.Mutex
	failed      int
	skipped     int
	consecutive int
	fatal       string
}

// record notes a failed unit and sets the fatal reason when this failure is
// the last straw.
func (g *gate) record(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failed++
	if g.fatal != "" {
		return
	}
	if terminal := terminalReason(msg); terminal != "" {
		g.fatal = fmt.Sprintf("%s — stopped after %d failed file(s). First error: %s", terminal, g.failed, msg)
		return
	}
	g.consecutive++
	if g.consecutive >= MaxConsecutiveFailures {
		g.fatal = fmt.Sprintf("%d files in a row failed, so the pass stopped rather than keep calling. Last error: %s", g.consecutive, msg)
	}
}

// recordQuality counts a unit the provider ANSWERED but that produced nothing
// usable (graft's #235/#177 content-quality miss): it is reported as a failure
// so it can never be mistaken for success, but it does not advance the
// consecutive cutoff. Quota and auth stay immediately terminal.
func (g *gate) recordQuality(msg string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failed++
	if g.fatal == "" {
		if terminal := terminalReason(msg); terminal != "" {
			g.fatal = fmt.Sprintf("%s — stopped after %d failed file(s). First error: %s", terminal, g.failed, msg)
		}
	}
}

// succeeded breaks an unbroken failure run.
func (g *gate) succeeded() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.consecutive = 0
}

// stop notes a unit never attempted because the pass had already given up.
func (g *gate) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.skipped++
}

// stopped reports whether the pass should stop issuing calls.
func (g *gate) stopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fatal != ""
}

func (g *gate) fatalReason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fatal
}

func (g *gate) counts() (failed, skipped int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failed, g.skipped
}
