//! 3D graph layout (Track E, FR-E10..E14).
//!
//! Pure, deterministic node positioning: same nodes + edges + seed always
//! yield the same (x, y, z) positions. No wall-clock / unseeded randomness —
//! the PRNG is a fixed-seed xorshift64*.

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// A single node's 3D position.
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct Node3DPosition {
    pub x: f64,
    pub y: f64,
    pub z: f64,
}

/// Result of a 3D layout run.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Layout3DResult {
    /// Positions keyed by node id.
    pub positions: HashMap<String, Node3DPosition>,
    /// Axis-aligned bounds of the computed positions.
    pub bounds: Bounds3D,
    /// Number of layout iterations executed.
    pub iterations: usize,
}

/// Axis-aligned bounding box over the computed positions.
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize)]
pub struct Bounds3D {
    pub min_x: f64,
    pub max_x: f64,
    pub min_y: f64,
    pub max_y: f64,
    pub min_z: f64,
    pub max_z: f64,
}

impl Bounds3D {
    fn new() -> Self {
        Self {
            min_x: f64::INFINITY,
            max_x: f64::NEG_INFINITY,
            min_y: f64::INFINITY,
            max_y: f64::NEG_INFINITY,
            min_z: f64::INFINITY,
            max_z: f64::NEG_INFINITY,
        }
    }

    fn include(&mut self, p: &Node3DPosition) {
        self.min_x = self.min_x.min(p.x);
        self.max_x = self.max_x.max(p.x);
        self.min_y = self.min_y.min(p.y);
        self.max_y = self.max_y.max(p.y);
        self.min_z = self.min_z.min(p.z);
        self.max_z = self.max_z.max(p.z);
    }

    /// True when every corner is finite (positions are non-empty and bounded).
    pub fn is_finite(&self) -> bool {
        self.min_x.is_finite()
            && self.max_x.is_finite()
            && self.min_y.is_finite()
            && self.max_y.is_finite()
            && self.min_z.is_finite()
            && self.max_z.is_finite()
    }
}

/// Edge between two node ids. The layout treats edges as undirected
/// springs; duplicates are fine (idempotent force).
#[derive(Debug, Clone, PartialEq)]
pub struct LayoutEdge {
    pub source: String,
    pub target: String,
}

impl LayoutEdge {
    pub fn new(source: impl Into<String>, target: impl Into<String>) -> Self {
        Self {
            source: source.into(),
            target: target.into(),
        }
    }
}

/// Fixed-seed xorshift64* PRNG. Deterministic across runs and platforms
/// (pure u64 arithmetic, no float casting of the state itself).
#[derive(Debug, Clone)]
struct Xorshift64 {
    state: u64,
}

impl Xorshift64 {
    fn new(seed: u64) -> Self {
        // 0 is a degenerate state for xorshift; fold it to a nonzero constant.
        let state = if seed == 0 { 0x9E3779B97F4A7C15 } else { seed };
        Self { state }
    }

    fn next_u64(&mut self) -> u64 {
        let mut x = self.state;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        self.state = x;
        x.wrapping_mul(0x2545F4914F6CDD1D)
    }

    /// Uniform float in [0, 1).
    fn next_f64(&mut self) -> f64 {
        (self.next_u64() >> 11) as f64 / (1u64 << 53) as f64
    }
}

