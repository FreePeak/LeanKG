//! Redis Stack (RediSearch HNSW) side-store for embedding vectors.
//!
//! Enabled when `LEANKG_EMBED_VECTOR_STORE=redis` (and optionally
//! `LEANKG_REDIS_URL`, default `redis://127.0.0.1:6379`).
//!
//! Cold embed: drop HNSW → pipeline HSET → rebuild HNSW (same pattern as Cozo).

use std::sync::Mutex;

const DEFAULT_URL: &str = "redis://127.0.0.1:6379";
const INDEX_NAME: &str = "leankg_emb";
const KEY_PREFIX: &str = "leankg:emb:";
const DIM: usize = 384;

/// True when env requests Redis as the embedding vector store.
pub fn redis_vector_store_enabled() -> bool {
    match std::env::var("LEANKG_EMBED_VECTOR_STORE") {
        Ok(v) => {
            let t = v.trim().to_ascii_lowercase();
            t == "redis" || t == "1" || t == "true"
        }
        Err(_) => false,
    }
}

fn redis_url() -> String {
    std::env::var("LEANKG_REDIS_URL").unwrap_or_else(|_| DEFAULT_URL.to_string())
}

/// Shared connection for the embed writer thread.
pub struct RedisVectorStore {
    conn: Mutex<redis::Connection>,
}

impl RedisVectorStore {
    pub fn connect() -> Result<Self, String> {
        let url = redis_url();
        let client = redis::Client::open(url.as_str()).map_err(|e| format!("redis client: {e}"))?;
        let conn = client
            .get_connection()
            .map_err(|e| format!("redis connect ({url}): {e}"))?;
        Ok(Self {
            conn: Mutex::new(conn),
        })
    }

    /// Drop the HNSW index (keep HASH keys) so bulk HSET is fast.
    pub fn drop_index_keep_docs(&self) -> Result<(), String> {
        let mut conn = self.conn.lock().map_err(|e| format!("redis mutex: {e}"))?;
        match redis::cmd("FT.DROPINDEX")
            .arg(INDEX_NAME)
            .query::<()>(&mut conn)
        {
            Ok(()) => Ok(()),
            Err(e) => {
                let m = e.to_string();
                if m.contains("Unknown Index")
                    || m.contains("no such index")
                    || m.contains("Unknown index")
                {
                    Ok(())
                } else {
                    Err(format!("FT.DROPINDEX: {m}"))
                }
            }
        }
    }

    /// Recreate HNSW over existing HASH docs after bulk load.
    pub fn rebuild_index(&self) -> Result<(), String> {
        let mut conn = self.conn.lock().map_err(|e| format!("redis mutex: {e}"))?;
        ensure_index(&mut conn)
    }

    /// Upsert a batch of (qualified_name, vector) pairs via a Redis pipeline.
    pub fn upsert_pairs(&self, pairs: &[(String, Vec<f32>)]) -> Result<(), String> {
        if pairs.is_empty() {
            return Ok(());
        }
        let mut conn = self.conn.lock().map_err(|e| format!("redis mutex: {e}"))?;
        let mut pipe = redis::pipe();
        pipe.atomic();
        for (qn, vec) in pairs {
            if vec.len() != DIM {
                return Err(format!(
                    "redis upsert: expected dim {DIM}, got {} for {qn}",
                    vec.len()
                ));
            }
            let key = format!("{KEY_PREFIX}{qn}");
            let blob = f32_slice_to_le_bytes(vec);
            pipe.cmd("HSET")
                .arg(&key)
                .arg("qn")
                .arg(qn)
                .arg("vector")
                .arg(blob);
        }
        pipe.query::<()>(&mut conn)
            .map_err(|e| format!("redis pipeline HSET: {e}"))?;
        Ok(())
    }

    /// KNN search; returns (qualified_name, score) — lower score is closer.
    pub fn knn_search(&self, query: &[f32], limit: usize) -> Result<Vec<(String, f64)>, String> {
        if query.len() != DIM {
            return Err(format!("knn: expected dim {DIM}, got {}", query.len()));
        }
        let mut conn = self.conn.lock().map_err(|e| format!("redis mutex: {e}"))?;
        let blob = f32_slice_to_le_bytes(query);
        let q = format!("*=>[KNN {limit} @vector $BLOB AS score]");
        let result: redis::Value = redis::cmd("FT.SEARCH")
            .arg(INDEX_NAME)
            .arg(&q)
            .arg("PARAMS")
            .arg(2)
            .arg("BLOB")
            .arg(blob)
            .arg("RETURN")
            .arg(2)
            .arg("qn")
            .arg("score")
            .arg("SORTBY")
            .arg("score")
            .arg("DIALECT")
            .arg(2)
            .query(&mut conn)
            .map_err(|e| format!("FT.SEARCH: {e}"))?;
        parse_ft_search_qn_score(result)
    }
}

fn ensure_index(conn: &mut redis::Connection) -> Result<(), String> {
    let create = redis::cmd("FT.CREATE")
        .arg(INDEX_NAME)
        .arg("ON")
        .arg("HASH")
        .arg("PREFIX")
        .arg(1)
        .arg(KEY_PREFIX)
        .arg("SCHEMA")
        .arg("qn")
        .arg("TEXT")
        .arg("vector")
        .arg("VECTOR")
        .arg("HNSW")
        .arg(6)
        .arg("TYPE")
        .arg("FLOAT32")
        .arg("DIM")
        .arg(DIM)
        .arg("DISTANCE_METRIC")
        .arg("COSINE")
        .query::<()>(conn);
    match create {
        Ok(()) => {
            tracing::info!("created Redis HNSW index {INDEX_NAME}");
            Ok(())
        }
        Err(e) => {
            let msg = e.to_string();
            if msg.contains("already exists") {
                Ok(())
            } else {
                Err(format!("FT.CREATE {INDEX_NAME}: {msg}"))
            }
        }
    }
}

fn f32_slice_to_le_bytes(v: &[f32]) -> Vec<u8> {
    let mut out = Vec::with_capacity(v.len() * 4);
    for f in v {
        out.extend_from_slice(&f.to_le_bytes());
    }
    out
}

fn parse_ft_search_qn_score(value: redis::Value) -> Result<Vec<(String, f64)>, String> {
    let arr = match value {
        redis::Value::Array(items) => items,
        other => return Err(format!("unexpected FT.SEARCH reply: {other:?}")),
    };
    if arr.is_empty() {
        return Ok(vec![]);
    }
    let mut out = Vec::new();
    let mut i = 1usize;
    while i + 1 < arr.len() {
        let fields = match &arr[i + 1] {
            redis::Value::Array(f) => f,
            _ => {
                i += 2;
                continue;
            }
        };
        let mut qn: Option<String> = None;
        let mut score: Option<f64> = None;
        let mut j = 0;
        while j + 1 < fields.len() {
            let name = value_as_string(&fields[j]);
            let val = value_as_string(&fields[j + 1]);
            if name == "qn" {
                qn = Some(val);
            } else if name == "score" {
                score = val.parse().ok();
            }
            j += 2;
        }
        if let (Some(qn), Some(score)) = (qn, score) {
            out.push((qn, score));
        }
        i += 2;
    }
    Ok(out)
}

fn value_as_string(v: &redis::Value) -> String {
    match v {
        redis::Value::BulkString(bytes) => String::from_utf8_lossy(bytes).into_owned(),
        redis::Value::SimpleString(s) => s.clone(),
        redis::Value::Okay => "OK".into(),
        redis::Value::Int(i) => i.to_string(),
        redis::Value::Nil => String::new(),
        other => format!("{other:?}"),
    }
}
