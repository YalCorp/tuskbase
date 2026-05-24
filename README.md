# Tuskbase

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Local-first repo memory for AI coding agents.**

Tuskbase helps coding agents share repo context and decision history before they change code, so each session does not have to rediscover architecture, conventions, or settled decisions from scratch.

Agents working across the same repo should not silently contradict prior direction. Tuskbase turns that drift into an explicit workflow: look up context, preflight a proposal, remember the final decision, and surface conflicts when new work disagrees with active project direction.

> Project status: first implementation slice. This repository includes a local Go service with temporal decision records, SQLite-backed local storage, a Postgres store adapter package, deterministic active-memory lookup, optional OpenAI embeddings, preflight conflict detection, lookup receipts, stdio MCP, loopback HTTP MCP, and an optional REST API. UI, SDKs, cloud sync, and packaging wrappers are still deferred.

## How It Works

The core loop is:

```text
attach -> lookup -> preflight -> remember
```

| Step | Purpose |
|---|---|
| `attach` | Understand the workspace and repo context. |
| `lookup` | Retrieve relevant decisions, claims, repo docs, conflicts, and constraints. |
| `preflight` | Check whether a proposal follows, extends, duplicates, supersedes, or conflicts with prior direction. |
| `remember` | Store the final decision with reasoning, evidence, claims, files, and relationships. |

## Quick Start

Build or run the current Go command during development:

```bash
go run ./cmd/tuskbase version
go run ./cmd/tuskbase init
```

Demo mode is the fastest path for one local MCP client:

```bash
go run ./cmd/tuskbase serve
```

Local Basic mode runs one loopback daemon so multiple local MCP clients can share the same SQLite-backed memory:

```bash
go run ./cmd/tuskbase daemon start
```

The Local Basic MCP endpoint is:

```text
http://127.0.0.1:8765/mcp
```

Generate example MCP client config:

```bash
go run ./cmd/tuskbase init-mcp codex --mode demo
go run ./cmd/tuskbase init-mcp codex --mode local-basic
```

The REST API is optional and is not mounted by the default Local Basic daemon. Enable it explicitly only for local development/debugging:

```bash
go run ./cmd/tuskbase serve --http-mcp --rest
```

## Current Interfaces

The first product surface is MCP. Tuskbase currently supports two MCP paths from the same application core: stdio MCP for Demo mode and loopback HTTP MCP for Local Basic daemon mode.

The current runtime is a Go local service backed by local SQLite storage by default. Packaging wrappers such as npm, Homebrew, or GitHub release binaries remain distribution conveniences.

A UI can come after the API and MCP flows are useful. SDKs can come after the core contracts are stable. The CLI exists as a guided front door for setup, diagnostics, and daemon operation.

### Optional REST API

| Capability | Purpose |
|---|---|
| Attach workspace | Create or update repo workspace context. |
| Lookup memory | Retrieve relevant repo memory. |
| Preflight proposal | Evaluate a proposal before an agent acts. |
| Remember decision | Record a final decision. |
| Recent decisions | List recent decisions for a workspace. |
| Active conflicts | List active conflicts for a workspace. |
| Health and status | Report local server, adapter, and indexing health. |

### MCP

| Tool | Purpose |
|---|---|
| `tuskbase_attach` | Attach or refresh repo workspace context when exposed to agents. |
| `tuskbase_lookup` | Retrieve context before an agent acts. |
| `tuskbase_preflight` | Detect proposal relationships and conflicts. |
| `tuskbase_remember` | Store a completed decision. |
| `tuskbase_recent` | Show recent decisions for the workspace. |
| `tuskbase_conflicts` | Show active conflicts for the workspace. |

## Architecture Direction

Tuskbase is interface-first. The product is not Postgres, pgvector, Qdrant, Kafka, a Go package, an MCP SDK, a UI framework, an SDK, or any single embedding provider. Those are adapters.

The intended shape is:

```text
HTTP API / MCP / later UI / later SDKs / optional support CLI
  -> application use cases
    -> domain model
      -> infrastructure interfaces
        -> replaceable adapters
```

The domain and application layers should depend on explicit ports such as `EntryStore`, `GraphStore`, `VectorIndex`, `DocumentStore`, `ReceiptStore`, `ConflictStore`, and `EmbeddingProvider`. Concrete technologies belong at the composition root and adapter layer.

SQLite is the default durable local adapter because Tuskbase should be easy to run on a developer machine. A Postgres store adapter now exists behind the same ports for shared/team deployments, but it is still an adapter rather than product identity. Canonical records live behind store interfaces. Search indexes are derived and rebuildable. Indexing failures must not cause a decision to be lost.

## Docs

| Document | Purpose |
|---|---|
| [Product Brief](docs/00_product_brief.md) | Product identity, target users, core loop, and non-goals. |
| [Architecture](docs/01_architecture.md) | Layering, interfaces, flows, and anti-drift rules. |
| [Technology Direction](docs/02_tech_stack.md) | Current technology defaults and adapter boundaries. |
| [Product Tiers](docs/03_product_tiers.md) | Demo, Local Basic, Local Shared, and Hosted product tier direction. |
| [Auth And Security](docs/04_auth_security.md) | Tiered auth, identity, and security direction. |
| [Security](SECURITY.md) | Vulnerability reporting and current security posture. |
| [Changelog](CHANGELOG.md) | Release notes and notable project changes. |
| [Agent Guide](AGENTS.md) | Instructions for AI coding agents working in this repo. |

## License

Apache 2.0. See [LICENSE](LICENSE).
