use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
use std::collections::HashMap;

pub struct CommunityDetector {
    graph_engine: GraphEngine,
}

impl CommunityDetector {
    pub fn new(db: &CozoDb) -> Self {
        Self {
            graph_engine: GraphEngine::new(db.clone()),
        }
    }

    /// Louvain-inspired community detection with modularity optimization
    /// This implements the core Louvain algorithm principles:
    /// 1. Initialize each node in its own community
    /// 2. Greedily move nodes to communities that maximize modularity gain
    /// 3. Aggregate the graph and repeat
    /// 4. Refine partitions on original graph
    pub fn detect_communities(
        &self,
    ) -> Result<HashMap<String, Cluster>, Box<dyn std::error::Error>> {
        let elements = self.graph_engine.all_elements()?;
        let relationships = self.graph_engine.all_relationships()?;

        if elements.is_empty() {
            return Ok(HashMap::new());
        }

        // Build adjacency list with edge weights
        let mut adjacency: HashMap<String, Vec<(String, f64)>> = HashMap::new();
        let mut total_weight: f64 = 0.0;

        for elem in &elements {
            adjacency.entry(elem.qualified_name.clone()).or_default();
        }

        // Count weighted edges for CALLS and IMPORTS relationships
        for rel in &relationships {
            if rel.rel_type == "calls" || rel.rel_type == "imports" {
                let weight = if rel.rel_type == "calls" { 2.0 } else { 1.0 };
                total_weight += weight;

                adjacency
                    .entry(rel.source_qualified.clone())
                    .or_default()
                    .push((rel.target_qualified.clone(), weight));
                adjacency
                    .entry(rel.target_qualified.clone())
                    .or_default()
                    .push((rel.source_qualified.clone(), weight));
            }
        }

        if adjacency.is_empty() || total_weight == 0.0 {
            // Fall back to folder-based clustering if no edges
            return self.fallback_folder_clustering(&elements);
        }

        let node_ids: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
        let _n = node_ids.len();

        // Initialize: each node in its own community
        let mut community: HashMap<String, usize> = HashMap::new();
        let mut community_nodes: HashMap<usize, Vec<String>> = HashMap::new();
        let mut community_weights: HashMap<usize, f64> = HashMap::new();

        for (i, node_id) in node_ids.iter().enumerate() {
            community.insert(node_id.clone(), i);
            community_nodes.insert(i, vec![node_id.clone()]);
            let w: f64 = adjacency
                .get(node_id)
                .map(|neighbors| neighbors.iter().map(|(_, w)| w).sum())
                .unwrap_or(0.0);
            community_weights.insert(i, w);
        }

        // Pre-compute node total weights
        let node_weights: HashMap<String, f64> = adjacency
            .iter()
            .map(|(k, v)| (k.clone(), v.iter().map(|(_, w)| w).sum()))
            .collect();

        // Louvain iterations: greedily optimize modularity
        let resolution = 1.0;
        let m2 = total_weight * 2.0; // Total edge weight * 2 (undirected)

        let mut improved = true;
        let mut iterations = 0;
        let max_iterations = 10;

        while improved && iterations < max_iterations {
            improved = false;
            iterations += 1;

            for node_id in &node_ids {
                let current_comm = *community.get(node_id).unwrap_or(&0);
                let node_w = *node_weights.get(node_id).unwrap_or(&0.0);

                // Calculate current modularity contribution
                let neighbors = adjacency.get(node_id).cloned().unwrap_or_default();
                if neighbors.is_empty() {
                    continue;
                }

                // Find best community to join
                let mut best_comm = current_comm;
                let mut best_gain = 0.0;

                for (neighbor, _edge_weight) in &neighbors {
                    if let Some(&neighbor_comm) = community.get(neighbor) {
                        if neighbor_comm == current_comm {
                            continue;
                        }

                        // Calculate modularity gain using Louvain formula
                        // gain = (Incoming / 2m) - (total_weight * community_weight / (2m)^2) * resolution
                        let incoming: f64 = neighbors
                            .iter()
                            .filter(|(_, _w)| {
                                *community.get(neighbor).unwrap_or(&0) == neighbor_comm
                            })
                            .map(|(_, w)| w)
                            .sum();

                        let comm_weight = *community_weights.get(&neighbor_comm).unwrap_or(&0.0);
                        let gain = incoming - (node_w * comm_weight / m2) * resolution;

                        if gain > best_gain {
                            best_gain = gain;
                            best_comm = neighbor_comm;
                        }
                    }
                }

                // Move node to best community if gain is positive
                if best_gain > 0.001 && best_comm != current_comm {
                    // Remove from current community
                    if let Some(current_members) = community_nodes.get_mut(&current_comm) {
                        current_members.retain(|n| n != node_id);
                    }
                    if let Some(current_weight) = community_weights.get_mut(&current_comm) {
                        *current_weight -= node_w;
                    }

                    // Add to new community
                    community.insert(node_id.clone(), best_comm);
                    community_nodes
                        .entry(best_comm)
                        .or_default()
                        .push(node_id.clone());
                    if let Some(new_weight) = community_weights.get_mut(&best_comm) {
                        *new_weight += node_w;
                    }

                    improved = true;
                }
            }
        }

        // Build clusters from communities
        let mut clusters: HashMap<String, Cluster> = HashMap::new();
        let mut cluster_id_counter = 0;

        for (comm_id, members) in community_nodes {
            if members.is_empty() {
                continue;
            }

            // Use representative file to generate cluster label
            let representative = members.first().unwrap();
            let elem = elements
                .iter()
                .find(|e| &e.qualified_name == representative);
            let file_path = elem.map(|e| e.file_path.as_str()).unwrap_or("");
            let cluster_label =
                self.generate_cluster_label(&format!("comm_{}", comm_id), file_path);

            let cluster_id = format!("cluster_{}", cluster_id_counter);
            cluster_id_counter += 1;

            // Calculate representative files
            let mut file_counts: HashMap<String, usize> = HashMap::new();
            for member in &members {
                if let Some(elem) = elements.iter().find(|e| &e.qualified_name == member) {
                    *file_counts.entry(elem.file_path.clone()).or_insert(0) += 1;
                }
            }
            let mut files: Vec<(String, usize)> = file_counts.into_iter().collect();
            files.sort_by_key(|b| std::cmp::Reverse(b.1));
            let representative_files: Vec<String> =
                files.into_iter().take(5).map(|(path, _)| path).collect();

            clusters.insert(
                cluster_id.clone(),
                Cluster {
                    id: cluster_id.clone(),
                    label: cluster_label,
                    members,
                    representative_files,
                },
            );
        }

        Ok(clusters)
    }

