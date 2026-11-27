// Central agent memory verbs: `leankg memory <subcommand>`. Each subcommand is
// one entry in memorySubcommands. Domain logic lives in internal/ccmemory and
// internal/memory; these functions only parse flags, resolve paths and print.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/FreePeak/LeanKG/internal/ccmemory"
	"github.com/FreePeak/LeanKG/internal/cliexit"
	"github.com/FreePeak/LeanKG/internal/memory"
	"github.com/FreePeak/LeanKG/internal/sessionlink/claudecode"
	"github.com/FreePeak/LeanKG/internal/telemetry"
)

// withMemoryTelemetry wires the process memory recorder for one memory verb: the
// serve wiring (telemetryRecorder, memory.SetRecorder, closeTelemetry) with the
// caller identity added to ctx on the stdio transport. With capture Off it opens
// nothing, so no recorder and no ledger file. The returned closer detaches the
// recorder and flushes it within telemetryCloseBudget.
func withMemoryTelemetry(ctx context.Context, id telemetry.Identity) (context.Context, func()) {
	return withMemoryTelemetryFlush(ctx, id, closeTelemetry)
}

// withMemoryTelemetryFlush is withMemoryTelemetry with the flush step supplied by
// the caller, so a run with its own deadline can bound the flush by that deadline.
func withMemoryTelemetryFlush(ctx context.Context, id telemetry.Identity, flush func()) (context.Context, func()) {
	cfg, err := telemetry.LoadConfig(telemetry.Home())
	if err == nil && telemetry.EffectiveLevel(cfg, os.Getenv("LEANKG_TELEMETRY")) == telemetry.Off {
		return ctx, func() {}
	}
	memory.SetRecorder(telemetryRecorder(ctx))
	return telemetry.WithIdentity(ctx, id, telemetry.TransportStdio), func() {
		memory.SetRecorder(nil)
		flush()
	}
}

// memorySynopsis is the usage of `leankg memory`, one subcommand per line. The
// main usage and memoryUsage both print it, so each subcommand is listed once.
var memorySynopsis = []string{
	"leankg memory sync [--dry-run] [--rebuild] [--json]",
	"leankg memory migrate [--project SLUG] [--dry-run] [--json]",
	"leankg memory recall <query...> [--scope project|global|all] [--type KIND ...] [--limit N] [--dir DIR] [--json] (keyword search)",
	"leankg memory hook (SessionStart: reads the hook JSON on stdin; always exits 0)",
}

// memoryUsageText joins memorySynopsis; every line after the first starts with
// indent. The caller writes the indent for the first line.
func memoryUsageText(indent string) string {
	return strings.Join(memorySynopsis, "\n"+indent)
}

// memorySubcommands routes `leankg memory <name>` to its handler.
var memorySubcommands = map[string]verbFunc{
	"sync":    cmdMemorySync,
	"migrate": cmdMemoryMigrate,
	"recall":  cmdMemoryRecall,
	"hook":    cmdMemoryHook,
}

func cmdMemory(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return memoryUsage()
	}
	fn, ok := memorySubcommands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "leankg memory: unknown subcommand %q\n", args[0])
		return memoryUsage()
	}
	return fn(ctx, args[1:])
}

func memoryUsage() error {
	fmt.Fprintln(os.Stderr, "usage: "+memoryUsageText("       "))
	return cliexit.Code(1)
}

// cmdMemorySync makes the central cc- banks equal the Claude Code memory files
// under the projects root, then compacts the central root. It waits for a sync
// already running, unlike the session hook, which skips.
func cmdMemorySync(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("memory sync", flag.ContinueOnError)
	dryRun := flags.Bool("dry-run", false, "report the plan; write nothing")
	rebuild := flags.Bool("rebuild", false, "delete every cc- row, then sync from scratch")
	jsonOut := flags.Bool("json", false, "print the result as JSON")
	if _, err := parseInterspersed("memory sync", flags, args, 0); err != nil {
		return err
	}
	ctx, flush := withMemoryTelemetry(ctx, telemetry.Identity{})
	defer flush()
	home, err := os.UserHomeDir()
	if err != nil {
		return verbFatal(err)
	}
	var res ccmemory.SyncResult
	central, err := runOnCentral(ctx, home, *dryRun, func(mem *memory.Memory) error {
		var err error
		res, err = ccmemory.Sync(ctx, mem, ccmemory.SyncOptions{
			Projects:    claudecode.ProjectsRoot(home),
			CentralRoot: mem.Root(),
			DryRun:      *dryRun,
			Rebuild:     *rebuild,
			WaitForLock: true,
		})
		return err
	})
	if err != nil {
		return verbFatal(err)
	}
	res.Root = central
	if *jsonOut {
		return printOrgJSON(res)
	}
	printMemorySyncSummary(res, *dryRun)
	return nil
}

