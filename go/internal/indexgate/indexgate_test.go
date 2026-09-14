package indexgate

import (
	"testing"

	"github.com/FreePeak/LeanKG/go/internal/projectcfg"
)

// fakeGit is a literal GitProbe.
type fakeGit struct {
	has    bool
	commit int64
}

func (f fakeGit) HasGitContext() bool   { return f.has }
func (f fakeGit) LastCommitTime() int64 { return f.commit }

// gateCfg is the template config the setup pipeline writes, which is what the
// gates read in production.
func gateCfg() projectcfg.MCPConfig {
	return projectcfg.MCPConfig{
		Enabled:                   true,
		AutoIndexOnStart:          true,
		AutoIndexThresholdMinutes: 60,
		AutoIndexOnDBWrite:        false,
		RequireGitForAutoIndex:    false,
	}
}

func TestDecideTable(t *testing.T) {
	// now is fixed at 10_000 seconds; the threshold window is 60 min = 3600 s.
	const now = int64(10_000)
	cases := []struct {
		name string
		cfg  func(projectcfg.MCPConfig) projectcfg.MCPConfig
		git  GitProbe
		st   StoreState
		ro   bool
		skip bool
		want Decision
	}{
		{
			name: "read-only wins over everything",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now},
			st:   StoreState{Elements: 100, LastWrite: 0, LastWriteOK: true},
			ro:   true,
			want: ReadOnly,
		},
		{
			name: "auto_index_on_start false disables",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { c.AutoIndexOnStart = false; return c },
			git:  fakeGit{has: true, commit: now},
			st:   StoreState{Elements: 100, LastWrite: 0, LastWriteOK: true},
			want: Disabled,
		},
		{
			name: "skip-freshness env disables",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now},
			st:   StoreState{Elements: 100, LastWrite: 0, LastWriteOK: true},
			skip: true,
			want: Disabled,
		},
		{
			name: "require_git_for_auto_index with no git context skips",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { c.RequireGitForAutoIndex = true; return c },
			git:  fakeGit{has: false},
			st:   StoreState{Elements: 100, LastWrite: 0, LastWriteOK: true},
			want: NoGit,
		},
		{
			name: "require_git_for_auto_index with git context proceeds",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { c.RequireGitForAutoIndex = true; return c },
			git:  fakeGit{has: true, commit: now + 7200},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "git context within the threshold is fresh",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now + 3599},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Fresh,
		},
		{
			name: "threshold boundary is inclusive (<=)",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now + 3600},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Fresh,
		},
		{
			name: "one second past the threshold is stale",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now + 3601},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "empty store is never fresh",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: true, commit: now},
			st:   StoreState{Elements: 0, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "no git context and no git requirement forces a reindex",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: false},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "no git context still skips an empty store",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  fakeGit{has: false},
			st:   StoreState{Elements: 0, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "zero threshold means every newer commit is stale",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { c.AutoIndexThresholdMinutes = 0; return c },
			git:  fakeGit{has: true, commit: now + 1},
			st:   StoreState{Elements: 100, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "nil git probe with the requirement off forces a reindex",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { return c },
			git:  nil,
			st:   StoreState{Elements: 5, LastWrite: now, LastWriteOK: true},
			want: Index,
		},
		{
			name: "nil git probe with the requirement on skips",
			cfg:  func(c projectcfg.MCPConfig) projectcfg.MCPConfig { c.RequireGitForAutoIndex = true; return c },
			git:  nil,
			st:   StoreState{Elements: 5, LastWrite: now, LastWriteOK: true},
			want: NoGit,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(gateCfg())
			got := Decide(cfg, tc.git, tc.st, tc.ro, tc.skip)
			if got != tc.want {
				t.Fatalf("Decide = %s (%v), want %s", got, got, tc.want)
			}
			if got.Go() != (got == Index) {
				t.Fatalf("Go() disagrees with the decision: %v", got)
			}
		})
	}
}

func TestDecisionReasonStrings(t *testing.T) {
	// The reason vocabulary mirrors the Rust AutoIndexDecision variants so an
	// operator can grep a log line for the exact path taken.
	want := map[Decision]string{
		ReadOnly: "skipped_read_only",
		Disabled: "skipped_disabled",
		Fresh:    "skipped_fresh",
		NoGit:    "skipped_no_git",
		Index:    "indexed",
	}
	for decision, reason := range want {
		if got := decision.String(); got != reason {
			t.Errorf("%d.String() = %q, want %q", decision, got, reason)
		}
	}
}

func TestSkipFreshnessFromEnv(t *testing.T) {
	env := map[string]string{}
	getenv := func(key string) string { return env["LEANKG_SKIP_FRESHNESS_CHECK"] }
	if SkipFreshnessFromEnv(getenv) {
		t.Fatal("unset must be false")
	}
	for _, on := range []string{"1", "true", "TRUE", "True", "yes", "on"} {
		env["LEANKG_SKIP_FRESHNESS_CHECK"] = on
		if !SkipFreshnessFromEnv(getenv) {
			t.Errorf("%q must be on", on)
		}
	}
	for _, off := range []string{"0", "false", "no", ""} {
		env["LEANKG_SKIP_FRESHNESS_CHECK"] = off
		if SkipFreshnessFromEnv(getenv) {
			t.Errorf("%q must be off", off)
		}
	}
	if SkipFreshnessFromEnv(nil) {
		t.Fatal("a nil getenv must be false, not panicking")
	}
}

func TestDBWriteGate(t *testing.T) {
	cfg := gateCfg()
	cfg.AutoIndexOnDBWrite = true
	cases := []struct {
		ro, dirty bool
		want      bool
	}{
		{false, true, true},
		{false, false, false},
		{true, true, false}, // read-only never indexes
		{true, false, false},
	}
	for _, tc := range cases {
		if got := DBWriteGate(cfg, tc.ro, tc.dirty); got != tc.want {
			t.Errorf("DBWriteGate(ro=%v, dirty=%v) = %v, want %v", tc.ro, tc.dirty, got, tc.want)
		}
	}
	cfg.AutoIndexOnDBWrite = false
	if DBWriteGate(cfg, false, true) {
		t.Error("the flag is off: a dirty write must not trigger")
	}
}
