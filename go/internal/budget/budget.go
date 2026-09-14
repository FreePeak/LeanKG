// Package budget ports the Rust budget model (src/budget.rs +
// src/mcp/token_budget.rs) onto the Go engine.
//
// Two halves, one package because both answer "how much response/output is
// this tool allowed to produce":
//
//   - BudgetGuard (budget.go): per-call wall-clock / RSS / iteration caps for
//     heavy loops (impact, export, consistency, path, ...). Constructed once
//     per call and checked inside the hot loop.
//   - TokenBudget (tokens.go): per-tool response-size cap applied to a JSON
//     response before it is handed back to the caller.
//
// Configuration (env, identical names to the Rust engine):
//
//	LEANKG_TOOL_TIMEOUT_SECS (default 60)   — per-call wall-clock cap
//	LEANKG_MAX_RSS_MB        (default 4096) — soft RSS cap; abort if exceeded
//	LEANKG_TOOL_BUDGET_OFF   (default unset)— set 1 to disable every guard
package budget

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Reason classifies why a guard aborted a loop.
type Reason int

const (
	// ReasonTimeout is a wall-clock cap breach.
	ReasonTimeout Reason = iota
	// ReasonMemory is an RSS cap breach.
	ReasonMemory
	// ReasonIterations is a caller-chosen iteration cap breach.
	ReasonIterations
)

// BudgetExceeded is the error returned by BudgetGuard.Check. It mirrors Rust
// `budget::BudgetExceeded` including the rendered message, so CLI/MCP surfaces
// print the same text the Rust engine did.
type BudgetExceeded struct {
	Reason Reason
	Tool   string
	// ElapsedSecs / CapSecs are set for ReasonTimeout.
	ElapsedSecs, CapSecs uint64
	// RSSMb / CapMb are set for ReasonMemory.
	RSSMb, CapMb uint64
	// Count / Cap are set for ReasonIterations.
	Count, Cap uint64
}

func (e *BudgetExceeded) Error() string {
	switch e.Reason {
	case ReasonTimeout:
		return fmt.Sprintf("tool '%s' aborted: budget timeout (%ds >= %ds)", e.Tool, e.ElapsedSecs, e.CapSecs)
	case ReasonMemory:
		return fmt.Sprintf("tool '%s' aborted: RSS budget exceeded (%d MB >= %d MB). "+
			"Raise LEANKG_MAX_RSS_MB or scope the query smaller.", e.Tool, e.RSSMb, e.CapMb)
	default:
		return fmt.Sprintf("tool '%s' aborted: iteration cap reached (%d >= %d)", e.Tool, e.Count, e.Cap)
	}
}

// NoCap disables a cap (Rust `u64::MAX`); for CapIters 0 also means no cap.
const NoCap = ^uint64(0)

// BudgetGuard wraps one algorithm invocation in wall-clock + RSS + iteration
// caps. It is cheap to construct and check; call Check once per loop body.
//
// ponytail: single-goroutine by design (the Rust original is only made
// concurrent by an AtomicBool one-shot flag). Heavy loops are per-call and
// serial; if a shared guard is ever needed, swap `reported` for sync/atomic.
type BudgetGuard struct {
	tool     string
	started  time.Time
	capSecs  uint64
	capRSSMb uint64
	capIters uint64
	iters    uint64
	disabled bool
	reported bool
}

// toolBudgetOff reports whether LEANKG_TOOL_BUDGET_OFF disables guards.
// Rust semantics: any parseable non-zero number disables; garbage does not.
func toolBudgetOff() bool {
	v, err := strconv.ParseUint(os.Getenv("LEANKG_TOOL_BUDGET_OFF"), 10, 8)
	return err == nil && v != 0
}

// defaultTimeoutSecs is LEANKG_TOOL_TIMEOUT_SECS or 60.
func defaultTimeoutSecs() uint64 {
	if v, err := strconv.ParseUint(os.Getenv("LEANKG_TOOL_TIMEOUT_SECS"), 10, 64); err == nil {
		return v
	}
	return 60
}