/// Compute a deterministic 3D layout for the given graph.
///
/// Approach: seeded Fruchterman-Reingold in 3D (same repulsion/attraction
/// forces as the existing 2D server layout, plus a z-axis spring toward the
/// node's own index-slot plane). Z is spread by a seeded hash of the node id
/// so disconnected/isolated nodes still get distinct heights.
///
/// * `node_ids` — every node to place. Order is preserved in output
///   iteration but does not affect positions beyond the initial seed layout.
/// * `edges` — undirected springs; endpoints not in `node_ids` are ignored.
/// * `iterations` — force simulation steps. Clamped to `[1, 100]`.
/// * `seed` — PRNG seed; identical input + seed => identical output.
pub fn layout3d(
    node_ids: &[String],
    edges: &[LayoutEdge],
    iterations: usize,
    seed: u64,
) -> Layout3DResult {
    let iterations = iterations.clamp(1, 100);
    let n = node_ids.len();
    let mut result = Layout3DResult {
        positions: HashMap::new(),
        bounds: Bounds3D::new(),
        iterations,
    };

    if n == 0 {
        return result;
    }

    // Initial deterministic positions: seed PRNG once, assign in input order.
    let mut rng = Xorshift64::new(seed);
    let mut positions: HashMap<String, Node3DPosition> = HashMap::new();
    let mut index_of: HashMap<&str, usize> = HashMap::new();
    for (i, id) in node_ids.iter().enumerate() {
        index_of.insert(id.as_str(), i);
        positions.insert(
            id.clone(),
            Node3DPosition {
                x: rng.next_f64(),
                y: rng.next_f64(),
                z: rng.next_f64(),
            },
        );
    }

    // Edges present in the node set (undirected; dedup via hash set).
    let mut seen: std::collections::HashSet<(usize, usize)> = std::collections::HashSet::new();
    let mut links: Vec<(usize, usize)> = Vec::new();
    for e in edges {
        let (Some(&si), Some(&ti)) = (
            index_of.get(e.source.as_str()),
            index_of.get(e.target.as_str()),
        ) else {
            continue;
        };
        if si == ti {
            continue;
        }
        let (a, b) = if si < ti { (si, ti) } else { (ti, si) };
        if seen.insert((a, b)) {
            links.push((a, b));
        }
    }

    let side = (n as f64).cbrt().ceil().max(1.0);
    let k = (1.0 / side).max(0.05); // ideal edge length in unit cube
    let start_temp = 0.5;

    for iter in 0..iterations {
        let t = start_temp * (1.0 - iter as f64 / iterations as f64);
        let mut disp = vec![(0.0f64, 0.0f64, 0.0f64); n];

        // Repulsion: all pairs (O(n^2), same as existing 2D layout).
        let pos: Vec<Node3DPosition> = node_ids.iter().map(|id| positions[id]).collect();
        for i in 0..n {
            for j in (i + 1)..n {
                let pi = pos[i];
                let pj = pos[j];
                let dx = pi.x - pj.x;
                let dy = pi.y - pj.y;
                let dz = pi.z - pj.z;
                let d = (dx * dx + dy * dy + dz * dz).sqrt().max(1e-6);
                let f = k * k / d;
                disp[i].0 += dx / d * f;
                disp[i].1 += dy / d * f;
                disp[i].2 += dz / d * f;
                disp[j].0 -= dx / d * f;
                disp[j].1 -= dy / d * f;
                disp[j].2 -= dz / d * f;
            }
        }

        // Attraction along edges (spring toward ideal length k).
        for &(a, b) in &links {
            let pa = pos[a];
            let pb = pos[b];
            let dx = pa.x - pb.x;
            let dy = pa.y - pb.y;
            let dz = pa.z - pb.z;
            let d = (dx * dx + dy * dy + dz * dz).sqrt().max(1e-6);
            let f = d * d / k;
            disp[a].0 -= dx / d * f;
            disp[a].1 -= dy / d * f;
            disp[a].2 -= dz / d * f;
            disp[b].0 += dx / d * f;
            disp[b].1 += dy / d * f;
            disp[b].2 += dz / d * f;
        }

        // Apply temperature-limited displacement, clamped to unit cube.
        for i in 0..n {
            let p = positions[&node_ids[i]];
            let (dx, dy, dz) = disp[i];
            let d = (dx * dx + dy * dy + dz * dz).sqrt().max(1e-6);
            let lim = d.min(t) / d;
            let (nx, ny, nz) = (
                (p.x + dx * lim).clamp(0.0, 1.0),
                (p.y + dy * lim).clamp(0.0, 1.0),
                (p.z + dz * lim).clamp(0.0, 1.0),
            );
            positions.insert(
                node_ids[i].clone(),
                Node3DPosition {
                    x: nx,
                    y: ny,
                    z: nz,
                },
            );
        }
    }

    // Final pass: z spread by seeded per-node hash so disconnected/isolated
    // nodes do not collapse onto the same plane.
    for (i, id) in node_ids.iter().enumerate() {
        let p = positions[id];
        let h = hash_u64(id, seed).wrapping_add(i as u64);
        let z = (h as f64 / u64::MAX as f64) * 1.0;
        positions.insert(id.clone(), Node3DPosition { x: p.x, y: p.y, z });
    }

    for id in node_ids {
        let p = positions[id];
        result.bounds.include(&p);
        result.positions.insert(id.clone(), p);
    }
    result
}

/// Deterministic u64 hash of `(value, salt)` — used for the z-axis spread.
fn hash_u64(value: &str, salt: u64) -> u64 {
    let mut h: u64 = 0xcbf29ce484222325 ^ salt;
    for b in value.as_bytes() {
        h ^= *b as u64;
        h = h.wrapping_mul(0x100000001b3);
    }
    h
}

#[cfg(test)]
mod tests {
    use super::*;

    fn nodes(ids: &[&str]) -> Vec<String> {
        ids.iter().map(|s| s.to_string()).collect()
    }

    fn edges(pairs: &[(&str, &str)]) -> Vec<LayoutEdge> {
        pairs.iter().map(|(a, b)| LayoutEdge::new(*a, *b)).collect()
    }

