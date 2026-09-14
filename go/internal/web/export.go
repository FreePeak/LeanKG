// Exported wrappers over the dashboard's graph analytics, used by the
// cmd/leankg verbs that surface them on the command line (report, gods). The
// dashboard handlers and these wrappers share one implementation, so the CLI
// can never drift from /api/graph/report.
package web

import (
	"path/filepath"

	"github.com/FreePeak/LeanKG/go/internal/core"
)

// GodNodes returns the top-N most connected elements for the engine's project
// (US-GF-05 / Rust GraphEngine::get_god_nodes). A percentile above 0 drops
// the super-hub tail before the limit is applied.
func GodNodes(engine *core.Engine, limit, excludeHubsPercentile int) ([]GodNode, error) {
	snap, err := loadSnapshot(engine.Store())
	if err != nil {
		return nil, err
	}
	rels, err := allRelationships(engine.Store())
	if err != nil {
		return nil, err
	}
	return getGodNodes(snap, rels, limit, excludeHubsPercentile), nil
}

// GraphReportFor builds the US-GF-06 graph report for the engine's project.
// projectDir supplies the default display name; projectName overrides it.
func GraphReportFor(engine *core.Engine, projectDir, projectName string) (GraphReport, error) {
	return buildGraphReport(projectDir, engine, projectName)
}

// GraphReportMarkdown renders a report as the GRAPH_REPORT.md body
// (Rust GraphReport::to_markdown).
func GraphReportMarkdown(r GraphReport) string { return reportToMarkdown(r) }

// projectNameFor is the display-name rule shared by the report verb and the
// dashboard handler: explicit name, then the directory basename, then
// "project".
func projectNameFor(projectDir, projectName string) string {
	if projectName != "" {
		return projectName
	}
	if projectDir != "" {
		if base := filepath.Base(projectDir); base != "" && base != "." {
			return base
		}
	}
	return "project"
}
