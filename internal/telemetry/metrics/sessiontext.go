package metrics

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/internal/telemetry/report"
)

// SessionText renders one session for `leankg metrics --session` (DS-16):
// the session line, its KPIs (estimates marked with their method, plan §2
// rule 5) and its ranked failures. Every line is indented two spaces.
func SessionText(d report.SessionDetail) string {
	var b strings.Builder
	s := d.Session
	fmt.Fprintf(&b, "  %s  client=%s  correlation=%s", s.ID, s.ClientName, s.Correlation)
	if s.LinkStatus != "" {
		fmt.Fprintf(&b, "  transcript=%s", s.LinkStatus)
	}
	b.WriteString("\n")
	for _, k := range d.KPIs {
		v := strconv.FormatFloat(k.Value, 'f', -1, 64)
		if k.Unit == "%" || k.Unit == "ms" {
			v = strconv.FormatFloat(k.Value, 'f', 1, 64)
		}
		unit := ""
		switch k.Unit {
		case "":
		case "%":
			unit = "%"
		default:
			unit = " " + k.Unit
		}
		fmt.Fprintf(&b, "  %s: %s%s", k.Label, v, unit)
		if k.Estimate {
			if k.Method != "" {
				fmt.Fprintf(&b, " (estimate, %s)", k.Method)
			} else {
				b.WriteString(" (estimate)")
			}
		}
		b.WriteString("\n")
	}
	if len(d.Failures) > 0 {
		b.WriteString("  Failures:\n")
		for _, f := range d.Failures {
			fmt.Fprintf(&b, "    %s: %d\n", f.Key, f.Count)
		}
	}
	return b.String()
}