// cmdMemoryMigrate is the one-time import of every Claude Code memory file into
// the central root. It runs the sync apply and then verifies the rows. Exit 0
// when verified, 1 otherwise; a dry run exits 0 after printing the plan.
func cmdMemoryMigrate(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("memory migrate", flag.ContinueOnError)
	slug := flags.String("project", "", "migrate only this project slug")
	dryRun := flags.Bool("dry-run", false, "print the inventory and plan; write nothing")
	jsonOut := flags.Bool("json", false, "print the result as JSON")
	if _, err := parseInterspersed("memory migrate", flags, args, 0); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return verbFatal(err)
	}
	var res ccmemory.MigrateResult
	central, err := runOnCentral(ctx, home, *dryRun, func(mem *memory.Memory) error {
		var err error
		res, err = ccmemory.Migrate(ctx, mem, ccmemory.SyncOptions{
			Projects:    claudecode.ProjectsRoot(home),
			CentralRoot: mem.Root(),
			DryRun:      *dryRun,
			WaitForLock: true,
			OnlySlug:    *slug,
		})
		return err
	})
	if err != nil {
		return verbFatal(err)
	}
	res.Root = central
	if *jsonOut {
		if err := printOrgJSON(res); err != nil {
			return verbFatal(err)
		}
	} else {
		printMigrateSummary(res, *dryRun)
	}
	if *dryRun {
		return nil
	}
	if code := migrateExitCode(res); code != 0 {
		return cliexit.Code(code)
	}
	return nil
}

// cmdMemoryRecall ranks the central memories for a query with ccmemory.Recall,
// the function the agent_recall MCP command calls, so the rows match. No
// embedder is wired on this path, so the ranking is keyword (BM25) only.
func cmdMemoryRecall(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("memory recall", flag.ContinueOnError)
	scope := flags.String("scope", "project", "project, global or all")
	var types stringList
	flags.Var(&types, "type", "keep only this kind (user, feedback, project, reference, unknown); repeatable")
	limit := flags.Int("limit", memory.RecallLimit, "maximum rows")
	dir := flags.String("dir", "", "caller repository (default: the current directory)")
	jsonOut := flags.Bool("json", false, "print the result as JSON")
	words, err := parseInterspersed("memory recall", flags, args, -1)
	if err != nil {
		return err
	}
	query := strings.Join(words, " ")
	if strings.TrimSpace(query) == "" {
		return cliexit.Usage(errors.New("memory recall: a query is required"))
	}
	ctx, flush := withMemoryTelemetry(ctx, telemetry.Identity{})
	defer flush()
	home, err := os.UserHomeDir()
	if err != nil {
		return verbFatal(err)
	}
	repo, err := filepath.Abs(*dir) // Abs("") is the working directory
	if err != nil {
		return verbFatal(err)
	}
	h := ccmemory.NewHandle(home, nil)
	defer h.Close()
	mem, err := h.Get(ctx)
	if err != nil {
		return verbFatal(err)
	}
	res, err := ccmemory.Recall(ctx, mem, ccmemory.RecallRequest{
		Query:  query,
		Limit:  *limit,
		Scope:  *scope,
		Types:  types,
		Caller: ccmemory.ProjectKey(claudecode.ProjectsRoot(home), repo),
	})
	if err != nil {
		return verbFatal(err)
	}
	if *jsonOut {
		return printOrgJSON(res)
	}
	printRecallText(res)
	return nil
}

