# Changelog

## [0.37.2](https://github.com/FreePeak/LeanKG/compare/v0.37.1...v0.37.2) (2026-11-25)


### Bug Fixes

* **auth:** REST 401 for a bearer token names the token, not the login ([d1b15c9](https://github.com/FreePeak/LeanKG/commit/d1b15c9248ef518914e84048116d6944317ed352))
* **hosted:** no host paths in project errors, clearer 401 hint, public REST /health ([277e646](https://github.com/FreePeak/LeanKG/commit/277e646543acbb497817d00d80a48828d6751b40))
* **projects:** hosted unknown-project errors name projects, never paths ([d353746](https://github.com/FreePeak/LeanKG/commit/d35374679284121bc0aa26d8ef8d764ad40a28a1))
* **rest:** hosted /health answers without a token ([f7abc24](https://github.com/FreePeak/LeanKG/commit/f7abc240b7434e48ed80a6e4209dedc6fe251d72))

## [0.37.1](https://github.com/FreePeak/LeanKG/compare/v0.37.0...v0.37.1) (2026-11-24)


### Bug Fixes

* **hosted:** build routed handlers per project instance ([e10e942](https://github.com/FreePeak/LeanKG/commit/e10e942c515bf8300574baa64209216bd7bf2f2d))
* **hosted:** restore claimed projects into the router at startup ([b12c508](https://github.com/FreePeak/LeanKG/commit/b12c5087ff1d5a1d8298ebce1350aebfd6c51688))
* **hosted:** restore registrations at startup and hold the open-project cap ([53645ae](https://github.com/FreePeak/LeanKG/commit/53645aeccd14371374277b895c0b4d2bbfb399b1))
* **hosted:** typed refusal for graph writes on routed projects ([94d1fcf](https://github.com/FreePeak/LeanKG/commit/94d1fcf735588b425fafe17ab9f91c905915021f))
* **projects:** hold the open-project cap when leases are released ([cba4260](https://github.com/FreePeak/LeanKG/commit/cba426013896dcde518c5df9a1a89ff44889d54b))

## [0.37.0](https://github.com/FreePeak/LeanKG/compare/v0.36.0...v0.37.0) (2026-11-24)


### Features

* **auth:** enforce per-project token grants and report access on every answer ([35e724d](https://github.com/FreePeak/LeanKG/commit/35e724de587e5e6c2c06624dbc95478ce7c350db))
* **auth:** enforce per-project token grants on every project-selecting surface ([3e9f3c1](https://github.com/FreePeak/LeanKG/commit/3e9f3c1a665bfaf82bda6c3980b918811a842805))
* **bench:** pin the cross_tool benchmark and add a three-arm run ([54ce7de](https://github.com/FreePeak/LeanKG/commit/54ce7de31090ece83d2184c565a0ab52b8723672))
* **bench:** pin the cross_tool benchmark and add a three-arm run ([7343ed1](https://github.com/FreePeak/LeanKG/commit/7343ed171548cc239b0088bf8c7a6889b1b874cb))
* **connect:** write an env-var Authorization header for remote entries ([1b62c89](https://github.com/FreePeak/LeanKG/commit/1b62c8953baab8356b58c053a556370cb7b48176))
* **core:** report the caller's access on every query answer ([ab22725](https://github.com/FreePeak/LeanKG/commit/ab227256bd308548483572e4f3db2ea83d783ac8))
* **core:** stamp retrieval and freshness on every query answer ([dcc34c2](https://github.com/FreePeak/LeanKG/commit/dcc34c28a29aa153b1dec21c5af2bf3fb94496a2))
* **core:** stamp retrieval and freshness on every query answer ([ea1afa3](https://github.com/FreePeak/LeanKG/commit/ea1afa3d81cea1923e3598d06574c9d3b4d83771))
* **embed:** redact secrets from documents before embedding ([ac475fa](https://github.com/FreePeak/LeanKG/commit/ac475fa43c04799394518ef4ccbcbdb9819f7ddc))
* hosted multi-tenant serving hooks ([99b6fe3](https://github.com/FreePeak/LeanKG/commit/99b6fe3c02e6ed716f09eeec7e6cb70a99fb76d0))
* **mcp:** enforce org ownership on project selection ([7d43746](https://github.com/FreePeak/LeanKG/commit/7d43746e06298e21d747a467145cb277c889c8a6))
* **projects:** add runtime Add/Remove and LRU eviction of open projects ([d19c589](https://github.com/FreePeak/LeanKG/commit/d19c5898e84dea9f8b387162c13b2acb26f00134))
* **rest:** admin project registration with org ownership ([2f23b6c](https://github.com/FreePeak/LeanKG/commit/2f23b6ca4232a09f373463256cdc920fdb62fefc))
* **serve:** add --hosted mode ([788eb19](https://github.com/FreePeak/LeanKG/commit/788eb191d201b9a83bb8e0658363bb604022a253))
* **store:** cap the per-project Postgres pool at 2 connections ([2fb9139](https://github.com/FreePeak/LeanKG/commit/2fb9139c4c737ba856596c1e684179afa9876bde))


### Bug Fixes

* **auth:** a store failure does not count toward the login lockout ([7dcd0ec](https://github.com/FreePeak/LeanKG/commit/7dcd0eccdafa9c98396cb7384695f625ab6e3868))
* **auth:** answer a register store failure generically and log the cause ([556e27a](https://github.com/FreePeak/LeanKG/commit/556e27aa50746dbcdd7670cdc699ab8722230ac9))
* **auth:** answer store failures generically on every auth route ([c4b26bb](https://github.com/FreePeak/LeanKG/commit/c4b26bb0500f9a3ae79c306921cca7d9a8df7a38)), closes [#586](https://github.com/FreePeak/LeanKG/issues/586)
* **auth:** cap public auth request bodies at 1 MiB with 413 ([4e38cb6](https://github.com/FreePeak/LeanKG/commit/4e38cb685c6e5cbc3a1adf40ad818b1ff4c783aa))
* **auth:** gate auth write paths and harden registration and token listing ([499b0dc](https://github.com/FreePeak/LeanKG/commit/499b0dca5a07277fc9b10c175683b05f5a45dd2a))
* **auth:** keep project grants through token minting and hide the registry path ([a389049](https://github.com/FreePeak/LeanKG/commit/a3890498fc4706986c2b676b80383e0dabb3f224))
* **auth:** refuse token issuance from a process caller that cannot write ([d901a3b](https://github.com/FreePeak/LeanKG/commit/d901a3b511f8b2f805f92f368161aa81be60425c))
* **auth:** spend hashing work on duplicate registration ([56e321a](https://github.com/FreePeak/LeanKG/commit/56e321a9619b1b561fd64af0fdae03fd63d947c0))
* **bench:** fail closed on unlocked repos and describe the byte metric accurately ([f9fe0fc](https://github.com/FreePeak/LeanKG/commit/f9fe0fc016b74dbf89633cd0bc6de16e52f15206))
* **cancel:** exit 130 on cancelled runs and honour ctx in the index walk ([ddd5192](https://github.com/FreePeak/LeanKG/commit/ddd5192d831c375aec25d7d3b0673f09066fcee8))
* **cancel:** exit 130 when a run's child is killed by SIGINT or SIGTERM ([75489e3](https://github.com/FreePeak/LeanKG/commit/75489e39b2199bfbecb368cdcbb4e4683264c271)), closes [#589](https://github.com/FreePeak/LeanKG/issues/589)
* **cancel:** keep ctx.Err, not ctx, in the gitobj Reader ([7dfe16e](https://github.com/FreePeak/LeanKG/commit/7dfe16e45c67beadb7b79288a8e4fe954f23328e)), closes [#589](https://github.com/FreePeak/LeanKG/issues/589)
* **cli:** leankg-embed help exits 0 and surplus positional is a labelled usage error ([f555857](https://github.com/FreePeak/LeanKG/commit/f555857f4015366c9c30d4329a463c98297bd31a)), closes [#592](https://github.com/FreePeak/LeanKG/issues/592)
* **cli:** read the PG URL from leankg.yaml in multi-project serve and the registry sweep ([631edf6](https://github.com/FreePeak/LeanKG/commit/631edf6fa2cdb90179b2944b52281fc77d0a59bc)), closes [#592](https://github.com/FreePeak/LeanKG/issues/592)
* **cli:** report flag errors once, wrap store errors, add repo hygiene guard ([4ef4aed](https://github.com/FreePeak/LeanKG/commit/4ef4aed468176d969c09a09df03bf65f3e8be5a8))
* **cmd:** pass the caller ctx to language activation in openEngine ([7c06b58](https://github.com/FreePeak/LeanKG/commit/7c06b5831fe291d4149f6ef89d4c4306fdffb35f)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **core:** count conflict separator only inside an open conflict block ([021f776](https://github.com/FreePeak/LeanKG/commit/021f77661bd9f968ad6ef9e9389b210935dff8af)), closes [#592](https://github.com/FreePeak/LeanKG/issues/592)
* **core:** degrade freshness on a count error and tighten the provenance test ([e6a5067](https://github.com/FreePeak/LeanKG/commit/e6a5067ddbbe26284109efed5caae4406cd175b1))
* **core:** degrade freshness on a count error and tighten the provenance test ([6c0d044](https://github.com/FreePeak/LeanKG/commit/6c0d04413d0455c621209f36c96d4b03db7b7bfa))
* **dual-engine:** keep the e2e fixture out of the real portfolio registry ([d71cfd3](https://github.com/FreePeak/LeanKG/commit/d71cfd35306ba559e6e51dce0c229687dd21f644))
* **embed:** cancel the embed ledger cleanly and reuse the started provider ([0cba766](https://github.com/FreePeak/LeanKG/commit/0cba7667fd6dead49fade6a63b13a0d93e3303e8))
* **embed:** let a cancelled Probe waiter return at once ([96eb536](https://github.com/FreePeak/LeanKG/commit/96eb536be2f4f75a369fe649a322431aa76101a2)), closes [#587](https://github.com/FreePeak/LeanKG/issues/587)
* **http:** cap ConnectRPC and federation push bodies at 1 MiB ([b33469c](https://github.com/FreePeak/LeanKG/commit/b33469c10326a89825ac7a0e4beca003b0597f43)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** keep driver text out of federation publish faults ([44ba699](https://github.com/FreePeak/LeanKG/commit/44ba69937e507b4582a1e616a86c18ad2f958312)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** keep driver text out of RPC and federation faults ([e91bf56](https://github.com/FreePeak/LeanKG/commit/e91bf5699178fa3fc1d82d97671b257fbc6401f9)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** keep driver text out of server faults and the plain body error ([b867ce9](https://github.com/FreePeak/LeanKG/commit/b867ce9e1ca46c32d9cabaa06d55fec96c6aadc5)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** keep the publish relationship cap visible to operators ([4bdc90e](https://github.com/FreePeak/LeanKG/commit/4bdc90efd0843643ced885d0e144cb66f97ffba1)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** move the query-graph budget clamp into core ([cca94e4](https://github.com/FreePeak/LeanKG/commit/cca94e49c79e66c5d1c9ba2a05caeff93c0f997b)), closes [#590](https://github.com/FreePeak/LeanKG/issues/590)
* **http:** return structured errors, cap bodies at 1 MiB, decode through httpx ([8f440dd](https://github.com/FreePeak/LeanKG/commit/8f440dd59150913fee6d0acc8341658479c94de6))
* **index:** bound spans at the brace, drop comment call edges, order FindExact ([cc457cd](https://github.com/FreePeak/LeanKG/commit/cc457cdee2770d2401b5cbe5323e044b658b2629))
* **index:** end spans at the real brace and drop comment call edges ([bd35621](https://github.com/FreePeak/LeanKG/commit/bd3562151a43f91bde2430bc957d3755d8dc9428))
* **index:** roll back file records first and count only stored files ([978d7d5](https://github.com/FreePeak/LeanKG/commit/978d7d5b8356fcd6f6dd8a1b4da68b4bd3c58963)), closes [#591](https://github.com/FreePeak/LeanKG/issues/591)
* **live:** make the harness hermetic and bounded ([971e449](https://github.com/FreePeak/LeanKG/commit/971e449de615a4a1a14591d71fddbdc0413dd7fc))
* **memory:** keep redaction to real secret shapes and mask missed ones ([960da6c](https://github.com/FreePeak/LeanKG/commit/960da6cee7c6efb2b8d10011ae0b34a329fea38f)), closes [#591](https://github.com/FreePeak/LeanKG/issues/591)
* **memory:** mask bare lkg_ secrets as well as minted tokens ([bb24b7f](https://github.com/FreePeak/LeanKG/commit/bb24b7f9747791705fcc0224c6584774040ae668)), closes [#591](https://github.com/FreePeak/LeanKG/issues/591)
* **memory:** match pre-redaction bank rows by raw id and keep untouched rows ([2521716](https://github.com/FreePeak/LeanKG/commit/2521716a5d621dc6e5c8eb81ea212453595b1884)), closes [#591](https://github.com/FreePeak/LeanKG/issues/591)
* **memory:** redact lessons and reflections before they are written ([a3576a5](https://github.com/FreePeak/LeanKG/commit/a3576a5d3bdd39b6e7eda6f9d8e290843684fa6c)), closes [#591](https://github.com/FreePeak/LeanKG/issues/591)
* **memory:** redact secrets on write and compaction; roll back cancelled directories ([870317f](https://github.com/FreePeak/LeanKG/commit/870317faf9ed76871f1a7a07dcbe8801600ad334))
* PR [#585](https://github.com/FreePeak/LeanKG/issues/585) review follow-ups ([#586](https://github.com/FreePeak/LeanKG/issues/586)-[#592](https://github.com/FreePeak/LeanKG/issues/592)) ([6bcf6b2](https://github.com/FreePeak/LeanKG/commit/6bcf6b26e2824c3ed929f1cc6f43e3bcb043dbbf))
* **projects:** apply the open-project cap only in hosted mode ([355cdf0](https://github.com/FreePeak/LeanKG/commit/355cdf05eaff97069c1d8bdf50a6c13b3968d33e))
* **rpc:** declare a role for the top-level RPC routes ([e553c3d](https://github.com/FreePeak/LeanKG/commit/e553c3d3268fd32331020c1b98d8d7cef7c0b3d3))
* **serve:** keep URL-only checks off the MCP listener routing ([73c45c9](https://github.com/FreePeak/LeanKG/commit/73c45c9c7f6a3fad79a7531cb46848a09fb09af8))
* **sources:** clean credentialed remotes from existing checkouts ([5cded47](https://github.com/FreePeak/LeanKG/commit/5cded47120e7b11e233ae104e3f0b59c4bed9b53))
* **sources:** pass git credentials by environment, not the clone URL ([a8451a4](https://github.com/FreePeak/LeanKG/commit/a8451a461388219df6c9f81414b85a48673f183c))
* **sources:** redact git credentials from errors and progress lines ([1d152f9](https://github.com/FreePeak/LeanKG/commit/1d152f998af20a7fc55f1b481a8139ff90561b4a))
* **store:** align PostgreSQL FTS, migrations and org knowledge with SQLite ([031690d](https://github.com/FreePeak/LeanKG/commit/031690d1e086797f52c9199609ca217872af8a37))
* **store:** correct stale pgvector and search comments ([1e124c0](https://github.com/FreePeak/LeanKG/commit/1e124c014f6cc145c3515ce654bb3b3c35317af1)), closes [#588](https://github.com/FreePeak/LeanKG/issues/588)
* **store:** order PostgreSQL FindExact paths by C collation ([0fab142](https://github.com/FreePeak/LeanKG/commit/0fab1428be77e462b89beec2047232623c2722fd)), closes [#588](https://github.com/FreePeak/LeanKG/issues/588)
* **store:** realign the knowledge FTS index after every VACUUM ([757f9d3](https://github.com/FreePeak/LeanKG/commit/757f9d3df564c052933418bbe2455884de44d4fd)), closes [#588](https://github.com/FreePeak/LeanKG/issues/588)
* **store:** return degraded keyword hits with a nil error ([34218f2](https://github.com/FreePeak/LeanKG/commit/34218f261b5a29368e7fd2112471936ac9a819ff)), closes [#588](https://github.com/FreePeak/LeanKG/issues/588)

## [0.36.0](https://github.com/FreePeak/LeanKG/compare/v0.35.3...v0.36.0) (2026-11-18)


### Features

* add authenticate MCP tool and admin bootstrap ([#579](https://github.com/FreePeak/LeanKG/issues/579)) ([b871753](https://github.com/FreePeak/LeanKG/commit/b87175352bc81a39ff2aed8117ca655945a92bdc))
* add LeanKG self-ontology (concepts and workflows) ([#583](https://github.com/FreePeak/LeanKG/issues/583)) ([6b4b741](https://github.com/FreePeak/LeanKG/commit/6b4b741e619be1ac194676373953424d7c27deba))
* index git history (commits, authors, co-change) with history/cochange/churn queries ([#581](https://github.com/FreePeak/LeanKG/issues/581)) ([70b4186](https://github.com/FreePeak/LeanKG/commit/70b4186af6e6d1133ceef1f47af97f6ca6263eeb))
* **store:** replace a file's call and reference edges in place ([c81cae1](https://github.com/FreePeak/LeanKG/commit/c81cae1c8063b721b3056996748ef76c8bb83073))


### Bug Fixes

* hide git history rows from default search and serialize git imports ([#582](https://github.com/FreePeak/LeanKG/issues/582)) ([bfc2f03](https://github.com/FreePeak/LeanKG/commit/bfc2f03ae609a3cbaa5dd57353dd0563ca11d37e))
* **index:** bump the extractor version so stores that lost edges re-extract once ([b0458c1](https://github.com/FreePeak/LeanKG/commit/b0458c1acf9906d630c37d402711509ef3da60d5))
* **index:** keep cross-file edges on an incremental index ([3858818](https://github.com/FreePeak/LeanKG/commit/3858818491cb8ae2e05039351f22fea9e68c2471))
* **index:** keep cross-file edges on an incremental index ([#598](https://github.com/FreePeak/LeanKG/issues/598)) ([c0b195f](https://github.com/FreePeak/LeanKG/commit/c0b195f6963e8091c6c57cdfdfa7f01a1f0028ca))
* stop the maintenance fleet at shutdown and pin the reflect mirror detach ([0100df0](https://github.com/FreePeak/LeanKG/commit/0100df0caeaa41a27de3d28ffe44885ebb482d8a))
* stop the maintenance fleet at shutdown and pin the reflect mirror detach ([#596](https://github.com/FreePeak/LeanKG/issues/596)) ([0d7034e](https://github.com/FreePeak/LeanKG/commit/0d7034eef851815c04924978531b9ffafac2e4a8))
* **store:** keep the pgvector extension in public so every project schema sees it ([88d0304](https://github.com/FreePeak/LeanKG/commit/88d03045e821f5ae54be832171f6665dd175b7ce))
* **store:** keep the pgvector extension in public so every project schema sees it ([#597](https://github.com/FreePeak/LeanKG/issues/597)) ([5fab2cb](https://github.com/FreePeak/LeanKG/commit/5fab2cb1515f9824b9ec0248433caf19fd8279a8))

## [0.35.3](https://github.com/FreePeak/LeanKG/compare/v0.35.2...v0.35.3) (2026-10-22)


### Bug Fixes

* **maintain:** a reconciled store is not a stale one ([#573](https://github.com/FreePeak/LeanKG/issues/573)) ([9061d69](https://github.com/FreePeak/LeanKG/commit/9061d69c288f6602c1e7f2ccaed923768447bb52))
* **maintain:** heal the inventory a reconcile left behind ([#574](https://github.com/FreePeak/LeanKG/issues/574)) ([944f614](https://github.com/FreePeak/LeanKG/commit/944f6141eba6378daf1bc2fffffee4209cdd0b37))

### Miscellaneous Chores

* declare the 0.35.3 release version ([#575](https://github.com/FreePeak/LeanKG/issues/575)) ([20af689](https://github.com/FreePeak/LeanKG/commit/20af6898a609544f83e46acef539a03d4ceede51))

## [0.35.2](https://github.com/FreePeak/LeanKG/compare/v0.35.1...v0.35.2) (2026-10-21)


### Bug Fixes

* **index:** a markdown heading stores its own text, not its descendants' ([#565](https://github.com/FreePeak/LeanKG/issues/565)) ([46a4dfa](https://github.com/FreePeak/LeanKG/commit/46a4dfa4db9c7344057108a2ccebc32c6cb15453))
* **index:** reconcile the store with HEAD, and report head_sync ([#571](https://github.com/FreePeak/LeanKG/issues/571)) ([a20681c](https://github.com/FreePeak/LeanKG/commit/a20681c532bc14c8d3853a116ffb0afbb247f307))
* **maintain:** make the hourly pass do its job, and say what it did ([#566](https://github.com/FreePeak/LeanKG/issues/566)) ([22dd194](https://github.com/FreePeak/LeanKG/commit/22dd1949de79eb7302596a574cee33d97e6cc782))
* **memory:** content identity, bank compaction, fleet memory sweep, MCP bank delete ([#564](https://github.com/FreePeak/LeanKG/issues/564)) ([ed42e76](https://github.com/FreePeak/LeanKG/commit/ed42e762065e17146ee144903e2c1fec98d24af6))
* **portfolioreg:** an automatic registry prune, and a store whose index root moved is reported ([#568](https://github.com/FreePeak/LeanKG/issues/568)) ([d96cdba](https://github.com/FreePeak/LeanKG/commit/d96cdba527bf0dac18b7aea6103890cc4e305a68))
* **store:** give a new store a page that can hold three vectors ([#567](https://github.com/FreePeak/LeanKG/issues/567)) ([7d768b0](https://github.com/FreePeak/LeanKG/commit/7d768b058ef7226fbfb27929b878f95cd625baae))
* **telemetry:** sweep the ledger whatever the capture level, and let it shrink ([#570](https://github.com/FreePeak/LeanKG/issues/570)) ([cd8e51a](https://github.com/FreePeak/LeanKG/commit/cd8e51af24bab59e5f2db575a05a332fcad524ef))

## [0.35.1](https://github.com/FreePeak/LeanKG/compare/v0.35.0...v0.35.1) (2026-10-21)


### Bug Fixes

* **ci:** test every push to main and require CI before a merge ([#553](https://github.com/FreePeak/LeanKG/issues/553)) ([3e25834](https://github.com/FreePeak/LeanKG/commit/3e25834fea065c151a452e2cb73688faa1bda0dc))
* **cli:** metrics, dashboard and migrate refuse a project that has no store ([#556](https://github.com/FreePeak/LeanKG/issues/556)) ([5b5d51e](https://github.com/FreePeak/LeanKG/commit/5b5d51e6715eef7ed70d36439f5817d72e261ce2))
* **dashboard:** give the dashboard verb one flag set ([#554](https://github.com/FreePeak/LeanKG/issues/554)) ([ff8e405](https://github.com/FreePeak/LeanKG/commit/ff8e4051a37faed1cee0c63cde7c365b5fa69aa1))
* **projects:** a read must not rewrite the store it reads ([#560](https://github.com/FreePeak/LeanKG/issues/560)) ([9d7bd24](https://github.com/FreePeak/LeanKG/commit/9d7bd24fb087fceb899258d098f719baaa1e9888))
* **serve:** pin the self-host scan root to the projects LeanKG owns ([#552](https://github.com/FreePeak/LeanKG/issues/552)) ([5e829cc](https://github.com/FreePeak/LeanKG/commit/5e829cce9f71fb6aff393b29c3f48fe4ce700f44))
* **serve:** refuse a non-loopback bind with no auth token ([#558](https://github.com/FreePeak/LeanKG/issues/558)) ([4af470d](https://github.com/FreePeak/LeanKG/commit/4af470d0b9b019d331fbf2d934bf53b79fcc56a4))
* **status:** derive healthy from the reads the payload publishes ([#555](https://github.com/FreePeak/LeanKG/issues/555)) ([723d7dc](https://github.com/FreePeak/LeanKG/commit/723d7dc4ae81eeee5fabda507995dad98367907f))
* **store:** read the model stamp on a pre-017 store, and name the read error ([#557](https://github.com/FreePeak/LeanKG/issues/557)) ([1939784](https://github.com/FreePeak/LeanKG/commit/1939784ea0107319df374b3c7fe77072e0c84d22))

## [0.35.0](https://github.com/FreePeak/LeanKG/compare/v0.34.0...v0.35.0) (2026-10-20)


### Features

* **cli:** add delete-annotation, the one annotation operation with no caller ([#492](https://github.com/FreePeak/LeanKG/issues/492)) ([6453913](https://github.com/FreePeak/LeanKG/commit/64539135b6b203539d217395a0940027eb1a4fe0))
* **cli:** make a team note readable (it was write-only) ([#508](https://github.com/FreePeak/LeanKG/issues/508)) ([35ca43e](https://github.com/FreePeak/LeanKG/commit/35ca43ec37d4520ea7802d7d3bcf6db5ef4de094))
* **connect:** add the xdev client (FR-ZCP-14 K9) ([#467](https://github.com/FreePeak/LeanKG/issues/467)) ([d6a1a1a](https://github.com/FreePeak/LeanKG/commit/d6a1a1aed6f8629f761f281d36c9308c9472734d))
* **core:** args.weight is validated, applied, and not over-claimed ([#514](https://github.com/FreePeak/LeanKG/issues/514)) ([bcea583](https://github.com/FreePeak/LeanKG/commit/bcea583cd33b70dd9f559d0dc63276d6dda6b6d4))
* **core:** let a caller rank the population it asked for (args.scope) ([#484](https://github.com/FreePeak/LeanKG/issues/484)) ([f4ef492](https://github.com/FreePeak/LeanKG/commit/f4ef4922ee37716ce3b3b9b3e25c8a5113c6b18d))
* **core:** Noul L3 gate — skip rungSemantic when ontology confidence &lt; MinConfidence ([#429](https://github.com/FreePeak/LeanKG/issues/429)) ([99eb052](https://github.com/FreePeak/LeanKG/commit/99eb05285672443174ab2e75860c276676924375))
* **dashboard:** v4.15 session telemetry, efficiency metrics and the web dashboard ([#458](https://github.com/FreePeak/LeanKG/issues/458)) ([87002bc](https://github.com/FreePeak/LeanKG/commit/87002bc04b20dddc65d6fca5c1ec366507c2b3b9))
* **embed:** EmbeddingGemma 2 catalog row, Matryoshka widths and a Hugging Face Job bulk path ([a196a90](https://github.com/FreePeak/LeanKG/commit/a196a900fcf4795455afb946b5c721236a011c48))
* **embed:** EmbeddingGemma 2 catalog row, Matryoshka widths and a Hugging Face Job bulk path ([0bc52bb](https://github.com/FreePeak/LeanKG/commit/0bc52bbf16dcc3a96e1e658bb0f9e17a7a525d20))
* **embed:** local sidecar now serves the pinned bge-small-en-v1.5 GGUF by default ([#428](https://github.com/FreePeak/LeanKG/issues/428)) ([d95693c](https://github.com/FreePeak/LeanKG/commit/d95693c62570bbdfa3bd7c27704455afaa4fd619))
* **embed:** persist the text cap in the stamp and drop the silent 1000 fallback ([b463bfa](https://github.com/FreePeak/LeanKG/commit/b463bfa98b184a54f32df7032e6659414d6541d5))
* **examples:** ship the reference memory hook, and the route it needs ([#474](https://github.com/FreePeak/LeanKG/issues/474)) ([1f89dee](https://github.com/FreePeak/LeanKG/commit/1f89dee56810bbd6dd173045124680d0f42f6def))
* **index:** index HEAD git objects and gate the session hook ([d6dcc80](https://github.com/FreePeak/LeanKG/commit/d6dcc802322876f802c72ebb8ca893baf27ca0da))
* **index:** index HEAD git objects and gate the session hook ([55828b1](https://github.com/FreePeak/LeanKG/commit/55828b15a3ef3dd739a56326455bb0fecd71e6e5))
* **judge:** Laya/System One judgment abstraction + convo classify path + use-case doc ([#434](https://github.com/FreePeak/LeanKG/issues/434)) ([f1cd232](https://github.com/FreePeak/LeanKG/commit/f1cd2327839a9051101b0549e31060edec3aff14)), closes [#433](https://github.com/FreePeak/LeanKG/issues/433)
* **mcp:** advertise agent usage protocol ([#439](https://github.com/FreePeak/LeanKG/issues/439)) ([04c82ff](https://github.com/FreePeak/LeanKG/commit/04c82ffe0abcfbe4fa2d895de1348f4af4017141))
* **memory:** first-class tags on session_retain (K2) ([#472](https://github.com/FreePeak/LeanKG/issues/472)) ([ed4cc35](https://github.com/FreePeak/LeanKG/commit/ed4cc352be740c235de3be861cba6a4e09048309))
* **memory:** name what the injection budget hid (FR-SMA-06, marker half) ([#489](https://github.com/FreePeak/LeanKG/issues/489)) ([d1afd9f](https://github.com/FreePeak/LeanKG/commit/d1afd9faec3b622d9efc7d2d48a9118f8692a041))
* **memory:** redact secrets in memory writes (FR-SMA-06, redaction half) ([#487](https://github.com/FreePeak/LeanKG/issues/487)) ([cbdff32](https://github.com/FreePeak/LeanKG/commit/cbdff328788c60e921939b7ba02f0d02f9402ad9))
* **memory:** report which rows an inject block actually placed ([#476](https://github.com/FreePeak/LeanKG/issues/476)) ([daffd2a](https://github.com/FreePeak/LeanKG/commit/daffd2af15c2f40bc286560fcf3e3b1555fd24ce))
* **memory:** serve as a first-class memory backend service (FR-ZCP-14) ([#427](https://github.com/FreePeak/LeanKG/issues/427)) ([a6166b9](https://github.com/FreePeak/LeanKG/commit/a6166b907f67b66fa3eee7168df22093ab79c3f9))
* **rest:** add /api/v1/mcp/auto-config endpoint for session-scoped auto-init (FR-ZCP-14) ([384b8eb](https://github.com/FreePeak/LeanKG/commit/384b8eb5b226af79bd60a6979ca4aa40901bdfac))
* **rest:** mount auto-config endpoint on served project's mux ([#429](https://github.com/FreePeak/LeanKG/issues/429)) ([ff239a2](https://github.com/FreePeak/LeanKG/commit/ff239a2d911bb16303b85c31cf185922634f047d))
* **retrieval:** v4.14 — confine file access, fused semantic search, honest budgets, ranked memory recall ([#452](https://github.com/FreePeak/LeanKG/issues/452)) ([889468e](https://github.com/FreePeak/LeanKG/commit/889468ee7cda6197bcbc9244b7ad1a29a8dc604a))
* **session:** make a recorded lesson readable (it was write-only) ([#507](https://github.com/FreePeak/LeanKG/issues/507)) ([551d4d2](https://github.com/FreePeak/LeanKG/commit/551d4d2675ec491a31e947408b9c8afdf924d12d))
* **session:** make a recorded reflection recallable (K5 honest reflect) ([#470](https://github.com/FreePeak/LeanKG/issues/470)) ([e745d98](https://github.com/FreePeak/LeanKG/commit/e745d98a472771070415aea9841f6a6a2fdc2c8c))
* **storage:** the store reclaims itself on a schedule, across the fleet ([#541](https://github.com/FreePeak/LeanKG/issues/541)) ([ee9247d](https://github.com/FreePeak/LeanKG/commit/ee9247d0b2b68bfe0d12e3dd45dda4ec0ca6a8bc))
* **store:** fuse the element's name into the L3 rung (the [#449](https://github.com/FreePeak/LeanKG/issues/449) name arm) ([#481](https://github.com/FreePeak/LeanKG/issues/481)) ([e501af1](https://github.com/FreePeak/LeanKG/commit/e501af1a5778ede3516c9ea607387ee06869a2cf))
* **store:** make project directory optional via config/env DB path ([#430](https://github.com/FreePeak/LeanKG/issues/430)) ([b0e7c1d](https://github.com/FreePeak/LeanKG/commit/b0e7c1d5938f2642dde936f5b1e3e42cc1e656e8))
* **store:** the name arm fires on prose, at a measured rarity bound ([#515](https://github.com/FreePeak/LeanKG/issues/515)) ([1bb8bda](https://github.com/FreePeak/LeanKG/commit/1bb8bda60d7213e524e11f5c7f81b03d9a359ddd))
* **telemetry:** reload consent on SIGHUP, including the case the design missed ([#475](https://github.com/FreePeak/LeanKG/issues/475)) ([9554a40](https://github.com/FreePeak/LeanKG/commit/9554a4021e520f98ab3c0de30eddd85b38cc1d9a))
* **telemetry:** show how much of the ledger is attributable, and why the rest is not ([#477](https://github.com/FreePeak/LeanKG/issues/477)) ([ac078e1](https://github.com/FreePeak/LeanKG/commit/ac078e1f2228a66f243d9b05892ce284106edf70))


### Bug Fixes

* **bench:** honest retrieval bench with grep baselines and robust runs ([#533](https://github.com/FreePeak/LeanKG/issues/533)) ([7b701c9](https://github.com/FreePeak/LeanKG/commit/7b701c972ac4508a5168f3e1668a4f0ac9353b01))
* **bench:** the vector benchmark must own the elements it hydrates ([#456](https://github.com/FreePeak/LeanKG/issues/456)) ([ca40972](https://github.com/FreePeak/LeanKG/commit/ca409724088b9e2b95b8d0beae3cd5247dfe4a73))
* **ci:** let a contended runner widen the two external-binary budgets ([#488](https://github.com/FreePeak/LeanKG/issues/488)) ([a3f7523](https://github.com/FreePeak/LeanKG/commit/a3f752364567315bf8a79bfcac1daa09f2e2996a))
* **ci:** one runner slot stops queueing runs behind itself ([#530](https://github.com/FreePeak/LeanKG/issues/530)) ([82f3f58](https://github.com/FreePeak/LeanKG/commit/82f3f586fcb1b8a94d4d4800fb90cd778482c643))
* **ci:** stop pgvector after the dual-engine gate ([#529](https://github.com/FreePeak/LeanKG/issues/529)) ([f64ecf5](https://github.com/FreePeak/LeanKG/commit/f64ecf56cf67fc8f2864c9e175fb19d587d96fbf))
* **cli:** a search-annotations miss now says what search actually matches ([#493](https://github.com/FreePeak/LeanKG/issues/493)) ([4dbeb0e](https://github.com/FreePeak/LeanKG/commit/4dbeb0ed5411c760eb0203f435cc5aeb7a7f635c))
* **cli:** annotate and link refuse an element the index does not have ([#510](https://github.com/FreePeak/LeanKG/issues/510)) ([d44314d](https://github.com/FreePeak/LeanKG/commit/d44314d0696e710283f90f0bc15282ffdd137801))
* **cli:** doctor says "not indexed", not the syscall that noticed ([#512](https://github.com/FreePeak/LeanKG/issues/512)) ([7ec2924](https://github.com/FreePeak/LeanKG/commit/7ec292413011484be9e37799bab9aafa9ca3212b))
* **connect:** a client writer can no longer emit a stale ?project= URL ([#498](https://github.com/FreePeak/LeanKG/issues/498)) ([6c8edef](https://github.com/FreePeak/LeanKG/commit/6c8edef864f791f6e9387c60d4ae0717c63233e5))
* **connect:** write only the autoStart keys xdev defines, plus pidFile ([#468](https://github.com/FreePeak/LeanKG/issues/468)) ([becde9e](https://github.com/FreePeak/LeanKG/commit/becde9e326dc587a6fe71ed0b4cd14421e5032d7))
* **convo:** remove the Laya/Jev judge integration ([6115822](https://github.com/FreePeak/LeanKG/commit/6115822fb1c9245a5164d9ab08e2c096c673d179))
* **convo:** remove the Laya/Jev judge integration ([57be7a4](https://github.com/FreePeak/LeanKG/commit/57be7a46ee3dcc90f4d62e8a6c823d467bc89591))
* **core:** a lesson from the schema's own field is no longer empty ([#509](https://github.com/FreePeak/LeanKG/issues/509)) ([f7e28b6](https://github.com/FreePeak/LeanKG/commit/f7e28b6df97c2c56d5db11d4a761d6c8d09967e5))
* **core:** an empty ontology match set says why it is empty ([#511](https://github.com/FreePeak/LeanKG/issues/511)) ([813c1b5](https://github.com/FreePeak/LeanKG/commit/813c1b58a02dc58ba561c9ecc68ecb2476366500))
* **core:** name the memory read commands, and accept the search term in either place ([#471](https://github.com/FreePeak/LeanKG/issues/471)) ([f5afd63](https://github.com/FreePeak/LeanKG/commit/f5afd63396674a7c5cf8a23d00d8a2fe1722b9ab))
* **core:** resolve bare names for graph verbs; unlock dead-token REST ([#448](https://github.com/FreePeak/LeanKG/issues/448)) ([c25141b](https://github.com/FreePeak/LeanKG/commit/c25141b4d17088c4d394eef3d3b2f7db0b106d0e))
* **dashboard:** keep the failure reason verbatim on the failures page ([#462](https://github.com/FreePeak/LeanKG/issues/462)) ([f1bb808](https://github.com/FreePeak/LeanKG/commit/f1bb808a595bdbb47db498339a302ad785d7ad50))
* **dashboard:** re-stamp the embedded dashboard after the dependency bumps ([#540](https://github.com/FreePeak/LeanKG/issues/540)) ([da93880](https://github.com/FreePeak/LeanKG/commit/da938809cf2d6f1df16d9df113bc5fe04f8af51f))
* **embed:** a provider label alone no longer blocks a run or clears vectors ([#536](https://github.com/FreePeak/LeanKG/issues/536)) ([202e97e](https://github.com/FreePeak/LeanKG/commit/202e97ec1ab5f1b1bcf52a6730986b4fdacd5ef8))
* **embed:** adopt a running sidecar instead of spawning into a busy port ([#450](https://github.com/FreePeak/LeanKG/issues/450)) ([22aa35c](https://github.com/FreePeak/LeanKG/commit/22aa35cc531f48732abe3c08ddd8b643fcf3df2a))
* **embed:** treat Provider as transport label in ModelStamp identity ([#532](https://github.com/FreePeak/LeanKG/issues/532)) ([5bc5356](https://github.com/FreePeak/LeanKG/commit/5bc5356fc94d74b4360feb3ea1454a7261a29357))
* **errs:** stop the error-code audit from scanning sibling worktrees ([#465](https://github.com/FreePeak/LeanKG/issues/465)) ([f77d0d3](https://github.com/FreePeak/LeanKG/commit/f77d0d3f63bc18600b438fcd3fc0d454f005cc4b))
* **index:** bump extractor version so the TS function fix re-extracts ([#534](https://github.com/FreePeak/LeanKG/issues/534)) ([bacd1c2](https://github.com/FreePeak/LeanKG/commit/bacd1c22c7d94e4bcf3cf33f5b777eba4dff2f3c))
* **index:** the retrieval label set is guarded, and the tstree Go tier keeps its types and owners ([#548](https://github.com/FreePeak/LeanKG/issues/548)) ([853e603](https://github.com/FreePeak/LeanKG/commit/853e603051d24a3efae3f0eef97e8a583aaba2c6))
* **index:** word boundary after TS/JS function keyword ([#531](https://github.com/FreePeak/LeanKG/issues/531)) ([226825c](https://github.com/FreePeak/LeanKG/commit/226825cd71f6515d8e5d6d8a0575b1bd5ebb2598))
* **install:** repair the source installer and make the documented install deliver memory ([#442](https://github.com/FreePeak/LeanKG/issues/442)) ([1767cf1](https://github.com/FreePeak/LeanKG/commit/1767cf183861588eed8728340350c64fdc4a0353))
* **lsp:** the handshake gets its own budget, and the first failure its reason ([#523](https://github.com/FreePeak/LeanKG/issues/523)) ([df38f34](https://github.com/FreePeak/LeanKG/commit/df38f343cfb95d0366533ed7d13559132852cf9e))
* **maintain:** a pass that grew the store says it grew, not that it reclaimed -4096 bytes ([#549](https://github.com/FreePeak/LeanKG/issues/549)) ([a447f1b](https://github.com/FreePeak/LeanKG/commit/a447f1b5af327e4b05a6b8f03e57448eacb907db))
* **mcp:** a routed multi-project handler no longer re-asks for the project ([#501](https://github.com/FreePeak/LeanKG/issues/501)) ([dccc3ff](https://github.com/FreePeak/LeanKG/commit/dccc3ff157b5f5a94b6b7aaf15f93450524db77a))
* **mcp:** fail closed when a multi-project server gets no `project` ([#480](https://github.com/FreePeak/LeanKG/issues/480)) ([39a8c98](https://github.com/FreePeak/LeanKG/commit/39a8c98c915f8d17721e66a416b8a634213303ec))
* **mcp:** serve protocol 2026-07-28 over streamable HTTP ([#440](https://github.com/FreePeak/LeanKG/issues/440)) ([ab469f2](https://github.com/FreePeak/LeanKG/commit/ab469f2f3c46f3254e825b6accf7ec1a7b631491))
* **mcp:** teach agents memory retain/recall and empty-hit recovery ([#444](https://github.com/FreePeak/LeanKG/issues/444)) ([18d655a](https://github.com/FreePeak/LeanKG/commit/18d655ae19b8eb818f7dfc147c81b145b0962234))
* **mcp:** the multi-project MCP surface says which project to name ([#537](https://github.com/FreePeak/LeanKG/issues/537)) ([09086d7](https://github.com/FreePeak/LeanKG/commit/09086d7bd155eb017fce1096ea87c25063653398))
* **memory:** break recall ties toward the newer row (K7 recall ranking) ([#479](https://github.com/FreePeak/LeanKG/issues/479)) ([c9c33bc](https://github.com/FreePeak/LeanKG/commit/c9c33bc63887c2399b37e747e8b1f25d09f5acba))
* **memory:** hindsight-compat follows Hindsight tags_match and document upsert/delete ([#451](https://github.com/FreePeak/LeanKG/issues/451)) ([14e165b](https://github.com/FreePeak/LeanKG/commit/14e165bd3234ed509421882e2f63aae06167584f))
* **memory:** honour the advertised flat shape on memory curation writes ([#443](https://github.com/FreePeak/LeanKG/issues/443)) ([7585d39](https://github.com/FreePeak/LeanKG/commit/7585d3930b745f393fdfcb75b43699cd9f9fb4d7))
* **obsidian:** avoid EEXIST when symbol notes nest under source paths ([#445](https://github.com/FreePeak/LeanKG/issues/445)) ([976690f](https://github.com/FreePeak/LeanKG/commit/976690f41ae9660f5412a9647e8c3a67b8e2c122))
* **portfolio:** reclaim gone registry rows — rebased superset of [#504](https://github.com/FreePeak/LeanKG/issues/504), plus two postgres-registry fixes ([#528](https://github.com/FreePeak/LeanKG/issues/528)) ([6208e03](https://github.com/FreePeak/LeanKG/commit/6208e03e2bcca8a3aa8010654f69ef6a848e7cc4))
* **projects:** an unknown project name no longer answers from the cwd's project ([#538](https://github.com/FreePeak/LeanKG/issues/538)) ([a40f322](https://github.com/FreePeak/LeanKG/commit/a40f32269cf7e55d89c0f0abbdc20218887ddb19))
* **repo:** drop the employer's retrieval corpus from the public tree ([ed60748](https://github.com/FreePeak/LeanKG/commit/ed60748851669aef1fb896a423dc6611c79716e3))
* **repo:** drop the employer's retrieval corpus from the public tree ([6bb269d](https://github.com/FreePeak/LeanKG/commit/6bb269d82848851ba0bd2e19477552e6976eb69b))
* **rest:** fail closed when auto-config's cwd is another project (FR-ZCP-01 c3) ([#490](https://github.com/FreePeak/LeanKG/issues/490)) ([41d4295](https://github.com/FreePeak/LeanKG/commit/41d42951a272c414ca69abf84d7afeef17efcec1))
* **rest:** make ?project= actually reach auto-config, so [#490](https://github.com/FreePeak/LeanKG/issues/490)'s advice is true ([#491](https://github.com/FreePeak/LeanKG/issues/491)) ([52275a4](https://github.com/FreePeak/LeanKG/commit/52275a4ffd923960729bcd6b3b937bbb1860247d))
* **retrieval:** weight the vector arm 3:1 over lexical by default ([#535](https://github.com/FreePeak/LeanKG/issues/535)) ([7432ba7](https://github.com/FreePeak/LeanKG/commit/7432ba79ba73653a3d710c8db8ab5894b22aeaf8))
* **rpc:** make the second wire's refusal name the caller's value ([#483](https://github.com/FreePeak/LeanKG/issues/483)) ([44b3abf](https://github.com/FreePeak/LeanKG/commit/44b3abf373b5f6fbef920ee4cf6ffa6a7717b002))
* **rpc:** MemoryRead answers the same vocabulary as MCP and the CLI ([#494](https://github.com/FreePeak/LeanKG/issues/494)) ([f793f65](https://github.com/FreePeak/LeanKG/commit/f793f65e5d6936146d0ab33702b122aa6f588cd0))
* **serve:** mount the auto-config endpoint and give ConnectRPC a /health ([#463](https://github.com/FreePeak/LeanKG/issues/463)) ([934056a](https://github.com/FreePeak/LeanKG/commit/934056abb0f76bfea9c9121ccf7221cd2d5b88aa))
* **storage:** reclaim the disk space the engine was silently keeping ([#446](https://github.com/FreePeak/LeanKG/issues/446)) ([69690e1](https://github.com/FreePeak/LeanKG/commit/69690e1a2cf652806e25e801792ca1fa667f5bb4))
* **store:** one place resolves the standalone path, so every verb sees the same store ([#482](https://github.com/FreePeak/LeanKG/issues/482)) ([83f7e48](https://github.com/FreePeak/LeanKG/commit/83f7e48ed75b0d11a8c741c62bcc2092f1d10ce4))


### Miscellaneous Chores

* declare the 0.35.0 release version ([140a71e](https://github.com/FreePeak/LeanKG/commit/140a71e41f0f8ee6134bc9668a66ca9472bdc070))

## [Unreleased]

### Features

* **index:** for a git repository root, `leankg index <dir>` reads the HEAD commit's git objects (`git ls-tree` / `git cat-file --batch`) instead of walking the filesystem; uncommitted, untracked, ignored, symlinked and submodule/nested-repo files are not indexed, and change detection compares git blob IDs so an unchanged rerun reads no file content. Directories without their own `.git` or with no commits keep the filesystem walk.
* **index:** `leankg index` runs the incremental embed after indexing; `--no-embed` skips it. Embed failure is reported and skipped, not fatal.
* **index:** `--hook` agent mode (used by the SessionStart hook) exits 0 without indexing when the HEAD tree exceeds `LEANKG_HOOK_MAX_FILES` (default 10000; prints the manual `leankg index <dir>` to run instead), or when another index of the store is running. A directory without its own `.git` or with no commits is scanned from the filesystem, hook mode included. Hook embedding attaches only to a running local provider and never spawns a sidecar or calls a remote provider. Hook embedding also skips, printing the manual `leankg index <dir>` to run instead, when more than `LEANKG_HOOK_MAX_EMBED` (default 500) elements need vectors; manual runs ignore the limit.
* **writer:** for a git-backed store, a new commit triggers a reindex; saving a file without committing does not change the graph.
* **embed:** spawn path for `LEANKG_EMBED_PROVIDER=local` serves the pinned bge-small-en-v1.5 GGUF by default (`internal/embed/sidecar.go`): `-m ~/.leankg/models/bge-small-en-v1.5-f16.gguf --embeddings` when the file exists, otherwise `-hf CompendiumLabs/bge-small-en-v1.5-gguf:f16` which self-downloads into the llama.cpp cache on first use; the not-found error names the download step. `leankg-embed run/full/export/import/status` and `leankg serve --embed-provider local` work out of the box with no hand-written `LEANKG_EMBED_SIDECAR_ARGS`.

* **embed:** `embeddinggemma-2` catalog row (google/embeddinggemma-2 @ `914f7f8`, 768-d, document and query prefixes) for a llama.cpp sidecar serving the BF16 GGUF (`ggml-org/embeddinggemma-2-GGUF`, llama.cpp >= b11454); `.env.example` documents every embed and HF variable; README gains "Embedding providers" and "EmbeddingGemma 2 with a Hugging Face Storage Bucket".
* **embed:** `LEANKG_EMBED_DIMS` truncates a catalogued model to its Matryoshka widths (512, 256 or 128 for `embeddinggemma-2`), re-normalized to unit length (`internal/embed/matryoshka.go`).
* **scripts:** `scripts/hf-embed.sh` runs the EmbeddingGemma 2 bulk embed on a Hugging Face Job (export, upload to a bucket, `hf jobs uv run scripts/hf_embed_job.py`, download, import; `--dry-run`, `--yes`, `--limit N`, `--keep`). Cost guards: every run prints an estimate and a worst case (job timeout x hourly price); a run is refused without `--yes`, when the worst case exceeds `LEANKG_HF_MAX_USD` (default 0.50), or when the estimate exceeds the timeout.

### Changes

* **install:** the generated `--register-cwd` SessionStart hook is async, uses matcher `startup|resume`, and runs `leankg index --hook "$CLAUDE_PROJECT_DIR"`; re-running install replaces a previously generated `index "$CLAUDE_PROJECT_DIR"` entry instead of adding a second one.
* **embed:** a catalogued model with an unsupported `LEANKG_EMBED_DIMS` now fails at construction; only its native width or a Matryoshka width is accepted.
* **embed:** a permanent endpoint configuration error (HTTP 404/405, HTTP 401/403, or a 200 reply that is not an OpenAI embeddings object, such as llama-server's native top-level array when `LEANKG_EMBED_BASE_URL` lacks `/v1`) stops the run after the first request with `ErrEndpointConfig`, naming the URL called and the fix. It is no longer retried or shrunk item by item, and the run writes no vectors; error text redacts the API key.
* **embed:** `leankg-embed import` stamps the collection from the `LEANKG_EMBED_*` environment (model, revision, distance, width, prefixes). `--model`, `--revision`, `--dims` and `--distance` are optional overrides and must agree with it. `export` takes `--model` (required), which must agree with the same environment.
* **embed:** an unpinned local sidecar needs `LEANKG_EMBED_REVISION` for `import` and `export`, because the revision cannot be read from a running sidecar.
* **embed:** `export` refuses a local sidecar that reports no `n_ctx` on `/props`, since the live text cap is then unknown.
* **scripts:** `scripts/embed_batch.py` is removed; use `scripts/hf-embed.sh` and `scripts/hf_embed_job.py`.

## [0.34.0](https://github.com/FreePeak/LeanKG/compare/v0.33.0...v0.34.0) (2026-09-14)


### Features

* **memory:** hindsight-wire compat mount on the REST surface ([#414](https://github.com/FreePeak/LeanKG/issues/414)) ([#423](https://github.com/FreePeak/LeanKG/issues/423)) ([bc92346](https://github.com/FreePeak/LeanKG/commit/bc92346d02e1b6d4028a8d03b4963c1100aed573))


### Bug Fixes

* **docker:** name the demo-stage COPY destinations so the grammar exclusion works ([#419](https://github.com/FreePeak/LeanKG/issues/419)) ([#421](https://github.com/FreePeak/LeanKG/issues/421)) ([1879ba4](https://github.com/FreePeak/LeanKG/commit/1879ba41a4a22ebd8a9470a9c8cd8c51ca7a0ea9))

## [0.33.0](https://github.com/FreePeak/LeanKG/compare/v0.32.1...v0.33.0) (2026-09-14)


### Miscellaneous Chores

* declare the module-root release version ([#417](https://github.com/FreePeak/LeanKG/issues/417)) ([4480c79](https://github.com/FreePeak/LeanKG/commit/4480c7975ac4c1462f19d4512f2ac448aa8e16b5))

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
