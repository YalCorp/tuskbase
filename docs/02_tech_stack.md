# Tuskbase Technology Direction

Tuskbase is intentionally adapter-oriented. The technologies below are current implementation defaults, not product identity.

The core product should remain stable if a database, vector index, embedding provider, API framework, MCP implementation, UI framework, SDK strategy, or optional CLI changes later.

## Product Surfaces

The first product surfaces are:

- local HTTP API server,
- local MCP server.

Both surfaces should use the same application core. A UI can come after the API and MCP flows are useful. SDKs can come after the core contracts are stable. A CLI is not a primary offering; it may be added later only for local administration, development, or debugging if needed.

## Current Defaults

These defaults are meant to make the first implementation practical while preserving replaceability.

| Area | Current Direction |
|---|---|
| Server runtime | Go local service, built toward a single native binary. |
| HTTP surface | Go HTTP adapter at the edge, sharing the same application core as MCP. |
| Agent integration | Local MCP server as a first-class surface, hosted by the Go service. |
| Durable storage | SQLite as the zero-config local default; Postgres available as a shared/team store adapter behind the same ports. |
| Semantic retrieval | Text search first, with vector retrieval behind `VectorIndex`; pgvector or another vector adapter can follow without becoming core. |
| Embeddings | Local, OpenAI-compatible, and deterministic test providers behind one provider interface. |
| Tests | Offline-friendly Go tests with fake providers by default. |
| Distribution | Native binary first; npm/Homebrew-style wrappers can come later as packaging conveniences. |

These choices should live at the edge of the system. Domain and application code should depend on interfaces rather than concrete libraries or services.

The Postgres adapter is implemented as a `database/sql` adapter. Runtime composition must register a Postgres driver, such as pgx stdlib, before calling `postgres.Open`; SQLite remains the default binary wiring.

## Product Tiers And Storage Direction

Tuskbase should grow through four product tiers. The same application core and decision model should remain stable across them.

| Tier | Intended use | MCP transport | Durable store | Retrieval direction |
|---|---|---|---|---|
| Demo | Prove Tuskbase works with the least setup | stdio MCP | SQLite | text search |
| Local Basic | One developer using one or more local agents on one machine | local HTTP MCP daemon | SQLite | text search, optional OpenAI embeddings |
| Local Shared | Heavy local multi-agent usage or small shared setup | local HTTP MCP daemon | Postgres | pgvector with OpenAI, Ollama, or future embedded embeddings |
| Hosted | Future managed team product | managed HTTP MCP | managed Postgres | managed vector retrieval, Qdrant optional at scale |

SQLite is the Demo and Local Basic default, not the ceiling for serious multi-agent workflows. Local Shared should use Postgres so Codex, Claude, Cursor, and other tools can coordinate through one durable decision store. Postgres also creates a natural path to pgvector, Supabase, self-hosted deployments, and future hosted Tuskbase.

Vector search is a derived retrieval layer. Canonical decisions live in SQLite or Postgres first. pgvector is the default serious vector path because it keeps vectors with Postgres data. Qdrant or another vector database should remain an optional scale adapter, not a first-use requirement.

Embeddings should be provider-based:

- no embeddings required for Demo,
- optional OpenAI embeddings for Local Basic,
- OpenAI, Ollama, and eventually an embedded local model for Local Shared,
- managed provider choices for Hosted later.

Temporal graph behavior should be modeled in the durable store first with decision relationships, conflicts, `valid_from`, `valid_to`, `transaction_time`, and status fields. A dedicated temporal graph database is deferred until real query needs prove the relational model insufficient.

## Adapter Boundaries

Tuskbase should keep clear interfaces around:

- durable records,
- decision relationships,
- repo documents,
- lookup receipts,
- conflicts,
- vector search,
- embeddings,
- HTTP and MCP surfaces.

Replacing one adapter should not require rewriting the decision model or core workflows.

## Local-First Requirements

The first useful version should run on a developer machine with:

- one local Go service,
- a local API server,
- a local MCP server,
- local durable storage,
- optional local embeddings,
- simple local authentication,
- no required cloud account,
- no required external embedding service for tests.

Cloud sync, multi-user governance, enterprise auth, and hosted operations come only after local value is proven.

## Future Direction

Likely later additions:

- audit UI after API and MCP flows are useful,
- SDKs after contracts are stable,
- production wiring and driver selection for alternate durable stores,
- alternate vector indexes behind the vector interface,
- hooks for coding-agent workflows,
- optional support CLI if local operations need it.

These should remain extensions of the same core, not separate product centers.

## Initial Non-Goals

The initial build should avoid:

- dashboard-first development,
- SDK-first development,
- cloud-first architecture,
- enterprise governance workflows,
- required external queues,
- required dedicated graph databases,
- required external embedding services for default tests.
