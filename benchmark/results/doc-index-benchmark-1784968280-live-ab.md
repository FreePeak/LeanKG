# Live A/B Benchmark: Doc Indexing

**Before**: 5 ms baseline avg
**After**: 5 ms after avg
**Duration**: 6.5s
**Docs Indexed**: 0
**Elements Created**: 0
**Relationships Created**: 0
**Timestamp**: 1784968280

| Query | Type | Before (ms) | Before (results) | After (ms) | After (results) | Delta (ms) | Delta (results) |
|-------|------|-------------|------------------|------------|-----------------|------------|----------------|
| search_code:documentation | search_code | 10.0 | 0 | 10.2 | 0 | 0.1 | +0 (same) |
| search_code:knowledge graph | search_code | 10.3 | 0 | 10.1 | 0 | -0.3 | +0 (same) |
| search_code:How to index | search_code | 9.6 | 0 | 9.8 | 0 | 0.2 | +0 (same) |
| concept_search:documentation | concept_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |
| concept_search:code indexing | concept_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |
| find_function:index_docs | find_function | 9.3 | 0 | 10.6 | 0 | 1.3 | +0 (same) |
| semantic_search:document ... | semantic_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |
| semantic_search:markdown ... | semantic_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |

**Avg Latency Delta**: 0.2 ms (slower)