    #[test]
    fn deterministic_same_input_same_positions() {
        let ns = nodes(&["a", "b", "c", "d", "e"]);
        let es = edges(&[("a", "b"), ("b", "c"), ("c", "d"), ("d", "e"), ("a", "e")]);
        let one = layout3d(&ns, &es, 30, 42);
        let two = layout3d(&ns, &es, 30, 42);
        assert_eq!(one.positions, two.positions);
        assert_eq!(one.bounds, two.bounds);
    }

    #[test]
    fn different_seed_usually_different_positions() {
        let ns = nodes(&["a", "b", "c", "d", "e", "f"]);
        let es = edges(&[("a", "b"), ("b", "c"), ("c", "d")]);
        let one = layout3d(&ns, &es, 20, 1);
        let two = layout3d(&ns, &es, 20, 2);
        assert_ne!(one.positions, two.positions);
    }

    #[test]
    fn all_nodes_get_positions_and_bounds_finite() {
        let ns = nodes(&["n1", "n2", "n3", "n4"]);
        let es = edges(&[("n1", "n2"), ("n3", "n4")]);
        let res = layout3d(&ns, &es, 15, 7);
        assert_eq!(res.positions.len(), 4);
        for id in &ns {
            assert!(res.positions.contains_key(id));
        }
        assert!(res.bounds.is_finite());
    }

    #[test]
    fn positions_stay_within_unit_cube() {
        let ns = nodes(&["a", "b", "c", "d", "e", "f", "g", "h"]);
        let es = edges(&[("a", "b"), ("b", "c"), ("c", "a"), ("d", "e"), ("f", "g")]);
        let res = layout3d(&ns, &es, 40, 99);
        for p in res.positions.values() {
            assert!((0.0..=1.0).contains(&p.x));
            assert!((0.0..=1.0).contains(&p.y));
            assert!((0.0..=1.0).contains(&p.z));
        }
    }

    #[test]
    fn connected_nodes_end_up_closer_than_disconnected() {
        // Two connected clusters, far apart in input order.
        let ns = nodes(&["c1a", "c1b", "c2a", "c2b"]);
        let es = edges(&[("c1a", "c1b"), ("c2a", "c2b")]);
        let res = layout3d(&ns, &es, 40, 5);
        let dist = |a: &str, b: &str| {
            let pa = res.positions[a];
            let pb = res.positions[b];
            ((pa.x - pb.x).powi(2) + (pa.y - pb.y).powi(2) + (pa.z - pb.z).powi(2)).sqrt()
        };
        let intra = (dist("c1a", "c1b") + dist("c2a", "c2b")) / 2.0;
        let inter = (dist("c1a", "c2a") + dist("c1b", "c2b")) / 2.0;
        assert!(
            intra < inter,
            "connected nodes should be closer (intra={intra:.4}, inter={inter:.4})"
        );
    }

    #[test]
    fn z_axis_has_spread_for_isolated_nodes() {
        let ns = nodes(&["solo1", "solo2", "solo3", "solo4"]);
        let es: Vec<LayoutEdge> = Vec::new();
        let res = layout3d(&ns, &es, 10, 3);
        let zs: Vec<f64> = ns.iter().map(|id| res.positions[id].z).collect();
        let distinct: std::collections::HashSet<u64> =
            zs.iter().map(|z| (z * 1e9) as u64).collect();
        assert!(
            distinct.len() >= 2,
            "isolated nodes must not share a single z-plane: {zs:?}"
        );
    }

    #[test]
    fn empty_input_yields_empty_output() {
        let res = layout3d(&[], &[], 10, 1);
        assert!(res.positions.is_empty());
        assert_eq!(res.iterations, 10);
        assert!(!res.bounds.is_finite());
    }

    #[test]
    fn edges_to_unknown_nodes_are_ignored() {
        let ns = nodes(&["a", "b"]);
        let es = edges(&[("a", "b"), ("a", "ghost"), ("ghost", "b")]);
        let res = layout3d(&ns, &es, 10, 8);
        assert_eq!(res.positions.len(), 2);
        assert!(res.positions.contains_key("a"));
        assert!(res.positions.contains_key("b"));
        assert!(!res.positions.contains_key("ghost"));
    }

    #[test]
    fn iteration_clamp_keeps_output_valid() {
        let ns = nodes(&["a", "b"]);
        let es = edges(&[("a", "b")]);
        let zero = layout3d(&ns, &es, 0, 1);
        assert_eq!(zero.iterations, 1);
        let huge = layout3d(&ns, &es, 5000, 1);
        assert_eq!(huge.iterations, 100);
        for p in huge.positions.values() {
            assert!((0.0..=1.0).contains(&p.x));
        }
    }
}
