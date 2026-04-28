use crate::db::schema::CozoDb;
use crate::mcp::tracker::WriteTracker;
use std::collections::BTreeMap;
use std::sync::Arc;

pub struct TrackingDb {
    inner: CozoDb,
    tracker: Arc<WriteTracker>,
}

impl TrackingDb {
    pub fn new(inner: CozoDb, tracker: Arc<WriteTracker>) -> Self {
        Self { inner, tracker }
    }

    pub fn run_script(
        &self,
        script: &str,
        params: BTreeMap<String, serde_json::Value>,
    ) -> Result<cozo::NamedRows, cozo::Error> {
        if is_write_operation(script) {
            self.tracker.mark_dirty();
        }
        self.inner.run_script(script, params)
    }

    pub fn into_inner(self) -> CozoDb {
        self.inner
    }
}

fn is_write_operation(script: &str) -> bool {
    let script_lower = script.to_lowercase();
    script_lower.contains(":put") || script_lower.contains(":delete")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_write_operation_put() {
        assert!(is_write_operation(
            "?[id, name] <- [[$id, $name]] :put code_elements"
        ));
        assert!(is_write_operation(":put code_elements { id, name }"));
    }

    #[test]
    fn test_is_write_operation_delete() {
        assert!(is_write_operation(":delete code_elements where id = $id"));
        assert!(is_write_operation("?[id] := *code_elements[id] :delete"));
    }

    #[test]
    fn test_is_write_operation_query() {
        assert!(!is_write_operation(
            "?[id, name] := *code_elements[id, name]"
        ));
        assert!(!is_write_operation(":schema code_elements"));
    }
}