func printRecallText(res ccmemory.RecallResult) {
	if res.Count == 0 {
		fmt.Println("recall: no memories match")
		return
	}
	for i, m := range res.Memories {
		project := m.Project
		if project == "" {
			project = "global"
		}
		fmt.Printf("%d. [%s] %s (%s)\n", i+1, m.Kind, m.Name, project)
		if m.Description != "" {
			fmt.Printf("   %s\n", m.Description)
		}
		fmt.Printf("   %s\n", recallPreview(m.Content))
	}
}

// recallPreview is the first non-empty line of content, cut to 120 runes.
func recallPreview(content string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	if r := []rune(line); len(r) > 120 {
		return string(r[:120]) + "..."
	}
	return line
}

// migrateExitCode maps a migrate result to the process status: 0 when every
// row matches the desired set, 1 otherwise.
func migrateExitCode(res ccmemory.MigrateResult) int {
	if res.Verified {
		return 0
	}
	return 1
}

// runOnCentral opens the central root under home, calls run with it and then
// compacts it, unless this is a dry run. A dry run on an absent root calls run
// on an empty scratch root instead, so the central root is never created. It
// returns the central root path, which the caller reports as the result's root.
func runOnCentral(ctx context.Context, home string, dryRun bool, run func(*memory.Memory) error) (string, error) {
	central := memory.GlobalRoot(home)
	if _, err := os.Stat(central); dryRun && errors.Is(err, os.ErrNotExist) {
		scratch, err := os.MkdirTemp("", "leankg-memory-dry-run-")
		if err != nil {
			return central, verbFatal(err)
		}
		defer os.RemoveAll(scratch)
		mem, err := memory.Open(ctx, scratch, false)
		if err != nil {
			return central, err
		}
		defer mem.Close()
		return central, run(mem)
	}
	mem, err := memory.Open(ctx, "", true)
	if err != nil {
		return central, err
	}
	defer mem.Close()
	if err := run(mem); err != nil || dryRun {
		return central, err
	}
	return central, mem.Compact(ctx)
}

func printMemorySyncSummary(res ccmemory.SyncResult, dryRun bool) {
	prefix := "memory sync"
	if dryRun {
		prefix = "memory sync (dry run, nothing written)"
	}
	fmt.Printf("%s: %d documents, %d upserted, %d deleted, %d skipped\n",
		prefix, res.Documents, res.Upserted, res.Deleted, res.Skipped)
	if res.DeletesWithheld {
		fmt.Println("  WARNING: deletes withheld; the projects root is missing or holds no memories while central rows exist. Use --rebuild to clear them.")
	}
	for _, b := range res.Banks {
		project := b.Project
		if project == "" {
			project = "global"
		}
		fmt.Printf("  %-24s %-24s %d rows\n", b.Bank, project, b.Rows)
	}
}

func printMigrateSummary(res ccmemory.MigrateResult, dryRun bool) {
	prefix := "memory migrate"
	if dryRun {
		prefix = "memory migrate (dry run, nothing written)"
	}
	fmt.Println(prefix)
	fmt.Printf("  %-24s %-24s %6s %7s %8s\n", "project", "bank", "files", "global", "skipped")
	for _, p := range res.Projects {
		fmt.Printf("  %-24s %-24s %6d %7d %8d\n", p.Slug, p.Bank, p.Files, p.Global, p.Skipped)
	}
	action := "applied"
	if dryRun {
		action = "planned"
	}
	fmt.Printf("%s: %d documents, %d upserted, %d deleted, %d skipped\n",
		action, res.Documents, res.Upserted, res.Deleted, res.Skipped)
	if res.DeletesWithheld {
		fmt.Println("  WARNING: deletes withheld; the scope holds no memories while central rows exist.")
	}
	if !dryRun {
		if res.Verified {
			fmt.Printf("verification: verified, %d documents match their rows\n", res.Documents)
		} else {
			fmt.Printf("verification: FAILED, %d mismatches\n", len(res.Mismatches))
			for _, m := range res.Mismatches {
				fmt.Printf("  %s %s %s\n", m.Bank, m.DocID, m.Reason)
			}
		}
	}
	fmt.Println("slug to bank:")
	for _, p := range res.Projects {
		fmt.Printf("  %s -> %s\n", p.Slug, p.Bank)
	}
}
