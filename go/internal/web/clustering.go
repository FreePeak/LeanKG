// Community detection ported from the deleted Rust src/graph/clustering.rs:
// Louvain-inspired modularity optimization with a folder-based fallback,
// re-expressed over store.Backend. The dashboard /api/graph/report uses it
// as the cluster source for "surprising cross-cluster edges" (the Rust
// engine read the persisted cluster_id column; the Go store has none).
package web

import (
	"sort"
	"strconv"
	"strings"

	"github.com/FreePeak/LeanKG/go/internal/store"
)

// cluster is the Rust clustering::Cluster.
type cluster struct {
	ID                  string
	Label               string
	Members             []string
	RepresentativeFiles []string
}

type weightedEdge struct {
	to string
	w  float64
}

// detectCommunities ports CommunityDetector::detect_communities. Element
// iteration is in qualified_name order (the store's natural order), so the
// result is deterministic — the Rust HashMap iteration was not.
func detectCommunities(snap *snapshot, rels []store.Relationship) map[string]*cluster {
	if snap.count() == 0 {
		return map[string]*cluster{}
	}

	// Adjacency with edge weights: CALLS weight 2, IMPORTS weight 1.
	adjacency := map[string][]weightedEdge{}
	totalWeight := 0.0
	for _, rel := range rels {
		var weight float64
		switch rel.RelType {
		case "calls":
			weight = 2.0
		case "imports":
			weight = 1.0
		default:
			continue
		}
		totalWeight += weight
		adjacency[rel.Source] = append(adjacency[rel.Source], weightedEdge{rel.Target, weight})
		adjacency[rel.Target] = append(adjacency[rel.Target], weightedEdge{rel.Source, weight})
	}
	if totalWeight == 0.0 {
		return fallbackFolderClustering(snap.elements)
	}

	// Initialize: each node in its own community.
	community := map[string]int{}
	nodeIDs := make([]string, 0, snap.count())
	communityWeights := map[int]float64{}
	for i, e := range snap.elements {
		qn := e.QualifiedName
		community[qn] = i
		nodeIDs = append(nodeIDs, qn)
		w := 0.0
		for _, nb := range adjacency[qn] {
			w += nb.w
		}
		communityWeights[i] = w
	}
	nodeWeights := map[string]float64{}
	for qn, nbs := range adjacency {
		w := 0.0
		for _, nb := range nbs {
			w += nb.w
		}
		nodeWeights[qn] = w
	}
	communityNodes := map[int][]string{}
	for _, qn := range nodeIDs {
		c := community[qn]
		communityNodes[c] = append(communityNodes[c], qn)
	}

	const (
		resolution    = 1.0
		maxIterations = 10
	)
	m2 := totalWeight * 2.0

	improved := true
	for iteration := 0; improved && iteration < maxIterations; iteration++ {
		improved = false
		for _, qn := range nodeIDs {
			current := community[qn]
			neighbors := adjacency[qn]
			if len(neighbors) == 0 {
				continue
			}
			nodeW := nodeWeights[qn]

			bestComm := current
			bestGain := 0.0
			for _, nb := range neighbors {
				neighborComm, ok := community[nb.to]
				if !ok || neighborComm == current {
					continue
				}
				incoming := 0.0
				for _, nb2 := range neighbors {
					if c, ok := community[nb2.to]; ok && c == neighborComm {
						incoming += nb2.w
					}
				}
				gain := incoming - (nodeW*communityWeights[neighborComm]/m2)*resolution
				if gain > bestGain {
					bestGain = gain
					bestComm = neighborComm
				}
			}

			if bestGain > 0.001 && bestComm != current {
				kept := communityNodes[current][:0]
				for _, m := range communityNodes[current] {
					if m != qn {
						kept = append(kept, m)
					}
				}
				communityNodes[current] = kept
				communityWeights[current] -= nodeW
				community[qn] = bestComm
				communityNodes[bestComm] = append(communityNodes[bestComm], qn)
				communityWeights[bestComm] += nodeW
				improved = true
			}
		}
	}

	// Build clusters from communities, in deterministic community order.
	commIDs := make([]int, 0, len(communityNodes))
	for id := range communityNodes {
		commIDs = append(commIDs, id)
	}
	sort.Ints(commIDs)

	byQN := snap.byQN
	clusters := map[string]*cluster{}
	clusterIDCounter := 0
	for _, commID := range commIDs {
		members := communityNodes[commID]
		if len(members) == 0 {
			continue
		}
		sort.Strings(members)
		filePath := ""
		if e, ok := byQN[members[0]]; ok {
			filePath = e.FilePath
		}
		label := generateClusterLabel("comm_"+strconv.Itoa(commID), filePath)
		id := "cluster_" + strconv.Itoa(clusterIDCounter)
		clusterIDCounter++

		fileCounts := map[string]int{}
		for _, m := range members {
			if e, ok := byQN[m]; ok {
				fileCounts[e.FilePath]++
			}
		}
		files := make([]string, 0, len(fileCounts))
		for f := range fileCounts {
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool {
			if fileCounts[files[i]] != fileCounts[files[j]] {
				return fileCounts[files[i]] > fileCounts[files[j]]
			}
			return files[i] < files[j]
		})
		if len(files) > 5 {
			files = files[:5]
		}
		clusters[id] = &cluster{ID: id, Label: label, Members: members, RepresentativeFiles: files}
	}
	return clusters
}

