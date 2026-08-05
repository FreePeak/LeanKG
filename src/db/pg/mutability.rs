//! Script mutability classification (write vs read) for the Postgres
//! backend. Mirrors the historical cozo 0.7.x behavior: a Datalog script
//! can combine a read head (`?[...] := ...`) with an action operator
//! (`:put`, `:rm`, …) in one script, so the whole query is scanned for
//! write operators rather than just the leading token.

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ScriptMutability {
    Immutable,
    Mutable,
}

const WRITE_TOKENS: &[&str] = &[
    ":put",
    ":rm",
    ":create",
    ":replace",
    ":delete",
    ":update",
    ":insert",
    "PRAGMA",
    "::set_triggers",
    "::hnsw",
    "::lsh",
    "::fts",
    "::index",
];

pub fn mutability_for(query: &str) -> ScriptMutability {
    if WRITE_TOKENS.iter().any(|t| query.contains(t)) {
        ScriptMutability::Mutable
    } else {
        ScriptMutability::Immutable
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn read_query_is_immutable() {
        assert_eq!(
            mutability_for("?[a, b] := *code_elements[a, b]"),
            ScriptMutability::Immutable
        );
        assert_eq!(mutability_for("::relations"), ScriptMutability::Immutable);
    }

    #[test]
    fn write_queries_are_mutable() {
        assert_eq!(
            mutability_for("?[a, b] <- [[$a, $b]] :put code_elements {a, b}"),
            ScriptMutability::Mutable
        );
        assert_eq!(
            mutability_for("?[a] <- [[$a]] :rm code_elements {a}"),
            ScriptMutability::Mutable
        );
        assert_eq!(
            mutability_for(":create code_elements {a: String, b: String}"),
            ScriptMutability::Mutable
        );
        assert_eq!(
            mutability_for("::hnsw create embedding_vectors:vec_idx { dim: 384 }"),
            ScriptMutability::Mutable
        );
        assert_eq!(
            mutability_for("::index create code_elements:name_idx { name }"),
            ScriptMutability::Mutable
        );
    }

    #[test]
    fn combined_read_head_with_put_is_mutable() {
        assert_eq!(
            mutability_for("?[a, b] := *rel[a, b], b = $val :put rel2 {a, b}"),
            ScriptMutability::Mutable
        );
    }
}
