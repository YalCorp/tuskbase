# Changelog

All notable changes to Tuskbase will be documented in this file.

This project follows a human-readable changelog style inspired by Keep a Changelog, and release tags should use semantic versioning once public releases begin.

## [Unreleased]

## [v0.1.0] - 2026-05-24

### Added

- Guided CLI entrypoint with `version`, `init`, `init-mcp`, `serve`, `daemon start`, `daemon status`, and `doctor` commands.
- Demo mode using stdio MCP with SQLite for lowest-friction local evaluation.
- Local Basic mode using a loopback HTTP MCP daemon with SQLite so multiple local agent clients can share one memory process.
- Optional REST API mounting behind an explicit `--rest` flag.
- Optional OpenAI embedding provider behind the embedding interface.
- Hybrid retrieval path that keeps text search as the default fallback and uses vectors only when configured.
- SQLite-backed vector records for local experimentation.
- Release workflow and GoReleaser config for tag-based binary builds.

### Changed

- README now describes Demo and Local Basic usage directly instead of presenting REST as the default local surface.
- Product docs now describe Demo, Local Basic, Local Shared, and Hosted tiers.

### Security

- Local Basic HTTP mode is restricted to loopback addresses unless explicitly overridden.
- REST endpoints are not exposed by the default Local Basic daemon.
