use leankg::watcher::{FileChange, FileChangeKind, FileWatcher};
use notify::EventKind;
use std::path::PathBuf;
use tempfile::TempDir;

#[test]
fn test_file_change_kind_from_create() {
    let kind = FileChangeKind::from(&EventKind::Create(notify::event::CreateKind::File));
    assert!(matches!(kind, FileChangeKind::Created));
}

#[test]
fn test_file_change_kind_from_modify() {
    let kind = FileChangeKind::from(&EventKind::Modify(notify::event::ModifyKind::Data(
        notify::event::DataChange::Content,
    )));
    assert!(matches!(kind, FileChangeKind::Modified));
}

#[test]
fn test_file_change_kind_from_remove() {
    let kind = FileChangeKind::from(&EventKind::Remove(notify::event::RemoveKind::File));
    assert!(matches!(kind, FileChangeKind::Deleted));
}

#[test]
fn test_file_change_kind_from_any_other_becomes_modified() {
    let kind = FileChangeKind::from(&EventKind::Access(notify::event::AccessKind::Open(
        notify::event::AccessMode::Read,
    )));
    assert!(matches!(kind, FileChangeKind::Modified));
}

#[test]
fn test_file_watcher_new_valid_directory() {
    let tmp = TempDir::new().unwrap();
    let result = FileWatcher::new(tmp.path());
    assert!(result.is_ok());
}

#[test]
fn test_file_watcher_watch_path_returns_correct_path() {
    let tmp = TempDir::new().unwrap();
    let path = tmp.path().to_path_buf();
    let watcher = FileWatcher::new(&path).unwrap();
    assert_eq!(watcher.watch_path(), path.as_path());
}

#[test]
fn test_file_change_created_variant() {
    let change = FileChange {
        path: PathBuf::from("/test/path"),
        kind: FileChangeKind::Created,
    };
    assert!(matches!(change.kind, FileChangeKind::Created));
    assert_eq!(change.path, PathBuf::from("/test/path"));
}

#[test]
fn test_file_change_modified_variant() {
    let change = FileChange {
        path: PathBuf::from("/test/path"),
        kind: FileChangeKind::Modified,
    };
    assert!(matches!(change.kind, FileChangeKind::Modified));
}

#[test]
fn test_file_change_deleted_variant() {
    let change = FileChange {
        path: PathBuf::from("/test/path"),
        kind: FileChangeKind::Deleted,
    };
    assert!(matches!(change.kind, FileChangeKind::Deleted));
}

#[test]
fn test_file_change_clone() {
    let change = FileChange {
        path: PathBuf::from("/test/path"),
        kind: FileChangeKind::Created,
    };
    let cloned = change.clone();
    assert_eq!(cloned.path, change.path);
    assert!(matches!(cloned.kind, FileChangeKind::Created));
}

#[test]
fn test_file_change_kind_clone() {
    let kind = FileChangeKind::Created;
    let cloned = kind.clone();
    assert!(matches!(cloned, FileChangeKind::Created));

    let kind = FileChangeKind::Modified;
    let cloned = kind.clone();
    assert!(matches!(cloned, FileChangeKind::Modified));

    let kind = FileChangeKind::Deleted;
    let cloned = kind.clone();
    assert!(matches!(cloned, FileChangeKind::Deleted));
}

#[test]
fn test_file_watcher_into_async_creates_async_watcher() {
    let tmp = TempDir::new().unwrap();
    let watcher = FileWatcher::new(tmp.path()).unwrap();
    let (tx, _rx) = tokio::sync::mpsc::channel(100);
    let async_watcher = watcher.into_async(tx);
    drop(async_watcher);
}

#[test]
#[ignore = "run() is an infinite loop, this test verifies into_async conversion only"]
fn test_async_file_watcher_into_async_and_run() {
    let tmp = TempDir::new().unwrap();
    let watcher = FileWatcher::new(tmp.path()).unwrap();
    let (tx, _rx) = tokio::sync::mpsc::channel(100);
    let async_watcher = watcher.into_async(tx);
    drop(async_watcher);
}
