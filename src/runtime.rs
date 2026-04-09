use std::sync::OnceLock;

/// Returns a shared, static Tokio runtime for the entire LeanKG engine.
/// This prevents spawning new threads and tokio runtimes repeatedly across synchronous methods.
pub fn get_runtime() -> &'static tokio::runtime::Runtime {
    static RUNTIME: OnceLock<tokio::runtime::Runtime> = OnceLock::new();
    RUNTIME.get_or_init(|| tokio::runtime::Runtime::new().expect("Failed to initialize static tokio runtime"))
}

/// A safe blocking executor that adapts regardless of whether the calling thread is inside a Tokio runtime or not.
pub fn run_blocking<F: std::future::Future>(f: F) -> F::Output {
    if let Ok(handle) = tokio::runtime::Handle::try_current() {
        tokio::task::block_in_place(move || handle.block_on(f))
    } else {
        get_runtime().block_on(f)
    }
}