// DefaultRSSMb is LEANKG_MAX_RSS_MB or 4096.
func DefaultRSSMb() uint64 {
	if v, err := strconv.ParseUint(os.Getenv("LEANKG_MAX_RSS_MB"), 10, 64); err == nil {
		return v
	}
	return 4096
}

// WithCaps builds a guard with explicit caps. CapIters 0 disables the
// iteration cap; NoCap disables a time/RSS cap.
func WithCaps(tool string, capSecs, capRSSMb, capIters uint64) *BudgetGuard {
	return &BudgetGuard{
		tool:     tool,
		started:  time.Now(),
		capSecs:  capSecs,
		capRSSMb: capRSSMb,
		capIters: capIters,
		disabled: toolBudgetOff(),
	}
}

// ForTool builds a guard for tool using default caps (60s, 4 GB RSS, 1M iters).
func ForTool(tool string) *BudgetGuard {
	return WithCaps(tool, defaultTimeoutSecs(), DefaultRSSMb(), 1_000_000)
}

// IterOnly builds a guard with only an iteration cap (no time / RSS). Use for
// bounded loops whose runtime is naturally capped by the iteration count.
func IterOnly(tool string, capIters uint64) *BudgetGuard {
	return WithCaps(tool, NoCap, NoCap, capIters)
}

// Unlimited builds a guard with no caps at all. Use for tools that paginate
// internally.
func Unlimited(tool string) *BudgetGuard {
	return WithCaps(tool, NoCap, NoCap, 0)
}

// Tick increments the iteration counter. Call once per loop body.
func (g *BudgetGuard) Tick() { g.iters++ }

// Elapsed is the wall-clock time since the guard was created.
func (g *BudgetGuard) Elapsed() time.Duration { return time.Since(g.started) }

// Iterations is the current iteration count.
func (g *BudgetGuard) Iterations() uint64 { return g.iters }

// Disabled reports whether LEANKG_TOOL_BUDGET_OFF suppressed all checks.
func (g *BudgetGuard) Disabled() bool { return g.disabled }

// AlreadyReported reports whether a breach has already been returned, so
// callers can avoid emitting duplicate abort messages.
func (g *BudgetGuard) AlreadyReported() bool { return g.reported }

// RSSMbFn reads the process's current RSS in MiB. It is a variable so tests
// (and non-Unix builds) can substitute a probe; CurrentRSSMb is the default.
var RSSMbFn = CurrentRSSMb

// Exhausted reports whether any cap has been breached.
func (g *BudgetGuard) Exhausted() bool {
	if g.disabled {
		return false
	}
	if g.capIters != 0 && g.iters >= g.capIters {
		return true
	}
	if g.capSecs != NoCap && uint64(time.Since(g.started).Seconds()) >= g.capSecs {
		return true
	}
	if g.capRSSMb != NoCap {
		if rssMb, err := RSSMbFn(); err == nil && rssMb >= g.capRSSMb {
			return true
		}
	}
	return false
}

// Check returns the first breach reason, or nil when the loop may continue.
// Iteration cap wins because it is cheapest to evaluate.
func (g *BudgetGuard) Check() error {
	if g.disabled {
		return nil
	}
	if g.capIters != 0 && g.iters >= g.capIters {
		g.reported = true
		return &BudgetExceeded{Reason: ReasonIterations, Tool: g.tool, Count: g.iters, Cap: g.capIters}
	}
	if g.capSecs != NoCap {
		elapsed := uint64(time.Since(g.started).Seconds())
		if elapsed >= g.capSecs {
			g.reported = true
			return &BudgetExceeded{Reason: ReasonTimeout, Tool: g.tool, ElapsedSecs: elapsed, CapSecs: g.capSecs}
		}
	}
	if g.capRSSMb != NoCap {
		if rssMb, err := RSSMbFn(); err == nil && rssMb >= g.capRSSMb {
			g.reported = true
			return &BudgetExceeded{Reason: ReasonMemory, Tool: g.tool, RSSMb: rssMb, CapMb: g.capRSSMb}
		}
	}
	return nil
}
