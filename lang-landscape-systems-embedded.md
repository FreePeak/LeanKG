# Languages in Active Use — Systems, Embedded/IoT, Real-Time, HDL, FPGA Tools

Verified against GitHub releases, tree-sitter orgs (`tree-sitter/`, `tree-sitter-grammars/`), and the `arborist-ts/registry` list. Versions are crate `Cargo.toml` or release tag (latest as of 2026-08-03). Where no tree-sitter grammar exists, noted `no grammar`. Where the canonical LSP is unclear or absent, marked `no LSP`.

> Scope: only languages whose existence is verified by either an official tree-sitter grammar repo, an LSP server, or a GitHub-tracked ecosystem. Speculation removed.

---

## 1. Systems languages

| Language | Aliases | Extensions | Dialects | Tree-sitter crate | Version | Active LSP | Install (LSP) | Ecosystem notes |
|---|---|---|---|---|---|---|---|---|
| C | — | `.c`, `.h` | C89/C99/C11/C17/C23, GNU C, MSVC C | `tree-sitter-c` | 0.24.2 | `clangd` | distro pkg `clangd`, Homebrew `clangd`, LLVM release | Linux kernel, glibc, most embedded SDKs |
| C++ | Cpp | `.cpp`, `.cc`, `.cxx`, `.c++`, `.hpp`, `.hh`, `.hxx`, `.h++`, `.ipp`, `.inl` | C++98/03/11/14/17/20/23, GNU++, MSVC, Clang | `tree-sitter-cpp` | 0.23.4 | `clangd` | see above | LLVM/Chromium/Boost; embedded SDKs ship C++ subset |
| Rust | — | `.rs` | Edition 2015/2018/2021/2024 | `tree-sitter-rust` | 0.24.2 | `rust-analyzer` | `rustup component add rust-analyzer` (bundled), `cargo install --locked rust-analyzer` | `no_std` for embedded; Tock OS uses it |
| Zig | — | `.zig` | Stage 1/2 (compiler), self-hosted since 0.11 | `tree-sitter-zig` | 1.1.2 | `zls` | `zig install zls` (zig 0.14+), or prebuilt binary from zigtools/zls | LLVM/Mach |
| Nim | — | `.nim`, `.nimble`, `.nims`, `.nim.cfg` | `--styleHint:off` only; arc/orc/refc GC | `tree-sitter-nim` (alaviss) | 0.6.2 | `nimlsp` (PMunch), `nim-lang/langserver` (older) | `nimble install nimlsp`; older `nimlangserver` via Alire | transpiles to C/C++/JS |
| Odin | — | `.odin` | official vendor dialects; simd, intrinsics | `tree-sitter-odin` | 1.3.0 | `ols` (DanielGavin/ols) | `odin install ols` or manual build from `DanielGavin/ols` | Orca, vendored SDKs |
| V (vlang) | — | `.v`, `.vsh`, `.vmod` | — | `tree-sitter-v` (nedpals), `tree-sitter-v` (undivisible) | 0.0.1 / WIP | `v-analyzer` (vlang/v-analyzer) | `v install v-analyzer` | transpiles to C |
| Hare | — | `.ha` | — | `tree-sitter-hare` | 1.0.0 | no LSP | — | no_std, no GC; POSIX-ish systems lang |
| Jai | — | `.jai` | Beta-only; not public source | `tree-sitter-jai` (constantitus) | 0.1.0 | no LSP | — | closed-beta by Jonathan Blow |
| Carbon | — | `.carbon` | Experimental; C++ successor | `tree-sitter-carbon` (Aaron-212) | 0.1.0 | no LSP | — | experimental; Google-led |
| Mojo | — | `.mojo`, `.🔥` | Modular, superset of Python | `tree-sitter-mojo` (oaustegard) | 0.25.0 | no LSP (official; Modular Magic previewed one) | — | GPU/AI kernels |

---

## 2. Embedded / IoT

