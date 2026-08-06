//! Write-bus seam for LeanKG's DB writes.
//!
//! FR-P0-MCP-RC-02: writes must go through ONE shared per-path RocksDB/SQLite
//! handle. This module adds a thin priority write bus so an operator can later
//! swap the in-process serial bus for a distributed one (Kafka / Google
//! Pub/Sub) WITHOUT touching callers — but only when a multi-writer / remote
//! topology exists (`LEANKG_COZO_ENDPOINT`, see `src/db/schema.rs`). Embedded
//! RocksDB is single-host: two processes cannot share one RocksDB directory,
//! so a distributed queue buys nothing today (YAGNI).
//!
//! The in-process default is a priority-ordered serial bus: tool writes
//! (`Priority::ToolWrite`) are dequeued ahead of embed writes
//! (`Priority::EmbedWrite`) so a long embed batch can never starve a tool
//! write. Actual `:put`/`:update` calls still execute on the caller's shared
//! `Arc<CozoDb>`; the bus only orders them.

use std::collections::BinaryHeap;
use std::sync::Arc;

use tokio::sync::mpsc;

/// Priority of a queued write. Higher number = dequeued first.
#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum Priority {
    /// Embed / index bulk writes — yield to tool writes.
    EmbedWrite = 0,
    /// MCP tool writes (add_knowledge, add_annotation, ...) — fast lane.
    ToolWrite = 1,
}

/// A unit of queued work. The payload is opaque to the bus; callers execute it.
pub struct WriteJob {
    /// Ordering priority.
    pub priority: Priority,
    /// A caller-supplied label for diagnostics (e.g. `add_knowledge`, `embed`).
    pub kind: &'static str,
    /// The synchronous write closure, executed serially on the bus worker.
    /// Writes run on the shared `Arc<CozoDb>` handle (never a second open).
    pub run: Box<dyn FnOnce() -> Result<(), String> + Send + 'static>,
}

// BinaryHeap needs Ord; tie-break on insertion sequence for FIFO within a
// priority class.
struct QueuedJob {
    seq: u64,
    priority: Priority,
    kind: &'static str,
    run: Option<Box<dyn FnOnce() -> Result<(), String> + Send + 'static>>,
}

impl PartialEq for QueuedJob {
    fn eq(&self, other: &Self) -> bool {
        self.priority == other.priority && self.seq == other.seq
    }
}
impl Eq for QueuedJob {}

impl PartialOrd for QueuedJob {
    fn partial_cmp(&self, other: &Self) -> Option<std::cmp::Ordering> {
        Some(self.cmp(other))
    }
}

// Max-heap: higher priority first; within a priority, lower seq first.
impl Ord for QueuedJob {
    fn cmp(&self, other: &Self) -> std::cmp::Ordering {
        self.priority
            .cmp(&other.priority)
            .then_with(|| other.seq.cmp(&self.seq))
    }
}

/// Generic write bus. Callers submit a job; the implementation decides order
/// and execution (in-process serial today, distributed later).
pub trait WriteBus: Send + Sync {
    /// Queue a write job. Non-blocking (fires-and-forgets to the bus).
    fn submit(&self, job: WriteJob) -> Result<(), String>;
    /// Drain / wait for all currently-queued jobs. Best-effort.
    fn flush(&self) -> Result<(), String>;
}

/// In-process serial, priority-ordered write bus. The default implementation.
pub struct InProcessWriteBus {
    tx: mpsc::Sender<QueuedJob>,
    /// The worker receiver, moved into the spawned task on first submit.
    /// Keeps sync constructors (e.g. `MCPServer::new` in non-async tests) free
    /// of a Tokio runtime requirement (the worker only spawns on a submit,
    /// which callers always make from an async context).
    rx: std::sync::Mutex<Option<mpsc::Receiver<QueuedJob>>>,
    /// Set on first spawn so the worker starts exactly once.
    spawned: std::sync::OnceLock<()>,
    seq: std::sync::atomic::AtomicU64,
    shutdown: Arc<tokio::sync::Notify>,
}

