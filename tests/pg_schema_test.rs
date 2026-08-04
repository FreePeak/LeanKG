//! Phase 2 schema assertions (plan T2 exit) — runs migrations from
//! src/db/pg/migrations.rs against a scratch schema and asserts the full
//! table inventory.
//!
//! Requires the Phase 0 Postgres container (Postgres 18 + pgvector):
//!   docker exec leankg-pg-phase0 psql -U postgres -d leankg -c "CREATE EXTENSION IF NOT EXISTS vector;"
//!
//! Run only these (the crate has slow unrelated integration tests):
//!   cargo test --release --test pg_schema_test
//!
//! Every test is #[ignore]-gated so the default `cargo test` run skips them
//! (the container is not required on dev machines).

use std::env;

/// Vector dimension — must match the Rust const in src/db/pg/migrations.rs
/// (VEC_DIM = 384) and the vector(384) in schema.sql.
const VEC_DIM: usize = 384;

/// The 16 tables of the Phase 2 inventory (query_cache dropped per D2).
const EXPECTED_TABLES: &[&str] = &[
    "code_elements",
    "relationships",
    "business_logic",
    "context_metrics",
    "service_metadata",
    "teams",
    "team_invites",
    "migrations",
    "knowledge_entries",
    "feature_workflow_links",
    "incidents",
    "index_inventory",
    "api_keys",
    "embedding_state",
    "embedding_vectors",
    "index_hashes",
];

fn pg_url() -> String {
    env::var("LEANKG_PG_URL")
        .unwrap_or_else(|_| "postgresql://postgres:postgres@localhost:5433/leankg".to_string())
}

/// Connect, create a scratch schema `leankg_test_<pid>`, point search_path at
/// it, run migrations, and return (client, schema). The schema is dropped at
/// end of scope; the shared `leankg` database is never touched.
struct ScratchSchema {
    client: postgres::Client,
    name: String,
}

impl ScratchSchema {
    fn new() -> ScratchSchema {
        let url = pg_url();
        let name = format!("leankg_test_{}", std::process::id());
        let mut admin = postgres::Client::connect(&url, postgres::NoTls)
            .unwrap_or_else(|e| panic!("cannot connect to {url}: {e}"));
        admin
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {name} CASCADE"))
            .unwrap();
        admin
            .batch_execute(&format!("CREATE SCHEMA {name}"))
            .unwrap();
        // Unqualified tables land in the scratch schema; keep `public` on the
        // path so the existing `vector` extension (installed in public) is
        // resolvable — `CREATE EXTENSION IF NOT EXISTS vector` no-ops because
        // the extension is already installed database-wide.
        admin
            .batch_execute(&format!("SET search_path TO {name}, public"))
            .unwrap();
        // The admin connection owns the schema; migrations must run on it.
        ScratchSchema {
            client: admin,
            name,
        }
    }
}

impl Drop for ScratchSchema {
    fn drop(&mut self) {
        let _ = self
            .client
            .batch_execute(&format!("DROP SCHEMA IF EXISTS {} CASCADE", self.name));
    }
}

/// Full inventory: all 16 tables exist, query_cache does not.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn all_16_tables_exist_and_no_query_cache() {
    let mut s = ScratchSchema::new();
    let report = leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();
    assert_eq!(
        report.applied,
        vec!["001_schema".to_string()],
        "migrations should apply exactly once on a fresh schema"
    );

    let rows = s
        .client
        .query(
            "SELECT table_name FROM information_schema.tables
             WHERE table_schema = $1 AND table_type = 'BASE TABLE'
             ORDER BY table_name",
            &[&s.name],
        )
        .unwrap();
    let tables: Vec<String> = rows.iter().map(|r| r.get::<_, String>(0)).collect();

    for t in EXPECTED_TABLES {
        assert!(
            tables.iter().any(|n| n == t),
            "missing table {t}; got: {tables:?}"
        );
    }
    assert!(
        !tables.iter().any(|n| n == "query_cache"),
        "query_cache must not exist (dropped per D2); got: {tables:?}"
    );
}

/// Re-running migrations is a no-op (idempotent).
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn rerun_is_noop() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();
    let report = leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();
    assert!(report.applied.is_empty(), "second run must apply nothing");
    assert_eq!(report.skipped, vec!["001_schema".to_string()]);
}

