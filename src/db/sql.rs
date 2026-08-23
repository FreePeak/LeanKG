//! SQL-first query seam (W8 P0 — the SQL-migration plan under `docs/`).
//!
//! The Datalog dialect is being removed wave-by-wave: every query becomes
//! plain, parameterized PostgreSQL issued through [`crate::db::backend::DbBackend`]
//! methods (`sql_query`, `sql_query_gucs`, `sql_execute`, `sql_execute_batch`,
//! `sql_copy_import`). This module holds the bind/result types shared by all
//! call sites plus the pg -> cell mapping used to build results.
//!
//! Binding rules:
//! - [`SqlParam::Vector`] binds as the pgvector text literal `[0.1,0.2]`;
//!   cast at the use site (`$3::text::vector`, matching the translator convention).
//! - NULL binds as [`SqlParam::Null`] (distinct from every other variant).
//!
//! Reading rules ([`row_from_pg`]): column-type-driven mapping into
//! [`DataValue`] cells; unknown/custom types (e.g. pgvector's `vector`)
//! fall back to their text form, vectors parsed into floats by
//! [`parse_vector_text`].

use crate::db::value::{DataValue, Num};
use postgres::types::ToSql;
use std::error::Error;

/// A bind parameter for the SQL-first API.
#[derive(Debug, Clone, PartialEq)]
pub enum SqlParam {
    Null,
    Bool(bool),
    Int(i64),
    Float(f64),
    Text(String),
    Bytes(Vec<u8>),
    Json(serde_json::Value),
    /// Bound as the pgvector literal `[a,b,...]`; cast `$n::text::vector` in SQL.
    Vector(Vec<f32>),
}

impl SqlParam {
    /// Bind as a boxed `ToSql`. Vectors become their pgvector text form.
    pub fn to_pg(&self) -> Box<dyn ToSql + Sync + Send> {
        match self {
            SqlParam::Null => Box::new(Option::<String>::None),
            SqlParam::Bool(b) => Box::new(*b),
            SqlParam::Int(i) => Box::new(*i),
            SqlParam::Float(f) => Box::new(*f),
            SqlParam::Text(s) => Box::new(s.clone()),
            SqlParam::Bytes(b) => Box::new(b.clone()),
            // serde_json::Value implements ToSql for json/jsonb directly
            // (postgres `with-serde_json-1`); a String would be rejected
            // with WrongType when the SQL casts `$n::jsonb`.
            SqlParam::Json(j) => Box::new(j.clone()),
            SqlParam::Vector(items) => Box::new(pgvector_literal(items)),
        }
    }

    /// Render for the COPY text format ([`crate::db::backend`] bulk path):
    /// NULL is the `\N` marker; metacharacters are escaped by the writer.
    pub(crate) fn to_copy_text(&self) -> String {
        match self {
            SqlParam::Null => "\\N".to_string(),
            SqlParam::Bool(b) => b.to_string(),
            SqlParam::Int(i) => i.to_string(),
            SqlParam::Float(f) => f.to_string(),
            SqlParam::Text(s) => s.clone(),
            SqlParam::Bytes(b) => {
                let mut s = String::with_capacity(b.len() * 4);
                for byte in b {
                    s.push_str(&format!("\\{:03o}", byte));
                }
                s
            }
            SqlParam::Json(j) => j.to_string(),
            SqlParam::Vector(items) => pgvector_literal(items),
        }
    }
}

/// pgvector text literal: `[0.1,0.2,...]`.
pub fn pgvector_literal(items: &[f32]) -> String {
    let mut s = String::from("[");
    for (i, f) in items.iter().enumerate() {
        if i > 0 {
            s.push(',');
        }
        s.push_str(&format!("{f}"));
    }
    s.push(']');
    s
}