impl InProcessWriteBus {
    /// Create the bus without spawning the worker.
    pub fn new(queue_len: usize) -> Self {
        let (tx, rx) = mpsc::channel::<QueuedJob>(queue_len.max(1));
        let shutdown = Arc::new(tokio::sync::Notify::new());
        Self {
            tx,
            rx: std::sync::Mutex::new(Some(rx)),
            spawned: std::sync::OnceLock::new(),
            seq: std::sync::atomic::AtomicU64::new(0),
            shutdown,
        }
    }

    /// Spawn the worker task exactly once. Callers must be inside a Tokio
    /// runtime (they are: `submit` runs from async write handlers).
    fn ensure_worker(&self) {
        self.spawned.get_or_init(|| {
            let rx = self.rx.lock().unwrap().take().expect("worker started once");
            let shutdown_task = self.shutdown.clone();
            tokio::spawn(async move {
                let mut rx = rx;
                let mut heap: BinaryHeap<QueuedJob> = BinaryHeap::new();
                loop {
                    tokio::select! {
                        // Prefer draining the heap before waiting for more input.
                        biased;
                        maybe = rx.recv() => {
                            match maybe {
                                Some(first) => {
                                    // Batch-receive everything currently pending so
                                    // priority ordering applies across the batch
                                    // (a tool write submitted while embeds queue
                                    // still jumps them). Then execute the highest-
                                    // priority job. One job per drain pass keeps a
                                    // long job from delaying higher-priority ones
                                    // that arrive mid-run.
                                    heap.push(first);
                                    while let Ok(next) = rx.try_recv() {
                                        heap.push(next);
                                    }
                                }
                                None => {
                                    // Channel closed: drain whatever remains.
                                    while let Some(job) = heap.pop() {
                                        if let Some(run) = job.run {
                                            let _ = run();
                                        }
                                    }
                                    break;
                                }
                            }
                            // Execute the highest-priority pending job. Keep the
                            // rest in the heap so a tool write arriving mid-run
                            // still jumps them on the next pass.
                            if let Some(job) = heap.pop() {
                                if let Some(run) = job.run {
                                    let _ = run();
                                }
                            }
                        }
                        _ = shutdown_task.notified() => break,
                    }
                    // When nothing more is buffered, drain the heap (priority
                    // order) rather than waiting for more input.
                    if rx.is_empty() {
                        while let Some(job) = heap.pop() {
                            if let Some(run) = job.run {
                                let _ = run();
                            }
                        }
                    }
                }
            });
        });
    }

    /// Stop the worker (drains then exits). Used in tests / teardown.
    pub fn shutdown(&self) {
        self.shutdown.notify_one();
    }
}

impl Default for InProcessWriteBus {
    fn default() -> Self {
        Self::new(1024)
    }
}

impl WriteBus for InProcessWriteBus {
    fn submit(&self, job: WriteJob) -> Result<(), String> {
        // Spawn the worker on first submit (idempotent). Must run inside a
        // Tokio runtime; callers that submit from async contexts satisfy this.
        self.ensure_worker();
        let seq = self.seq.fetch_add(1, std::sync::atomic::Ordering::SeqCst);
        let queued = QueuedJob {
            seq,
            priority: job.priority,
            kind: job.kind,
            run: Some(job.run),
        };
        self.tx
            .try_send(queued)
            .map_err(|e| format!("write bus submit failed ({})", e))
    }

