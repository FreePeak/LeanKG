package ccmemory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
)

// HookDeadline bounds one SessionStart run (plan 6.6, rule 1).
const HookDeadline = 3 * time.Second

const (
	hookSyncBudget  = 2 * time.Second // plan 6.6, rule 1: budget for the sync step
	hookTokenBudget = 1500            // plan 6.6, rule 3: about 6,000 bytes
	hookContextLine = "LeanKG cross-repository memory: preferences and feedback saved in other repositories. This repository's own memory is loaded by Claude Code itself."
	hookHintFormat  = `More on a topic: call the leankg query tool with action "memory", args.command "agent_recall", args.cwd "%s".`
)

// HookOptions is one SessionStart run: the memory home, the repository the
// session runs in, and the two streams the run writes.
type HookOptions struct {
	Home      string
	CallerDir string
	Stdout    io.Writer
	Stderr    io.Writer
}

// ResolveHookDir returns the repository a hook run is for (plan 6.6): the cwd
// of the hook JSON on stdin, else projectDir (CLAUDE_PROJECT_DIR), else wd.
// Input that is empty or not JSON falls through to the next source.
func ResolveHookDir(stdin []byte, projectDir, wd string) string {
	var in struct {
		Cwd string `json:"cwd"`
	}
	if json.Unmarshal(stdin, &in) == nil && in.Cwd != "" {
		return filepath.Clean(in.Cwd)
	}
	if projectDir != "" {
		return filepath.Clean(projectDir)
	}
	return filepath.Clean(wd)
}

// RunHook is one SessionStart run (plan 6.6). It syncs the cc- banks when that
// is due and the lock is free, then writes the cross-repository block for
// opts.CallerDir to opts.Stdout. Nothing is written to stdout unless there is a
// block. Each failure is one line on opts.Stderr and the run goes on, so the
// caller always exits 0.
func RunHook(ctx context.Context, opts HookOptions) {
	defer func() {
		if r := recover(); r != nil {
			hookFail(opts.Stderr, fmt.Errorf("panic: %v", r))
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, HookDeadline)
	defer cancel()

	synced, err := syncForHook(ctx, opts)
	if err != nil {
		hookFail(opts.Stderr, err)
		return
	}
	if synced != nil {
		defer synced.Close()
	}
	h := NewHandle(opts.Home, synced)
	defer h.Close()
	mem, err := h.Get(ctx)
	if errors.Is(err, ErrNoCentralRoot) {
		return
	}
	if err != nil {
		hookFail(opts.Stderr, err)
		return
	}
	block, err := hookBlock(ctx, mem, opts)
	if err != nil {
		hookFail(opts.Stderr, err)
		return
	}
	if block != "" {
		fmt.Fprint(opts.Stdout, block)
	}
}

// syncForHook runs the sync step when it is due and returns the central memory
// it opened, or nil when it opened none. A sync problem is printed here and the
// run goes on from the stored rows. Only a failure to open the central memory
// is returned, because the injection cannot read it either.
func syncForHook(ctx context.Context, opts HookOptions) (*memory.Memory, error) {
	central := memory.GlobalRoot(opts.Home)
	projects := claudecode.ProjectsRoot(opts.Home)

	patterns, digest, err := memory.LoadScrubFile(filepath.Join(central, scrubFile))
	if err != nil {
		hookFail(opts.Stderr, fmt.Errorf("deny-list invalid, sync skipped: %w", err))
		return nil, nil
	}
	memory.SetScrubPatterns(patterns, digest)

	if !isDir(projects) {
		return nil, nil
	}
	// The sync may create the central root, so it runs only when memory files
	// exist. A central root that already has an index is always synced.
	if _, err := os.Stat(filepath.Join(central, "index.db")); err != nil {
		disc, err := Discover(projects)
		if err != nil {
			hookFail(opts.Stderr, fmt.Errorf("sync skipped: %w", err))
			return nil, nil
		}
		if len(disc.Docs) == 0 {
			return nil, nil
		}
	}

	mem, err := memory.Open(ctx, opts.Home, false)
	if err != nil {
		return nil, err
	}
	sctx, cancel := context.WithTimeout(ctx, hookSyncBudget)
	defer cancel()
	_, err = Sync(sctx, mem, SyncOptions{Projects: projects, CentralRoot: mem.Root()})
	if err != nil && !errors.Is(err, ErrLockHeld) {
		hookFail(opts.Stderr, fmt.Errorf("sync: %w", err))
	}
	return mem, nil
}

// hookBlock is the stdout of one run: the context line, the newest global
// memories saved by other repositories (the caller's own are left out, since
// Claude Code loads them itself), and the hint line. It is "" when nothing is
// left to inject.
func hookBlock(ctx context.Context, mem *memory.Memory, opts HookOptions) (string, error) {
	caller := ProjectKey(claudecode.ProjectsRoot(opts.Home), opts.CallerDir)
	keep := func(e memory.Entry) bool { return metaString(e, "project") != caller }
	block, _, _, _, err := mem.InjectBanks(ctx, []string{GlobalBank}, "", memory.RecallLimit, hookTokenBudget, keep)
	if err != nil || block == "" {
		return "", err
	}
	hint := fmt.Sprintf(hookHintFormat, opts.CallerDir)
	return hookContextLine + "\n" + block + "\n" + hint + "\n", nil
}

// hookFail writes one error line to w.
func hookFail(w io.Writer, err error) {
	fmt.Fprintf(w, "leankg memory hook: %v\n", err)
}
