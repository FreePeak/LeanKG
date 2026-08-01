//! PR-22 (US-SM-03 / US-SM-04, FR-SM-07..09): provenance fields on
//! durable agent-memory writes + typed kinds + hybrid RRF (k=60) search.

use leankg::search::{
    fuse_ranked_lists, SearchRankedHit, SearchRankedItem, DEFAULT_RRF_K, MIN_RRF_K,
};
use leankg::session::{
    classify_memory_kind, lesson_with_provenance, MemoryKind, MemoryProvenance, RecallStore,
};
use std::path::Path;

// ---------------------------------------------------------------------------
// FR-SM-08: typed agent-memory kinds
// ---------------------------------------------------------------------------

#[test]
fn memory_kind_classifies_preference() {
    assert_eq!(
        classify_memory_kind("prefer get_overview_context at session start"),
        MemoryKind::Preference
    );
    assert_eq!(
        classify_memory_kind("we prefer RocksDB over sqlite for the index"),
        MemoryKind::Preference
    );
}

#[test]
fn memory_kind_classifies_decision() {
    assert_eq!(
        classify_memory_kind("decision: use RRF k=60 for hybrid search"),
        MemoryKind::Decision
    );
    assert_eq!(
        classify_memory_kind("we decided to drop the npm distribution channel"),
        MemoryKind::Decision
    );
}

#[test]
fn memory_kind_classifies_standing_rule() {
    assert_eq!(
        classify_memory_kind("standing_rule: never pass host paths to the Docker MCP project arg"),
        MemoryKind::StandingRule
    );
    assert_eq!(
        classify_memory_kind("always use --release for rust builds"),
        MemoryKind::StandingRule
    );
}

#[test]
fn memory_kind_falls_back_to_preference() {
    // No decision/standing-rule/`prefer` signal → Preference (most common
    // agent-memory kind for durable lesson text).
    assert_eq!(
        classify_memory_kind("RocksDB survives 256GB SSD writes without mmap thrash"),
        MemoryKind::Preference
    );
}

#[test]
fn memory_kind_label_prefix_wins() {
    assert_eq!(
        classify_memory_kind("decision: prefer x over y"),
        MemoryKind::Decision
    );
}

// ---------------------------------------------------------------------------
// FR-SM-07: provenance fields on durable writes
// ---------------------------------------------------------------------------

#[test]
fn lesson_provenance_attached_and_round_trips_through_recall_store() {
    let tmp = tempfile::TempDir::new().unwrap();
    let store = RecallStore::new(tmp.path()).expect("recall store");

    let prov = MemoryProvenance {
        source_session_id: "sess-9f31".to_string(),
        node_id: Some("offload-007".to_string()),
        kind: MemoryKind::Decision,
        element_refs: vec!["src/search/rrf.rs::fuse_ranked_lists".to_string()],
        timestamp: Some(1754100000),
        tool_call: Some("search_memory_rrf".to_string()),
    };
    let lesson = lesson_with_provenance(
        "r-1",
        "report_query_outcome",
        9.0,
        "we decided to fuse with k=60",
        prov.clone(),
    );

    store.push_dedup(&lesson).expect("push");

    let loaded = store.load().expect("load");
    assert_eq!(loaded.len(), 1);
    let p = loaded[0].provenance.as_ref().expect("provenance present");
    assert_eq!(p.source_session_id, "sess-9f31");
    assert_eq!(p.node_id.as_deref(), Some("offload-007"));
    assert_eq!(p.kind, MemoryKind::Decision);
    assert_eq!(p.element_refs, prov.element_refs);
    assert_eq!(p.timestamp, Some(1754100000));
    assert_eq!(p.tool_call.as_deref(), Some("search_memory_rrf"));
    assert_eq!(loaded[0].kind(), MemoryKind::Decision);
}

#[test]
fn provenance_dedup_still_works_by_text() {
    let tmp = tempfile::TempDir::new().unwrap();
    let store = RecallStore::new(tmp.path()).expect("recall store");
    let prov = MemoryProvenance::new("sess-a", "offload-001", MemoryKind::Decision, Vec::new());
    let a = lesson_with_provenance("r-1", "LESSONS.md", 9.0, "same lesson text", prov.clone());
    let b = lesson_with_provenance("r-2", "knowledge", 8.0, "same lesson text", prov);
    store.push_dedup(&a).expect("push a");
    store.push_dedup(&b).expect("push b");
    let lessons = store.load().expect("load");
    assert_eq!(
        lessons.len(),
        1,
        "same text deduped regardless of provenance"
    );
    assert_eq!(lessons[0].source, "LESSONS.md", "first write wins");
}

#[test]
fn provenance_kind_derived_from_text_when_not_given() {
    let tmp = tempfile::TempDir::new().unwrap();
    let store = RecallStore::new(tmp.path()).expect("recall store");
    let lesson = lesson_with_provenance(
        "r-1",
        "diary",
        5.0,
        "decision: ship the layout API this week",
        MemoryProvenance::new("sess-x", "offload-003", MemoryKind::Preference, Vec::new()),
    );
    store.push_dedup(&lesson).expect("push");
    let loaded = store.load().expect("load");
    assert_eq!(
        loaded[0].provenance.as_ref().unwrap().kind,
        MemoryKind::Decision,
        "kind defaults to the classifier when caller passes Preference"
    );
}

