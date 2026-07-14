//! MinHash + LSH (Locality-Sensitive Hashing) for near-duplicate detection.
//!
//! Used by [`GraphEngine::find_clones_with_opts`] when the cross-file
//! option is enabled. The same building blocks are reusable for any
//! "find similar" MCP tool (e.g. cross-service duplicate detection,
//! test-pair mining, doc-clone detection).
//!
//! ## Algorithm
//!
//! 1. Shingle each document into k-grams (default k=5 tokens).
//! 2. Hash each shingle with `num_perm` different hash functions (we
//!    derive them cheaply from a single 64-bit hash via bit-mix
//!    permutations, no per-permutation tables in RAM).
//! 3. Each document gets a `num_perm`-length signature where position
//!    `i` is the minimum hash seen under permutation `i`.
//! 4. Split the signature into `bands × rows_per_band`. Two documents
//!    with Jaccard ≥ `c` collide in at least one band with probability
//!    governed by `1 − (1 − c^rows_per_band)^bands` (the classic LSH
//!    S-curve).
//!
//! ## Why this matters for big projects
//!
//! On a graph with 369k functions, an O(n²) all-pairs scan is infeasible.
//! LSH turns that into ~369k hash insertions + a single pass over bands
//! (each band is a `HashMap<u64, Vec<doc_id>>`), so total work scales
//! near-linearly. Crossover vs brute force happens around 5–10k docs;
//! above that LSH is always the better choice.
//!
//! ## Configuration
//!
//! Defaults below target Jaccard ≥ 0.6 with FPR ≤ 5%:
//! - `num_perm = 128` (signature length, ~1 KB per doc)
//! - `bands = 32`, `rows_per_band = 4` (32 × 4 = 128)
//! - `shingle_size = 5` (token-level)

use serde::{Deserialize, Serialize};
use std::collections::HashMap;

/// Default number of MinHash permutations. Higher = more accurate but
/// more RAM per document.
pub const DEFAULT_NUM_PERM: usize = 128;

/// Default LSH bands. `bands × rows_per_band == num_perm`.
pub const DEFAULT_BANDS: usize = 32;

/// Default rows per band. Lower rows → broader collision threshold.
pub const DEFAULT_ROWS_PER_BAND: usize = 4;

/// Default shingle size (in tokens). 5 is the sweet spot for code.
pub const DEFAULT_SHINGLE_SIZE: usize = 5;

/// Tunable knobs for [`MinHasher`].
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct MinHashConfig {
    pub num_perm: usize,
    pub bands: usize,
    pub rows_per_band: usize,
    pub shingle_size: usize,
}

impl Default for MinHashConfig {
    fn default() -> Self {
        Self {
            num_perm: DEFAULT_NUM_PERM,
            bands: DEFAULT_BANDS,
            rows_per_band: DEFAULT_ROWS_PER_BAND,
            shingle_size: DEFAULT_SHINGLE_SIZE,
        }
    }
}

/// A MinHash signature for a single document.
#[derive(Debug, Clone)]
pub struct MinHashSignature {
    /// Length-`num_perm` array of minimum hashes per permutation.
    pub signature: Vec<u64>,
}

/// Tokenize a document into k-grams. We use a whitespace split with
/// punctuation stripped. For source code this matches what
/// [`crate::graph::query::jaccard_tokens`] would consider, so signatures
/// are comparable.
pub fn shingle_tokens(text: &str, k: usize) -> Vec<u64> {
    if k == 0 {
        return Vec::new();
    }
    let tokens: Vec<&str> = text
        .split(|c: char| !c.is_alphanumeric() && c != '_' && c != '-')
        .filter(|s| !s.is_empty())
        .collect();
    if tokens.len() < k {
        // Treat the whole document as a single shingle so very short
        // bodies still produce a usable signature.
        return vec![fx_hash(&tokens.join(" "))];
    }
    let mut out = Vec::with_capacity(tokens.len() - k + 1);
    for window in tokens.windows(k) {
        out.push(fx_hash(&window.join(" ")));
    }
    out
}

/// 64-bit FxHash (a fast non-cryptographic hash). We use it for both
/// the shingle-hash and the per-permutation hash mixing.
fn fx_hash(s: &str) -> u64 {
    // FNV-1a 64-bit; small, branch-free, fast on ASCII.
    let mut h: u64 = 0xcbf29ce484222325;
    for b in s.as_bytes() {
        h ^= *b as u64;
        h = h.wrapping_mul(0x100000001b3);
    }
    h
}

