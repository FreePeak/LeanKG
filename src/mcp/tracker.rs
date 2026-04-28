use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::sync::RwLock;
use std::time::Instant;

#[derive(Debug)]
pub struct WriteTracker {
    dirty: Arc<AtomicBool>,
    last_write: Arc<RwLock<Instant>>,
}

impl WriteTracker {
    pub fn new() -> Self {
        Self {
            dirty: Arc::new(AtomicBool::new(false)),
            last_write: Arc::new(RwLock::new(Instant::now())),
        }
    }

    pub fn mark_dirty(&self) {
        self.dirty.store(true, Ordering::SeqCst);
        *self.last_write.write().unwrap() = Instant::now();
    }

    pub fn is_dirty(&self) -> bool {
        self.dirty.load(Ordering::SeqCst)
    }

    pub fn clear_dirty(&self) {
        self.dirty.store(false, Ordering::SeqCst);
    }

    pub fn last_write_time(&self) -> Instant {
        *self.last_write.read().unwrap()
    }
}

impl Default for WriteTracker {
    fn default() -> Self {
        Self::new()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_write_tracker_initial_state() {
        let tracker = WriteTracker::new();
        assert!(!tracker.is_dirty());
    }

    #[test]
    fn test_mark_dirty() {
        let tracker = WriteTracker::new();
        tracker.mark_dirty();
        assert!(tracker.is_dirty());
    }

    #[test]
    fn test_clear_dirty() {
        let tracker = WriteTracker::new();
        tracker.mark_dirty();
        tracker.clear_dirty();
        assert!(!tracker.is_dirty());
    }

    #[test]
    fn test_last_write_time() {
        let tracker = WriteTracker::new();
        let before = Instant::now();
        tracker.mark_dirty();
        let after = Instant::now();
        let last_write = tracker.last_write_time();
        assert!(last_write >= before && last_write <= after);
    }
}
