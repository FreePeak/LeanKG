package ccmemory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// These tests read the exclude list from its default file under HOME, the file
// a real run reads. Every path (full and scoped sync, the hook events, the
// SessionStart injection, Discover, Recall and Restore) must agree about the
// same excluded and kept project.

// Names of the synthetic memories. Each memory file is named name + ".md".
const (
	agreeExcludedName = "excluded-note"
	agreeGoneName     = "excluded-gone"
	agreeKeptName     = "kept-note"
	agreeExcludedBody = "EXCLUDED-BODY-MARKER"
	agreeKeptBody     = "KEPT-BODY-MARKER"
	agreeQuery        = "sharedagreementzeta"
)

// agreeFixture is one isolated HOME holding an excluded project, a kept project
// and a third repository that starts the SessionStart runs.
type agreeFixture struct {
	home, projects                  string
	excludedDir, keptDir, callerDir string
	excludedSlug, keptSlug          string
}

// newAgreeFixture isolates HOME. An empty LEANKG_MEMORY_EXCLUDE_FILE is unset
// to loadExcludeList, so the list comes from the default file under HOME.
func newAgreeFixture(t *testing.T) agreeFixture {
	t.Helper()
	home := hookFixtureEnv(t)
	t.Setenv(excludeFileEnv, "")
	f := agreeFixture{
		home:        home,
		projects:    claudecode.ProjectsRoot(home),
		excludedDir: filepath.Join(home, "synthetic", "excluded_project"),
		keptDir:     filepath.Join(home, "synthetic", "kept-project"),
		callerDir:   filepath.Join(home, "synthetic", "caller-project"),
	}
	f.excludedSlug = claudecode.Slug(f.excludedDir)
	f.keptSlug = claudecode.Slug(f.keptDir)
	return f
}

func (f agreeFixture) excludeFile() string {
	return filepath.Join(f.home, ".config", "leankg", "memory-exclude")
}

// excludeExcludedProject writes the default exclude file with the excluded project.
func (f agreeFixture) excludeExcludedProject(t *testing.T) {
	t.Helper()
	writeFile(t, f.excludeFile(), f.excludedDir+"\n")
}

// writeMemories writes one feedback memory per project. Feedback rows live in
// cc-global, the bank the SessionStart injection reads.
func (f agreeFixture) writeMemories(t *testing.T) {
	t.Helper()
	writeHookMemory(t, f.projects, f.excludedSlug, agreeExcludedName, "feedback", agreeExcludedBody+" "+agreeQuery)
	writeHookMemory(t, f.projects, f.keptSlug, agreeKeptName, "feedback", agreeKeptBody+" "+agreeQuery)
}

// hookOut runs one hook event for this fixture and returns its stdout.
func (f agreeFixture) hookOut(t *testing.T, opts HookOptions) string {
	t.Helper()
	opts.Home = f.home
	out, _ := runHookEvent(t, opts)
	return out
}

// assertNoExcludedText fails when stdout names the excluded project.
func (f agreeFixture) assertNoExcludedText(t *testing.T, out string) {
	t.Helper()
	for _, leak := range []string{agreeExcludedName, agreeExcludedBody, f.excludedSlug} {
		if strings.Contains(out, leak) {
			t.Errorf("stdout names the excluded project (%q):\n%s", leak, out)
		}
	}
}

// withCentral opens the central memory under home for the duration of fn and
// closes it, so the hook runs in the same test never hold a second handle.
func withCentral(t *testing.T, home string, fn func(mem *memory.Memory)) {
	t.Helper()
	mem, err := memory.Open(context.Background(), home, false)
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	defer mem.Close()
	fn(mem)
}

// storedRows returns every row of the cc- banks and of ccarchive, keyed by bank and document id.
func storedRows(t *testing.T, mem *memory.Memory) map[string]memory.Entry {
	t.Helper()
	banks, err := mem.Banks()
	if err != nil {
		t.Fatalf("Banks: %v", err)
	}
	out := map[string]memory.Entry{}
	for _, bank := range banks {
		if !strings.HasPrefix(bank, CentralBankPrefix) && bank != ArchiveBank {
			continue
		}
		for id, row := range bankRows(t, mem, bank) {
			out[bank+"/"+id] = row
		}
	}
	return out
}

// rowsOf returns the keys of the rows whose project metadata names slug, in the
// raw or the redacted form that stored rows may carry.
func rowsOf(rows map[string]memory.Entry, slug string) []string {
	var keys []string
	for key, e := range rows {
		if p := metaString(e, "project"); p == slug || p == memory.Redact(slug) {
			keys = append(keys, key)
		}
	}
	return keys
}