#[test]
fn lesson_without_provenance_is_backfilled_on_push() {
    let tmp = tempfile::TempDir::new().unwrap();
    let store = RecallStore::new(tmp.path()).expect("recall store");
    let lesson = leankg::session::Lesson {
        id: "r-1".to_string(),
        source: "report_query_outcome".to_string(),
        rank: 9.0,
        text: "prefer get_overview_context at session start".to_string(),
        provenance: None,
    };
    store.push_dedup(&lesson).expect("push");
    let loaded = store.load().expect("load");
    let p = loaded[0]
        .provenance
        .as_ref()
        .expect("provenance backfilled");
    assert_eq!(p.source_session_id, "recall");
    assert!(p.timestamp.is_some());
    assert_eq!(p.kind, MemoryKind::Preference);
}

#[test]
fn legacy_recall_index_without_provenance_still_loads() {
    // A `recall_index.jsonl` written before PR-22 (no provenance key) must
    // still parse and receive a backfilled provenance.
    let tmp = tempfile::TempDir::new().unwrap();
    let dir = tmp.path().join(".leankg").join("sessions");
    std::fs::create_dir_all(&dir).unwrap();
    let legacy =
        "{\"id\":\"r-9\",\"source\":\"diary\",\"rank\":4.0,\"text\":\"validate_key is hot\"}\n";
    std::fs::write(dir.join("recall_index.jsonl"), legacy).unwrap();
    let store = RecallStore::new(tmp.path()).expect("recall store");
    let lessons = store.load().expect("load");
    assert_eq!(lessons.len(), 1);
    assert_eq!(lessons[0].id, "r-9");
    assert_eq!(
        lessons[0].provenance.as_ref().unwrap().kind,
        MemoryKind::Preference
    );
    assert_eq!(
        lessons[0].provenance.as_ref().unwrap().source_session_id,
        "recall"
    );
}

// ---------------------------------------------------------------------------
// FR-SM-09: RRF math — pure, deterministic, k=60
// ---------------------------------------------------------------------------

/// Fixture: two ranked lists from "session recall" and "graph search".
fn fixture_lists() -> (Vec<SearchRankedItem>, Vec<SearchRankedItem>) {
    let list_a = vec![
        SearchRankedItem {
            id: "decision-1".to_string(),
            score: 0.9,
        },
        SearchRankedItem {
            id: "preference-2".to_string(),
            score: 0.7,
        },
        SearchRankedItem {
            id: "milestone-3".to_string(),
            score: 0.5,
        },
    ];
    let list_b = vec![
        SearchRankedItem {
            id: "preference-2".to_string(),
            score: 0.85,
        },
        SearchRankedItem {
            id: "problem-4".to_string(),
            score: 0.6,
        },
    ];
    (list_a, list_b)
}

#[test]
fn rrf_fuses_two_lists_with_k60() {
    let (a, b) = fixture_lists();
    let fused = fuse_ranked_lists(&[a, b]);
    // RRF: doc at 1-indexed rank r contributes 1/(k+r). k=60 → rank1=1/61.
    // "preference-2" is rank 2 in list A + rank 1 in list B → 1/62 + 1/61,
    // the highest fused score.
    assert_eq!(fused[0].id, "preference-2");
    assert_eq!(
        fused[0].score,
        1.0 / 61.0 + 1.0 / 62.0,
        "RRF score = sum over lists of 1/(k+rank), k=60"
    );
    // Only in list A at rank 1 → 1/61.
    assert_eq!(fused[1].id, "decision-1");
    assert_eq!(fused[1].score, 1.0 / 61.0);
    // Only in list B at rank 2 → 1/62; ties with any 1/61-hit break
    // deterministically (id ascending → decision-1 first).
    assert_eq!(fused[2].id, "problem-4");
    assert_eq!(fused[2].score, 1.0 / 62.0);
    assert_eq!(fused[3].id, "milestone-3");
    assert_eq!(fused[3].score, 1.0 / 63.0);
    assert_eq!(fused.len(), 4);
}

#[test]
fn rrf_uses_k60_default_and_validation() {
    assert_eq!(DEFAULT_RRF_K, 60, "FR-SM-09 mandates k=60");
    assert!(MIN_RRF_K <= 60);
    // Score math for a doc present in both lists at rank 1.
    let a = vec![SearchRankedItem {
        id: "x".to_string(),
        score: 1.0,
    }];
    let b = vec![SearchRankedItem {
        id: "x".to_string(),
        score: 0.5,
    }];
    let fused = fuse_ranked_lists(&[a, b]);
    assert!((fused[0].score - (1.0 / 61.0 + 1.0 / 61.0)).abs() < 1e-12);
}

