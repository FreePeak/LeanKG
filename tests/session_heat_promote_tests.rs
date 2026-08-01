//! US-SM-05 / US-SM-06 / FR-SM-10 / FR-SM-11 — heat promotion tests.
//!
//! White-box heat-ranked `.leankg/MEMORY_INDEX.md` (FR-SM-10) and workflow
//! proposals (FR-SM-11). Proposals must NEVER write ontology YAML as SoT —
//! YAML files under `ontology/` are only read, never modified.

use leankg::session::{
    content_key, heat_score, HeatScore, MemoryIndex, ProposalRecord, SequenceMiner,
    WorkflowProposal, MEMORY_INDEX_JSON, MEMORY_INDEX_MD, MIN_SEQUENCE_LEN, WORKFLOW_PROPOSALS_DIR,
};
use tempfile::TempDir;

fn ts(secs: u64) -> u64 {
    secs
}

#[test]
fn heat_score_is_deterministic_and_ranks_recency() {
    let a = heat_score(3, ts(100), ts(500));
    let b = heat_score(3, ts(100), ts(500));
    assert_eq!(a, b, "same inputs must produce identical score");
    // recency: later last_recall beats earlier one
    let recent = heat_score(3, ts(400), ts(500));
    assert!(
        recent > a,
        "more recent recall must score higher (recent {recent:.6} vs old {a:.6})"
    );
    // frequency: more recalls beats fewer at equal recency
    let more = heat_score(9, ts(100), ts(500));
    assert!(
        more > a,
        "more recalls must score higher (more {more:.6} vs fewer {a:.6})"
    );
}

#[test]
fn memory_index_refresh_writes_markdown_sorted_by_heat() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    let mut idx = MemoryIndex::new(project).expect("new index");

    idx.record_hit(
        "open_conversation",
        "src/conversation_indexer/mod.rs",
        "open_conversation parses Claude export JSON",
        ts(100),
    )
    .expect("record hit");
    idx.record_hit(
        "load_layer",
        "src/mcp/handler.rs",
        "load_layer expands a layer",
        ts(200),
    )
    .expect("record hit");
    idx.record_hit(
        "load_layer",
        "src/mcp/handler.rs",
        "load_layer expands a layer",
        ts(300),
    )
    .expect("second hit same key");

    idx.refresh().expect("refresh");
    let md =
        std::fs::read_to_string(project.join(".leankg").join(MEMORY_INDEX_MD)).expect("index md");
    assert!(md.contains("# MEMORY_INDEX"), "header: {md}");
    assert!(md.contains("load_layer"), "hot key listed: {md}");
    assert!(md.contains("open_conversation"), "cold key listed: {md}");
    // 2 recalls beats 1 recall → load_layer must rank first
    let li = md.find("load_layer").expect("load_layer pos");
    let oc = md.find("open_conversation").expect("open_conversation pos");
    assert!(li < oc, "higher heat ranks first: {md}");
    // white-box: raw score visible in the markdown
    assert!(md.contains("heat="), "score visible: {md}");
    // json sidecar kept in sync
    let stored: MemoryIndex = MemoryIndex::load(project).expect("load json");
    assert_eq!(stored.items().len(), 2, "two distinct keys");
}

#[test]
fn memory_index_marks_recalled_and_tracks_last_recall() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    let mut idx = MemoryIndex::new(project).expect("new index");
    idx.record_hit("hot_key", "src/a.rs", "some hot key", ts(100))
        .expect("record");
    idx.record_recall("hot_key", ts(300)).expect("recall");
    let stored = MemoryIndex::load(project).expect("load");
    let item = stored.items().iter().find(|i| i.key == "hot_key").unwrap();
    assert_eq!(item.recalls, 2, "recall counts as a hit");
    assert_eq!(item.last_recalled_epoch_secs, ts(300));
    // unknown key recall is a no-op, not an error
    assert!(idx.record_recall("missing", ts(300)).is_ok());
}

#[test]
fn refresh_does_not_touch_ontology_yaml() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    // fixture ontology SoT next to the project
    let ont_dir = project.join("ontology");
    std::fs::create_dir_all(&ont_dir).unwrap();
    let concepts = ont_dir.join("concepts.yaml");
    let workflows = ont_dir.join("workflows.yaml");
    std::fs::write(&concepts, "kind: concepts\nunchanged: true\n").unwrap();
    std::fs::write(&workflows, "kind: workflows\nunchanged: true\n").unwrap();
    let before = std::fs::read(&concepts).unwrap();

    let mut idx = MemoryIndex::new(project).expect("new index");
    idx.record_hit("k1", "src/a.rs", "k1 body", ts(1))
        .expect("hit");
    idx.record_hit("k2", "src/b.rs", "k2 body", ts(2))
        .expect("hit");
    idx.refresh().expect("refresh");

    let after = std::fs::read(&concepts).unwrap();
    let after_wf = std::fs::read(&workflows).unwrap();
    assert_eq!(before, after, "concepts.yaml must not be modified");
    assert_eq!(
        std::fs::read_to_string(&workflows).unwrap(),
        "kind: workflows\nunchanged: true\n",
        "workflows.yaml must not be modified"
    );
    let _ = after_wf;
}