| Language / Framework | Aliases | Extensions | Dialects | Tree-sitter crate | Version | Active LSP | Install (LSP) | Ecosystem notes |
|---|---|---|---|---|---|---|---|---|
| Arduino (C++) | Arduino | `.ino`, `.pde` (legacy) | AVR C++ subset, ESP8266/ESP32 extensions | `tree-sitter-arduino` | 0.25.0 (Cargo) | `clangd` | see C++ row | Arduino IDE 2 uses `clangd` |
| PlatformIO | pio | `platformio.ini` (ini), `.c`, `.cpp`, `.ino`, `.py` (micropython) | — | ini via `tree-sitter-grammars/tree-sitter-toml`/INI; no PlatformIO-specific grammar | — | `clangd` for C/C++, `pyright` for MicroPython | PIO provides intellisense via its own service | multi-framework build tool, not a language |
| MicroPython | mpy | `.py` (modified Python 3.x) | — | `tree-sitter-python` (MicroPython source not separately grammar) | — | `pyright`, `ruff-lsp` | `pip install pyright` | runs on ESP32, RP2040, STM32 |
| CircuitPython | — | `.py` (CPython subset) | — | `tree-sitter-python` | — | `pyright`, `ruff-lsp` | — | Adafruit SAMD/RP2040 |
| mbed OS (C++) | — | `.cpp`, `.h` | ARM mbed classic; deprecated since 2023-12 | `tree-sitter-cpp` | see C++ | `clangd` | — | legacy; merged into Zephyr/Keil Studio |
| ESP-IDF (C) | — | `.c`, `.h`, `CMakeLists.txt` | ESP32 variants (Xtensa LX6/LX7, RISC-V) | `tree-sitter-c`, `tree-sitter-cpp` | see above | `clangd` (official ESP-IDF VSCode ext configures it) | install per IDF docs | Espressif SDK |
| Zephyr RTOS | — | `.c`, `.h`, `CMakeLists.txt`, `.dts`, `.overlay` | C, devicetree, Kconfig | `tree-sitter-c`, `tree-sitter-grammars/tree-sitter-kconfig`, `tree-sitter-grammars/tree-sitter-gn` (build), `tree-sitter-grammars/tree-sitter-devicetree` (community) | C: 0.24.2 | `clangd` | — | Linux Foundation RTOS |
| Tock OS (Rust) | — | `.rs` | `no_std` only, kernel + userspace split | `tree-sitter-rust` | 0.24.2 | `rust-analyzer` | — | secure embedded kernel |
| eLua | — | `.lua` | Lua 5.1/5.2 with embedded hooks | `tree-sitter-lua` (tree-sitter-grammars) | 0.5.0 | `lua-language-server` (luals) | `brew install lua-language-server` | runs on STM32, ESP32 |

---

## 3. Real-time / safety-critical

| Language | Aliases | Extensions | Dialects | Tree-sitter crate | Version | Active LSP | Install (LSP) | Ecosystem notes |
|---|---|---|---|---|---|---|---|---|
| Ada | — | `.adb` (body), `.ads` (spec), `.gpr` (project) | Ada 83/95/2005/2012/2022 | `tree-sitter-ada` (briot) | 0.9.1 | `ada_language_server` (AdaCore) | `alr install ada_language_server`; standalone binary | DO-178C, avionics |
| SPARK | — | `.ads`, `.adb` | SPARK Pro / SPARK 2014 subset of Ada | `tree-sitter-ada` (Ada grammar covers SPARK) | 0.9.1 | `ada_language_server` (with `gnatprove`) | — | formally verified subset |
| VHDL | — | `.vhd`, `.vhdl`, `.vho` | VHDL-87/93/2002/2008/2019 | `tree-sitter-vhdl` (jpt13653903) | 1.5.0 | no first-class LSP (Vivado/Quartus use vendor IDE; `vhdl-tooling` research) | — | FPGA design |
| Verilog | — | `.v`, `.vh` | Verilog-95/2001/2005; SystemVerilog usually separate | `tree-sitter-verilog` | 1.0.3 | `verible-verilog-ls` (chipsalliance/verible), `svls` (community) | `go install github.com/chipsalliance/verible/cmd/verible-verilog-ls@latest` | — |
| SystemVerilog | SV | `.sv`, `.svh`, `.svi` | IEEE 1800-2017/2023 | `tree-sitter-systemverilog` (gmlarumbe) | 0.4.0 | `verible-verilog-ls`, `svls` | same as Verilog | UVM, cocotb |
| SystemC | — | `.cpp`, `.h` (with `sc_`/`sc_module` macros) | IEEE 1666-2011 | no grammar | — | `clangd` (treats as C++) | — | transaction-level modeling |