// TestExcludeAgreementAcrossPaths: each write path, run on its own against a
// fresh central root, writes no row of the excluded project to any cc- bank or
// to ccarchive, writes the kept project's row, and prints nothing about the
// excluded project.
func TestExcludeAgreementAcrossPaths(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, f agreeFixture) string // returns the stdout the path printed
	}{
		{"full sync", func(t *testing.T, f agreeFixture) string {
			withCentral(t, f.home, func(mem *memory.Memory) { runSync(t, mem, syncOpts(f.projects, mem)) })
			return ""
		}},
		{"PostToolUse on the excluded memory file", func(t *testing.T, f agreeFixture) string {
			out := f.hookOut(t, HookOptions{Event: EventPostToolUse, FilePath: memFile(f.projects, f.excludedSlug, agreeExcludedName+".md")})
			// Control: the same path syncs the kept project, so a path that
			// does nothing cannot pass the kept-row check.
			f.hookOut(t, HookOptions{Event: EventPostToolUse, FilePath: memFile(f.projects, f.keptSlug, agreeKeptName+".md")})
			return out
		}},
		{"SessionEnd for the excluded cwd", func(t *testing.T, f agreeFixture) string {
			out := f.hookOut(t, HookOptions{Event: EventSessionEnd, CallerDir: f.excludedDir})
			f.hookOut(t, HookOptions{Event: EventSessionEnd, CallerDir: f.keptDir})
			return out
		}},
		{"SessionStart injection", func(t *testing.T, f agreeFixture) string {
			out, _ := runHook(t, f.home, f.callerDir)
			return out
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAgreeFixture(t)
			f.writeMemories(t)
			f.excludeExcludedProject(t)

			out := c.run(t, f)

			withCentral(t, f.home, func(mem *memory.Memory) {
				rows := storedRows(t, mem)
				if keys := rowsOf(rows, f.excludedSlug); len(keys) > 0 {
					t.Errorf("excluded project rows written: %v", keys)
				}
				if _, ok := rows[GlobalBank+"/"+DocID(f.keptSlug, agreeKeptName+".md")]; !ok {
					t.Error("kept project row missing: the path wrote nothing for the kept project")
				}
			})
			f.assertNoExcludedText(t, out)
		})
	}

	t.Run("Discover lists only the kept project", func(t *testing.T) {
		f := newAgreeFixture(t)
		f.writeMemories(t)
		f.excludeExcludedProject(t)

		disc, err := Discover(context.Background(), f.projects)
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		var slugs []string
		for _, d := range disc.Docs {
			slugs = append(slugs, d.Slug)
		}
		if !slices.Equal(slugs, []string{f.keptSlug}) {
			t.Errorf("Discover slugs = %v, want [%s]", slugs, f.keptSlug)
		}
	})
}

// TestExcludeAgreementReadPathsFilterWithoutSync: rows stored before the project
// was excluded (one live, one archived) are hidden by Recall, Restore and the
// SessionStart injection. The sync budget is spent before the injection, so no
// sync runs between the exclusion and the reads.
func TestExcludeAgreementReadPathsFilterWithoutSync(t *testing.T) {
	f := newAgreeFixture(t)
	f.writeMemories(t)
	writeHookMemory(t, f.projects, f.excludedSlug, agreeGoneName, "feedback", agreeExcludedBody+" "+agreeQuery)

	// The earlier syncs, before the exclusion: the second one archives the
	// excluded row whose file is removed.
	runHook(t, f.home, f.callerDir)
	if err := os.Remove(memFile(f.projects, f.excludedSlug, agreeGoneName+".md")); err != nil {
		t.Fatal(err)
	}
	runHook(t, f.home, f.callerDir)
	f.excludeExcludedProject(t)
	spendSyncBudget(t)

	withCentral(t, f.home, func(mem *memory.Memory) {
		rows := storedRows(t, mem)
		if len(rows) != 3 || len(rowsOf(rows, f.excludedSlug)) != 2 {
			t.Fatalf("setup: stored rows = %d (excluded %d), want 3 rows with 2 excluded (one live, one archived)", len(rows), len(rowsOf(rows, f.excludedSlug)))
		}
	})

	t.Run("Recall", func(t *testing.T) {
		withCentral(t, f.home, func(mem *memory.Memory) {
			res := recallOK(t, mem, RecallRequest{Query: agreeQuery, Scope: scopeAll})
			if res.Count != 1 || len(res.Memories) != 1 || res.Memories[0].ID != DocID(f.keptSlug, agreeKeptName+".md") {
				t.Errorf("Recall returned %d rows %+v, want only the kept project's row", res.Count, res.Memories)
			}
		})
	})

	restore := func(t *testing.T, deleted bool) (RestoreResult, string) {
		t.Helper()
		into := t.TempDir()
		var res RestoreResult
		withCentral(t, f.home, func(mem *memory.Memory) {
			res = runRestore(t, mem, RestoreOptions{Projects: f.projects, Into: into, Deleted: deleted})
		})
		return res, into
	}
	t.Run("Restore without deleted", func(t *testing.T) {
		res, into := restore(t, false)
		if res.Written != 1 || res.SkippedExcluded != 1 {
			t.Errorf("Restore counts = %+v, want written 1 and skipped_excluded 1", res)
		}
		if _, err := os.Stat(filepath.Join(into, f.excludedSlug)); !os.IsNotExist(err) {
			t.Errorf("Restore wrote under the excluded project: stat err = %v", err)
		}
	})
	t.Run("Restore with deleted", func(t *testing.T) {
		res, into := restore(t, true)
		if res.Written != 1 || res.SkippedExcluded != 2 {
			t.Errorf("Restore counts = %+v, want written 1 and skipped_excluded 2 (live and archived)", res)
		}
		if _, err := os.Stat(filepath.Join(into, f.excludedSlug)); !os.IsNotExist(err) {
			t.Errorf("Restore wrote under the excluded project: stat err = %v", err)
		}
	})

	t.Run("SessionStart injection", func(t *testing.T) {
		out, _ := runHook(t, f.home, f.callerDir)
		if !strings.Contains(out, agreeKeptName) {
			t.Errorf("stdout lacks the kept row, so the injection printed nothing to check:\n%s", out)
		}
		f.assertNoExcludedText(t, out)
	})
}