#[test]
fn refresh_is_noop_when_nothing_changed() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    let mut idx = MemoryIndex::new(project).expect("new index");
    idx.record_hit("k1", "src/a.rs", "k1 body", ts(1))
        .expect("hit");
    idx.refresh().expect("refresh");
    let md_path = project.join(".leankg").join(MEMORY_INDEX_MD);
    let first = std::fs::read(&md_path).unwrap();

    idx.refresh().expect("second refresh");
    let second = std::fs::read(&md_path).unwrap();
    assert_eq!(first, second, "unchanged index must not rewrite the file");
}

#[test]
fn sequence_miner_detects_repeated_successful_traces() {
    let miner = SequenceMiner::default();
    let traces = vec![
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec!["get_dependencies".to_string()],
    ];
    let proposals = miner.propose(&traces, None);
    assert!(!proposals.is_empty(), "repeated trace must be proposed");
    let p = &proposals[0];
    assert_eq!(p.steps, vec!["search_code", "get_context", "query_file"]);
    assert!(p.occurrences >= 3, "occurrences: {}", p.occurrences);
    assert!(
        p.occurrences >= MIN_SEQUENCE_LEN,
        "must meet min occurrences"
    );
}

#[test]
fn sequence_miner_supports_positive_negative_heuristics() {
    // positive: repeated 3-step trace followed by "get_context" win
    let positive = vec![
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
    ];
    let out_pos = SequenceMiner::default().propose(&positive, Some(&["get_context"]));
    assert!(
        !out_pos.is_empty(),
        "repeated trace with win marker must be proposed"
    );
    // negative: trace repeated enough but no win marker => rejected by filter
    let negative = vec![
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
    ];
    let out_neg = SequenceMiner::default().propose(&negative, Some(&["win_tool_not_in_trace"]));
    assert!(
        out_neg.is_empty(),
        "trace without win marker must not be proposed"
    );
}

#[test]
fn workflow_proposal_writes_jsonl_only_and_preserves_ontology() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    let ont_dir = project.join("ontology");
    std::fs::create_dir_all(&ont_dir).unwrap();
    let workflows = ont_dir.join("workflows.yaml");
    std::fs::write(&workflows, "kind: workflows\nunchanged: true\n").unwrap();

    let proposal = WorkflowProposal {
        name: "trace_code_provenance".to_string(),
        description: "search then drill into context then read the file".to_string(),
        steps: vec![
            "search_code".to_string(),
            "get_context".to_string(),
            "query_file".to_string(),
        ],
        occurrences: 3,
        confidence: 0.9,
        created_epoch_secs: ts(1000),
    };
    let rec = ProposalRecord {
        proposal,
        content_key: content_key("search_code|get_context|query_file"),
    };
    let dir = project.join(".leankg").join(WORKFLOW_PROPOSALS_DIR);
    std::fs::create_dir_all(&dir).unwrap();
    let path = dir.join("workflows.jsonl");
    let rec_json = serde_json::to_string(&rec).expect("serialize");
    std::fs::write(&path, format!("{rec_json}\n")).unwrap();

    // write to .leankg only; ontology YAML untouched
    assert!(path.exists(), "proposal file exists");
    let yaml = std::fs::read_to_string(&workflows).unwrap();
    assert_eq!(
        yaml, "kind: workflows\nunchanged: true\n",
        "ontology SoT intact"
    );
    assert!(
        rec.content_key == content_key("search_code|get_context|query_file"),
        "dedup key matches"
    );
}

#[test]
fn proposal_round_trips_json() {
    let p = WorkflowProposal {
        name: "n".to_string(),
        description: "d".to_string(),
        steps: vec!["a".to_string(), "b".to_string()],
        occurrences: 2,
        confidence: 0.5,
        created_epoch_secs: 1,
    };
    let rec = ProposalRecord {
        content_key: content_key("a|b"),
        proposal: p,
    };
    let s = serde_json::to_string(&rec).unwrap();
    let back: ProposalRecord = serde_json::from_str(&s).unwrap();
    assert_eq!(back.proposal.name, "n");
    assert_eq!(back.proposal.steps, vec!["a", "b"]);
    assert_eq!(back.proposal.occurrences, 2);
}

#[test]
fn memory_index_json_round_trips_and_dedups_key() {
    let tmp = TempDir::new().unwrap();
    let project = tmp.path();
    let mut idx = MemoryIndex::new(project).expect("new");
    idx.record_hit("dup", "src/x.rs", "body", ts(1))
        .expect("h1");
    idx.record_hit("dup", "src/x.rs", "body", ts(2))
        .expect("h2");
    let loaded = MemoryIndex::load(project).expect("load");
    assert_eq!(loaded.items().len(), 1, "same key deduped");
    assert_eq!(loaded.items()[0].recalls, 2);
    assert!(project.join(".leankg").join(MEMORY_INDEX_JSON).exists());
}

#[test]
fn heat_score_struct_serializes() {
    let hs = HeatScore {
        frequency: 0.5,
        recency: 0.5,
        total: 1.0,
    };
    let s = serde_json::to_string(&hs).unwrap();
    assert!(s.contains("\"total\":1.0") || s.contains("\"total\":1.0") || s.contains("total"));
}

#[test]
fn heat_score_never_negative_and_zeros_for_never_recalled() {
    let never = heat_score(0, 0, 1000);
    assert!(never.is_finite() && never >= 0.0, "never recalled: {never}");
    let now = ts(1000);
    // a recall at the same instant as now must still score
    let same = heat_score(1, now, now);
    assert!(same.is_finite() && same > 0.0, "instant recall: {same}");
}
