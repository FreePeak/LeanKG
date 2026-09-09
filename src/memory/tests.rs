//! Synthetic-fixture tests: ≤10 docs, no models, no ONNX (FR-ZCP-11
//! constraint applies to memory tests a fortiori — file-backed store only).

use super::bank::{mnemopi_bank_name, BankScope};
use super::store::MemoryStore;
use std::path::Path;

fn fixture_root(tag: &str) -> (tempfile::TempDir, MemoryStore) {
    let tmp = tempfile::TempDir::new().unwrap();
    let dir = tmp.path().join(tag);
    std::fs::create_dir_all(&dir).unwrap();
    (tmp, MemoryStore::new(&dir))
}

#[test]
fn retain_then_recall_injects_the_lesson() {
    let (_tmp, store) = fixture_root("retain-recall");
    store
        .retain_transcript(
            "proj-bank",
            "session-A",
            &["lesson: always run cargo clippy before pushing".into()],
            3,
            "/repo",
        )
        .unwrap();
    // New session recalls it.
    let hits = store.recall(&["proj-bank".to_string()], "cargo clippy before pushing", 8);
    assert!(!hits.is_empty(), "the lesson must be recallable");
    assert!(hits[0].content.contains("clippy"));
    assert_eq!(hits[0].source, "coding-agent-transcript");
}

#[test]
fn retained_through_user_turn_cursor_is_honored() {
    let (_tmp, store) = fixture_root("cursor");
    let r1 = store
        .retain_transcript("proj-bank", "session-B", &["turn one".into()], 3, "/repo")
        .unwrap();
    assert_eq!(r1.written, 1);
    // Resume: same session, cursor at 3 → re-retain through 5 writes only
    // the new entries (the store has no per-turn granularity in slice 1, so
    // a batch at or below the cursor is skipped entirely).
    let r2 = store
        .retain_transcript("proj-bank", "session-B", &["turn two".into()], 3, "/repo")
        .unwrap();
    assert_eq!(r2.written, 0, "cursor must prevent duplicate retention");
    assert_eq!(r2.skipped, 1);
    assert_eq!(r2.retained_through_user_turn, 3);
}

#[test]
fn cursor_and_rows_agree_after_invalidate() {
    let (_tmp, store) = fixture_root("invalidate");
    store
        .retain_transcript("proj-bank", "session-C", &["a lesson".into()], 7, "/repo")
        .unwrap();
    let removed = store.invalidate_session("session-C").unwrap();
    assert_eq!(removed, 1);
    assert_eq!(store.retained_through_user_turn("session-C"), None);
    assert!(store
        .recall(&["proj-bank".to_string()], "lesson", 8)
        .is_empty());
}

#[test]
fn scoped_read_merges_project_and_shared_deduped() {
    let (_tmp, store) = fixture_root("scopes");
    // Shared bank: a global write.
    store
        .retain_transcript(
            "shared-bank",
            "session-S",
            &["org-wide fact".into()],
            1,
            "/org",
        )
        .unwrap();
    // Project bank: a project-specific write.
    store
        .retain_transcript(
            "proj-bank",
            "session-P",
            &["project fact".into()],
            2,
            "/repo",
        )
        .unwrap();

    // per-project: reads only the project bank.
    let hits = store.recall(&["proj-bank".to_string()], "fact", 8);
    assert!(hits.iter().all(|h| h.content.contains("project")));

    // per-project-tagged: merged [project, shared].
    let merged = store.recall(
        &["proj-bank".to_string(), "shared-bank".to_string()],
        "fact",
        8,
    );
    assert_eq!(merged.len(), 2, "merged + deduped: {merged:?}");

    // global: writes and reads the shared bank.
    assert_eq!(
        BankScope::Global.write_bank("proj-bank", "shared-bank"),
        "shared-bank"
    );
}

#[test]
fn get_update_forget_roundtrip() {
    let (_tmp, store) = fixture_root("crud");
    store
        .retain_transcript(
            "proj-bank",
            "session-D",
            &["to be updated".into()],
            1,
            "/repo",
        )
        .unwrap();
    let hits = store.recall(&["proj-bank".to_string()], "updated", 8);
    let id = hits[0].id.clone();
    assert!(store.update(&id, "now updated").unwrap());
    assert!(store.get(&id).unwrap().content.contains("now updated"));
    assert!(store.forget(&id).unwrap());
    assert!(
        store.get(&id).unwrap().content.is_empty(),
        "forget must empty the entry"
    );
    let after_forget = store.recall(&["proj-bank".to_string()], "to be updated", 8);
    assert!(
        after_forget.iter().all(|h| h.id != id),
        "the forgotten entry must not be recallable: {after_forget:?}"
    );
}

#[test]
fn bank_name_matches_across_cwd_forms() {
    // Canonicalization: on macOS /tmp is a symlink to /private/tmp — both
    // forms of the SAME directory must produce the same bank (canonicalize
    // resolves the symlink before hashing).
    let dir = std::path::PathBuf::from("/tmp/leankg-memory-cwd-forms-test");
    std::fs::create_dir_all(&dir).unwrap();
    let via_tmp = mnemopi_bank_name(&dir);
    let via_private = mnemopi_bank_name(&std::path::PathBuf::from(
        "/private/tmp/leankg-memory-cwd-forms-test",
    ));
    assert_eq!(via_tmp, via_private);
    let _ = Path::new(via_tmp.as_str());
}