macro_rules! param_from {
    ($ty:ty, $variant:expr) => {
        impl From<$ty> for SqlParam {
            fn from(v: $ty) -> Self {
                $variant(v)
            }
        }
    };
}
param_from!(bool, SqlParam::Bool);
param_from!(i64, SqlParam::Int);
param_from!(f64, SqlParam::Float);
param_from!(String, SqlParam::Text);
param_from!(Vec<u8>, SqlParam::Bytes);
param_from!(Vec<f32>, SqlParam::Vector);
param_from!(serde_json::Value, SqlParam::Json);
impl From<&str> for SqlParam {
    fn from(v: &str) -> Self {
        SqlParam::Text(v.to_string())
    }
}
impl From<i32> for SqlParam {
    fn from(v: i32) -> Self {
        SqlParam::Int(v as i64)
    }
}
impl From<Option<String>> for SqlParam {
    fn from(v: Option<String>) -> Self {
        match v {
            Some(s) => SqlParam::Text(s),
            None => SqlParam::Null,
        }
    }
}
impl From<DataValue> for SqlParam {
    /// Transition shim while call sites still hold `DataValue` rows
    /// (e.g. bulk imports). Vector-shaped lists need the column name hint
    /// via [`data_value_to_param`]; this default maps lists to JSON.
    fn from(v: DataValue) -> Self {
        data_value_to_param(&v, "")
    }
}

/// Convert a legacy `DataValue` cell to a bind parameter. `col` disambiguates
/// vector columns (`vec` / `vector`), mirroring the historical convention.
pub fn data_value_to_param(v: &DataValue, col: &str) -> SqlParam {
    match v {
        DataValue::Null | DataValue::Bot => SqlParam::Null,
        DataValue::Bool(b) => SqlParam::Bool(*b),
        DataValue::Num(Num::Int(i)) => SqlParam::Int(*i),
        DataValue::Num(Num::Float(f)) => SqlParam::Float(*f),
        DataValue::Str(s) => SqlParam::Text(s.clone()),
        DataValue::Bytes(b) => SqlParam::Bytes(b.clone()),
        DataValue::List(items) if col == "vec" || col == "vector" => SqlParam::Vector(
            items
                .iter()
                .filter_map(|it| match it {
                    DataValue::Num(Num::Float(f)) => Some(*f as f32),
                    DataValue::Num(Num::Int(i)) => Some(*i as f32),
                    _ => None,
                })
                .collect(),
        ),
        DataValue::List(items) => {
            SqlParam::Json(serde_json::to_value(items).unwrap_or(serde_json::Value::Null))
        }
        DataValue::Json(j) => SqlParam::Json(
            serde_json::from_str::<serde_json::Value>(j)
                .unwrap_or(serde_json::Value::String(j.clone())),
        ),
    }
}

/// One result row: named headers plus positionally-aligned cells.
#[derive(Debug, Clone, Default, PartialEq)]
pub struct SqlRow {
    pub headers: Vec<String>,
    pub cells: Vec<DataValue>,
}

impl SqlRow {
    pub fn new(headers: Vec<String>, cells: Vec<DataValue>) -> Self {
        Self { headers, cells }
    }

    /// Cell by column name or ordinal.
    pub fn get(&self, key: impl RowKey) -> Option<&DataValue> {
        key.lookup(self)
    }

    /// String view of a cell. SQL NULL (and the legacy bottom type) map to
    /// `None` — same contract as `NamedRows::get_str` on the Datalog path,
    /// which callers like `keys.rs` rely on to detect optional columns.
    pub fn text(&self, key: impl RowKey) -> Option<String> {
        match self.get(key) {
            Some(DataValue::Str(s)) => Some(s.clone()),
            Some(DataValue::Json(j)) => Some(j.clone()),
            Some(DataValue::Null) | Some(DataValue::Bot) => None,
            Some(other) => Some(other.to_string()),
            None => None,
        }
    }

    pub fn int(&self, key: impl RowKey) -> Option<i64> {
        match self.get(key) {
            Some(DataValue::Num(Num::Int(i))) => Some(*i),
            Some(DataValue::Num(Num::Float(f))) => Some(*f as i64),
            _ => None,
        }
    }

    pub fn float(&self, key: impl RowKey) -> Option<f64> {
        match self.get(key) {
            Some(DataValue::Num(Num::Float(f))) => Some(*f),
            Some(DataValue::Num(Num::Int(i))) => Some(*i as f64),
            _ => None,
        }
    }

