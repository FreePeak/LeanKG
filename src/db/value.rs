//! Row/result value types for the Postgres backend.
//!
//! Post-migration (Phase 8) these are the concrete row values flowing out of
//! `run_script` — the subset of the old embedded engine's `DataValue` API
//! that the codebase actually consumed positionally (`row[0].get_str()`,
//! `NamedRows::new`, `DataValue::Num`), now self-contained so the legacy
//! dependency can be deleted.

use std::cmp::Ordering;
use std::fmt;

/// A row value. `Null` is distinct from every other variant so
/// `NULL = NULL` comparisons behave as expected.
#[derive(Debug, Clone, PartialEq, serde::Serialize, serde::Deserialize)]
pub enum DataValue {
    Null,
    Bool(bool),
    Num(Num),
    Str(String),
    Bytes(Vec<u8>),
    List(Vec<DataValue>),
    Json(String),
    /// Bottom type, kept for callers that pattern-match legacy shapes.
    Bot,
}

impl fmt::Display for DataValue {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            DataValue::Null => write!(f, "null"),
            DataValue::Bool(b) => write!(f, "{b}"),
            DataValue::Num(n) => write!(f, "{n}"),
            DataValue::Str(s) => write!(f, "{s}"),
            DataValue::Bytes(b) => write!(f, "{:?}", String::from_utf8_lossy(b)),
            DataValue::List(l) => {
                write!(f, "[")?;
                for (i, v) in l.iter().enumerate() {
                    if i > 0 {
                        write!(f, ", ")?;
                    }
                    write!(f, "{v}")?;
                }
                write!(f, "]")
            }
            DataValue::Json(j) => write!(f, "{j}"),
            DataValue::Bot => write!(f, "bot"),
        }
    }
}

/// A number: int or float (kept distinct — `get_int` on a float with a
/// whole value returns it, mirroring the old engine's semantics).
#[derive(Debug, Clone, Copy, serde::Serialize, serde::Deserialize)]
pub enum Num {
    Int(i64),
    Float(f64),
}

impl PartialEq for Num {
    fn eq(&self, other: &Self) -> bool {
        self.cmp(other) == Ordering::Equal
    }
}
impl Eq for Num {}

impl PartialOrd for Num {
    fn partial_cmp(&self, other: &Self) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

impl Ord for Num {
    fn cmp(&self, other: &Self) -> Ordering {
        self.to_f64()
            .partial_cmp(&other.to_f64())
            .unwrap_or(Ordering::Equal)
    }
}

impl Num {
    fn to_f64(self) -> f64 {
        match self {
            Num::Int(i) => i as f64,
            Num::Float(f) => f,
        }
    }
}

impl fmt::Display for Num {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Num::Int(i) => write!(f, "{i}"),
            Num::Float(x) => write!(f, "{x}"),
        }
    }
}

impl DataValue {
    pub fn get_str(&self) -> Option<&str> {
        match self {
            DataValue::Str(s) => Some(s),
            _ => None,
        }
    }
    pub fn get_int(&self) -> Option<i64> {
        match self {
            DataValue::Num(n) => match n {
                Num::Int(i) => Some(*i),
                Num::Float(f) if f.round() == *f => Some(*f as i64),
                _ => None,
            },
            _ => None,
        }
    }
    pub fn get_float(&self) -> Option<f64> {
        match self {
            DataValue::Num(n) => Some(n.to_f64()),
            _ => None,
        }
    }
    pub fn get_bool(&self) -> Option<bool> {
        match self {
            DataValue::Bool(b) => Some(*b),
            _ => None,
        }
    }
    pub fn get_bytes(&self) -> Option<&[u8]> {
        match self {
            DataValue::Bytes(b) => Some(b),
            _ => None,
        }
    }
    pub fn get_slice(&self) -> Option<&[DataValue]> {
        match self {
            DataValue::List(l) => Some(l),
            _ => None,
        }
    }
}

impl From<i64> for DataValue {
    fn from(v: i64) -> Self {
        DataValue::Num(Num::Int(v))
    }
}
impl From<f64> for DataValue {
    fn from(v: f64) -> Self {
        DataValue::Num(Num::Float(v))
    }
}
impl From<&str> for DataValue {
    fn from(v: &str) -> Self {
        DataValue::Str(v.to_string())
    }
}
impl From<String> for DataValue {
    fn from(v: String) -> Self {
        DataValue::Str(v)
    }
}
impl From<bool> for DataValue {
    fn from(v: bool) -> Self {
        DataValue::Bool(v)
    }
}

/// Rows together with their headers (positional access contract).
#[derive(Debug, Clone, Default, serde::Serialize, serde::Deserialize)]
pub struct NamedRows {
    pub headers: Vec<String>,
    pub rows: Vec<Vec<DataValue>>,
    pub next: Option<Box<NamedRows>>,
}

impl NamedRows {
    pub fn new(headers: Vec<String>, rows: Vec<Vec<DataValue>>) -> Self {
        Self {
            headers,
            rows,
            next: None,
        }
    }
    pub fn has_more(&self) -> bool {
        self.next.is_some()
    }
    pub fn flatten(self) -> Vec<Self> {
        let mut collected = vec![];
        let mut current = self;
        loop {
            let nxt = current.next.take();
            collected.push(current);
            if let Some(n) = nxt {
                current = *n;
            } else {
                break;
            }
        }
        collected
    }
}
