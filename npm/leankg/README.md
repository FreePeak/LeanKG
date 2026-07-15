# leankg (npm)

Thin npm wrapper for [LeanKG](https://github.com/FreePeak/LeanKG) — a
lightweight knowledge graph for AI-assisted development.

## Install

```bash
npm install -g leankg
```

The `postinstall` script downloads the prebuilt binary for your
platform + arch from the latest GitHub release. No Rust toolchain
required.

## Usage

```bash
leankg init
leankg index ./src
leankg serve
```

All CLI commands work the same as the cargo-installed binary. See
<https://github.com/FreePeak/LeanKG> for the full command list.

## Supported platforms

- darwin / amd64, arm64
- linux / amd64, arm64
- windows / amd64, arm64

## Manual install

If the postinstall can't reach GitHub (proxy / offline):

```bash
cargo install --git https://github.com/FreePeak/LeanKG leankg
```

## License

Apache-2.0
