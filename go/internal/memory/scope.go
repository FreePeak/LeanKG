package memory

import "fmt"

// Scoping matrix ported from the Rust src/memory/bank.rs:60-96
// (computeMnemopiBankScope): which bank a write targets and which banks a
// read merges, per scope mode.

// SharedBank is the cross-project bank Global writes to and reads from
// (Rust handler.rs: `let shared = "leankg-shared"`).
const SharedBank = "leankg-shared"

// Scope selects the bank-routing mode of the session verbs.
type Scope int

const (
	// ScopePerProject writes and reads the cwd's mnemopi bank (default).
	ScopePerProject Scope = iota
	// ScopeGlobal writes and reads the shared bank only.
	ScopeGlobal
	// ScopePerProjectTagged writes the project bank; reads merge
	// [project, shared] in that order.
	ScopePerProjectTagged
)

// ParseScope maps wire strings to modes (bank.rs parse_scope). "" selects
// the per-project default. Unlike the reference — which silently fell back
// to per-project for ANY unknown string — an unrecognized scope is an
// error: a typo must not quietly retarget a session's memories.
func ParseScope(s string) (Scope, error) {
	switch s {
	case "", "per-project":
		return ScopePerProject, nil
	case "global":
		return ScopeGlobal, nil
	case "per-project-tagged":
		return ScopePerProjectTagged, nil
	}
	return ScopePerProject, fmt.Errorf("memory: unknown scope %q (valid: per-project, global, per-project-tagged)", s)
}

// String renders the wire form ParseScope accepts.
func (s Scope) String() string {
	switch s {
	case ScopeGlobal:
		return "global"
	case ScopePerProjectTagged:
		return "per-project-tagged"
	default:
		return "per-project"
	}
}

// WriteBank returns the write-target bank for the project bank
// (bank.rs:81-86).
func (s Scope) WriteBank(projectBank string) string {
	if s == ScopeGlobal {
		return SharedBank
	}
	return projectBank
}

// ReadBanks returns the read banks in merge order (bank.rs:88-95).
func (s Scope) ReadBanks(projectBank string) []string {
	switch s {
	case ScopeGlobal:
		return []string{SharedBank}
	case ScopePerProjectTagged:
		return []string{projectBank, SharedBank}
	default:
		return []string{projectBank}
	}
}

// Recall/injection limits (PRD FR-ZCP-07 contract, mnemopi/state.ts:472-490,
// 914-922): OMP's first-turn <memories> injection caps at recallLimit
// entries and injectionTokenLimit tokens.
const (
	RecallLimit         = 8
	InjectionTokenLimit = 5000
)

// FirstTurnMemories is the <memories>-equivalent cold snapshot: the
// session_recall rows for (scope, cwd, bank) rendered as the injectable
// memory text. With a query it is ranked recall (the reference composes one
// from the prompt + last 3 turns); with an empty query — the true cold
// case, nothing to search on — it is the banks' most recent rows. Either
// way capped at RecallLimit entries and InjectionTokenLimit estimated
// tokens (bytes/4, the package-wide estimator). Empty recall renders ""
// (callers skip an empty block, as mnemopi does).
func (m *Memory) FirstTurnMemories(scope Scope, cwd, bank, query string) (string, []Entry, error) {
	var banks []string
	var entries []Entry
	var err error
	if query == "" {
		banks = m.sessionReadBanks(scope, cwd, bank)
		entries, err = m.recentBanks(banks, RecallLimit)
	} else {
		banks, entries, err = m.SessionRecall(scope, cwd, bank, query, RecallLimit)
	}
	if err != nil {
		return "", nil, err
	}
	return InjectBlock(entries, RecallLimit, InjectionTokenLimit), entries, nil
}
