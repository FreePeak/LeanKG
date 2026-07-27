# Alamofire 30-Question 3-Way Benchmark Report

**Date:** 2026-07-27
**Repo:** Typhoon (Objective-C)
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

### T01 (Protocol)

_TyphoonAssembly is the user-facing protocol for declaring dependency injection assemblies. How does ..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m44s | 46,041 / 6,214 | $0.48 | 17 | 7 | 18 |
| CodeGraph | 1 | 5m51s | 83,230 / 6,610 | $0.74 | 10 | 0 | 12 |
| No Graph | 1 | 3m53s | 61,081 / 4,566 | $0.49 | 11 | 7 | 12 |

### T02 (Definition)

_TyphoonDefinition describes component lifecycle, scope (ObjectGraph/Prototype/ Singleton/LazySinglet..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 6m14s | 82,661 / 9,579 | $0.80 | 38 | 14 | 39 |
| CodeGraph | 1 | 2m55s | 65,238 / 5,785 | $0.56 | 11 | 0 | 12 |
| No Graph | 1 | 2m44s | 45,631 / 4,870 | $0.49 | 21 | 13 | 22 |

### T03 (Factory)

_How does TyphoonComponentFactory resolve circular dependencies? The factory maintains a TyphoonCallS..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 2m52s | 57,011 / 6,041 | $0.51 | 21 | 14 | 22 |
| CodeGraph | 1 | 5m42s | 101,966 / 6,249 | $0.80 | 10 | 0 | 12 |
| No Graph | 1 | 5m31s | 24,426 / 1,439 | $0.47 | 17 | 9 | 6 |

### T04 (AutoInjection)

_TyphoonAutoInjection defines macros (InjectedProtocol, InjectedClass) and TyphoonAutoInjectVisibilit..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 3m28s | 55,289 / 5,047 | $0.58 | 33 | 11 | 35 |
| CodeGraph | 1 | 2m30s | 31,163 / 5,798 | $0.40 | 19 | 14 | 20 |
| No Graph | 1 | 4m53s | 27,379 / 5,042 | $0.63 | 16 | 10 | 17 |

### T05 (Storyboard)

_How does TyphoonStoryboard integrate with UIStoryboard for dependency injection in view controllers?..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 8m29s | 7,577 / 3,459 | $2.35 | 2 | 0 | 1 |
| CodeGraph | 1 | 1m54s | 79,407 / 5,319 | $0.58 | 6 | 0 | 8 |
| No Graph | 1 | 10m46s | 28,720 / 3,503 | $1.56 | 2 | 0 | 1 |

### T06 (Configuration)

_How does TyphoonConfigPostProcessor handle plist/json/property-list config injection? Explain the Ty..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 1m55s | 49,952 / 7,539 | $0.50 | 19 | 14 | 20 |
| CodeGraph | 1 | 4m6s | 62,079 / 6,651 | $0.58 | 15 | 0 | 16 |
| No Graph | 1 | 5m15s | 44,234 / 2,651 | $0.62 | 31 | 17 | 7 |

### T07 (Injection)

_How does TyphoonInjectionContext manage injection scope and carry factory/runtime arguments through ..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 4m40s | 49,600 / 3,443 | $0.39 | 13 | 6 | 15 |
| CodeGraph | 1 | 3m47s | 36,372 / 4,517 | $0.41 | 20 | 17 | 21 |
| No Graph | 1 | 1m44s | 12,653 / 4,387 | $0.22 | 13 | 10 | 14 |

### T08 (Imports)

_Trace the #import dependency chain starting from the umbrella Typhoon.h header. It imports TyphoonAs..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 5m52s | 59,761 / 9,358 | $0.69 | 14 | 1 | 16 |
| CodeGraph | 1 | 6m39s | 73,305 / 13,258 | $1.04 | 61 | 43 | 62 |
| No Graph | 1 | 5m57s | 43,266 / 10,952 | $0.72 | 15 | 1 | 16 |

### T09 (Injection)

_How does TyphoonMethod bridge method injection with TyphoonParameterInjection? The TyphoonMethod cla..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 4m14s | 35,163 / 6,424 | $0.48 | 17 | 11 | 18 |
| CodeGraph | 1 | 3m4s | 62,951 / 6,428 | $0.59 | 12 | 0 | 13 |
| No Graph | 1 | 5m51s | 60,863 / 5,577 | $1.17 | 65 | 39 | 23 |

### T10 (Testing)

_How does TyphoonPatcher patch assembly definitions at runtime for testing? TyphoonPatcher extends Ty..._

| Arm | Runs | Latency | Tokens (in/out) | Cost | Tools | Reads | Turns |
| --- | --- | --- | --- | --- | --- | --- | --- |
| LeanKG | 1 | 5m18s | 8,467 / 3,412 | $0.63 | 1 | 0 | 1 |
| CodeGraph | 1 | 3m52s | 29,079 / 4,944 | $0.34 | 13 | 8 | 14 |
| No Graph | 1 | 5m29s | 49,891 / 2,841 | $0.99 | 33 | 19 | 14 |

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

- 10 architecture questions covering Typhoon (Objective-C).
- Each arm = `claude -p` headless with `--strict-mcp-config`, `--output-format json`, `--dangerously-skip-permissions`.
- LeanKG index rebuilt before its arm; CodeGraph index pre-built.
- N=3 runs per arm per question; median reported.
- Metrics parsed from claude CLI JSON envelope (v2.1.201+).

## Caveats

- Self-reported single-vendor benchmark. Treat as best-case.
- LeanKG Swift extraction is regex-based (no tree-sitter); under-reports call graph edges.
- Cost/token numbers depend on model version; pin with `--model` for reproducibility.
- Small sample (N=3); high variance expected. IQR appendix shows spread.
