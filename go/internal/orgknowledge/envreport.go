// The CLI-shaped environment report: the Rust `leankg env-conflicts
// --service <svc>` verb (show_env_conflicts, src/main.rs). It is a different
// read from the MCP/API find_env_conflicts (which compares one element's
// metadata across environments): this one lists the conflicts_with edges
// around a service plus every qualified name that exists in more than one
// environment.
package orgknowledge

import (
	"fmt"
	"io"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cliVariantCap is the Rust print cap on cross-environment variants before it
// switches to the "... and N more" line.
const cliVariantCap = 20

// EnvConflictReport is the data behind the `env-conflicts` verb.
type EnvConflictReport struct {
	Service string
	// Pairs are the conflicts_with edges with the service on either side.
	Pairs []store.Relationship
	// Variants are the qualified names present in more than one environment
	// (capped at cliVariantCap for rendering), VariantTotal being the count
	// before that cap.
	Variants     []store.EnvVariant
	VariantTotal int
}

// EnvConflictReport gathers the verb's two sections. The service argument is
// matched as a case-insensitive substring of the endpoints (the Rust
// regex_matches(lowercase(...), ".*svc.*")).
func (k *Knowledge) EnvConflictReport(service string) (EnvConflictReport, error) {
	rep := EnvConflictReport{Service: service}
	pairs, err := k.st.EnvSnapshotConflictsWith(service, 0)
	if err != nil {
		return rep, err
	}
	rep.Pairs = pairs

	variants, err := k.st.EnvSnapshotEnvVariants(service, 0)
	if err != nil {
		return rep, err
	}
	rep.VariantTotal = len(variants)
	if len(variants) > cliVariantCap {
		variants = variants[:cliVariantCap]
	}
	rep.Variants = variants
	return rep, nil
}

// Empty reports whether the verb would print the "no conflicts" line.
func (r EnvConflictReport) Empty() bool { return len(r.Pairs) == 0 && r.VariantTotal == 0 }

// WriteText renders the Rust CLI output verbatim, including the blank line
// before the second section and the 20-entry cap note.
func (r EnvConflictReport) WriteText(w io.Writer) error {
	var sb strings.Builder
	if len(r.Pairs) > 0 {
		fmt.Fprintf(&sb, "Environment conflicts for service '%s':\n", r.Service)
		for _, p := range r.Pairs {
			fmt.Fprintf(&sb, "  - %s <-> %s (confidence: %.2f)\n", p.Source, p.Target, p.Confidence)
		}
	}
	if len(r.Variants) > 0 {
		fmt.Fprintf(&sb, "\nCross-environment element variants for '%s':\n", r.Service)
		for _, v := range r.Variants {
			fmt.Fprintf(&sb, "  - %s (envs: %s)\n", v.QualifiedName, strings.Join(v.Envs, ", "))
		}
		if r.VariantTotal > len(r.Variants) {
			fmt.Fprintf(&sb, "  ... and %d more\n", r.VariantTotal-len(r.Variants))
		}
	}
	if r.Empty() {
		fmt.Fprintf(&sb, "No environment conflicts found for service '%s'\n", r.Service)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}