    pub fn boolean(&self, key: impl RowKey) -> Option<bool> {
        match self.get(key) {
            Some(DataValue::Bool(b)) => Some(*b),
            _ => None,
        }
    }

    pub fn vec_f32(&self, key: impl RowKey) -> Option<Vec<f32>> {
        match self.get(key) {
            Some(DataValue::List(items)) => Some(
                items
                    .iter()
                    .filter_map(|it| match it {
                        DataValue::Num(Num::Float(f)) => Some(*f as f32),
                        DataValue::Num(Num::Int(i)) => Some(*i as f32),
                        _ => None,
                    })
                    .collect(),
            ),
            Some(DataValue::Str(s)) => parse_vector_text(s),
            _ => None,
        }
    }
}

/// Index a row by column name (`&str`) or ordinal (`usize`).
pub trait RowKey {
    fn lookup(self, row: &SqlRow) -> Option<&DataValue>;
}
impl RowKey for usize {
    fn lookup(self, row: &SqlRow) -> Option<&DataValue> {
        row.cells.get(self)
    }
}
impl RowKey for &str {
    fn lookup(self, row: &SqlRow) -> Option<&DataValue> {
        row.headers
            .iter()
            .position(|h| h == self)
            .and_then(|i| row.cells.get(i))
    }
}

/// Map a borrowed pg row into an owned [`SqlRow`].
pub fn row_from_pg(row: &postgres::Row) -> SqlRow {
    let cols = row.columns();
    let mut headers = Vec::with_capacity(cols.len());
    let mut cells = Vec::with_capacity(cols.len());
    for (i, col) in cols.iter().enumerate() {
        headers.push(col.name().to_string());
        cells.push(cell_from_pg(row, i));
    }
    SqlRow::new(headers, cells)
}

fn str_cell(v: Option<String>) -> DataValue {
    match v {
        Some(s) => DataValue::Str(s),
        None => DataValue::Null,
    }
}

// Per-type extraction. Each read goes through Option so SQL NULL maps to
// DataValue::Null instead of erroring.
fn cell_from_pg(row: &postgres::Row, i: usize) -> DataValue {
    let ty = row.columns()[i].type_();
    match ty.name() {
        "bool" => str_opt_bool(row, i),
        "int2" => int_cell(
            row.try_get::<_, Option<i16>>(i)
                .ok()
                .flatten()
                .map(i64::from),
        ),
        "int4" => int_cell(
            row.try_get::<_, Option<i32>>(i)
                .ok()
                .flatten()
                .map(i64::from),
        ),
        "int8" => int_cell(row.try_get::<_, Option<i64>>(i).ok().flatten()),
        "float4" => float_cell(
            row.try_get::<_, Option<f32>>(i)
                .ok()
                .flatten()
                .map(f64::from),
        ),
        "float8" => float_cell(row.try_get::<_, Option<f64>>(i).ok().flatten()),
        "bytea" => match row.try_get::<_, Option<Vec<u8>>>(i) {
            Ok(Some(b)) => DataValue::Bytes(b),
            _ => DataValue::Null,
        },
        "json" | "jsonb" => match row.try_get::<_, Option<serde_json::Value>>(i) {
            Ok(Some(v)) => DataValue::Json(v.to_string()),
            _ => DataValue::Null,
        },
        "uuid" | "void" => str_cell(row.try_get::<_, Option<String>>(i).ok().flatten()),
        "_text" | "_varchar" | "_name" => match row.try_get::<_, Option<Vec<String>>>(i) {
            Ok(Some(vs)) => DataValue::List(vs.into_iter().map(DataValue::Str).collect()),
            _ => DataValue::Null,
        },
        "vector" => str_cell(row.try_get::<_, Option<String>>(i).ok().flatten()),
        // text/varchar/name/citext and anything unhandled: string form.
        _ => str_cell(row.try_get::<_, Option<String>>(i).ok().flatten()),
    }
}

fn str_opt_bool(row: &postgres::Row, i: usize) -> DataValue {
    match row.try_get::<_, Option<bool>>(i) {
        Ok(Some(b)) => DataValue::Bool(b),
        _ => DataValue::Null,
    }
}

