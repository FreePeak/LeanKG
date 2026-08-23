//! Hackathon Cycle-2 R2b — pool-slot recovery on the internal-watchdog
//! path (issue B / sweep N4).
//!
//! A tool whose watchdog expires while its pooled PG connection is still
//! checked out must not starve later tools beyond LEANKG_PG_POOL_WAIT_MS;
//! slots must return promptly when the holder finishes OR when its future is
//! cancelled at an await point (what tokio::time::timeout does on expiry).
//!
//! Run (needs LEANKG_PG_URL):
//! ```bash
//! set -a; source ../.env; set +a
//! cargo test --release --test pool_watchdog_recovery -- --test-threads=1 --nocapture
//! ```

use leankg::db::backend::ClientPool;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

/// Serialize tests that mutate process env (LEANKG_PG_POOL_WAIT_MS).
static ENV_LOCK: Mutex<()> = Mutex::new(());

fn pg_url() -> Option<String> {
    match std::env::var("LEANKG_PG_URL") {
        Ok(v)
            if !v.trim().is_empty()
                && !v.contains("localhost:5433")
                && !v.contains("127.0.0.1:5433") =>
        {
            Some(v)
        }
        _ => None,
    }
}

#[test]
fn pool_checkout_fails_fast_when_slots_held_and_recovers_after_release() {
    let Some(base) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let _guard = ENV_LOCK.lock().unwrap_or_else(|e| e.into_inner());
    let prev_wait = std::env::var("LEANKG_PG_POOL_WAIT_MS").ok();
    std::env::set_var("LEANKG_PG_POOL_WAIT_MS", "1500");

    let pool = Arc::new(ClientPool::new(1));
    // Holder thread: connect (outside the timed section), then run a slow
    // query while holding the only slot.
    let (ready_tx, ready_rx) = std::sync::mpsc::channel::<()>();
    let holder_pool = pool.clone();
    let holder_url = base.clone();
    let holder = std::thread::spawn(move || {
        let mut client = holder_pool.checkout(&holder_url).expect("holder checkout");
        ready_tx.send(()).unwrap();
        // Simulate the heavy tool's blocking PG call continuing after its
        // future was abandoned: hold the slot for 3s of real DB work.
        let _ = client.query("SELECT pg_sleep(3)", &[]);
        client
    });
    ready_rx.recv().expect("holder ready");
    // Give the holder a moment to enter pg_sleep so the slot is truly busy.
    std::thread::sleep(Duration::from_millis(300));

    let t0 = Instant::now();
    let res = pool.checkout(&base);
    let waited = t0.elapsed();
    assert!(
        res.is_err(),
        "checkout must fail while the only slot is held"
    );
    assert!(
        waited < Duration::from_millis(1500 + 1200),
        "fail-fast violated: waited {waited:?}"
    );
    eprintln!(
        "starved checkout failed fast after {waited:?}: {}",
        res.err().unwrap()
    );

    // Recovery: once the holder's query drains and the slot returns, the
    // next checkout succeeds promptly.
    let client = holder.join().expect("holder join");
    drop(client);
    let t1 = Instant::now();
    let again = pool.checkout(&base);
    let recover_wait = t1.elapsed();
    assert!(again.is_ok(), "pool must recover after release");
    assert!(
        recover_wait < Duration::from_secs(3),
        "recovery slow: {recover_wait:?}"
    );
    eprintln!("recovered slot in {recover_wait:?}");

    match prev_wait {
        Some(v) => std::env::set_var("LEANKG_PG_POOL_WAIT_MS", v),
        None => std::env::remove_var("LEANKG_PG_POOL_WAIT_MS"),
    }
}

/// Watchdog-expiry simulation: a tool task checks out a connection and then
/// parks at an await point (like an async tool between PG calls). Cancelling
/// that task (what tokio::time::timeout does on expiry) must return the slot
/// PROMPTLY — later tools must not starve behind it.
#[tokio::test(flavor = "multi_thread")]
async fn pool_slot_returns_promptly_when_holder_task_cancelled() {
    let Some(base) = pg_url() else {
        eprintln!("skipping: LEANKG_PG_URL not set");
        return;
    };
    let pool = Arc::new(ClientPool::new(1));

    let task = tokio::spawn({
        let pool = pool.clone();
        let base = base.clone();
        async move {
            // checkout may block up to the wait budget if racing; fine here.
            // The sync postgres client spins its own internal runtime, so —
            // exactly like production call sites — it must run under
            // block_in_place when invoked from async context.
            let client =
                tokio::task::block_in_place(|| pool.checkout(&base)).expect("holder checkout");
            // Park forever WHILE HOLDING the pooled client — the await point
            // where watchdog cancellation would drop the future.
            std::future::pending::<()>().await;
            #[allow(unreachable_code)]
            drop(client);
        }
    });

    // Wait until the slot is checked out.
    let deadline = Instant::now() + Duration::from_secs(30);
    while pool.live_count() < 1 {
        assert!(Instant::now() < deadline, "holder never checked out");
        tokio::time::sleep(Duration::from_millis(50)).await;
    }

    // Cancel like the internal watchdog would.
    task.abort();
    let _ = task.await;

    // Slot must be back immediately.
    let t0 = Instant::now();
    let res = tokio::task::block_in_place(|| pool.checkout(&base));
    let waited = t0.elapsed();
    assert!(
        res.is_ok(),
        "slot must be returned when holder task is cancelled"
    );
    assert!(
        waited < Duration::from_secs(2),
        "cancelled holder did not return slot promptly: {waited:?}"
    );
}
