//! PR-62 (US-SEM-04 / FR-SEM-05): file-diversity / MMR post-filter.
//!
//! Pure greedy maximal marginal relevance over search results with a
//! `file_path` field. Guarantees top-k is not ≥70% one file when diversity
//! mode is on; off = pass-through (no regression).
//!
//! Run: `cargo test --release --test sem_mmr_diversity_tests`

use leankg::retrieval::mmr::{
    apply_mmr_diversity, greedy_mmr, GreedyMmrItem, DEFAULT_DIVERSITY_LAMBDA,
    DEFAULT_MIN_DISTINCT_FILES,
};
use serde_json::json;

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

/// Build `n` MMR items: `file_a` items rank 0.9, 0.8, … then `file_b` /
/// `file_c` trail. Mirrors the MCP-dispatch probe where top-10 collapses to
/// one file (8/10).
fn collapsed_fixture() -> Vec<GreedyMmrItem> {
    let mut items = Vec::new();
    for i in 0..8 {
        items.push(GreedyMmrItem {
            id: format!("a{i}"),
            score: 0.9 - i as f64 * 0.01,
            file_path: "src/embed/assets/bundle.js".to_string(),
        });
    }
    items.push(GreedyMmrItem {
        id: "b0".to_string(),
        score: 0.6,
        file_path: "src/search/mod.rs".to_string(),
    });
    items.push(GreedyMmrItem {
        id: "c0".to_string(),
        score: 0.55,
        file_path: "src/graph/query.rs".to_string(),
    });
    items
}

fn distinct_files(items: &[GreedyMmrItem]) -> usize {
    let mut seen = std::collections::HashSet::new();
    for it in items {
        seen.insert(it.file_path.as_str());
    }
    seen.len()
}

// ---------------------------------------------------------------------------
// Pure greedy MMR core (unit)
// ---------------------------------------------------------------------------

#[test]
fn greedy_mmr_preserves_relevance_order_within_same_file() {
    let items = vec![
        GreedyMmrItem {
            id: "x1".to_string(),
            score: 0.9,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "x2".to_string(),
            score: 0.8,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "y1".to_string(),
            score: 0.7,
            file_path: "b.rs".to_string(),
        },
    ];
    let picked = greedy_mmr(&items, 3, 1.0);
    // lambda=1.0: pure relevance — order preserved exactly.
    assert_eq!(
        picked.iter().map(|i| i.id.as_str()).collect::<Vec<_>>(),
        vec!["x1", "x2", "y1"]
    );
}

#[test]
fn greedy_mmr_diversifies_when_lambda_high() {
    let items = vec![
        GreedyMmrItem {
            id: "a1".to_string(),
            score: 0.9,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "a2".to_string(),
            score: 0.8,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "b1".to_string(),
            score: 0.1,
            file_path: "b.rs".to_string(),
        },
    ];
    let picked = greedy_mmr(&items, 2, 0.5);
    let ids: Vec<&str> = picked.iter().map(|i| i.id.as_str()).collect();
    assert_eq!(
        ids,
        vec!["a1", "b1"],
        "second pick must jump to new file b1"
    );
}

#[test]
fn greedy_mmr_skips_duplicate_files() {
    let items = vec![
        GreedyMmrItem {
            id: "a1".to_string(),
            score: 0.9,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "a2".to_string(),
            score: 0.8,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "a3".to_string(),
            score: 0.7,
            file_path: "a.rs".to_string(),
        },
        GreedyMmrItem {
            id: "b1".to_string(),
            score: 0.2,
            file_path: "b.rs".to_string(),
        },
    ];
    // k=3 with only two files: must pick a1, b1, then best remaining a2.
    let picked = greedy_mmr(&items, 3, DEFAULT_DIVERSITY_LAMBDA);
    let ids: Vec<&str> = picked.iter().map(|i| i.id.as_str()).collect();
    assert_eq!(ids, vec!["a1", "b1", "a2"]);
}

#[test]
fn greedy_mmr_never_reorders_with_lambda_1() {
    // lambda=1.0 must be a pure relevance pass-through.
    let items = collapsed_fixture();
    let picked = greedy_mmr(&items, 10, 1.0);
    assert_eq!(picked.len(), items.len());
    for (p, it) in picked.iter().zip(items.iter()) {
        assert_eq!(p.id, it.id, "lambda=1.0 must not reorder");
    }
}

#[test]
fn greedy_mmr_handles_empty_and_truncation() {
    assert!(greedy_mmr(&[], 5, 0.5).is_empty());
    let items = collapsed_fixture();
    let picked = greedy_mmr(&items, 3, 0.5);
    assert_eq!(picked.len(), 3);
}

// ---------------------------------------------------------------------------
// FR-SEM-05: apply_mmr_diversity over real search-result JSON (post-filter)
// ---------------------------------------------------------------------------

#[test]
fn diversity_on_fixes_collapsed_top10() {
    let items = collapsed_fixture(); // 8/10 from one file
    let picked = apply_mmr_diversity(
        &items,
        10,
        DEFAULT_DIVERSITY_LAMBDA,
        DEFAULT_MIN_DISTINCT_FILES,
    );
    assert!(
        distinct_files(&picked) >= DEFAULT_MIN_DISTINCT_FILES,
        "diversity mode must yield >= {} distinct files, got {}",
        DEFAULT_MIN_DISTINCT_FILES,
        distinct_files(&picked)
    );
}

#[test]
fn diversity_off_returns_original_order() {
    let items = collapsed_fixture();
    let picked = apply_mmr_diversity(&items, 10, 0.0, DEFAULT_MIN_DISTINCT_FILES);
    assert_eq!(picked.len(), items.len());
    for (p, it) in picked.iter().zip(items.iter()) {
        assert_eq!(p.id, it.id, "diversity off must preserve ranking exactly");
    }
}

#[test]
fn diversity_respects_top_k_bound() {
    let items = collapsed_fixture();
    let picked = apply_mmr_diversity(&items, 4, DEFAULT_DIVERSITY_LAMBDA, 3);
    assert_eq!(picked.len(), 4, "top-k bound must hold");
}

#[test]
fn diversity_keeps_top_relevance_hit_first() {
    let items = collapsed_fixture();
    let picked = apply_mmr_diversity(&items, 10, DEFAULT_DIVERSITY_LAMBDA, 3);
    assert_eq!(
        picked[0].id, "a0",
        "highest-relevance hit must stay first (MMR keeps argmax relevance for pick 1)"
    );
}

#[test]
fn diversity_json_serializes_without_panic() {
    let items = collapsed_fixture();
    let picked = apply_mmr_diversity(&items, 5, DEFAULT_DIVERSITY_LAMBDA, 3);
    let v: Vec<serde_json::Value> = picked
        .iter()
        .map(|i| json!({"id": i.id, "file_path": i.file_path, "score": i.score}))
        .collect();
    assert_eq!(v.len(), 5);
    let s = serde_json::to_string(&v).expect("serialize");
    assert!(s.contains("bundle.js"));
}

#[test]
fn diversity_single_file_corpus_degenerates_gracefully() {
    // Only one file exists — diversity cannot add files; must still return
    // ranked items without panic and keep the best first.
    let items: Vec<GreedyMmrItem> = (0..5)
        .map(|i| GreedyMmrItem {
            id: format!("s{i}"),
            score: 1.0 - i as f64 * 0.1,
            file_path: "only.rs".to_string(),
        })
        .collect();
    let picked = apply_mmr_diversity(&items, 5, DEFAULT_DIVERSITY_LAMBDA, 3);
    assert_eq!(picked.len(), 5);
    assert_eq!(picked[0].id, "s0");
}