fn int_cell(v: Option<i64>) -> DataValue {
    match v {
        Some(i) => DataValue::Num(Num::Int(i)),
        None => DataValue::Null,
    }
}

fn float_cell(v: Option<f64>) -> DataValue {
    match v {
        Some(f) => DataValue::Num(Num::Float(f)),
        None => DataValue::Null,
    }
}

/// Parse pgvector text (`[0.1, 0.2]`) into floats.
pub fn parse_vector_text(s: &str) -> Option<Vec<f32>> {
    let inner = s.trim().trim_start_matches('[').trim_end_matches(']');
    if inner.trim().is_empty() {
        return Some(vec![]);
    }
    inner
        .split(',')
        .map(|p| p.trim().parse::<f32>().map_err(|_| ()))
        .collect::<Result<Vec<_>, _>>()
        .ok()
}

/// Error returned when a backend does not implement the SQL-first API.
pub fn unsupported() -> Box<dyn Error> {
    Box::new(std::io::Error::other(
        "SQL-first API not implemented for this backend (live PostgreSQL required)",
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::backend::DbBackend;
    use crate::db::value::Num;

    // ---- pure unit tests (no DB) ----

    #[test]
    fn pgvector_literal_formats_floats() {
        assert_eq!(pgvector_literal(&[0.1, 0.25]), "[0.1,0.25]");
        assert_eq!(pgvector_literal(&[]), "[]");
    }

    #[test]
    fn parses_vector_text_with_spaces() {
        assert_eq!(
            parse_vector_text("[0.5, -1.0, 2]"),
            Some(vec![0.5, -1.0, 2.0])
        );
        assert_eq!(parse_vector_text("[]"), Some(vec![]));
        assert_eq!(parse_vector_text("not a vec"), None);
    }

    #[test]
    fn data_value_param_maps_vector_columns() {
        let dv = DataValue::List(vec![
            DataValue::Num(Num::Float(1.0)),
            DataValue::Num(Num::Float(2.0)),
        ]);
        assert_eq!(
            data_value_to_param(&dv, "vec"),
            SqlParam::Vector(vec![1.0, 2.0])
        );
        let dv = DataValue::List(vec![DataValue::Str("x".into())]);
        assert!(matches!(
            data_value_to_param(&dv, "tags"),
            SqlParam::Json(_)
        ));
    }

    #[test]
    fn sql_row_accessors_by_name_and_index() {
        let row = SqlRow::new(
            vec!["name".into(), "count".into(), "opt".into()],
            vec![
                DataValue::Str("main.rs".into()),
                DataValue::Num(Num::Int(7)),
                DataValue::Null,
            ],
        );
        assert_eq!(row.text("name"), Some("main.rs".into()));
        assert_eq!(row.int(1), Some(7));
        assert_eq!(row.int("missing"), None);
        // NULL cells read as None through every typed accessor — parity
        // with the legacy `get_str` contract (regression guard: the WIP
        // seam rendered NULL text as Some("null"), which broke keys.rs).
        assert_eq!(row.text("opt"), None);
        assert_eq!(row.text("missing"), None);
        assert_eq!(row.int("opt"), None);
    }

    #[test]
    fn params_convert_from_common_scalars() {
        assert_eq!(SqlParam::from("x"), SqlParam::Text("x".into()));
        assert_eq!(SqlParam::from(7_i32), SqlParam::Int(7));
        assert_eq!(
            SqlParam::from(None::<String>),
            SqlParam::Null,
            "Option None binds NULL"
        );
        assert_eq!(
            SqlParam::from(Some("v".to_string())),
            SqlParam::Text("v".into())
        );
    }

    #[test]
    fn copy_text_renders_null_marker_and_escapes_bytes() {
        assert_eq!(SqlParam::Null.to_copy_text(), "\\N");
        assert_eq!(SqlParam::Bytes(vec![0o011]).to_copy_text(), "\\011");
        assert_eq!(SqlParam::Vector(vec![1.0]).to_copy_text(), "[1]");
        assert_eq!(
            SqlParam::Json(serde_json::json!({"a":1})).to_copy_text(),
            "{\"a\":1}"
        );
    }

    // ---- live-PG integration (probe-gated: skip when PG is unreachable) ----

    fn live_backend() -> Option<std::sync::Arc<crate::db::backend::PostgresBackend>> {
        if !crate::db::backend::test_pg_available() {
            return None;
        }
        Some(crate::db::backend::test_sql_scratch_backend())
    }

    #[test]
    fn query_maps_pg_types_to_cells() {
        let Some(db) = live_backend() else { return };
        let rows = db
            .sql_query(
                "SELECT 'main' AS name, 42::int AS n, 3.5::float8 AS x, true AS ok, \
                 NULL::text AS missing, '{\"a\":1}'::jsonb AS meta",
                &[],
            )
            .expect("query");
        assert_eq!(rows.len(), 1);
        let r = &rows[0];
        assert_eq!(r.text("name"), Some("main".into()));
        assert_eq!(r.int("n"), Some(42));
        assert_eq!(r.float("x"), Some(3.5));
        assert_eq!(r.boolean("ok"), Some(true));
        assert_eq!(r.get("missing"), Some(&DataValue::Null));
        let meta = r.text("meta").expect("jsonb text");
        assert!(meta.contains("\"a\""), "jsonb raw: {meta}");
    }

    #[test]
    fn params_roundtrip_through_table() {
        let Some(db) = live_backend() else { return };
        db.sql_execute_batch(&[
            ("DROP TABLE IF EXISTS leankg_sql_seam_t", vec![]),
            (
                "CREATE TABLE leankg_sql_seam_t (id bigserial primary key, name text, score float8, flags jsonb, embedding vector(3))",
                vec![],
            ),
        ])
        .expect("create");
        let n = db
            .sql_execute(
                "INSERT INTO leankg_sql_seam_t (name, score, flags, embedding) \
                 VALUES ($1, $2, $3::jsonb, $4::text::vector)",
                &[
                    SqlParam::Text("a.rs".into()),
                    SqlParam::Float(1.5),
                    SqlParam::Json(serde_json::json!({"k": 1})),
                    SqlParam::Vector(vec![0.1, 0.2, 0.3]),
                ],
            )
            .expect("insert");
        assert_eq!(n, 1);
        let rows = db
            .sql_query(
                "SELECT name, score FROM leankg_sql_seam_t WHERE name = $1 AND score = $2",
                &[SqlParam::Text("a.rs".into()), SqlParam::Float(1.5)],
            )
            .expect("select");
        assert_eq!(rows.len(), 1);
        assert_eq!(rows[0].text("name"), Some("a.rs".into()));
        assert_eq!(rows[0].float("score"), Some(1.5));
    }

    #[test]
    fn execute_reports_affected_rows() {
        let Some(db) = live_backend() else { return };
        db.sql_execute_batch(&[
            ("DROP TABLE IF EXISTS leankg_sql_seam_u", vec![]),
            (
                "CREATE TABLE leankg_sql_seam_u (id bigserial primary key, grp bigint)",
                vec![],
            ),
            (
                "INSERT INTO leankg_sql_seam_u (grp) SELECT generate_series(1, 5)",
                vec![],
            ),
        ])
        .expect("setup");
        let n = db
            .sql_execute(
                "UPDATE leankg_sql_seam_u SET grp = grp + 10 WHERE grp <= $1",
                &[SqlParam::Int(3)],
            )
            .expect("update");
        assert_eq!(n, 3);
    }

    #[test]
    fn batch_is_atomic_on_failure() {
        let Some(db) = live_backend() else { return };
        db.sql_execute_batch(&[
            ("DROP TABLE IF EXISTS leankg_sql_seam_b", vec![]),
            ("CREATE TABLE leankg_sql_seam_b (id int)", vec![]),
        ])
        .expect("setup");
        let err = db
            .sql_execute_batch(&[
                ("INSERT INTO leankg_sql_seam_b VALUES (1)", vec![]),
                ("INSERT INTO leankg_sql_seam_missing VALUES (2)", vec![]),
            ])
            .expect_err("second statement must fail");
        let _ = err;
        let rows = db
            .sql_query("SELECT count(*)::bigint AS c FROM leankg_sql_seam_b", &[])
            .expect("count");
        // First insert rolled back with the failed transaction.
        assert_eq!(rows[0].int("c"), Some(0), "batch must be atomic");
    }

    #[test]
    fn gucs_apply_within_the_read_transaction() {
        let Some(db) = live_backend() else { return };
        let rows = db
            .sql_query_gucs(
                "SELECT current_setting('hnsw.ef_search') AS ef",
                &[],
                &[("hnsw.ef_search", "77")],
            )
            .expect("guc query");
        assert_eq!(rows[0].text("ef"), Some("77".into()));
    }

    #[test]
    fn copy_import_bulk_loads() {
        let Some(db) = live_backend() else { return };
        db.sql_execute_batch(&[
            ("DROP TABLE IF EXISTS leankg_sql_seam_c", vec![]),
            (
                "CREATE TABLE leankg_sql_seam_c (qualified_name text, line_start bigint)",
                vec![],
            ),
        ])
        .expect("setup");
        let rows: Vec<Vec<SqlParam>> = (0..500)
            .map(|i| {
                vec![
                    SqlParam::Text(format!("src/f{i}.rs::f{i}")),
                    SqlParam::Int(i),
                ]
            })
            .collect();
        db.sql_copy_import(
            "leankg_sql_seam_c",
            &["qualified_name", "line_start"],
            &rows,
        )
        .expect("copy");
        let got = db
            .sql_query("SELECT count(*)::bigint AS c FROM leankg_sql_seam_c", &[])
            .expect("count");
        assert_eq!(got[0].int("c"), Some(500));
    }

    #[test]
    fn copy_import_preserves_nulls_and_empty_strings() {
        let Some(db) = live_backend() else { return };
        db.sql_execute_batch(&[
            ("DROP TABLE IF EXISTS leankg_sql_seam_n", vec![]),
            ("CREATE TABLE leankg_sql_seam_n (a text, b text)", vec![]),
        ])
        .expect("setup");
        db.sql_copy_import(
            "leankg_sql_seam_n",
            &["a", "b"],
            &[
                vec![SqlParam::Null, SqlParam::Text(String::new())],
                vec![SqlParam::Text("x".into()), SqlParam::Null],
            ],
        )
        .expect("copy");
        let got = db
            .sql_query(
                "SELECT a, b FROM leankg_sql_seam_n ORDER BY b NULLS LAST, a NULLS FIRST",
                &[],
            )
            .expect("read");
        assert_eq!(got.len(), 2);
        // Row ('x', NULL): a is text, b is SQL NULL.
        let with_null = got
            .iter()
            .find(|r| r.text("a") == Some("x".into()))
            .expect("x row");
        assert_eq!(with_null.get("b"), Some(&DataValue::Null), "\\N is NULL");
        // Row (NULL, ''): empty string survives, NOT collapsed to NULL.
        let empty = got
            .iter()
            .find(|r| r.get("a") == Some(&DataValue::Null))
            .expect("null-a row");
        assert_eq!(empty.text("b"), Some(String::new()), "'' is not NULL");
    }

    #[test]
    fn read_only_backend_rejects_writes_at_pg_layer() {
        let Some(_db) = live_backend() else { return };
        let ro = crate::db::backend::test_sql_scratch_backend_ro();
        let err = ro
            .sql_execute("CREATE TABLE leankg_sql_seam_ro_should_not_exist ()", &[])
            .expect_err("read-only session must reject writes");
        // postgres::Error's Debug carries the full server message; Display
        // alone may truncate to "db error".
        let rendered = format!("{err:?}");
        assert!(
            rendered.to_lowercase().contains("read-only"),
            "unexpected error: {rendered}"
        );
        let rows = ro
            .sql_query("SELECT 1::bigint AS one", &[])
            .expect("ro read");
        assert_eq!(rows[0].int("one"), Some(1));
    }

    #[test]
    fn fake_backend_reports_unsupported_clearly() {
        let fake = crate::db::fake::FakeBackend::for_path(std::path::Path::new("/tmp/x"));
        let err = fake
            .sql_query("SELECT 1", &[])
            .expect_err("fake has no SQL engine");
        assert!(err.to_string().contains("SQL"), "{err}");
    }
}
