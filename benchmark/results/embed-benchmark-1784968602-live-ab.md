# Live A/B Benchmark: Embedding

**Before**: 32 ms avg latency
**After**: 10 ms avg latency
**Embed Throughput**: 20082.7 vectors/sec
**Timestamp**: 1784968602

## Semantic Search Quality

| Query | Before (ms) | Before (results) | Before F1 | After (ms) | After (results) | After F1 | Delta (ms) | Delta F1 |
|-------|-------------|------------------|-----------|------------|-----------------|----------|------------|----------|
| semantic_search:doc... | 75.8 | 0 | 0.00 | 9.7 | 0 | 0.00 | -66.2 | 0.00 |
| semantic_search:mar... | 9.5 | 0 | 0.00 | 9.7 | 0 | 0.00 | 0.1 | 0.00 |
| semantic_search:kno... | 9.6 | 0 | 0.00 | 9.8 | 0 | 0.00 | 0.2 | 0.00 |

**Average Delta**: latency -22.0 ms, F1 0.00 (unchanged)