#[test]
fn rrf_deterministic_tie_break_by_id() {
    // Two docs each ranked #1 in exactly one list → equal 1/(k+1) scores.
    let a = vec![SearchRankedItem {
        id: "zeta".to_string(),
        score: 1.0,
    }];
    let b = vec![SearchRankedItem {
        id: "alpha".to_string(),
        score: 1.0,
    }];
    let f1 = fuse_ranked_lists(&[a.clone(), b.clone()]);
    let f2 = fuse_ranked_lists(&[a, b]);
    let ids1: Vec<&str> = f1.iter().map(|h| h.id.as_str()).collect();
    let ids2: Vec<&str> = f2.iter().map(|h| h.id.as_str()).collect();
    assert_eq!(ids1, ids2, "same inputs → same order");
    assert_eq!(f1[0].id, "alpha", "lexicographically smaller id first");
    assert_eq!(f1[0].score, f1[1].score);
}

#[test]
fn rrf_empty_and_singleton_lists() {
    assert!(fuse_ranked_lists(&[] as &[Vec<SearchRankedItem>]).is_empty());
    let only = vec![SearchRankedItem {
        id: "solo".to_string(),
        score: 0.5,
    }];
    let fused = fuse_ranked_lists(&[only]);
    assert_eq!(fused.len(), 1);
    assert_eq!(fused[0].id, "solo");
    assert_eq!(fused[0].score, 1.0 / 61.0);
}

#[test]
fn rrf_rank_one_contributes_one_over_k_plus_one() {
    let a = vec![SearchRankedItem {
        id: "first".to_string(),
        score: 1.0,
    }];
    let fused = fuse_ranked_lists(&[a]);
    assert!(
        (fused[0].score - 1.0 / 61.0).abs() < 1e-12,
        "k=60, rank=1 → 1/61"
    );
}

#[test]
fn rrf_duplicate_ids_within_one_list_keep_first_rank() {
    let a = vec![
        SearchRankedItem {
            id: "dup".to_string(),
            score: 1.0,
        },
        SearchRankedItem {
            id: "dup".to_string(),
            score: 0.9,
        },
        SearchRankedItem {
            id: "other".to_string(),
            score: 0.8,
        },
    ];
    let fused = fuse_ranked_lists(&[a]);
    assert_eq!(fused.len(), 2, "dup id counted once at its best rank");
    assert_eq!(fused[0].id, "dup");
    assert_eq!(fused[0].score, 1.0 / 61.0);
}

#[test]
fn rrf_hit_serializes_with_provenance_shape() {
    let hit = SearchRankedHit {
        id: "preference-2".to_string(),
        score: 1.0 / 60.0 + 1.0 / 61.0,
        rank: 1,
        sources: vec!["session".to_string(), "graph".to_string()],
        title: "prefer overview context".to_string(),
        kind: Some("preference".to_string()),
        node_id: Some("offload-002".to_string()),
        source_session_id: Some("sess-9f31".to_string()),
        element_refs: vec!["src/mcp/handler.rs::get_overview_context".to_string()],
    };
    let j = serde_json::to_value(&hit).expect("serialize");
    assert_eq!(j["kind"], "preference");
    assert_eq!(j["node_id"], "offload-002");
    assert_eq!(j["source_session_id"], "sess-9f31");
    assert_eq!(
        j["element_refs"][0],
        "src/mcp/handler.rs::get_overview_context"
    );
    assert_eq!(j["sources"][0], "session");
    assert_eq!(j["rank"], 1);
}

// ---------------------------------------------------------------------------
// FR-SM-09: full RRF search over session recall + graph lists
// ---------------------------------------------------------------------------

#[test]
fn search_memory_rrf_combines_recall_index_and_graph_ranks() {
    let tmp = tempfile::TempDir::new().unwrap();
    let project = tmp.path();

    // Seed the recall index (session side of the fusion).
    seed_recall_index(project);

    let hits = leankg::search::search_memory_rrf(project, "prefer overview", 10).expect("search");
    assert!(!hits.is_empty(), "hybrid search must return merged hits");
    let first = &hits[0];
    assert!(
        first.sources.contains(&"session".to_string())
            || first.sources.contains(&"graph".to_string()),
        "hit must carry provenance of its source list: {:?}",
        first.sources
    );
    // The recall-seeded lesson must be findable through the session list.
    assert!(
        hits.iter().any(|h| h.id.contains("r-")),
        "recall-index lesson present in fused results: {:?}",
        hits
    );
    // Score threshold + budget enforced by the lib seam.
    for h in &hits {
        assert!(h.score > 0.0, "no zero-score hits after fusion");
    }
}

fn seed_recall_index(project: &Path) {
    let store = RecallStore::new(project).expect("recall store");
    let prov = MemoryProvenance::new(
        "sess-9f31",
        "offload-002",
        MemoryKind::Preference,
        vec!["src/mcp/handler.rs::get_overview_context".to_string()],
    );
    store
        .push_dedup(&lesson_with_provenance(
            "r-1",
            "report_query_outcome",
            9.0,
            "prefer get_overview_context at session start (never grep first)",
            prov,
        ))
        .expect("push");
}