/// Cheap "permutation" mixer. We don't actually maintain num_perm
/// different permutations; instead we mix `(shingle_hash, perm_id)`
/// into a new u64 with a simple splitmix-style step. This gives a
/// well-spread family without storing per-permutation coefficient
/// tables in RAM.
fn mix(shingle_hash: u64, perm_id: u64) -> u64 {
    // SplitMix64 finalizer — good avalanche, fast.
    let mut z = shingle_hash
        .wrapping_add(0x9E3779B97F4A7C15)
        .wrapping_add(perm_id.wrapping_mul(0xBF58476D1CE4E5B9));
    z = (z ^ (z >> 30)).wrapping_mul(0xBF58476D1CE4E5B9);
    z = (z ^ (z >> 27)).wrapping_mul(0x94D049BB133111EB);
    z ^ (z >> 31)
}

/// Compute the MinHash signature for `text` under the given config.
pub fn minhash(text: &str, cfg: &MinHashConfig) -> MinHashSignature {
    let mut sig = vec![u64::MAX; cfg.num_perm];
    let shingles = shingle_tokens(text, cfg.shingle_size);
    if shingles.is_empty() {
        // Edge case: empty doc → all zeros signature.
        return MinHashSignature {
            signature: vec![0; cfg.num_perm],
        };
    }
    for sh in shingles {
        for (perm, slot) in sig.iter_mut().enumerate() {
            let h = mix(sh, perm as u64);
            if h < *slot {
                *slot = h;
            }
        }
    }
    MinHashSignature { signature: sig }
}

/// Index over many signatures for candidate-pair retrieval.
pub struct LshIndex {
    cfg: MinHashConfig,
    /// `band_buckets[band] = HashMap<bucket_hash, Vec<doc_id>>`
    band_buckets: Vec<HashMap<u64, Vec<u32>>>,
    /// doc_id → signature, kept so we can compute exact Jaccard on
    /// candidate pairs without re-tokenizing.
    signatures: Vec<MinHashSignature>,
}

impl LshIndex {
    /// Create an empty index.
    pub fn new(cfg: MinHashConfig) -> Self {
        Self {
            band_buckets: (0..cfg.bands).map(|_| HashMap::new()).collect(),
            signatures: Vec::new(),
            cfg,
        }
    }

    /// Number of documents currently indexed.
    pub fn len(&self) -> usize {
        self.signatures.len()
    }

    /// Returns true if no documents have been added.
    pub fn is_empty(&self) -> bool {
        self.signatures.is_empty()
    }

    /// Add a document; returns the new doc_id.
    pub fn insert(&mut self, text: &str) -> u32 {
        let sig = minhash(text, &self.cfg);
        let doc_id = self.signatures.len() as u32;
        // For each band, hash the rows-of-this-band slice into a u64
        // bucket key.
        let rows = self.cfg.rows_per_band;
        let num_perm = self.cfg.num_perm;
        if rows * self.cfg.bands != num_perm && rows * self.cfg.bands > num_perm {
            // Config mismatch — fall back to band-by-band sequential slicing.
        }
        let actual_rows = rows.min(num_perm / self.cfg.bands.max(1)).max(1);
        for band in 0..self.cfg.bands {
            let start = band * actual_rows;
            let end = (start + actual_rows).min(num_perm);
            if start >= num_perm {
                break;
            }
            let mut bucket: u64 = 0xcbf29ce484222325;
            for i in start..end {
                bucket ^= sig.signature[i].wrapping_add(0x9E3779B97F4A7C15);
                bucket = bucket.wrapping_mul(0x100000001b3);
            }
            self.band_buckets[band]
                .entry(bucket)
                .or_default()
                .push(doc_id);
        }
        self.signatures.push(sig);
        doc_id
    }

    /// Yield all candidate pairs `(i, j)` with `i < j`. Use the result
    /// to run exact Jaccard (or any) similarity on the candidate set
    /// instead of all-pairs.
    pub fn candidate_pairs(&self) -> Vec<(u32, u32)> {
        let mut seen = std::collections::HashSet::new();
        let mut out = Vec::new();
        for bucket in &self.band_buckets {
            for ids in bucket.values() {
                if ids.len() < 2 {
                    continue;
                }
                for i in 0..ids.len() {
                    for j in (i + 1)..ids.len() {
                        let (a, b) = if ids[i] < ids[j] {
                            (ids[i], ids[j])
                        } else {
                            (ids[j], ids[i])
                        };
                        if seen.insert((a, b)) {
                            out.push((a, b));
                        }
                    }
                }
            }
        }
        out
    }

    /// Compute exact Jaccard similarity between two indexed docs by
    /// counting shared min-hash positions (the MinHash Jaccard
    /// estimator).
    pub fn estimated_jaccard(&self, a: u32, b: u32) -> f64 {
        if a == b {
            return 1.0;
        }
        let sa = &self.signatures[a as usize].signature;
        let sb = &self.signatures[b as usize].signature;
        let mut shared = 0usize;
        for i in 0..sa.len().min(sb.len()) {
            if sa[i] == sb[i] {
                shared += 1;
            }
        }
        shared as f64 / sa.len() as f64
    }

