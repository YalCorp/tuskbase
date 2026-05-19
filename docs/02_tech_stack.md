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
| Server runtime | Python local service, with FastAPI as the likely first HTTP adapter. |
| Agent integration | Local MCP server as a first-class surface. |
| Durable storage | Postgres as the likely first durable adapter. |
| Semantic retrieval | pgvector as the likely first vector adapter. |
| Embeddings | Local, OpenAI-compatible, and deterministic test providers behind one provider interface. |
| Tests | Offline-friendly Python tests with fake providers by default. |

These choices should live at the edge of the system. Domain and application code should depend on interfaces rather than concrete libraries or services.

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
- alternate durable stores behind store interfaces,
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
