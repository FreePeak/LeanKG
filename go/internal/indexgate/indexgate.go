// Package indexgate is the decision half of the Rust auto-index gates
// (src/mcp/server.rs `auto_index_if_needed` / `ensure_project_indexed`, plus
// the `auto_index_on_db_write` arm of the tool dispatch). internal/projectcfg
// parses the `mcp.auto_index_*` keys; this package is their consumer.
//
// The gates are pure: the caller supplies the git probe, the store watermark,
// the clock, and the read-only bit, so the whole decision table is testable
// without a store or a repository. The positive branch (kick an index) stays
// with the caller — the wiring hunk.
//
// Rust freshness rule (Phase 8, after the DB moved to Postgres): "the index is
// fresh when the last commit is within the threshold of the last write", with
// the DB-modified term hardcoded to 0. The Go engine has a real watermark
// (write_watermark.at, stamped on every write commit), so the same rule is
// evaluated with that instead of a constant — the comparison is unchanged,
// only `now` is real data now.
package indexgate

import (
	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
)

// Decision is the gate outcome. The reason vocabulary mirrors the Rust
// AutoIndexDecision variants one-for-one so an operator or test can assert
// the exact path taken.
type Decision int

const (
	// ReadOnly: the server opened the store read-only — never index.
	ReadOnly Decision = iota
	// Disabled: config or env switched auto-index off.
	Disabled
	// Fresh: the index is within the freshness threshold.
	Fresh
	// NoGit: no git context and require_git_for_auto_index is set.
	NoGit
	// Index: the gates passed; the caller must run the index.
	Index
)

// String renders the reason in the Rust variant vocabulary (lower snake).
func (d Decision) String() string {
	switch d {
	case ReadOnly:
		return "skipped_read_only"
	case Disabled:
		return "skipped_disabled"
	case Fresh:
		return "skipped_fresh"
	case NoGit:
		return "skipped_no_git"
	case Index:
		return "indexed"
	default:
		return "unknown"
	}
}

// Go reports whether the caller should kick an index.
func (d Decision) Go() bool { return d == Index }

// GitProbe answers the git questions the gate asks about one project root.
// *Workspace implements the Rust git_workspace behavior; tests implement it
// with a literal.
type GitProbe interface {
	// HasGitContext is true when the root is a git work tree or contains
	// nested git repos.
	HasGitContext() bool
	// LastCommitTime is the newest HEAD commit timestamp (Unix seconds)
	// across the root repo and its nested repos. Only consulted when
	// HasGitContext is true.
	LastCommitTime() int64
}

// StoreState is the index side of the comparison: how many elements the store
// holds and when it was last written.
type StoreState struct {
	// Elements is the indexed element count. Zero is "nothing indexed yet" —
	// bootstrapping, so an index can never be "fresh".
	Elements int
	// LastWrite is the Unix-seconds timestamp of the last store write (the
	// write_watermark `at` column). Zero means no write recorded yet.
	LastWrite int64
	// LastWriteOK is false when the watermark could not be read (a brand-new
	// or unreadable store). Unknown write time forces a reindex.
	LastWriteOK bool
}

// Decide is the gate. Order (Rust):
//
//  1. read-only                          -> ReadOnly
//  2. !cfg.AutoIndexOnStart              -> Disabled
//  3. LEANKG_SKIP_FRESHNESS_CHECK=1|true -> Disabled
//  4. RequireGitForAutoIndex && !git     -> NoGit
//  5. elements == 0                      -> Index (nothing indexed yet)
//  6. lastCommit <= lastWrite+threshold  -> Fresh
//  7. otherwise                          -> Index
//
// With RequireGitForAutoIndex false and no git context, the commit time is
// Rust's i64::MAX ("forcing reindex") — so the freshness comparison always
// says stale, and the store-state check is what stops a pointless reindex of
// an empty tree. threshold <= 0 disables the window entirely (every commit
// newer than the last write is stale).
func Decide(cfg projectcfg.MCPConfig, git GitProbe, st StoreState, readOnly, skipFreshness bool) Decision {
	if readOnly {
		return ReadOnly
	}
	if !cfg.AutoIndexOnStart {
		return Disabled
	}
	if skipFreshness {
		return Disabled
	}
	thresholdSeconds := int64(cfg.AutoIndexThresholdMinutes) * 60
	hasGit := git != nil && git.HasGitContext()
	if cfg.RequireGitForAutoIndex && !hasGit {
		return NoGit
	}
	if !st.LastWriteOK {
		// A store that cannot report its write time cannot prove freshness.
		return Index
	}
	if st.Elements == 0 {
		return Index
	}
	var lastCommit int64
	if hasGit {
		lastCommit = git.LastCommitTime()
	} else {
		// Rust: "No git context ... forcing reindex" — i64::MAX.
		lastCommit = 1<<63 - 1
	}
	if lastCommit <= st.LastWrite+thresholdSeconds {
		return Fresh
	}
	return Index
}

// SkipFreshnessFromEnv reads the FR-MG-AUTO-01 escape hatch: operators can
// skip the freshness reindex without wiping data (the mega-graph Docker OOM
// escape hatch). Any of 1/true/yes (case-insensitive) is on.
func SkipFreshnessFromEnv(getenv func(string) string) bool {
	if getenv == nil {
		return false
	}
	switch getenv("LEANKG_SKIP_FRESHNESS_CHECK") {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

// DBWriteGate decides the `auto_index_on_db_write` arm: after an external
// write was detected, should the server trigger an incremental reindex? The
// dirty flag comes from whatever write tracker the caller runs.
func DBWriteGate(cfg projectcfg.MCPConfig, readOnly, dirty bool) bool {
	return !readOnly && dirty && cfg.AutoIndexOnDBWrite
}
