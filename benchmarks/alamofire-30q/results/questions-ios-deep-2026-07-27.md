# Alamofire 30-Question 3-Way Benchmark Report

**Date:** 2026-07-27
**Repo:** Alamofire (Swift)
**Method:** `claude -p` headless; 3 arms: LeanKG MCP / CodeGraph MCP / No graph (built-in Read/Grep/Bash)
**Total valid runs:** 71 | Dropped: 4

## Per-Arm Summary (median across 30 questions)

| Arm | Runs | Tool calls | Time | File reads | Input tok | Output tok | Total tok | turns | Cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| **LeanKG** | 23 | 10 | 3m20s | 3 | 45,681 | 3,459 | 49,140 | 11 | $0.45 |
| **CodeGraph** | 23 | 10 | 3m4s | 1 | 40,914 | 4,875 | 45,789 | 11 | $0.45 |
| **No Graph** | 25 | 13 | 3m53s | 5 | 29,941 | 3,637 | 33,578 | 12 | $0.47 |

## Efficiency Gains vs No Graph (baseline)

| Metric | LeanKG vs None | CodeGraph vs None | LeanKG vs CodeGraph |
| --- | --- | --- | --- |
| Total tokens | +46% | +36% | +7% |
| Input tokens | +53% | +37% | +12% |
| Wall-clock time | -14% | -21% | +9% |
| Tool calls | -23% | -23% | +0% |
| File reads | -40% | -80% | +200% |
| Cost | -4% | -3% | -1% |
| Agent turns | -8% | -8% | +0% |

## Per-Question Results (median per arm)

### D01 (Protocol)

_How do URLConvertible and URLRequestConvertible work together to build a URLRequest? Explain protoco..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m52s | 25,981 / 2,752 | $0.25 | 7 | 3 | 8 |
| CodeGraph | 1 | 1m59s | 20,102 / 2,535 | $0.20 | 5 | 2 | 6 |
| No Graph | 1 | 1m57s | 18,030 / 2,657 | $0.21 | 6 | 3 | 7 |

### D02 (Protocol)

_How is RequestInterceptor composed from RequestAdapter and RequestRetrier? Trace adapt → retry acros..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 0 | N/A | N/A | N/A | N/A | N/A | N/A |
| CodeGraph | 1 | 2m19s | 62,472 / 5,574 | $0.50 | 7 | 0 | 8 |
| No Graph | 1 | 3m8s | 23,129 / 4,552 | $0.32 | 10 | 2 | 11 |

### D03 (Protocol)

_How does ServerTrustEvaluating model certificate pinning? Compare DefaultTrustEvaluator, PublicKeysT..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 1m49s | 24,294 / 2,320 | $0.19 | 2 | 1 | 3 |
| CodeGraph | 1 | 6m39s | 5,102 / 220 | $0.79 | 5 | 1 | 1 |
| No Graph | 1 | 7m33s | 1,581 / 106 | $1.39 | 11 | 2 | 1 |

### D04 (NativeIOS)

_SessionDelegate is an NSObject subclass that implements URLSessionDelegate families. How does Alamof..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 4m3s | 27,860 / 5,954 | $0.35 | 11 | 4 | 12 |
| CodeGraph | 1 | 5m19s | 28,193 / 4,012 | $0.33 | 10 | 4 | 11 |
| No Graph | 1 | 3m11s | 29,941 / 4,590 | $0.34 | 13 | 7 | 14 |

### D05 (Protocol)

_Explain the EventMonitor protocol surface: which lifecycle hooks exist for request creation, resume,..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m4s | 28,995 / 3,033 | $0.27 | 6 | 1 | 7 |
| CodeGraph | 0 | N/A | N/A | N/A | N/A | N/A | N/A |
| No Graph | 1 | 6m0s | 62,873 / 2,286 | $0.44 | 10 | 2 | 12 |

### D06 (Protocol)

_How do AuthenticationCredential and Authenticator cooperate with AuthenticationInterceptor? Detail a..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m17s | 24,940 / 3,225 | $0.22 | 4 | 2 | 5 |
| CodeGraph | 1 | 7m9s | 7,207 / 568 | $0.70 | 5 | 1 | 1 |
| No Graph | 1 | 2m6s | 19,689 / 2,559 | $0.19 | 4 | 1 | 5 |

### D07 (NativeIOS)

_How does Protected<T> achieve thread-safe mutable state without actors? Describe the Lock protocol, ..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 0 | N/A | N/A | N/A | N/A | N/A | N/A |
| CodeGraph | 0 | N/A | N/A | N/A | N/A | N/A | N/A |
| No Graph | 1 | 5m50s | 74,917 / 2,300 | $0.48 | 9 | 4 | 11 |

### D08 (Protocol)

