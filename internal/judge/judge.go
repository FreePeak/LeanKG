package judge

import (
	"context"
	"fmt"
	"strings"
)

// Judge is the one narrow seam every graduated Laya call crosses.
//
// Laya answers three typed question shapes — choice (one option from a
// defined set), score (one position on an ordered ladder), noul (P(yes)) —
// in a single forward pass (~33 ms local, providers differ). Every one of
// those shapes exists in the stdlib or in five lines of code: the cheapest
// correct thing is almost always a threshold or a table. Judge exists so the
// handful of places where a fixed rule is demonstrably the wrong shape can
// call a learned judgment WITHOUT scattering provider wiring through the
// engine: one interface, one call shape, two backends (server, local
// sidecar), native rules by default.
//
// A Judge answers one batch of questions over one state:
//
//	state     the context the judgment is about (query text, file facts,
//	          row payloads — whatever was already in memory, serialized at
//	          the call site; nothing is fetched)
//	questions one or more typed questions; independent questions fire
//	          together in one call (the fan-out pattern), because one call
//	          costs one round trip no matter how many questions it carries
//
// Every answer carries a calibrated probability plus a confidence (they are
// different axes: the answer says WHAT, the confidence says whether to act
// on it — see the TypeSafe confidence-routing pattern). Callers MUST gate on
// confidence, not on the answer alone: below the call site's floor the native
// rule stands and the judgment is ignored with a reason, exactly like the
// L3 provider degrade path in internal/core (no vector collection → degrade
// with reason, never fail the query).
//
// Graduation rule (the bar every call site must clear): a Judge call lands
// only where a recorded experiment shows the native rule failing on real
// inputs — BM25/rank overlap measured, like the jev-1.13.0 A/B in #433
// (separation 8/8, overlap 1.2/5: complementary, not redundant). Native
// first, judged only where measured. Until a site has that evidence it stays
// a row in docs/judge-use-cases.md marked CANDIDATE, not a call.

// Kind is the typed question shape. It mirrors Laya's (and the retired Jev
// API's) three primitives — choice, score, noul — so questions written
// against one backend run unchanged on the other.
type Kind string

const (
	// Choice picks one option from a defined set. Criteria maps each option
	// key to its meaning in plain words ("line: a simple line through the
	// closing prices"); the keys are the values the caller consumes, so no
	// label→argument mapping step exists (the function-calling cookbook's
	// spec.json rule: option keys ARE the accepted strings).
	Choice Kind = "choice"
	// Score rates against ordered, descriptive levels. Criteria is the
	// ladder, worst first; the answer is the level index plus the full
	// distribution, so callers can thresholds on position, not on prose.
	Score Kind = "score"
	// Noul is P(yes) for one yes/no question. Criteria optionally names the
	// two poles ("true: direct, specific answer"); empty means the default
	// poles (statement holds / does not hold).
	Noul Kind = "noul"
)

// Question is one typed judgment. Instructions ask about the IDEA, not the
// words ("is amd tracking nvidia" beats "which resolution"), because the
// match is on meaning — the function-calling cookbook's central spec rule.
//
// Criteria and Ladder are not interchangeable, and picking the wrong one
// silently changes the answer:
//
//	Choice → Criteria (map). The keys ARE the accepted values, so there is
//	         no label→argument mapping step.
//	Score  → Ladder (ordered slice). Laya's ordinal head reads positional
//	         levels; a map loses the order and, because Go maps have no
//	         order, would also make the prompt non-deterministic. A Score
//	         with no Ladder is a programmer error (fail fast), and a Score
//	         carrying Criteria is rejected rather than guessed at.
//	Noul   → neither (the model answers P(yes) over an implicit
//	         [false,true]); Criteria may optionally name the two poles.
type Question struct {
	Type         Kind              `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria,omitempty"`
	Ladder       []string          `json:"ladder,omitempty"`
}

// Validate reports whether a question is well formed for its kind: the
// cheapest place to catch a malformed judgment, before any wire call.
func (q Question) Validate() error {
	switch q.Type {
	case Choice:
		if len(q.Criteria) == 0 {
			return fmt.Errorf("choice question needs a non-empty criteria map (option key → meaning)")
		}
	case Score:
		if len(q.Criteria) > 0 {
			return fmt.Errorf("score question must use the ordered ladder, not a criteria map (a map has no order and loses the ladder)")
		}
		if len(q.Ladder) < 2 {
			return fmt.Errorf("score question needs an ordered ladder of at least 2 levels")
		}
	case Noul:
		// Criteria optionally names the false/true poles; nothing required.
	default:
		return fmt.Errorf("unknown type %q", q.Type)
	}
	if strings.TrimSpace(q.Instructions) == "" {
		return fmt.Errorf("empty instructions")
	}
	return nil
}

// Answer is one judged answer. Probability is the calibrated P for the
// reported value (choice: the winner's share; score: the full level
// distribution; noul: P(yes)). Confidence is the act/don't-act axis —
// gate on it, not on Probability alone.
type Answer struct {
	Value        string             `json:"value"`
	Distribution map[string]float64 `json:"distribution,omitempty"`
	Confidence   float64            `json:"confidence"`
}

// Judge answers a batch of typed questions over one state in one call.
// Implementations: *Server (provider API over HTTP) and *Local (sidecar
// over HTTP); JudgeFunc adapts a stub for tests.
type Judge interface {
	// Ask answers every question over state. A nil map (not an error) means
	// the backend is unreachable or unconfigured — the caller keeps its
	// native rule and records the reason. Errors are reserved for malformed
	// questions (programmer error, fail fast).
	Ask(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error)
}

// JudgeFunc adapts a function to the Judge interface (tests, dry runs).
type JudgeFunc func(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error)

func (f JudgeFunc) Ask(ctx context.Context, state string, questions map[string]Question) (map[string]Answer, error) {
	return f(ctx, state, questions)
}

// Unavailable reports whether a nil answer map came back (backend down or
// unconfigured) — the single branch every call site shares: keep the native
// rule, record the reason, never fail the outer operation.
func Unavailable(answers map[string]Answer) bool { return answers == nil }
