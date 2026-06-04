# Tuskbase

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

**Local-first repo memory for AI coding agents.**

Tuskbase helps coding agents share repo context and decision history before they change code, so each session does not have to rediscover architecture, conventions, or settled decisions from scratch.

Agents working across the same repo should not silently contradict prior direction. Tuskbase turns that drift into an explicit workflow: look up context, preflight a proposal, remember the final decision, and surface conflicts when new work disagrees with active project direction.

> Project status: first implementation slice. This repository includes a local Go service with temporal decision records, SQLite-backed local storage, a Postgres store adapter package with Local Shared runtime selection, Docker-managed pgvector Postgres provisioning for Local Shared setup, deterministic active-memory lookup, optional OpenAI embeddings, preflight conflict detection, lookup receipts, stdio MCP, loopback HTTP MCP, a self-healing stdio MCP bridge for local daemon auth, user-scope daemon lifecycle helpers for local setup, required local bearer auth for HTTP MCP/REST, auth-derived actor attribution for authenticated writes, local key admin commands, and an optional REST API. UI, SDKs, semantic pgvector retrieval, cloud sync, and packaging wrappers are still deferred.

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

Use an installed or stable built `tuskbase` binary for normal local setup. Autostart service installation refuses temporary Go build artifacts, so do not enable autostart from `go run`.

```bash
tuskbase version
tuskbase setup
```

Recommended first setup is Local Basic. Tuskbase generates a local secret, stores it in a private user config file, and attempts to install and start a user-scope daemon service. If the service backend is unavailable, setup degrades without failing because `tuskbase bridge` can still start or wake the local daemon when an MCP client connects.

The Local Basic MCP endpoint is:

```text
http://127.0.0.1:8765/mcp
```

Print client-specific MCP setup help. The default client config uses `tuskbase bridge`, so MCP clients do not need `TUSKBASE_API_KEY` in every shell session. Add `--apply` for supported client automation such as Codex.

```bash
tuskbase connect codex
tuskbase connect codex --apply
tuskbase connect claude
tuskbase connect cursor
```

> **Important: Codex may show `Auth: Unsupported`.**
> This is expected for Tuskbase's default local setup because Codex launches
> `tuskbase bridge` over stdio, and the bridge authenticates to the local daemon
> internally. It does not mean Tuskbase daemon auth is disabled. Run
> `tuskbase status` to see daemon health, service state, and the active daemon auth policy.

Manage local auth keys:

```bash
tuskbase auth list
tuskbase auth rotate
tuskbase auth add --name windsurf --role agent
tuskbase auth rotate --name codex
```

Local Shared now has three setup entry points. The out-of-the-box path requires Docker Compose and starts a private Postgres+pgvector container on loopback port `8766` by default:

```bash
tuskbase setup --mode local-shared --yes
```

Existing Postgres and Supabase users can bring their own DSN instead:

```bash
tuskbase setup --mode local-shared --postgres-source existing --postgres-dsn postgres://tuskbase:secret@localhost:5432/tuskbase?sslmode=disable
tuskbase setup --mode local-shared --postgres-source supabase --postgres-dsn postgres://...
```

All Local Shared Postgres paths require the `vector` extension. The Docker path provisions it; existing Postgres and Supabase setups must allow `CREATE EXTENSION IF NOT EXISTS vector` or have pgvector enabled already. Semantic pgvector retrieval is still deferred. See [.env.example](.env.example).

Manual HTTP/environment-variable setup is still supported for developers and CI with `--transport http`. `TUSKBASE_AGENT_KEYS` takes precedence over `TUSKBASE_API_KEY`; stored setup config is used when neither env var is set.

Use diagnostics when recovering a local setup:

```bash
tuskbase status
tuskbase doctor
tuskbase daemon restart
```

For repository development or previewing generated output without installing autostart, `go run` is still useful:

```bash
go run ./cmd/tuskbase version
go run ./cmd/tuskbase setup --print-only
```

The REST API is optional and is not mounted by the default Local Basic daemon. Enable it explicitly only for local development/debugging after setup:

```bash
tuskbase serve --http-mcp --rest
```

## Current Interfaces

The first product surface is MCP. Tuskbase currently supports stdio MCP for Demo mode, loopback HTTP MCP for daemon mode, and a stdio bridge that lets local MCP clients use Tuskbase-managed credentials without storing bearer tokens in client config. For Local Basic and Local Shared setup, Tuskbase attempts to install a user-scope daemon autostart service; the bridge also checks `/healthz` and starts or wakes the daemon before MCP initialization when the daemon is down. Some MCP clients may label stdio bridge auth as unsupported because the client itself is not managing the bearer token; Tuskbase daemon auth is still enforced behind the bridge.

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

SQLite is the default durable local adapter because Tuskbase should be easy to run on a developer machine. A Postgres store adapter now exists behind the same ports for shared/team deployments, and Local Shared can select it at runtime from a Docker-managed pgvector Postgres instance or a configured DSN. It is still an adapter rather than product identity. Canonical records live behind store interfaces. Search indexes are derived and rebuildable. Indexing failures must not cause a decision to be lost.

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
