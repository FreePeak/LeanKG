// Org/ops knowledge verbs: ports of the Rust CLI `incident`, `note` and
// `env-conflicts`, plus the service-context and team-map reads (the Rust
// engine exposed those two over MCP and /api/v2). Domain logic lives in
// internal/orgknowledge; these functions only parse flags and print.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/FreePeak/LeanKG/go/internal/orgknowledge"
	"github.com/FreePeak/LeanKG/go/internal/store"
)

// withOrgKnowledge opens the project store (same --project/$LEANKG_PROJECT
// convention as the other verbs), runs fn and closes the handle.
func withOrgKnowledge(project string, mode store.Mode, fn func(*orgknowledge.Knowledge) error) {
	st, _, err := openVerbStore(project, mode)
	if err != nil {
		verbFatal(err)
	}
	defer st.Close()
	if err := fn(orgknowledge.New(st)); err != nil {
		verbFatal(err)
	}
}

// cmdIncident ports `leankg incident add|list|show`.
func cmdIncident(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: leankg incident <add|list|show> ...")
		os.Exit(2)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		fs := flag.NewFlagSet("incident add", flag.ExitOnError)
		project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
		title := fs.String("title", "", "incident title")
		severity := fs.String("severity", "", "severity: P0|P1|P2|P3")
		affected := fs.String("affected", "", "affected service(s), comma-separated")
		rootCause := fs.String("root-cause", "", "root cause description")
		resolution := fs.String("resolution", "", "resolution description")
		prevention := fs.String("prevention", "", "prevention advice")
		env := fs.String("env", "production", "environment")
		ticket := fs.String("ticket", "", "linked ticket id")
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		now := time.Now().Unix()
		inc := store.Incident{
			Env: *env, Title: *title, Severity: *severity, OccurredAt: now,
			// The Rust verb stamped resolved_at with the same clock value it
			// used for occurred_at; kept for parity.
			ResolvedAt:       &now,
			RootCause:        *rootCause,
			Resolution:       *resolution,
			AffectedServices: orgknowledge.SplitServices(*affected),
			Tags:             []string{},
			Author:           orgknowledge.AuthorFromEnv(),
		}
		if *prevention != "" {
			inc.Prevention = prevention
		}
		if *ticket != "" {
			inc.LinkedTicket = ticket
		}
		withOrgKnowledge(*project, store.RW, func(k *orgknowledge.Knowledge) error {
			created, err := k.CreateIncident(inc)
			if err != nil {
				return err
			}
			fmt.Printf("Created incident '%s' (%s)\n", created.ID, created.Title)
			fmt.Printf("  Severity: %s\n", created.Severity)
			fmt.Printf("  Affected: %s\n", strings.Join(created.AffectedServices, ", "))
			return nil
		})

	case "list":
		fs := flag.NewFlagSet("incident list", flag.ExitOnError)
		project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
		service := fs.String("service", "", "service name")
		env := fs.String("env", "production", "environment")
		pattern := fs.String("pattern", "", "search pattern (title or root cause)")
		limit := fs.Int("limit", 10, "limit results")
		if err := fs.Parse(rest); err != nil {
			os.Exit(2)
		}
		withOrgKnowledge(*project, store.RO, func(k *orgknowledge.Knowledge) error {
			incidents, err := k.QueryIncidents(*service, *pattern, *env, *limit)
			if err != nil {
				return err
			}
			if len(incidents) == 0 {
				fmt.Printf("No incidents found for service '%s' in env '%s'\n", *service, *env)
				return nil
			}
			fmt.Printf("Found %d incident(s) for service '%s' (env: %s):\n", len(incidents), *service, *env)
			for _, inc := range incidents {
				fmt.Printf("\n  ID:          %s\n", inc.ID)
				fmt.Printf("  Title:       %s\n", inc.Title)
				fmt.Printf("  Severity:    %s\n", inc.Severity)
				fmt.Printf("  Occurred:    %d\n", inc.OccurredAt)
				fmt.Printf("  Root Cause:  %s\n", inc.RootCause)
				fmt.Printf("  Resolution:  %s\n", inc.Resolution)
				if inc.Prevention != nil {
					fmt.Printf("  Prevention:  %s\n", *inc.Prevention)
				}
				if inc.LinkedTicket != nil {
					fmt.Printf("  Ticket:      %s\n", *inc.LinkedTicket)
				}
			}
			return nil
		})

	case "show":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: leankg incident show <id> [--project DIR]")
			os.Exit(2)
		}
		id := rest[0]
		fs := flag.NewFlagSet("incident show", flag.ExitOnError)
		project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
		if err := fs.Parse(rest[1:]); err != nil {
			os.Exit(2)
		}
		withOrgKnowledge(*project, store.RO, func(k *orgknowledge.Knowledge) error {
			inc, found, err := k.GetIncident(id)
			if err != nil {
				return err
			}
			if !found {
				fmt.Printf("Incident '%s' not found\n", id)
				return nil
			}
			fmt.Println("Incident Details:")
			fmt.Printf("  ID:             %s\n", inc.ID)
			fmt.Printf("  Title:          %s\n", inc.Title)
			fmt.Printf("  Environment:    %s\n", inc.Env)
			fmt.Printf("  Severity:       %s\n", inc.Severity)
			fmt.Printf("  Occurred At:    %d\n", inc.OccurredAt)
			if inc.ResolvedAt != nil {
				fmt.Printf("  Resolved At:    %d\n", *inc.ResolvedAt)
			}
			fmt.Printf("  Root Cause:     %s\n", inc.RootCause)
			fmt.Printf("  Resolution:     %s\n", inc.Resolution)
			fmt.Printf("  Affected Svcs:  %s\n", strings.Join(inc.AffectedServices, ", "))
			if inc.TriggerPattern != nil {
				fmt.Printf("  Trigger:        %s\n", *inc.TriggerPattern)
			}
			if inc.Prevention != nil {
				fmt.Printf("  Prevention:     %s\n", *inc.Prevention)
			}
			fmt.Printf("  Tags:           %s\n", strings.Join(inc.Tags, ", "))
			fmt.Printf("  Author:         %s\n", inc.Author)
			if inc.LinkedTicket != nil {
				fmt.Printf("  Ticket:         %s\n", *inc.LinkedTicket)
			}
			return nil
		})

	default:
		fmt.Fprintf(os.Stderr, "leankg: unknown incident subcommand %q\n", sub)
		os.Exit(2)
	}
}