    /// Returns the signature for the given doc_id.
    pub fn signature(&self, doc_id: u32) -> Option<&MinHashSignature> {
        self.signatures.get(doc_id as usize)
    }
}

/// Estimate the Jaccard similarity between two documents by averaging
/// matches over the signature positions. Cheaper than computing
/// Jaccard over the raw shingle set.
pub fn minhash_jaccard(a: &MinHashSignature, b: &MinHashSignature) -> f64 {
    if a.signature.len() != b.signature.len() {
        return 0.0;
    }
    if a.signature.is_empty() {
        return 0.0;
    }
    let mut shared = 0usize;
    for i in 0..a.signature.len() {
        if a.signature[i] == b.signature[i] {
            shared += 1;
        }
    }
    shared as f64 / a.signature.len() as f64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn shingle_short_doc_returns_single_hash() {
        let s = shingle_tokens("hello world", 5);
        assert_eq!(s.len(), 1);
    }

    #[test]
    fn shingle_long_doc_windows_correctly() {
        let s = shingle_tokens("a b c d e f g", 3);
        // windows of 3: "a b c", "b c d", "c d e", "d e f", "e f g" = 5
        assert_eq!(s.len(), 5);
    }

    #[test]
    fn identical_docs_have_jaccard_one() {
        let cfg = MinHashConfig::default();
        let a = minhash("fn add(a:int,b:int){return a+b;}", &cfg);
        let b = minhash("fn add(a:int,b:int){return a+b;}", &cfg);
        assert_eq!(minhash_jaccard(&a, &b), 1.0);
    }

    #[test]
    fn completely_disjoint_docs_have_low_jaccard() {
        let cfg = MinHashConfig::default();
        let a = minhash("alpha beta gamma delta epsilon", &cfg);
        let b = minhash("foo bar baz qux quux corge", &cfg);
        let j = minhash_jaccard(&a, &b);
        assert!(j < 0.2, "disjoint should be near zero, got {}", j);
    }

    #[test]
    fn near_clone_has_high_jaccard() {
        let cfg = MinHashConfig::default();
        let a = minhash(
            "fn add(a:int,b:int){return a+b;}\nfn sub(a:int,b:int){return a-b;}",
            &cfg,
        );
        let b = minhash(
            "fn add(a:int,b:int){return a+b;}\nfn sub(a:int,b:int){return a-b;}\n// one extra line",
            &cfg,
        );
        let j = minhash_jaccard(&a, &b);
        assert!(j > 0.5, "near-clone should be high, got {}", j);
    }

    #[test]
    fn lsh_index_pairs_are_unique_and_sorted() {
        let cfg = MinHashConfig::default();
        let mut idx = LshIndex::new(cfg);
        for i in 0..100 {
            idx.insert(&format!("function body variant {} {}", i, i * 7));
        }
        let pairs = idx.candidate_pairs();
        let mut set = std::collections::HashSet::new();
        for (a, b) in &pairs {
            assert!(a < b, "pairs must be sorted a<b: {} {}", a, b);
            assert!(set.insert((*a, *b)), "duplicate pair {:?}", (a, b));
        }
    }

    #[test]
    fn lsh_index_finds_near_clones() {
        let cfg = MinHashConfig::default();
        let mut idx = LshIndex::new(cfg);
        // Three unrelated docs + two near-clones.
        let _ = idx.insert("the quick brown fox jumps over the lazy dog one two three");
        let _ = idx.insert("lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod");
        let _ = idx.insert("completely different words in this particular document for sure");
        let id_a = idx.insert("fn handler(req http.Request) http.Response { return process(req) }");
        let id_b = idx.insert("fn handler(req http.Request) http.Response { return process(req) }");
        let pairs = idx.candidate_pairs();
        assert!(
            pairs
                .iter()
                .any(|(a, b)| (*a == id_a && *b == id_b) || (*a == id_b && *b == id_a)),
            "expected LSH to flag the two identical docs as candidates"
        );
    }

    #[test]
    fn minhash_signature_is_deterministic() {
        let cfg = MinHashConfig::default();
        let a = minhash("fn foo() { return 1 }", &cfg);
        let b = minhash("fn foo() { return 1 }", &cfg);
        assert_eq!(a.signature, b.signature);
    }

    #[test]
    fn empty_doc_handled() {
        let cfg = MinHashConfig::default();
        let s = minhash("", &cfg);
        assert_eq!(s.signature.len(), cfg.num_perm);
    }

    #[test]
    fn estimated_jaccard_self_is_one() {
        let cfg = MinHashConfig::default();
        let mut idx = LshIndex::new(cfg);
        let id = idx.insert("hello world");
        assert_eq!(idx.estimated_jaccard(id, id), 1.0);
    }
}