_Walk the ResponseSerializer protocol hierarchy: DataResponseSerializerProtocol, DownloadResponseSeri..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 33s | 36,800 / 2,456 | $0.25 | 3 | 1 | 4 |
| CodeGraph | 1 | 2m5s | 21,409 / 2,587 | $0.19 | 3 | 1 | 4 |
| No Graph | 1 | 2m38s | 23,654 / 3,637 | $0.24 | 5 | 1 | 6 |

### D09 (NativeIOS)

_How does Alamofire's Concurrency module bridge callback-based Request APIs to async/await? Explain c..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 3m29s | 47,865 / 2,945 | $0.38 | 8 | 1 | 10 |
| CodeGraph | 1 | 5m7s | 56,863 / 4,661 | $0.45 | 9 | 1 | 10 |
| No Graph | 1 | 2m18s | 29,976 / 3,227 | $0.34 | 10 | 4 | 11 |

### D10 (Protocol)

_How do RedirectHandler and CachedResponseHandler plug into URLSession delegate callbacks? Contrast R..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 4m34s | 52,388 / 3,914 | $0.41 | 10 | 5 | 11 |
| CodeGraph | 1 | 2m40s | 59,261 / 3,103 | $0.44 | 11 | 2 | 13 |
| No Graph | 1 | 5m0s | 70,057 / 2,263 | $0.69 | 26 | 18 | 18 |

### D11 (NativeIOS)

_How does Request's State machine interact with URLSessionTask suspend/resume/ cancel? Map Alamofire ..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 1m33s | 45,681 / 2,544 | $0.34 | 5 | 1 | 6 |
| CodeGraph | 1 | 3m47s | 28,397 / 3,997 | $0.26 | 3 | 1 | 4 |
| No Graph | 1 | 2m41s | 19,330 / 4,689 | $0.28 | 14 | 5 | 15 |

### D12 (Protocol)

_How does AlamofireExtended provide the `.af` namespace on Foundation types without polluting global ..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m40s | 60,534 / 2,609 | $0.45 | 15 | 2 | 17 |
| CodeGraph | 1 | 1m10s | 21,778 / 2,525 | $0.20 | 8 | 4 | 9 |
| No Graph | 1 | 45s | 17,249 / 2,132 | $0.18 | 7 | 3 | 8 |

### D13 (NativeIOS)

_How does WebSocketRequest wrap URLSessionWebSocketTask? Cover connect, send/receive, ping/pong, clos..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m6s | 44,030 / 4,146 | $0.35 | 6 | 3 | 7 |
| CodeGraph | 1 | 2m1s | 23,945 / 4,875 | $0.28 | 7 | 3 | 8 |
| No Graph | 1 | 58s | 23,632 / 4,873 | $0.29 | 10 | 4 | 11 |

### D14 (Protocol)

_Explain UploadableConvertible vs UploadConvertible and how UploadRequest selects data / file / strea..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 4m22s | 64,730 / 3,158 | $0.47 | 15 | 4 | 17 |
| CodeGraph | 1 | 1m52s | 40,914 / 4,087 | $0.38 | 10 | 0 | 11 |
| No Graph | 1 | 4m21s | 44,756 / 3,869 | $0.44 | 13 | 8 | 14 |

### D15 (NativeIOS)

_How does Session enqueue work onto rootQueue vs underlying URLSession delegate callbacks? Discuss se..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 3m20s | 34,804 / 6,829 | $0.46 | 10 | 6 | 11 |
| CodeGraph | 1 | 1m54s | 51,011 / 6,572 | $0.47 | 10 | 0 | 11 |
| No Graph | 1 | 3m27s | 36,266 / 7,427 | $0.64 | 20 | 12 | 21 |

## Variance Appendix (IQR across runs per arm)

| Question | Arm | Cost IQR | Latency IQR | Token IQR |
| --- | --- | --- | --- | --- |

## Dropped Runs

4 run(s) excluded.

| Q | Arm | Run | Model | Reason |
| --- | --- | --- | --- | --- |
| D05 | codegraph | 1 | MiniMax-M3[1m] | exit_code=1|exit_code=1 |
| D07 | codegraph | 1 | MiniMax-M3[1m] | exit_code=1|exit_code=1 |
| D02 | leankg | 1 | MiniMax-M3[1m] | exit_code=1|exit_code=1 |
| D07 | leankg | 1 | MiniMax-M3[1m] | exit_code=1|exit_code=1 |

## Methodology

- 15 architecture questions covering Alamofire (Swift).
- Each arm = `claude -p` headless with `--strict-mcp-config`, `--output-format json`, `--dangerously-skip-permissions`.
- LeanKG index rebuilt before its arm; CodeGraph index pre-built.
- N=3 runs per arm per question; median reported.
- Metrics parsed from claude CLI JSON envelope (v2.1.201+).

## Caveats

- Self-reported single-vendor benchmark. Treat as best-case.
- LeanKG Swift extraction is regex-based (no tree-sitter); under-reports call graph edges.
- Cost/token numbers depend on model version; pin with `--model` for reproducibility.
- Small sample (N=3); high variance expected. IQR appendix shows spread.
