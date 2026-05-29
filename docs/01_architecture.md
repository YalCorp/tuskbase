# Tuskbase Architecture

Tuskbase is designed so the product core stays stable while adapters change. The memory and decision model should not be tied to one database, vector store, embedding provider, framework, UI, SDK, or command-line tool.

## Core Principle

Tuskbase is interface-first.

The core product behavior is repo-aware decision memory:

- attach a workspace,
- retrieve relevant context,
- check proposals against prior direction,
- remember final decisions,
- surface conflicts when new work contradicts active context.

Those behaviors should live in the domain and application layers. Technologies such as API frameworks, MCP SDKs, databases, vector stores, embedding providers, queues, UI frameworks, SDKs, and optional CLIs belong at the edges.

## Runtime Shape

The first product surfaces are the local HTTP API server and local MCP server. Both should call the same application use cases.

The intended runtime is a Go local service. Go is a product delivery choice: it supports a single native binary, straightforward concurrency, low local resource use, and simple packaging for agent workflows. Domain and application logic should still avoid depending on concrete HTTP, MCP, database, vector, or embedding packages.

Later surfaces, such as a UI or SDKs, should use the same contracts or application core rather than introducing separate business logic.

```text
HTTP API / MCP / later UI / later SDKs / optional support CLI
  -> application use cases
    -> domain model
      -> infrastructure interfaces
        -> replaceable adapters
```

## Layers

### Domain Model

The domain model owns product vocabulary and invariants: workspaces, actors, decisions, claims, evidence, relationships, documents, receipts, conflicts, sessions, and handoffs.

The domain layer should not know where data is stored, how embeddings are generated, which API framework is serving requests, or which agent client is connected.

### Application Use Cases

Application use cases own workflows such as attaching a workspace, looking up memory, preflighting a proposal, remembering a decision, listing recent decisions, and listing conflicts.

Use cases should orchestrate interfaces. They should not call raw SQL, framework handlers, provider SDKs, queue clients, UI code, or MCP code directly.

### Infrastructure Interfaces

Infrastructure interfaces define the boundaries between core behavior and replaceable technology.

Important boundaries include:

- durable records,
- decision relationships,
- repo documents,
- lookup receipts,
- conflicts,
- vector search,
- embeddings.

Concrete adapters implement these boundaries. Replacing an adapter should not require rewriting the domain model or application workflows.

## Canonical Records And Derived Indexes

Canonical records should be stored through durable store interfaces. The first durable implementation may be SQLite for zero-config local use, while Postgres can follow as a team or scale adapter. The core architecture should not assume either one.

Vector search is a retrieval layer, not the source of truth. Vector indexes are derived and rebuildable. If embedding generation or vector indexing fails, canonical decision writes should still succeed and derived indexing should be repairable later.

## Conflict And Relationship Model

Tuskbase should model relationships between decisions over time: whether new work follows, extends, duplicates, supersedes, or conflicts with prior direction.

The exact storage shape is an implementation detail. Phase 1 can use a relational adapter for this model, but a dedicated graph database should not be required for the product core.

## Surface Order

Phase 1 should focus on:

- single-process Go local service,
- local HTTP API server,
- local MCP server,
- shared application use cases,
- replaceable storage, vector, and embedding adapters,
- offline-friendly Go tests.

A UI comes after the API and MCP flows are useful. SDKs come after the core contracts are stable. The CLI should stay focused on setup, diagnostics, daemon operation, and local auth administration rather than becoming the product center.

## Security Shape

Tuskbase starts local-first. Initial auth should be simple local authentication suitable for a developer machine.

Phase 1 should not include cloud auth, enterprise governance, RBAC, or multi-tenant controls. Those can come later only if local value is already proven.

## Failure Policy

- Never lose a decision because embedding generation fails.
- Store canonical decision records before derived indexes.
- Mark derived indexing as pending or repairable when it fails.
- Default tests should not depend on network access or real embedding services.

## Architecture Guardrails

- Keep domain and application logic independent from frameworks and concrete infrastructure.
- Keep API and MCP behavior backed by the same use cases.
- Treat storage, vector search, embeddings, UI, SDKs, queues, hooks, and optional CLI as adapters.
- Prefer clear interfaces over hard-coded technology choices.
- Avoid dashboard-first, SDK-first, cloud-first, or enterprise-first architecture in Phase 1.
