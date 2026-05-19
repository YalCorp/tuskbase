# AGENTS.md

This file tells AI coding agents how to work in this repository. It is an operating manual, not product marketing.

## Current Repo Stage

Tuskbase is in foundation stage.

- The repository currently contains product and architecture docs only.
- Runtime API routes, MCP tools, adapters, UI, SDKs, and package structure are planned, not implemented.
- Do not claim shipped functionality exists until code implements it.
- Do not add application code, package scaffolding, contribution rules, or governance files unless explicitly asked.

## Source Priority

Use sources in this order:

1. Explicit instructions from Priyavrat in the current task.
2. Active docs in this repository.
3. The current `README.md`.
4. Historical or external reference material.

If sources conflict, prefer newer explicit direction from Priyavrat, then active Tuskbase docs. Do not copy another product's structure, language, tech choices, or roadmap into Tuskbase without translating it into Tuskbase's product direction.

## Product Guardrails

Tuskbase is local-first, repo-aware memory and decision history for AI coding agents.

The core workflow is:

```text
attach -> lookup -> preflight -> remember
```

Keep the center of gravity on decision memory:

- what was decided,
- why it was decided,
- who or what decided it,
- which workspace and repo context it belongs to,
- what evidence supports it,
- whether new work contradicts active direction.

Do not turn Tuskbase into:

- a generic chatbot memory database,
- a notes app,
- a task manager,
- a wiki replacement,
- a dashboard-first product,
- an enterprise governance suite,
- a cloud sync product before local value is proven.

## Architecture Guardrails

Design around domain models, application use cases, and explicit interfaces.

Adapters are replaceable. Storage engines, vector indexes, embedding providers, API frameworks, MCP servers, UI frameworks, SDKs, CLI frameworks, queues, hooks, and dashboards must not become core assumptions.

Postgres and pgvector may be first local adapters, but they are not the product. FastAPI and the MCP SDK may be first surface adapters, but they are not the product either. Kafka, Qdrant, SQLite, Neo4j, Typer, Ollama, OpenAI, UI frameworks, SDK tooling, and other tools must stay outside domain and application logic unless a future task explicitly changes that architecture.

Use interface boundaries for concepts such as:

- `EntryStore`
- `GraphStore`
- `VectorIndex`
- `DocumentStore`
- `ReceiptStore`
- `ConflictStore`
- `EmbeddingProvider`

Expected dependency direction:

```text
interfaces / adapters
  -> application use cases
    -> domain model
```

Domain and application code must not import adapter-specific packages directly.

## Editing Rules

- Keep public docs honest about project status.
- Do not describe planned API routes, MCP tools, servers, UI, SDKs, CLI commands, or adapters as available until implemented.
- Prefer concise, repo-specific guidance over broad process boilerplate.
- Keep product docs and README consistent when changing product language.
- Do not add `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, SDK docs, UI docs, CLI docs, or deployment guides unless requested.
- Do not introduce a code skeleton, dependency manifest, Docker setup, CI, CLI, UI, SDK, or tests unless requested.
- Do not hard-code a single storage, vector, embedding, queue, or API technology into architectural language.
- Commit messages must use `type(scope): comment` format, with scope optional: `type: comment`. Examples: `docs: update architecture guide`, `docs(readme): clarify project status`.

## Future Implementation Rules

When implementation begins:

- put business behavior in application use cases, not HTTP, MCP, UI, SDK, or optional CLI handlers,
- put durable records behind store interfaces,
- put vector search behind `VectorIndex`,
- put embeddings behind `EmbeddingProvider`,
- store canonical decisions before derived indexes,
- let canonical decision writes succeed even if embedding or indexing fails,
- make default tests run without network access or real embedding services.

## Verification

For docs-only changes:

- check affected links,
- check that README and docs do not overstate current functionality,
- check that architecture language preserves replaceable adapters.

For future code changes:

- add or update focused tests for changed behavior,
- document the relevant test commands here once the project has a test harness,
- run the smallest meaningful verification before finishing.
