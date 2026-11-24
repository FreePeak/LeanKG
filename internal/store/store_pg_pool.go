package store

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultPGPoolSize caps each routed project's PostgreSQL pool. Every project
// has its own pool, so a server routing many projects would otherwise hold
// NumCPU-scale connections per project.
const DefaultPGPoolSize = 2

// ParsePoolSetting parses a LEANKG_PG_POOL_SIZE / LEANKG_PG_POOL_WAIT_MS value:
// surrounding space is ignored, the rest must be digits, and the value must be
// at least 1 and fit in int32 (pgxpool's MaxConns type). ok is false for
// anything else. The doctor reports the same rule.
func ParsePoolSetting(raw string) (v int64, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.Trim(trimmed, "0123456789") != "" {
		return 0, false
	}
	v, err := strconv.ParseInt(trimmed, 10, 32)
	if err != nil || v < 1 {
		return 0, false
	}
	return v, true
}

// configurePGPool applies the pool env settings to a pgx pool config. Unset
// LEANKG_PG_POOL_SIZE keeps DefaultPGPoolSize. An invalid value falls back to
// the default (or leaves the wait unset) and is returned as a problem in the
// doctor's wording, so the caller can surface it. MinConns stays 0 so an idle
// project holds no connection.
//
// LEANKG_PG_POOL_WAIT_MS is applied as the connect timeout: pgxpool has no
// acquire timeout, so a caller queued behind a full pool still waits for a
// free connection.
func configurePGPool(cfg *pgxpool.Config, getenv func(string) string) (problems []string) {
	cfg.MaxConns = DefaultPGPoolSize
	cfg.MinConns = 0

	if raw := getenv("LEANKG_PG_POOL_SIZE"); raw != "" {
		if v, ok := ParsePoolSetting(raw); ok {
			cfg.MaxConns = int32(v)
		} else {
			problems = append(problems, fmt.Sprintf("invalid LEANKG_PG_POOL_SIZE=%q", raw))
		}
	}
	if raw := getenv("LEANKG_PG_POOL_WAIT_MS"); raw != "" {
		if v, ok := ParsePoolSetting(raw); ok {
			cfg.ConnConfig.ConnectTimeout = time.Duration(v) * time.Millisecond
		} else {
			problems = append(problems, fmt.Sprintf("invalid LEANKG_PG_POOL_WAIT_MS=%q", raw))
		}
	}
	return problems
}
