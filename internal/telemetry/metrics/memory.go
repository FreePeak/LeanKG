package metrics

import (
	"context"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/internal/telemetry"
	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// memoryRecent is the number of newest memory events the page lists.
const memoryRecent = 50

// Memory is GET /memory: recall and retain KPIs, per-bank stats and the
// newest events. bank "" means every bank; a bank name restricts every figure
// to that bank.
//
// NeverRecalled is a proxy. The ledger stores how many rows a retain wrote,
// not their ids, so the share is 1 - distinct ids ever returned / written.
func Memory(ctx context.Context, st telemetry.Store, o Options, bank string) (report.Memory, error) {
	ds, err := load(ctx, st, o)
	if err != nil {
		return report.Memory{}, err
	}
	events := make([]telemetry.MemoryEvent, 0, len(ds.mem))
	for _, m := range ds.mem {
		if bank == "" || hasBank(m.Banks, bank) {
			events = append(events, m)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].TS.Equal(events[j].TS) {
			return events[i].TS.After(events[j].TS)
		}
		return events[i].ID < events[j].ID
	})

	var recalls, hits, written, skipped, replaced, injected int64
	var ages []float64
	for _, m := range events {
		switch m.Verb {
		case "recall":
			recalls++
			if m.Returned > 0 {
				hits++
				ages = append(ages, float64(m.AgeMedianS))
			}
		case "retain":
			written += int64(m.Written)
			skipped += int64(m.Skipped)
			replaced += int64(m.Replaced)
		case "inject":
			injected += m.Tokens
		}
	}
	hitRate := 0.0
	if recalls > 0 {
		hitRate = float64(hits) / float64(recalls) * 100
	}
	return report.Memory{
		KPIs: []report.KPI{
			kpi("recall_calls", "Recall calls", float64(recalls), ""),
			kpi("recall_hit_rate", "Recall hit rate", hitRate, "%"),
			kpi("median_age_s", "Median age of recalled rows", percentile(ages, 0.5), "s"),
			kpi("retain_written", "Rows written", float64(written), ""),
			kpi("retain_skipped", "Rows skipped", float64(skipped), ""),
			kpi("retain_replaced", "Rows replaced", float64(replaced), ""),
			kpi("injected_tokens", "Injected tokens", float64(injected), "tokens"),
		},
		Banks:  bankStats(events, bank),
		Recent: memoryRows(events),
	}, nil
}

func hasBank(banks, bank string) bool {
	for _, b := range splitBanks(banks) {
		if b == bank {
			return true
		}
	}
	return false
}

// splitBanks splits the comma-joined bank list. An event with no bank is
// attributed to "default".
func splitBanks(banks string) []string {
	var out []string
	for _, b := range strings.Split(banks, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		out = []string{"default"}
	}
	return out
}

type bankAcc struct {
	recalls, hits, written int64
	returned               map[string]bool
}

// bankStats attributes each event to every bank it names. When a bank filter
// is set, only that bank is reported.
func bankStats(events []telemetry.MemoryEvent, only string) []report.BankStat {
	acc := map[string]*bankAcc{}
	get := func(b string) *bankAcc {
		a := acc[b]
		if a == nil {
			a = &bankAcc{returned: map[string]bool{}}
			acc[b] = a
		}
		return a
	}
	for _, m := range events {
		for _, b := range splitBanks(m.Banks) {
			if only != "" && b != only {
				continue
			}
			a := get(b)
			switch m.Verb {
			case "recall":
				a.recalls++
				if m.Returned > 0 {
					a.hits++
				}
				for _, id := range strings.Split(m.ReturnedIDs, ",") {
					if id = strings.TrimSpace(id); id != "" {
						a.returned[id] = true
					}
				}
			case "retain":
				a.written += int64(m.Written)
			}
		}
	}
	names := make([]string, 0, len(acc))
	for n := range acc {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]report.BankStat, 0, len(names))
	for _, n := range names {
		a := acc[n]
		bs := report.BankStat{Bank: n, Recalls: a.recalls, Written: a.written}
		if a.recalls > 0 {
			bs.HitRate = float64(a.hits) / float64(a.recalls)
		}
		if a.written > 0 {
			share := 1 - float64(len(a.returned))/float64(a.written)
			if share < 0 {
				share = 0
			}
			bs.NeverRecalled = share
		}
		out = append(out, bs)
	}
	return out
}

func memoryRows(events []telemetry.MemoryEvent) []report.MemoryRow {
	out := []report.MemoryRow{}
	for i, m := range events {
		if i >= memoryRecent {
			break
		}
		out = append(out, memoryRow(m))
	}
	return out
}
