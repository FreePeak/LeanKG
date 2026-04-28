use crate::db::schema::CozoDb;
use crate::graph::GraphEngine;
use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Pre-calculated node layout for efficient graph rendering
/// This module provides server-side layout computation to offload
/// expensive force-directed layout calculations from the browser.

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct NodeLayout {
    pub node_id: String,
    pub x: f64,
    pub y: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ClusterLayout {
    pub cluster_id: String,
    pub centroid_x: f64,
    pub centroid_y: f64,
    pub radius: f64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PrecalculatedLayout {
    pub nodes: Vec<NodeLayout>,
    pub clusters: Vec<ClusterLayout>,
    pub bounds: LayoutBounds,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LayoutBounds {
    pub min_x: f64,
    pub max_x: f64,
    pub min_y: f64,
    pub max_y: f64,
}

pub struct LayoutEngine {
    graph_engine: GraphEngine,
}

impl LayoutEngine {
    pub fn new(db: &CozoDb) -> Self {
        Self {
            graph_engine: GraphEngine::new(db.clone()),
        }
    }

    /// Calculate pre-computed layout using Fruchterman-Reingold algorithm
    /// This is a simplified force-directed layout that can run on the server
    pub fn calculate_layout(
        &self,
        iterations: usize,
        width: f64,
        height: f64,
    ) -> Result<PrecalculatedLayout, Box<dyn std::error::Error + Send + Sync + 'static>> {
        let elements = self.graph_engine.all_elements().map_err(|e| {
            Box::new(std::io::Error::other(e.to_string()))
                as Box<dyn std::error::Error + Send + Sync>
        })?;
        let relationships = self.graph_engine.all_relationships().map_err(|e| {
            Box::new(std::io::Error::other(e.to_string()))
                as Box<dyn std::error::Error + Send + Sync>
        })?;

        if elements.is_empty() {
            return Ok(PrecalculatedLayout {
                nodes: Vec::new(),
                clusters: Vec::new(),
                bounds: LayoutBounds {
                    min_x: 0.0,
                    max_x: width,
                    min_y: 0.0,
                    max_y: height,
                },
            });
        }

        // Build adjacency for layout
        let mut adjacency: HashMap<String, Vec<String>> = HashMap::new();
        for elem in &elements {
            adjacency.entry(elem.qualified_name.clone()).or_default();
        }

        for rel in &relationships {
            if rel.rel_type == "calls" || rel.rel_type == "imports" || rel.rel_type == "contains" {
                adjacency
                    .entry(rel.source_qualified.clone())
                    .or_default()
                    .push(rel.target_qualified.clone());
                adjacency
                    .entry(rel.target_qualified.clone())
                    .or_default()
                    .push(rel.source_qualified.clone());
            }
        }

        let node_ids: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
        let n = node_ids.len();

        // Initialize random positions
        let mut positions: HashMap<String, (f64, f64)> = HashMap::new();
        let area = width * height;
        let k = (area / n as f64).sqrt().max(10.0);

        for node_id in &node_ids {
            positions.insert(
                node_id.clone(),
                ((rand_simple() * width), (rand_simple() * height)),
            );
        }

        // Fruchterman-Reingold iterations
        let temperature = width.min(height) / 10.0;
        let cooling: f64 = 0.95;

        for iter in 0..iterations {
            let t = temperature * cooling.powi(iter as i32);

            // Calculate repulsive forces between all node pairs
            let mut displacements: HashMap<String, (f64, f64)> =
                node_ids.iter().map(|id| (id.clone(), (0.0, 0.0))).collect();

            for i in 0..n {
                for j in (i + 1)..n {
                    let (x1, y1) = positions[&node_ids[i]];
                    let (x2, y2) = positions[&node_ids[j]];

                    let dx = x1 - x2;
                    let dy = y1 - y2;
                    let dist = (dx * dx + dy * dy).sqrt().max(0.01);

                    // Repulsive force
                    let rep_force = k * k / dist;
                    let fx = (dx / dist) * rep_force;
                    let fy = (dy / dist) * rep_force;

                    *displacements.get_mut(&node_ids[i]).unwrap() = (
                        displacements[&node_ids[i]].0 + fx,
                        displacements[&node_ids[i]].1 + fy,
                    );
                    *displacements.get_mut(&node_ids[j]).unwrap() = (
                        displacements[&node_ids[j]].0 - fx,
                        displacements[&node_ids[j]].1 - fy,
                    );
                }
            }

            // Calculate attractive forces along edges
            for rel in &relationships {
                if rel.rel_type == "calls"
                    || rel.rel_type == "imports"
                    || rel.rel_type == "contains"
                {
                    if let (Some(&(x1, y1)), Some(&(x2, y2))) = (
                        positions.get(&rel.source_qualified),
                        positions.get(&rel.target_qualified),
                    ) {
                        let dx = x1 - x2;
                        let dy = y1 - y2;
                        let dist = (dx * dx + dy * dy).sqrt().max(0.01);

                        // Attractive force
                        let att_force = dist * dist / k;
                        let fx = (dx / dist) * att_force;
                        let fy = (dy / dist) * att_force;

                        if let Some(d) = displacements.get_mut(&rel.source_qualified) {
                            d.0 -= fx;
                            d.1 -= fy;
                        }
                        if let Some(d) = displacements.get_mut(&rel.target_qualified) {
                            d.0 += fx;
                            d.1 += fy;
                        }
                    }
                }
            }

            // Apply displacements with temperature limit
            for node_id in &node_ids {
                if let (Some(&(x, y)), Some((dx, dy))) =
                    (positions.get(node_id), displacements.get(node_id))
                {
                    let dist = (dx * dx + dy * dy).sqrt().max(0.01);
                    let limited_dx = dx * t.min(dist) / dist;
                    let limited_dy = dy * t.min(dist) / dist;

                    positions.insert(
                        node_id.clone(),
                        (
                            (x + limited_dx).max(0.0).min(width),
                            (y + limited_dy).max(0.0).min(height),
                        ),
                    );
                }
            }
        }

        // Build node layout
        let nodes: Vec<NodeLayout> = positions
            .into_iter()
            .map(|(node_id, (x, y))| NodeLayout { node_id, x, y })
            .collect();

        // Calculate bounds
        let xs: Vec<f64> = nodes.iter().map(|n| n.x).collect();
        let ys: Vec<f64> = nodes.iter().map(|n| n.y).collect();
        let bounds = LayoutBounds {
            min_x: xs.iter().cloned().fold(f64::INFINITY, f64::min),
            max_x: xs.iter().cloned().fold(f64::NEG_INFINITY, f64::max),
            min_y: ys.iter().cloned().fold(f64::INFINITY, f64::min),
            max_y: ys.iter().cloned().fold(f64::NEG_INFINITY, f64::max),
        };

        Ok(PrecalculatedLayout {
            nodes,
            clusters: Vec::new(), // Cluster layout can be computed separately
            bounds,
        })
    }
}

/// Simple deterministic pseudo-random number generator
fn rand_simple() -> f64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .subsec_nanos();
    (nanos as f64 % 1000.0) / 1000.0
}