// fallbackFolderClustering groups elements by parent directory when the
// graph has no calls/imports edges (Rust fallback_folder_clustering).
func fallbackFolderClustering(elements []store.Element) map[string]*cluster {
	folders := map[string][]string{}
	var order []string
	for _, e := range elements {
		folder := "root"
		if i := strings.LastIndex(e.FilePath, "/"); i >= 0 {
			folder = e.FilePath[:i]
		}
		if _, ok := folders[folder]; !ok {
			order = append(order, folder)
		}
		folders[folder] = append(folders[folder], e.QualifiedName)
	}
	sort.Strings(order)

	clusters := map[string]*cluster{}
	for i, folder := range order {
		label := folder
		if j := strings.LastIndex(folder, "/"); j >= 0 {
			label = folder[j+1:]
		}
		clusters["cluster_"+strconv.Itoa(i)] = &cluster{
			ID:                  "cluster_" + strconv.Itoa(i),
			Label:               label,
			Members:             folders[folder],
			RepresentativeFiles: []string{folder},
		}
	}
	return clusters
}

// generateClusterLabel ports the Rust helper: normalized parent directory
// name, else module_<id>.
func generateClusterLabel(clusterID, filePath string) string {
	parts := strings.Split(filePath, "/")
	if len(parts) >= 2 {
		dir := parts[len(parts)-2]
		var b strings.Builder
		for i := 0; i < len(dir); i++ {
			c := dir[i]
			switch {
			case c >= 'A' && c <= 'Z':
				b.WriteByte(c + 32)
			case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
				b.WriteByte(c)
			default:
				b.WriteByte('_')
			}
		}
		normalized := b.String()
		if normalized != "" && normalized != "_" {
			return normalized
		}
	}
	return strings.NewReplacer("cluster_", "module_", "comm_", "module_").Replace(clusterID)
}

// --- exported community-detection surface (CLI, export, tunnel analysis) ---

// ClusterSummary is one detected community as surfaced to CLI/export
// consumers: the stable cluster id ("cluster_<n>"), the folder-derived label,
// the member count, and up to 5 representative files.
type ClusterSummary struct {
	ID                  string   `json:"id"`
	Label               string   `json:"label"`
	MemberCount         int      `json:"member_count"`
	RepresentativeFiles []string `json:"representative_files"`
}

// Clustering is the full community-detection result: per-cluster summaries in
// deterministic order (member count descending, then id — the Rust
// detect-clusters CLI ordering) plus the qualified_name -> cluster-id
// assignment covering every clustered element. JSON-marshalable for the CLI.
type Clustering struct {
	Clusters    []ClusterSummary  `json:"clusters"`
	Assignments map[string]string `json:"assignments"`
}

// ClusterAssignments runs the ported Louvain detector over the store and
// returns both views of the result. The Rust engine read a persisted
// cluster_id column; the Go store has none, so this is the single recompute
// path shared by the dashboard, the CLI, export, and tunnel analysis.
func ClusterAssignments(st store.Backend) (*Clustering, error) {
	snap, err := loadSnapshot(st)
	if err != nil {
		return nil, err
	}
	rels, err := allRelationships(st)
	if err != nil {
		return nil, err
	}
	clusters := detectCommunities(snap, rels)
	out := &Clustering{
		Clusters:    make([]ClusterSummary, 0, len(clusters)),
		Assignments: make(map[string]string, snap.count()),
	}
	for _, c := range clusters {
		out.Clusters = append(out.Clusters, ClusterSummary{
			ID:                  c.ID,
			Label:               c.Label,
			MemberCount:         len(c.Members),
			RepresentativeFiles: append([]string(nil), c.RepresentativeFiles...),
		})
		for _, m := range c.Members {
			out.Assignments[m] = c.ID
		}
	}
	sort.Slice(out.Clusters, func(i, j int) bool {
		if out.Clusters[i].MemberCount != out.Clusters[j].MemberCount {
			return out.Clusters[i].MemberCount > out.Clusters[j].MemberCount
		}
		return out.Clusters[i].ID < out.Clusters[j].ID
	})
	return out, nil
}