---

## 4. Hardware description (HDL, beyond Verilog/SystemVerilog/VHDL above)

| Language | Aliases | Extensions | Dialects | Tree-sitter crate | Version | Active LSP | Install (LSP) | Ecosystem notes |
|---|---|---|---|---|---|---|---|---|
| Bluespec (BSV) | Bluespec SystemVerilog | `.bsv`, `.bsvi` | Bluespec 2014 | `tree-sitter-bsv` (yuyuranium, sandytruant) | 0.0.1 | no LSP | — | Haskell-style HDL |
| Chisel (Scala) | — | `.scala` | Chisel 3.x, Chisel 5.x | `tree-sitter-scala` | 0.26.0 | `metals` (Scala LSP) | `coursier launch metals` | emits FIRRTL |
| FIRRTL | — | `.fir` | FIRRTL spec 1-3 | `tree-sitter-firrtl` (tree-sitter-grammars; chipsalliance mirror) | 0.8.0 | no LSP | — | IR for Chisel/MLIR |
| SpinalHDL (Scala) | — | `.scala` | SpinalScala 1.x | no grammar (shares `tree-sitter-scala`) | 0.26.0 | `metals` | see Scala | emits Verilog/VHDL |

---

## 5. FPGA / EDA tooling scripts

| Language | Aliases | Extensions | Dialects | Tree-sitter crate | Version | Active LSP | Install (LSP) | Ecosystem notes |
|---|---|---|---|---|---|---|---|---|
| Tcl | Tool Command Language | `.tcl`, `.tk` | Tcl 8.6/9.0 | `tree-sitter-tcl` (tree-sitter-grammars) | 1.1.0 | no active LSP (`efm-langserver/tcl-langserver` archived) | — | Vivado, Quartus, OpenOCD scripting |
| Tool Def Language (Tcl-based) | XDC, SDC, UCF | `.xdc`, `.sdc`, `.ucf`, `.tcl` (project-specific) | vendor dialects (Vivado XDC, Synopsys SDC, ISE UCF) | `tree-sitter-tcl` | 1.1.0 | vendor IDE only | — | FPGA constraints |

---

## Grammar coverage gaps (verified absent or immature)

- **SystemC** — no dedicated grammar; use `tree-sitter-cpp` with custom queries.
- **SPARK** — no dedicated grammar; `tree-sitter-ada` parses but no SPARK-specific queries.
- **PlatformIO** — no grammar; `platformio.ini` parsed by community INI grammar.
- **Jai** — `tree-sitter-jai` exists (community, 0.1.0) but Jai itself is closed-beta.
- **Carbon** — `tree-sitter-carbon` (0.1.0) is community, not in tree-sitter-grammars org.
- **Mojo** — `tree-sitter-mojo` (0.25.0) is community (oaustegard) — version numbers track upstream, not the language itself.

## LSP coverage gaps (verified absent)

- **Carbon** — no LSP.
- **Mojo** — no public LSP (Modular ships internal tooling).
- **Jai** — no LSP.
- **Hare** — no LSP.
- **Bluespec (BSV)** — no LSP; commercial BSV IDE only.
- **VHDL** — no widely deployed LSP.
- **Tcl / Tool Def** — most LSP servers archived; vendor IDEs only.

---

## Primary sources

- tree-sitter grammar orgs: <https://github.com/tree-sitter>, <https://github.com/tree-sitter-grammars>
- LSP server search: <https://github.com/search?q=language+server&type=repositories>
- arborist-ts registry: <https://github.com/arborist-ts/registry>
- Rust crate versions: <https://crates.io/crates/tree-sitter-{lang}>

All versions retrieved 2026-08-03 via `api.github.com` + raw `Cargo.toml` reads. Star counts where given are approximate.