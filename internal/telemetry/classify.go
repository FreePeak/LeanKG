package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

// Classification is the outcome of one call plus the response signals the
// metrics need.
type Classification struct {
	Outcome    string
	Reason     string
	ErrorCode  string
	Rung       string
	Confidence string
	Freshness  string
	Hits       int
	HitFiles   []string
}

// UnclassifiedCode labels an error that carries no LEANKG_ERROR_* catalog
// code (for example a malformed argument envelope). The outcome becomes
// "error:UNCLASSIFIED" so no captured call is left without a label.
const UnclassifiedCode = "UNCLASSIFIED"

// reasonCap bounds the stored outcome reason; a full error text can echo
// user input and is redacted by the recorder anyway.
const reasonCap = 300

var errCodeRE = regexp.MustCompile(`LEANKG_ERROR_[A-Z0-9_]+`)

// refusedCodes are the catalog codes that mean the server declined the call
// on purpose (RBAC, path confinement, auth) rather than failed to serve it.
var refusedCodes = map[string]bool{
	"LEANKG_ERROR_PERMISSION_DENIED":    true,
	"LEANKG_ERROR_PATH_OUTSIDE_PROJECT": true,
	"LEANKG_ERROR_UNAUTHORIZED":         true,
}

// Classify labels a handler result (any JSON-shaped value) or error. The
// first matching rule wins, in this order: an error (refused, timeout or
// error:<code>), then cold, degraded, zero_hit, ok_low_confidence, stale,
// and finally ok. Results are read through their JSON form so typed engine
// payloads and generic maps classify the same way.
func Classify(out any, err error) Classification {
	if err != nil {
		return classifyError(err)
	}
	m := asMap(out)
	c := Classification{Outcome: OutcomeOK}
	if m == nil {
		return c
	}
	var reason, guidance string
	if r, ok := m["retrieval"].(map[string]any); ok {
		c.Rung = str(r["rung"])
		c.Confidence = str(r["confidence"])
		reason = str(r["reason"])
	}
	c.Freshness = str(m["freshness"])
	guidance = str(m["guidance"])
	hits, hasHits := m["hits"].([]any)
	c.Hits = len(hits)
	c.HitFiles = distinctFiles(hits, m["results"])
	c.Reason = reason

	switch {
	case c.Rung == "L0" || c.Freshness == "cold":
		c.Outcome = OutcomeCold
		if c.Reason == "" {
			c.Reason = "no elements indexed"
		}
	case strings.Contains(strings.ToLower(reason), "degrad"):
		// Before zero_hit: a degraded empty answer is caused by the degrade.
		c.Outcome = OutcomeDegraded
	case hasHits && c.Hits == 0:
		c.Outcome = OutcomeZeroHit
		c.Reason = firstNonEmpty(guidance, reason, "no hits")
	case c.Confidence == "low":
		c.Outcome = OutcomeLowConf
	case c.Freshness == "possibly_stale":
		c.Outcome = OutcomeStale
	}
	return c
}

func classifyError(err error) Classification {
	msg := err.Error()
	c := Classification{Reason: capReason(msg)}
	code := errCodeRE.FindString(msg)
	switch {
	case code != "" && refusedCodes[code]:
		c.Outcome = OutcomeRefused
		c.ErrorCode = code
	case code != "":
		c.Outcome = OutcomeErrorPrefix + code
		c.ErrorCode = code
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "context deadline exceeded"):
		c.Outcome = OutcomeTimeout
	default:
		c.Outcome = OutcomeErrorPrefix + UnclassifiedCode
	}
	return c
}

// asMap returns out as a generic JSON object, or nil when it is not one. The
// round trip is unconditional: engine payloads carry typed slices such as
// []map[string]any that a plain type assertion would not see as lists.
func asMap(out any) map[string]any {
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// distinctFiles collects file_path values from hits and results in first-seen
// order. These are the files an agent would otherwise have opened (DS-07).
func distinctFiles(hits []any, results any) []string {
	var files []string
	seen := map[string]bool{}
	add := func(items []any) {
		for _, it := range items {
			im, ok := it.(map[string]any)
			if !ok {
				continue
			}
			f := str(im["file_path"])
			if f != "" && !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
	}
	add(hits)
	if rs, ok := results.([]any); ok {
		add(rs)
	}
	return files
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func capReason(s string) string {
	if len(s) <= reasonCap {
		return s
	}
	return s[:reasonCap] + "...[truncated]"
}