    fn flush(&self) -> Result<(), String> {
        // In-process bus is FIFO-serial; submit is async fire-and-forget.
        // A real flush would join the worker; for the seam we expose a
        // best-effort no-op (callers can await the channel drain).
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn priority_orders_tool_before_embed() {
        let mut heap: BinaryHeap<QueuedJob> = BinaryHeap::new();
        let mk = |seq: u64, p: Priority| QueuedJob {
            seq,
            priority: p,
            kind: "t",
            run: None,
        };
        heap.push(mk(0, Priority::EmbedWrite));
        heap.push(mk(1, Priority::ToolWrite));
        heap.push(mk(2, Priority::EmbedWrite));

        assert_eq!(heap.pop().unwrap().priority, Priority::ToolWrite);
        assert_eq!(heap.pop().unwrap().priority, Priority::EmbedWrite);
        assert_eq!(heap.pop().unwrap().priority, Priority::EmbedWrite);
    }

    #[test]
    fn fifo_within_same_priority() {
        let mut heap: BinaryHeap<QueuedJob> = BinaryHeap::new();
        let mk = |seq: u64| QueuedJob {
            seq,
            priority: Priority::ToolWrite,
            kind: "t",
            run: None,
        };
        heap.push(mk(5));
        heap.push(mk(2));
        heap.push(mk(9));
        assert_eq!(heap.pop().unwrap().seq, 2);
        assert_eq!(heap.pop().unwrap().seq, 5);
        assert_eq!(heap.pop().unwrap().seq, 9);
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn executes_all_jobs_serially() {
        let bus = InProcessWriteBus::default();
        let order = Arc::new(std::sync::Mutex::new(Vec::new()));

        for i in 0..5 {
            let order = order.clone();
            bus.submit(WriteJob {
                priority: Priority::EmbedWrite,
                kind: "embed",
                run: Box::new(move || {
                    order.lock().unwrap().push(i);
                    Ok(())
                }),
            })
            .unwrap();
        }
        let tool_order = order.clone();
        bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool",
            run: Box::new(move || {
                tool_order.lock().unwrap().push(99);
                Ok(())
            }),
        })
        .unwrap();

        // Give the worker time to drain all six.
        tokio::time::sleep(std::time::Duration::from_millis(150)).await;
        let got = order.lock().unwrap().clone();
        assert_eq!(got.len(), 6, "all jobs must run exactly once, got {got:?}");
        assert!(
            got.contains(&99),
            "tool write must be executed, got {got:?}"
        );
        bus.shutdown();
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn priority_job_queued_last_still_jumps_embeds() {
        // Deterministic priority check: the first embed job blocks the worker
        // thread on a std channel. While blocked, the remaining embeds buffer
        // in the bus channel and the tool write is submitted. Releasing the
        // block must cause the worker to run the tool write (priority) before
        // the buffered embeds.
        let bus = InProcessWriteBus::default();
        let order = Arc::new(std::sync::Mutex::new(Vec::new()));
        // Each embed gets its own blocking channel so the worker thread parks
        // until the test releases it (Receiver is not Clone).
        let mut release_txs = Vec::new();

        for i in 0..4 {
            let (release_tx, release_rx) = std::sync::mpsc::channel::<()>();
            release_txs.push(release_tx);
            let order = order.clone();
            bus.submit(WriteJob {
                priority: Priority::EmbedWrite,
                kind: "embed",
                run: Box::new(move || {
                    // The first embed job blocks the worker thread. Later
                    // embeds only run after the tool write has jumped them.
                    let _ = release_rx.recv();
                    order.lock().unwrap().push(i);
                    Ok(())
                }),
            })
            .unwrap();
        }

        // Let the worker enter the first (blocked) embed job; the other three
        // embeds buffer in the bus channel behind it.
        tokio::time::sleep(std::time::Duration::from_millis(50)).await;

        let tool_order = order.clone();
        bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool",
            run: Box::new(move || {
                tool_order.lock().unwrap().push(99);
                Ok(())
            }),
        })
        .unwrap();

        // Release all four blocked embeds.
        for tx in release_txs {
            tx.send(()).unwrap();
        }
        tokio::time::sleep(std::time::Duration::from_millis(150)).await;

