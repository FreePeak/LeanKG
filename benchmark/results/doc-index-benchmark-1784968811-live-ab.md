# Live A/B Benchmark: Doc Indexing

**Before**: 55 ms baseline avg
**After**: 15 ms after avg
**Duration**: 757.1s
**Docs Indexed**: 0
**Elements Created**: 0
**Relationships Created**: 0
**Timestamp**: 1784968811

| Query | Type | Before (ms) | Before (results) | After (ms) | After (results) | Delta (ms) | Delta (results) |
|-------|------|-------------|------------------|------------|-----------------|------------|----------------|
| search_code:documentation | search_code | 299.9 | 50 | 23.8 | 50 | -276.1 | +0 (same) |
| search_code:knowledge graph | search_code | 85.9 | 8 | 32.9 | 10 | -53.0 | +2 (improved) |
| search_code:How to index | search_code | 26.8 | 0 | 32.1 | 0 | 5.3 | +0 (same) |
| concept_search:documentation | concept_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |
| concept_search:code indexing | concept_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |
| find_function:index_docs | find_function | 26.7 | 7 | 32.5 | 7 | 5.9 | +0 (same) |
| semantic_search:document ... | semantic_search | 0.0 | 0 | 0.0 | 0 | -0.0 | +0 (same) |
| semantic_search:markdown ... | semantic_search | 0.0 | 0 | 0.0 | 0 | 0.0 | +0 (same) |

**Avg Latency Delta**: -39.7 ms (faster)
