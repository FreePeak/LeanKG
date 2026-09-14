# Changelog

## [0.32.1](https://github.com/FreePeak/LeanKG/compare/v0.32.0...v0.32.1) (2026-09-14)


### Bug Fixes

* **store,gc:** gc reclaims orphaned embedding vectors too (closes [#411](https://github.com/FreePeak/LeanKG/issues/411)) ([#412](https://github.com/FreePeak/LeanKG/issues/412)) ([fe129e6](https://github.com/FreePeak/LeanKG/commit/fe129e61c3e41fe5c4e1be7a22efdce810391cad))

## [0.32.0](https://github.com/FreePeak/LeanKG/compare/v0.31.3...v0.32.0) (2026-09-14)


### Features

* ship the public surface — graph-K brand mark, published Go module, container deploy, README badges ([#392](https://github.com/FreePeak/LeanKG/issues/392)) ([2606baf](https://github.com/FreePeak/LeanKG/commit/2606baf639f8dce2e37768506636455ae0d7f8ca))


### Bug Fixes

* **ci:** make the workflow_dispatch re-publish path actually run ([#395](https://github.com/FreePeak/LeanKG/issues/395)) ([e7ff765](https://github.com/FreePeak/LeanKG/commit/e7ff7656d2f789f063b8050d295b9dd6327c1fb2))
* **doctor,portfolio:** truthful classifications on every surface; inventory refresh on every writer ([#409](https://github.com/FreePeak/LeanKG/issues/409)) ([f34dd9e](https://github.com/FreePeak/LeanKG/commit/f34dd9e4037a5cf8ad91e1ea99d0eb707b71983f))
* **embed:** shrink over-context items; honour the positional project dir (self-host loop, wave two) ([#405](https://github.com/FreePeak/LeanKG/issues/405)) ([4ad0f18](https://github.com/FreePeak/LeanKG/commit/4ad0f185b2067e3b10b409cb4031e00d2640b9cc))
* **engine:** first dogfood wave from the live self-host - embed budget, poison-item fallback, leankg gc, held-lock probe; docs(prd): v4.11.2 ([#401](https://github.com/FreePeak/LeanKG/issues/401)) ([a9e19cc](https://github.com/FreePeak/LeanKG/commit/a9e19cc3009b86776d36c3f25eb41205ba0fd6e7))

## [0.31.3](https://github.com/FreePeak/LeanKG/compare/v0.31.2...v0.31.3) (2026-09-14)


### Bug Fixes

* **ci:** assert the four tarballs landed, and never move `latest` backwards ([#387](https://github.com/FreePeak/LeanKG/issues/387)) ([f679887](https://github.com/FreePeak/LeanKG/commit/f679887b301b3df5b24e47564922b2c4bda1a311))
* **ci:** read the release version straight from plan, dropping the resolver hop ([#386](https://github.com/FreePeak/LeanKG/issues/386)) ([479e92c](https://github.com/FreePeak/LeanKG/commit/479e92c8d3383cbab514963b0284db2255d94735))

## [0.31.2](https://github.com/FreePeak/LeanKG/compare/v0.31.1...v0.31.2) (2026-09-14)


### Bug Fixes

* **ci:** resolve the release version by value, not by event name ([#384](https://github.com/FreePeak/LeanKG/issues/384)) ([0b5aa7c](https://github.com/FreePeak/LeanKG/commit/0b5aa7c938685c815092dcaaec327a155ab8ea6b))

## [0.31.1](https://github.com/FreePeak/LeanKG/compare/v0.31.0...v0.31.1) (2026-09-14)


### Bug Fixes

* **ci:** read release-please v4 outputs by their real names ([#382](https://github.com/FreePeak/LeanKG/issues/382)) ([cd93b9f](https://github.com/FreePeak/LeanKG/commit/cd93b9f08e2823dbd9ea68a182e7b5fa7d556054))

## [0.31.0](https://github.com/FreePeak/LeanKG/compare/v0.30.0...v0.31.0) (2026-09-14)


### ⚠ BREAKING CHANGES

* **go:** full Rust → Go rewrite — parity engine, dual-backend, Rust removed ([#370](https://github.com/FreePeak/LeanKG/issues/370))

### Features

* **bench:** FR-ZCP-08 cross-tool harness hardening ([#366](https://github.com/FreePeak/LeanKG/issues/366)) ([959e550](https://github.com/FreePeak/LeanKG/commit/959e550df8e772150db9431c98760ba5dabd375f))
* **connect:** per-client SessionStart hook + FR-ZCP-04 docs sync ([#364](https://github.com/FreePeak/LeanKG/issues/364)) ([0a80748](https://github.com/FreePeak/LeanKG/commit/0a80748cab817f54c5f7052977acd79306085318))
* FR-ZCP-04 install --target opencode/omp + --register-cwd ([#361](https://github.com/FreePeak/LeanKG/issues/361)) ([a6425d0](https://github.com/FreePeak/LeanKG/commit/a6425d0a6c852f7b5e3a693fd9861ae7066ca4da))
* FR-ZCP-04 install --target opencode/omp + --register-cwd ([#363](https://github.com/FreePeak/LeanKG/issues/363)) ([ae1c6c5](https://github.com/FreePeak/LeanKG/commit/ae1c6c5156bfdd4291e9197211a96d1dbd1c904d))
* **go:** full Rust → Go rewrite — parity engine, dual-backend, Rust removed ([#370](https://github.com/FreePeak/LeanKG/issues/370)) ([b83206a](https://github.com/FreePeak/LeanKG/commit/b83206a1135c72e346c000e1fc1f7c80e28e5b57))


### Bug Fixes

* **bench:** judge-segmented quality medians + durable aggregate selftest ([#367](https://github.com/FreePeak/LeanKG/issues/367)) ([37fb695](https://github.com/FreePeak/LeanKG/commit/37fb6953836c38950cedf9116bd2380b364fcd3b))
* **ci:** single-run release pipeline + update-path comment/dedup sync ([#378](https://github.com/FreePeak/LeanKG/issues/378)) ([da1e83e](https://github.com/FreePeak/LeanKG/commit/da1e83e4c453e3372a610b5bce4202836c3b5072))