        let got = order.lock().unwrap().clone();
        assert_eq!(got.len(), 5, "all jobs must run, got {got:?}");
        // The first embed was already in-flight when the tool queued; the tool
        // must jump the three still-buffered embeds.
        assert_eq!(
            got[1], 99,
            "tool write queued last must jump the buffered embeds, got {got:?}"
        );
        bus.shutdown();
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn submit_accepts_job_on_fresh_bus() {
        let bus = InProcessWriteBus::new(1);
        let res = bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool",
            run: Box::new(|| Ok(())),
        });
        assert!(res.is_ok(), "fresh bus must accept a job");
        bus.shutdown();
    }

    // ------------------------------------------------------------------
    // Slice 1 — closure runs the backend write on the bus worker.
    // The integration under test: a caller submits a WriteJob whose
    // closure executes a write against a shared backend (FakeBackend in
    // tests, PostgresBackend in prod). After submit returns, the worker
    // must execute the closure exactly once. We assert via a recorder
    // shared between the closure and the test, mirroring the contract
    // the MCP tool handlers will rely on once they call submit().
    // ------------------------------------------------------------------

    use std::sync::atomic::{AtomicUsize, Ordering as AtomicOrdering};
    use std::sync::Arc;

    #[derive(Default)]
    struct Recorder(Arc<AtomicUsize>);

    impl Recorder {
        fn bump(&self) {
            self.0.fetch_add(1, AtomicOrdering::SeqCst);
        }
        fn count(&self) -> usize {
            self.0.load(AtomicOrdering::SeqCst)
        }
    }

    #[tokio::test(flavor = "multi_thread")]
    async fn closure_runs_against_shared_backend() {
        let bus = InProcessWriteBus::default();
        let recorder = Recorder::default();
        let recorder_for_job = Recorder(recorder.0.clone());

        // The "backend write" here is just a counter bump; the real backend
        // call (run_script on PostgresBackend / FakeBackend) is what callers
        // put inside the closure. We're testing that the bus executes the
        // closure on its worker thread exactly once.
        bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool_write",
            run: Box::new(move || {
                recorder_for_job.bump();
                Ok(())
            }),
        })
        .unwrap();

        // Poll until the worker has run the job (avoid fixed sleeps).
        let mut tries = 0;
        while recorder.count() == 0 && tries < 100 {
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            tries += 1;
        }
        assert_eq!(
            recorder.count(),
            1,
            "closure must run exactly once after submit() returns, got {} after {} polls",
            recorder.count(),
            tries
        );
        bus.shutdown();
    }

    // ------------------------------------------------------------------
    // Slice 2 — priority ordering across queued jobs (the contract MCP
    // tool writers rely on: an in-flight embed batch cannot starve a
    // tool write that arrives after). When all jobs are queued ahead of
    // execution, the highest-priority job runs first.
    // ------------------------------------------------------------------

    #[tokio::test(flavor = "multi_thread")]
    async fn tool_write_runs_before_buffered_embed_writes() {
        // The bus worker is single-threaded and serial. Submit several
        // embed jobs (Priority::EmbedWrite) followed by a tool job
        // (Priority::ToolWrite). Because all submissions happen before
        // any closure runs, the worker should pick the tool job first
        // per the priority heap.
        let bus = InProcessWriteBus::default();
        let order: Arc<std::sync::Mutex<Vec<&'static str>>> =
            Arc::new(std::sync::Mutex::new(Vec::new()));

        for _ in 0..3 {
            let order = order.clone();
            bus.submit(WriteJob {
                priority: Priority::EmbedWrite,
                kind: "embed",
                run: Box::new(move || {
                    order.lock().unwrap().push("embed");
                    Ok(())
                }),
            })
            .unwrap();
        }
        let order_tool = order.clone();
        bus.submit(WriteJob {
            priority: Priority::ToolWrite,
            kind: "tool",
            run: Box::new(move || {
                order_tool.lock().unwrap().push("tool");
                Ok(())
            }),
        })
        .unwrap();

        // Poll until the worker drains.
        let mut tries = 0;
        while order.lock().unwrap().len() < 4 && tries < 200 {
            tokio::time::sleep(std::time::Duration::from_millis(5)).await;
            tries += 1;
        }
        let got = order.lock().unwrap().clone();
        assert_eq!(got.len(), 4, "all jobs must run, got {got:?}");
        // The tool write is the only ToolWrite; it must be dequeued first
        // by the heap even though it was submitted last (FIFO-within-tier
        // is overridden by priority across tiers).
        assert_eq!(
            got[0], "tool",
            "tool write must run before any buffered embed write, got {got:?}"
        );
        bus.shutdown();
    }
}
