// Cross-cluster tunnels (Rust US-MP-06): relationships whose two endpoint
// elements belong to different detected communities. The Rust engine read the
// persisted cluster_id column; the Go store has none, so the clusters come from
// the shared Louvain detector (ClusterAssignments) and the assignment is
// threaded through by qualified name.
//
// RenderTunnels renders the Rust `tunnels` CLI output for the cmd layer.
package web

import (
	"fmt"
	"sort"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// Tunnel is one cross-cluster edge (Rust graph::query::Tunnel).
type Tunnel struct {
	Source          string  `json:"source"`
	Target          string  `json:"target"`
	RelType         string  `json:"rel_type"`
	Confidence      float64 `json:"confidence"`
	ConfidenceLabel string  `json:"confidence_label"`
	SourceCluster   string  `json:"source_cluster"`
	TargetCluster   string  `json:"target_cluster"`
}

// Tunnels returns every relationship whose source and target are both
// clustered and whose clusters differ, sorted by confidence descending
// (Rust GraphEngine::find_tunnels). Ties break by source, target, then
// rel_type so the result is deterministic — the Rust sort was stable over an
// unspecified row order.
//
// The caller applies the display limit: the Rust CLI truncated find_tunnels'
// result to --limit (default 50), and the MCP tool did the same.
func Tunnels(st store.Backend) ([]Tunnel, error) {
	cl, err := ClusterAssignments(st)
	if err != nil {
		return nil, err
	}
	rels, err := allRelationships(st)
	if err != nil {
		return nil, err
	}
	tunnels := make([]Tunnel, 0, 16)
	for _, r := range rels {
		srcCluster, srcOK := cl.Assignments[r.Source]
		tgtCluster, tgtOK := cl.Assignments[r.Target]
		if !srcOK || !tgtOK || srcCluster == tgtCluster {
			continue
		}
		tunnels = append(tunnels, Tunnel{
			Source:          r.Source,
			Target:          r.Target,
			RelType:         r.RelType,
			Confidence:      r.Confidence,
			ConfidenceLabel: confidenceLabelFor(r),
			SourceCluster:   srcCluster,
			TargetCluster:   tgtCluster,
		})
	}
	sort.Slice(tunnels, func(i, j int) bool {
		if tunnels[i].Confidence != tunnels[j].Confidence {
			return tunnels[i].Confidence > tunnels[j].Confidence
		}
		if tunnels[i].Source != tunnels[j].Source {
			return tunnels[i].Source < tunnels[j].Source
		}
		if tunnels[i].Target != tunnels[j].Target {
			return tunnels[i].Target < tunnels[j].Target
		}
		return tunnels[i].RelType < tunnels[j].RelType
	})
	return tunnels, nil
}

// RenderTunnels renders the Rust `tunnels` verb output verbatim: a "Found N
// cross-cluster tunnels:" header (printed even for zero) followed by one
// "  N. src --[type]--> tgt  (0.99)  [cluster_0 -> cluster_1]" line per tunnel.
func RenderTunnels(ts []Tunnel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d cross-cluster tunnels:\n", len(ts))
	for i, t := range ts {
		fmt.Fprintf(&b, "  %d. %s --[%s]--> %s  (%.2f)  [%s -> %s]\n",
			i+1, t.Source, t.RelType, t.Target, t.Confidence, t.SourceCluster, t.TargetCluster)
	}
	return b.String()
}