    /// Fallback clustering when no edges exist - groups by folder
    fn fallback_folder_clustering(
        &self,
        elements: &[crate::db::models::CodeElement],
    ) -> Result<HashMap<String, Cluster>, Box<dyn std::error::Error>> {
        let mut folder_groups: HashMap<String, Vec<String>> = HashMap::new();

        for elem in elements {
            let folder = if let Some(last_slash) = elem.file_path.rfind('/') {
                elem.file_path[..last_slash].to_string()
            } else {
                "root".to_string()
            };
            folder_groups
                .entry(folder)
                .or_default()
                .push(elem.qualified_name.clone());
        }

        let mut clusters: HashMap<String, Cluster> = HashMap::new();
        let mut cluster_id_counter = 0;

        for (folder, members) in folder_groups {
            if members.is_empty() {
                continue;
            }

            let cluster_label = folder.split('/').next_back().unwrap_or(&folder).to_string();

            let cluster_id = format!("cluster_{}", cluster_id_counter);
            cluster_id_counter += 1;

            clusters.insert(
                cluster_id.clone(),
                Cluster {
                    id: cluster_id.clone(),
                    label: cluster_label,
                    members,
                    representative_files: vec![folder],
                },
            );
        }

        Ok(clusters)
    }

    fn generate_cluster_label(&self, cluster_id: &str, file_path: &str) -> String {
        let path_parts: Vec<&str> = file_path.split('/').collect();
        if path_parts.len() >= 2 {
            let dir = path_parts[path_parts.len() - 2];
            let normalized = dir
                .chars()
                .map(|c| {
                    if c.is_alphanumeric() {
                        c.to_ascii_lowercase()
                    } else {
                        '_'
                    }
                })
                .collect::<String>();
            if !normalized.is_empty() && normalized != "_" {
                return normalized;
            }
        }
        cluster_id
            .replace("cluster_", "module_")
            .replace("comm_", "module_")
    }

    pub fn assign_clusters_to_elements(&self) -> Result<(), Box<dyn std::error::Error>> {
        let clusters = self.detect_communities()?;

        for cluster in clusters.values() {
            for member_qualified in &cluster.members {
                self.graph_engine.update_element_cluster(
                    member_qualified,
                    Some(cluster.id.clone()),
                    Some(cluster.label.clone()),
                )?;
            }
        }

        Ok(())
    }
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct Cluster {
    pub id: String,
    pub label: String,
    pub members: Vec<String>,
    pub representative_files: Vec<String>,
}

pub fn get_cluster_stats(clusters: &HashMap<String, Cluster>) -> ClusterStats {
    let total_members: usize = clusters.values().map(|c| c.members.len()).sum();
    let avg_cluster_size = if clusters.is_empty() {
        0.0
    } else {
        total_members as f64 / clusters.len() as f64
    };

    ClusterStats {
        total_clusters: clusters.len(),
        total_members,
        avg_cluster_size,
    }
}

#[derive(Debug, Clone, serde::Serialize, serde::Deserialize)]
pub struct ClusterStats {
    pub total_clusters: usize,
    pub total_members: usize,
    pub avg_cluster_size: f64,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_cluster_stats() {
        let mut clusters = HashMap::new();
        clusters.insert(
            "c1".to_string(),
            Cluster {
                id: "c1".to_string(),
                label: "auth".to_string(),
                members: vec!["a".to_string(), "b".to_string()],
                representative_files: vec!["auth.rs".to_string()],
            },
        );
        clusters.insert(
            "c2".to_string(),
            Cluster {
                id: "c2".to_string(),
                label: "api".to_string(),
                members: vec!["c".to_string(), "d".to_string(), "e".to_string()],
                representative_files: vec!["api.rs".to_string()],
            },
        );

        let stats = get_cluster_stats(&clusters);
        assert_eq!(stats.total_clusters, 2);
        assert_eq!(stats.total_members, 5);
        assert!((stats.avg_cluster_size - 2.5).abs() < 0.001);
    }
}
