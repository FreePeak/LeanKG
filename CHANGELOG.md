# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.13.0] - 2026-04-12

### Added
- Obsidian vault integration for annotation IDE
- Obsidian module with note generator and sync logic
- Watcher for live file monitoring
- CLI with obsidian subcommand
- New documentation: architecture.md, benchmark.md, metrics.md
- Dockerfile improvements for LeanKG indexing during build

### Changed
- Updated README with new UI architecture documentation
- Vite dev server integration for production deployments

### Fixed
- Dockerfile to build new Vite+React UI
- UI directory build copy issue
- WORKDIR setting in Dockerfile
- Preserved all elements for complete call graph