/// embedding_vectors: vector(384) column + HNSW cosine index.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn embedding_vectors_shape_and_hnsw_index() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();

    let row = s
        .client
        .query_one(
            "SELECT data_type, udt_name FROM information_schema.columns
             WHERE table_schema = $1 AND table_name = 'embedding_vectors' AND column_name = 'vec'",
            &[&s.name],
        )
        .unwrap();
    assert_eq!(row.get::<_, String>(0), "USER-DEFINED");
    assert_eq!(
        row.get::<_, String>(1),
        "vector",
        "vec must be pgvector type"
    );

    // pgvector vector(384) — udt_name is 'vector'; the dimension is in the
    // type metadata (pg_attribute.atttypmod), so probe by actually inserting.
    let literal = format!("[{}]", vec!["0.0"; VEC_DIM].join(","));
    s.client
        .query_one(
            "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ($1, $2::vector) RETURNING qualified_name",
            &[&"qn/unit/probe", &literal],
        )
        .unwrap();
    let bad = format!("[{}]", vec!["0.0"; VEC_DIM + 1].join(","));
    assert!(
        s.client
            .query_one(
                "INSERT INTO embedding_vectors (qualified_name, vec) VALUES ($1, $2::vector) RETURNING qualified_name",
                &[&"qn/unit/probe_bad", &bad],
            )
            .is_err(),
        "vector({VEC_DIM}) must reject a {}-dim literal",
        VEC_DIM + 1
    );

    let row = s
        .client
        .query_one(
            "SELECT indexdef FROM pg_indexes
             WHERE schemaname = $1 AND tablename = 'embedding_vectors' AND indexname = 'embedding_vectors_vec_hnsw_idx'",
            &[&s.name],
        )
        .unwrap();
    let indexdef: String = row.get(0);
    assert!(
        indexdef.contains("USING hnsw") && indexdef.contains("vector_cosine_ops"),
        "expected HNSW cosine index, got: {indexdef}"
    );
    assert!(
        indexdef.contains("m = 16") && indexdef.contains("ef_construction = 200"),
        "expected m=16 ef_construction=200, got: {indexdef}"
    );
}

/// code_elements: no PRIMARY KEY (cozo not keyed — duplicates allowed), but
/// the four cozo-mirroring indexes exist.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn code_elements_has_no_pk_and_expected_indexes() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();

    let pk: i64 = s
        .client
        .query_one(
            "SELECT count(*) FROM information_schema.table_constraints
             WHERE table_schema = $1 AND table_name = 'code_elements' AND constraint_type = 'PRIMARY KEY'",
            &[&s.name],
        )
        .unwrap()
        .get(0);
    assert_eq!(pk, 0, "code_elements must have NO primary key");

    let idx: Vec<String> = s
        .client
        .query(
            "SELECT indexname FROM pg_indexes
             WHERE schemaname = $1 AND tablename = 'code_elements' ORDER BY indexname",
            &[&s.name],
        )
        .unwrap()
        .iter()
        .map(|r| r.get::<_, String>(0))
        .collect();
    for expected in [
        "code_elements_file_path_index",
        "code_elements_qualified_name_index",
        "code_elements_element_type_index",
        "code_elements_parent_qualified_index",
    ] {
        assert!(
            idx.contains(&expected.to_string()),
            "missing {expected}; got: {idx:?}"
        );
    }

    // Duplicate qualified_name rows must be allowed (cozo non-keyed semantics,
    // risk note 6).
    let ins = "INSERT INTO code_elements (qualified_name, element_type, name, file_path, line_start, line_end, language) VALUES ($1, 'function', 'f', 'f.rs', 1, 2, 'rust')";
    s.client.execute(ins, &[&"a::b"]).unwrap();
    s.client.execute(ins, &[&"a::b"]).unwrap();
    let n: i64 = s
        .client
        .query_one("SELECT count(*) FROM code_elements", &[])
        .unwrap()
        .get(0);
    assert_eq!(n, 2, "duplicate qualified_name rows must be allowed");
}

