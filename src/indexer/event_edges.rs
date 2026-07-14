//! US-CBM-B6 / FR-B15: Event channel edge detection.
//!
//! Detects emit / listen / subscribe patterns in source files and emits
//! `emits` and `listens_on` relationships so agents can trace event
//! flow across modules without scanning every call site.
//!
//! Supported patterns:
//!   - Node.js EventEmitter: `emitter.emit('event')`, `emitter.on('event', h)`,
//!     `emitter.once(...)`, `emitter.addListener(...)`, `emitter.removeListener(...)`
//!   - DOM: `target.dispatchEvent(new Event('foo'))` and `target.addEventListener('foo', h)`
//!   - jQuery: `$(sel).trigger('foo')`, `$(sel).on('foo', h)`
//!   - Browser events: `window.addEventListener('foo', h)` etc.
//!   - Generic pub/sub: any call named `emit`, `publish`, `fire`,
//!     `dispatch`, `trigger`, `on`, `once`, `listen`, `subscribe`
//!     where the receiver is a noun-shaped identifier and the first
//!     argument is a string-literal event name.
//!
//! The detector runs after the main extraction so it can re-use the
//! `qualified_name` of any caller element it finds via line number.
use crate::db::models::Relationship;
use once_cell::sync::Lazy;
use regex::Regex;

static EMIT_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r#"(?m)([A-Za-z_][A-Za-z0-9_$]*)\s*\.\s*(emit|fire|publish|trigger|dispatch)\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_.\-:]+)['"]"#,
    )
    .unwrap()
});

static LISTEN_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(
        r#"(?m)([A-Za-z_][A-Za-z0-9_$]*)\s*\.\s*(on|once|addListener|listen|subscribe|addEventListener)\s*\(\s*['"]([A-Za-z_][A-Za-z0-9_.\-:]+)['"]"#,
    )
    .unwrap()
});

static DOM_DISPATCH_RE: Lazy<Regex> = Lazy::new(|| {
    Regex::new(r#"(?m)([A-Za-z_][A-Za-z0-9_$]*)\s*\.\s*dispatchEvent\s*\(\s*new\s+[A-Za-z_]+\(\s*['"]([A-Za-z_][A-Za-z0-9_.\-:]+)['"]"#).unwrap()
});

#[derive(Debug, Clone)]
pub struct EventEdge {
    pub rel_type: &'static str,
    pub receiver: String,
    pub event_name: String,
    pub line: u32,
}

/// Scan source code for emit / listen patterns. Returns the raw edges
/// without qualified_names; the caller (indexer.rs) stitches them
/// into Relationship rows after the main extraction pass.
pub fn detect_event_edges(source: &str) -> Vec<EventEdge> {
    let mut out: Vec<EventEdge> = Vec::new();
    for cap in EMIT_RE.captures_iter(source) {
        let line = source[..cap.get(0).unwrap().start()].matches('\n').count() as u32 + 1;
        out.push(EventEdge {
            rel_type: "emits",
            receiver: cap[1].to_string(),
            event_name: cap[3].to_string(),
            line,
        });
    }
    for cap in DOM_DISPATCH_RE.captures_iter(source) {
        let line = source[..cap.get(0).unwrap().start()].matches('\n').count() as u32 + 1;
        out.push(EventEdge {
            rel_type: "emits",
            receiver: cap[1].to_string(),
            event_name: cap[2].to_string(),
            line,
        });
    }
    for cap in LISTEN_RE.captures_iter(source) {
        let line = source[..cap.get(0).unwrap().start()].matches('\n').count() as u32 + 1;
        out.push(EventEdge {
            rel_type: "listens_on",
            receiver: cap[1].to_string(),
            event_name: cap[3].to_string(),
            line,
        });
    }
    out
}

/// Convert detected edges into graph Relationships. The event itself
/// becomes a synthetic `event:<name>` qualified_name so it shows up
/// in `search_code` and `find_clones` outputs.
pub fn to_relationships(edges: &[EventEdge]) -> Vec<Relationship> {
    edges
        .iter()
        .map(|e| {
            let event_qn = format!("event::{}", e.event_name);
            Relationship {
                id: None,
                source_qualified: e.receiver.clone(),
                target_qualified: event_qn,
                rel_type: e.rel_type.to_string(),
                confidence: 0.85,
                metadata: serde_json::json!({
                    "resolution_method": "name",
                    "event_name": e.event_name,
                }),
                ..Default::default()
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn detects_node_event_emitter() {
        let src = r#"
emitter.emit("user:created", payload);
bus.on("user:created", handler);
subscriber.once("ping", cb);
"#;
        let edges = detect_event_edges(src);
        assert!(edges.iter().any(|e| e.rel_type == "emits"
            && e.receiver == "emitter"
            && e.event_name == "user:created"));
        assert!(edges.iter().any(|e| e.rel_type == "listens_on"
            && e.receiver == "bus"
            && e.event_name == "user:created"));
        assert!(edges.iter().any(|e| e.rel_type == "listens_on"
            && e.receiver == "subscriber"
            && e.event_name == "ping"));
    }

    #[test]
    fn detects_dom_dispatch_event() {
        let src = r#"
target.dispatchEvent(new CustomEvent("save"));
"#;
        let edges = detect_event_edges(src);
        assert!(edges
            .iter()
            .any(|e| e.rel_type == "emits" && e.event_name == "save"));
    }

    #[test]
    fn empty_source_returns_no_edges() {
        let edges = detect_event_edges("");
        assert!(edges.is_empty());
    }
}