// cmdNote ports `leankg note`: a team note anchored on a service or element.
func cmdNote(args []string) {
	fs := flag.NewFlagSet("note", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	target := fs.String("target", "", "target service or element qualified name")
	content := fs.String("content", "", "note content")
	env := fs.String("env", "local", "environment")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	withOrgKnowledge(*project, store.RW, func(k *orgknowledge.Knowledge) error {
		note, err := k.AddNote(*target, *content, *env, orgknowledge.AuthorFromEnv())
		if err != nil {
			return err
		}
		fmt.Printf("Added note to '%s' (env: %s)\n", *target, note.Environment)
		fmt.Printf("  Content: %s\n", note.Content)
		return nil
	})
}

// cmdEnvConflicts ports `leankg env-conflicts --service NAME`.
func cmdEnvConflicts(args []string) {
	fs := flag.NewFlagSet("env-conflicts", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	service := fs.String("service", "", "service name")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	withOrgKnowledge(*project, store.RO, func(k *orgknowledge.Knowledge) error {
		report, err := k.EnvConflictReport(*service)
		if err != nil {
			return err
		}
		return report.WriteText(os.Stdout)
	})
}

// cmdServiceContext renders the get_service_context payload (Rust served it
// over MCP and /api/v2/service/context; the JSON matches that shape).
func cmdServiceContext(args []string) {
	fs := flag.NewFlagSet("service-context", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	service := fs.String("service", "", "service name (element qualified name)")
	env := fs.String("env", "production", "environment")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	withOrgKnowledge(*project, store.RO, func(k *orgknowledge.Knowledge) error {
		ctx, err := k.ServiceContext(*service, *env)
		if err != nil {
			return err
		}
		return printOrgJSON(ctx)
	})
}

// cmdTeamMap renders the get_team_map payload (Rust served it over MCP).
func cmdTeamMap(args []string) {
	fs := flag.NewFlagSet("team-map", flag.ExitOnError)
	project := fs.String("project", "", "project directory (default cwd, or LEANKG_PROJECT)")
	env := fs.String("env", "production", "environment")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	withOrgKnowledge(*project, store.RO, func(k *orgknowledge.Knowledge) error {
		teams, err := k.TeamMap(*env)
		if err != nil {
			return err
		}
		return printOrgJSON(teams)
	})
}

func printOrgJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