/// Keyed tables get PRIMARY KEYs; JSONB conversions hold.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn keyed_tables_have_pk_and_jsonb_columns() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();

    for (table, pk_col) in [
        ("embedding_state", "qualified_name"),
        ("embedding_vectors", "qualified_name"),
        ("index_inventory", "key"),
        ("index_hashes", "path"),
        ("migrations", "id"),
    ] {
        let col: Option<String> = s
            .client
            .query_opt(
                "SELECT kcu.column_name
                 FROM information_schema.table_constraints tc
                 JOIN information_schema.key_column_usage kcu
                   ON tc.constraint_name = kcu.constraint_name
                  AND tc.table_schema = kcu.table_schema
                 WHERE tc.table_schema = $1 AND tc.table_name = $2
                   AND tc.constraint_type = 'PRIMARY KEY'",
                &[&s.name, &table],
            )
            .unwrap()
            .map(|r| r.get(0));
        assert_eq!(
            col.as_deref(),
            Some(pk_col),
            "{table} must have PRIMARY KEY on {pk_col}"
        );
    }

    let jsonb: Vec<(String, String)> = s
        .client
        .query(
            "SELECT table_name, column_name FROM information_schema.columns
             WHERE table_schema = $1 AND data_type = 'jsonb'
             ORDER BY table_name, column_name",
            &[&s.name],
        )
        .unwrap()
        .iter()
        .map(|r| (r.get::<_, String>(0), r.get::<_, String>(1)))
        .collect();
    for (table, col) in [
        ("code_elements", "metadata"),
        ("relationships", "metadata"),
        ("teams", "members"),
        ("teams", "graph_read_users"),
        ("teams", "graph_write_users"),
        ("service_metadata", "tags"),
        ("service_metadata", "deploy_envs"),
        ("incidents", "tags"),
        ("incidents", "affected_services"),
        ("knowledge_entries", "tags"),
        ("index_inventory", "elements_by_type_json"),
        ("index_inventory", "relationships_by_type_json"),
        ("index_inventory", "vectors_by_type_json"),
    ] {
        assert!(
            jsonb.contains(&(table.to_string(), col.to_string())),
            "expected {table}.{col} as jsonb; got: {jsonb:?}"
        );
    }

    // JSONB round-trip of what cozo stored as a JSON string. Read back as a
    // TEXT value so we get the canonical jsonb rendering and compare in JSON
    // space (avoids needing the `serde-1` feature on the postgres crate).
    s.client
        .query_one(
            "INSERT INTO code_elements (qualified_name, element_type, name, file_path, line_start, line_end, language, metadata)
             VALUES ($1, 'function', 'f', 'f.rs', 1, 2, 'rust', $2::jsonb) RETURNING qualified_name",
            &[&"jsonb/probe", &r#"{"lang":"rust","hits":3}"#],
        )
        .unwrap();
    let meta_text: String = s
        .client
        .query_one(
            "SELECT metadata::text FROM code_elements WHERE qualified_name = 'jsonb/probe'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&meta_text).unwrap()["hits"],
        3
    );
}

/// migrations table records applied steps with a real timestamp (applied_at
/// is TIMESTAMPTZ, not the cozo Int epoch). We can't decode TIMESTAMPTZ with
/// the postgres crate's built-in types (needs the `with-chrono-*` feature, not
/// enabled here), so assert its type via information_schema instead.
#[test]
#[ignore = "requires the leankg-pg-phase0 container (localhost:5433)"]
fn migrations_table_records_applied_at() {
    let mut s = ScratchSchema::new();
    leankg::db::pg::migrations::run_migrations(&mut s.client).unwrap();

    let row = s
        .client
        .query_one(
            "SELECT data_type, is_nullable FROM information_schema.columns
             WHERE table_schema = $1 AND table_name = 'migrations' AND column_name = 'applied_at'",
            &[&s.name],
        )
        .unwrap();
    let data_type: String = row.get(0);
    let is_nullable: String = row.get(1);
    assert_eq!(data_type, "timestamp with time zone");
    assert_eq!(
        is_nullable, "YES",
        "applied_at must be nullable (DEFAULT now())"
    );

    let count: i64 = s
        .client
        .query_one(
            "SELECT count(*) FROM migrations WHERE id = '001_schema'",
            &[],
        )
        .unwrap()
        .get(0);
    assert_eq!(count, 1);
}
