use leankg::runtime::{get_runtime, run_blocking};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use tokio::time::{sleep, Duration};

#[test]
fn test_get_runtime_singleton_spawns_tasks() {
    let rt = get_runtime();
    
    // We can spawn 1000 tasks and verify they evaluate completely synchronously behind the scenes
    // without ever allocating multiple runtime configurations or deadlocking internally
    let counter = Arc::new(AtomicUsize::new(0));
    
    let mut handles = vec![];
    for _ in 0..10_000 {
        let c = counter.clone();
        handles.push(rt.spawn(async move {
            c.fetch_add(1, Ordering::SeqCst);
        }));
    }

    // Wait for all spawned logic constraints to complete evaluation
    rt.block_on(async {
        for handle in handles {
            let _ = handle.await;
        }
    });

    assert_eq!(counter.load(Ordering::SeqCst), 10_000);
}

#[tokio::test(flavor = "multi_thread")]
async fn test_run_blocking_fallback_multi_threaded() {
    // If run_blocking is called inside an existing active multi-threaded Tokio runtime it must fallback to block_in_place
    let result = run_blocking(async {
        let mut x = 0;
        for _ in 0..100 {
            x += 1;
        }
        x
    });
    
    assert_eq!(result, 100);
}

#[test]
fn test_run_blocking_no_active_runtime() {
    // Ensure if we run_blocking totally outside of an async thread (e.g. from Java CLI caller synchronously), it transparently spawns the fallback singleton runtime to evaluate
    let result = run_blocking(async {
        let mut x = 0;
        for i in 0..50 {
            x += i; // 0 + 1 + ... + 49 = 1225
        }
        x
    });
    
    assert_eq!(result, 1225);
}