// TestExcludeAgreementUnreadableFileFailsClosedEverywhere: with the default
// exclude file unreadable, every path stops with an error or empty output and
// the stored rows do not change. The file is a directory: os.ReadFile fails on
// it for any user, while chmod 000 would not stop root.
func TestExcludeAgreementUnreadableFileFailsClosedEverywhere(t *testing.T) {
	f := newAgreeFixture(t)
	f.writeMemories(t)
	runHook(t, f.home, f.callerDir) // an earlier sync stores both projects
	if err := os.MkdirAll(f.excludeFile(), 0o755); err != nil {
		t.Fatal(err)
	}

	var before map[string]memory.Entry
	withCentral(t, f.home, func(mem *memory.Memory) {
		before = storedRows(t, mem)
		if len(before) != 2 {
			t.Fatalf("setup: stored rows = %d, want 2", len(before))
		}
	})
	unchanged := func(t *testing.T) {
		t.Helper()
		withCentral(t, f.home, func(mem *memory.Memory) {
			if got := storedRows(t, mem); !reflect.DeepEqual(got, before) {
				t.Errorf("stored rows changed: %d now, %d before", len(got), len(before))
			}
		})
	}
	// oneErrorLine checks that a hook run printed nothing and one stderr line.
	oneErrorLine := func(t *testing.T, out, errw string) {
		t.Helper()
		if out != "" || errw == "" || strings.Count(strings.TrimSuffix(errw, "\n"), "\n")+1 != 1 {
			t.Errorf("stdout %q, stderr %q; want empty stdout and exactly one stderr line", out, errw)
		}
	}

	t.Run("Sync", func(t *testing.T) {
		withCentral(t, f.home, func(mem *memory.Memory) {
			if _, err := Sync(context.Background(), mem, syncOpts(f.projects, mem)); err == nil {
				t.Error("Sync returned nil error, want the unreadable exclude list")
			}
		})
		unchanged(t)
	})
	t.Run("scoped Sync", func(t *testing.T) {
		withCentral(t, f.home, func(mem *memory.Memory) {
			opts := syncOpts(f.projects, mem)
			opts.OnlySlug = f.excludedSlug
			if _, err := Sync(context.Background(), mem, opts); err == nil {
				t.Error("scoped Sync returned nil error, want the unreadable exclude list")
			}
		})
		unchanged(t)
	})
	t.Run("PostToolUse hook", func(t *testing.T) {
		out, errw := runHookEvent(t, HookOptions{Home: f.home, Event: EventPostToolUse, FilePath: memFile(f.projects, f.excludedSlug, agreeExcludedName+".md")})
		oneErrorLine(t, out, errw)
		unchanged(t)
	})
	t.Run("SessionStart injection", func(t *testing.T) {
		out, errw := runHook(t, f.home, f.callerDir)
		oneErrorLine(t, out, errw)
		unchanged(t)
	})
	t.Run("Recall", func(t *testing.T) {
		withCentral(t, f.home, func(mem *memory.Memory) {
			if _, err := Recall(context.Background(), mem, RecallRequest{Query: agreeQuery, Scope: scopeAll}); err == nil {
				t.Error("Recall returned nil error, want the unreadable exclude list")
			}
		})
		unchanged(t)
	})
	t.Run("Restore", func(t *testing.T) {
		into := t.TempDir()
		withCentral(t, f.home, func(mem *memory.Memory) {
			if _, err := Restore(context.Background(), mem, RestoreOptions{Projects: f.projects, Into: into, Deleted: true}); err == nil {
				t.Error("Restore returned nil error, want the unreadable exclude list")
			}
		})
		if entries, err := os.ReadDir(into); err != nil || len(entries) != 0 {
			t.Errorf("Restore wrote into the target: %d entries, err %v", len(entries), err)
		}
		unchanged(t)
	})
	t.Run("Discover", func(t *testing.T) {
		if _, err := Discover(context.Background(), f.projects); err == nil {
			t.Error("Discover returned nil error, want the unreadable exclude list")
		}
	})
}
