# Tuskbase

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Local-first repo memory for AI coding agents.**

Tuskbase helps coding agents share repo context and decision history before they change code, so each session does not have to rediscover architecture, conventions, or settled decisions from scratch.

Agents working across the same repo should not silently contradict prior direction. Tuskbase turns that drift into an explicit workflow: look up context, preflight a proposal, remember the final decision, and surface conflicts when new work disagrees with active project direction.

> Project status: first implementation slice. This repository now includes a local Go service with temporal decision records, SQLite-backed local storage, a Postgres store adapter package, deterministic active-memory lookup, preflight conflict detection, lookup receipts, an HTTP API, and local MCP tools. UI, SDKs, cloud sync, embeddings, and packaging wrappers are still deferred.

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

## Current Interfaces

The first product surfaces are the local HTTP API server and the local MCP server. Both use the same application core in this repository.

The current runtime is a Go local service. It can run the HTTP surface, the stdio MCP surface, or both from the same binary, backed by local SQLite storage. Packaging wrappers such as npm or Homebrew remain later distribution conveniences.

A UI can come after the API and MCP flows are useful. SDKs can come after the core contracts are stable. A CLI is not a primary offering; it may be added later only if it clearly helps local administration, development, or debugging.

### HTTP API

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
| [Security](SECURITY.md) | Vulnerability reporting and current security posture. |
| [Agent Guide](AGENTS.md) | Instructions for AI coding agents working in this repo. |

## License

Apache 2.0. See [LICENSE](LICENSE).
