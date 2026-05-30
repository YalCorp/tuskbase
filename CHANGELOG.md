# Changelog

All notable changes to Tuskbase will be documented in this file.

This project follows a human-readable changelog style inspired by Keep a Changelog, and release tags should use semantic versioning once public releases begin.

## [v0.2.2]

### Fixed

- Hardened MCP daemon lifecycle startup so bridge readiness checks retry detached fallback only for down daemons and fail clearly on bad `/healthz` responses.
- Made `tuskbase daemon install|restart|uninstall` exit non-zero for degraded lifecycle results.
- Refused temporary Go build artifacts for autostart service executables, and made detached fallback release child processes properly.

## [v0.2.1]

### Added

- Added `.env.example` for manual local configuration.
- Added friendly `setup`, `start`, `status`, `connect`, `bridge`, and `auth show` CLI commands.
- Added bridge-based MCP client setup so local clients can use Tuskbase-managed credentials without `TUSKBASE_API_KEY` in every shell session.
- Added Codex setup automation via `connect codex --apply`.
- Added Local Basic key rotation and Local Shared named-key admin commands.
- Added auth-derived actor attribution for authenticated writes.
- Documented that Codex may show `Auth: Unsupported` for stdio bridge setup even though daemon auth is still enforced.

### Security

- Made HTTP MCP and REST require bearer auth by default, with `tuskbase setup` generating local keys and env vars available as manual overrides.
- Added config-backed auth refresh so local key rotation does not require copying tokens into MCP client config.
- Hardened local config permissions and rejects symlinked secret config files.
- Enforced reader/agent/admin permissions for authenticated application calls.

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
