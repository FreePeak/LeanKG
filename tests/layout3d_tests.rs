//! FR-E10..E14 — Track E 3D layout API integration tests.
//!
//! Exercises `leankg::graph::layout3d` against a real (temp-file) database:
//! positions must be deterministic across runs, inside the unit cube, and
//! bounded for every indexed element.

use leankg::db::backend::init_db;
use leankg::graph::GraphEngine;
use tempfile::TempDir;

fn with_test_db<F>(callback: F)
where
    F: FnOnce(&GraphEngine, &TempDir),
{
    let tmp = TempDir::new().unwrap();
    let db_path = tmp.path().join("test.db");
    let db = init_db(db_path.as_path()).unwrap();
    let graph = GraphEngine::new(db.clone());
    callback(&graph, &tmp);
}

fn seed_graph(graph: &GraphEngine) {
    let elements = vec![
        leankg::db::models::CodeElement {
            qualified_name: "src/a.rs::a".into(),
            name: "a".into(),
            file_path: "src/a.rs".into(),
            element_type: "function".into(),
            ..Default::default()
        },
        leankg::db::models::CodeElement {
            qualified_name: "src/b.rs::b".into(),
            name: "b".into(),
            file_path: "src/b.rs".into(),
            element_type: "function".into(),
            ..Default::default()
        },
        leankg::db::models::CodeElement {
            qualified_name: "src/c.rs::c".into(),
            name: "c".into(),
            file_path: "src/c.rs".into(),
            element_type: "function".into(),
            ..Default::default()
        },
        leankg::db::models::CodeElement {
            qualified_name: "src/d.rs::d".into(),
            name: "d".into(),
            file_path: "src/d.rs".into(),
            element_type: "function".into(),
            ..Default::default()
        },
    ];
    let relationships = vec![leankg::db::models::Relationship {
        source_qualified: "src/a.rs::a".into(),
        target_qualified: "src/b.rs::b".into(),
        rel_type: "calls".into(),
        ..Default::default()
    }];
    graph.insert_elements(&elements).unwrap();
    graph.insert_relationships(&relationships).unwrap();
}

#[test]
fn db_backed_layout_is_deterministic_and_bounded() {
    with_test_db(|graph, _tmp| {
        seed_graph(graph);

        let elements = graph.all_elements().unwrap();
        let relationships = graph.all_relationships().unwrap();
        let node_ids: Vec<String> = elements.iter().map(|e| e.qualified_name.clone()).collect();
        let edges: Vec<leankg::graph::LayoutEdge> = relationships
            .iter()
            .map(|r| {
                leankg::graph::LayoutEdge::new(
                    r.source_qualified.clone(),
                    r.target_qualified.clone(),
                )
            })
            .collect();

        assert_eq!(node_ids.len(), 4);
        assert_eq!(edges.len(), 1);

        let one = leankg::graph::layout3d(&node_ids, &edges, 30, 42);
        let two = leankg::graph::layout3d(&node_ids, &edges, 30, 42);
        assert_eq!(
            one.positions, two.positions,
            "same input must yield same 3D positions"
        );
        assert_eq!(one.positions.len(), 4, "every element gets a position");

        for id in &node_ids {
            let p = one.positions[id];
            assert!((0.0..=1.0).contains(&p.x), "{id} x out of cube: {}", p.x);
            assert!((0.0..=1.0).contains(&p.y), "{id} y out of cube: {}", p.y);
            assert!((0.0..=1.0).contains(&p.z), "{id} z out of cube: {}", p.z);
        }
        assert!(one.bounds.is_finite());
        assert!(one.bounds.min_x <= one.bounds.max_x);
        assert!(one.bounds.min_y <= one.bounds.max_y);
        assert!(one.bounds.min_z <= one.bounds.max_z);

        // Different seed must change positions (PRNG actually engaged).
        let three = leankg::graph::layout3d(&node_ids, &edges, 30, 7);
        assert_ne!(
            one.positions, three.positions,
            "different seeds must differ"
        );
    });
}

#[test]
fn db_backed_layout_survives_empty_graph() {
    with_test_db(|_graph, _tmp| {
        let node_ids: Vec<String> = Vec::new();
        let edges: Vec<leankg::graph::LayoutEdge> = Vec::new();
        let res = leankg::graph::layout3d(&node_ids, &edges, 20, 1);
        assert!(res.positions.is_empty());
    });
}
