PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    repo_root TEXT NOT NULL,
    display_name TEXT NOT NULL,
    repo_fingerprint TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_json TEXT NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    outcome TEXT NOT NULL,
    rationale TEXT NOT NULL DEFAULT '',
    confidence REAL NOT NULL,
    status TEXT NOT NULL,
    valid_from TEXT NOT NULL,
    valid_to TEXT,
    transaction_time TEXT NOT NULL,
    precedent_ref TEXT NOT NULL DEFAULT '',
    supersedes_id TEXT NOT NULL DEFAULT '',
    completeness_score REAL NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    alternatives_json TEXT NOT NULL DEFAULT '[]',
    claims_json TEXT NOT NULL DEFAULT '[]',
    evidence_json TEXT NOT NULL DEFAULT '[]',
    relationships_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_decisions_workspace_active_created
ON decisions(workspace_id, valid_to, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_decisions_supersedes
ON decisions(workspace_id, supersedes_id);

CREATE TABLE IF NOT EXISTS decision_alternatives (
    id TEXT PRIMARY KEY,
    decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_decision_alternatives_decision
ON decision_alternatives(decision_id);

CREATE TABLE IF NOT EXISTS decision_claims (
    id TEXT PRIMARY KEY,
    decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    text TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_decision_claims_decision
ON decision_claims(decision_id);

CREATE TABLE IF NOT EXISTS decision_evidence (
    id TEXT PRIMARY KEY,
    decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT '',
    uri TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_decision_evidence_decision
ON decision_evidence(decision_id);

CREATE TABLE IF NOT EXISTS decision_relationships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    from_decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    to_decision_id TEXT NOT NULL,
    type TEXT NOT NULL,
    confidence REAL NOT NULL,
    reason TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_decision_relationships_from
ON decision_relationships(workspace_id, from_decision_id, type);

CREATE INDEX IF NOT EXISTS idx_decision_relationships_to
ON decision_relationships(workspace_id, to_decision_id, type);

CREATE TABLE IF NOT EXISTS repo_documents (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    chunk_index INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_repo_documents_workspace_path
ON repo_documents(workspace_id, path, chunk_index);

CREATE TABLE IF NOT EXISTS lookup_receipts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    query TEXT NOT NULL,
    result_count INTEGER NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS lookup_receipt_results (
    receipt_id TEXT NOT NULL REFERENCES lookup_receipts(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL,
    PRIMARY KEY(receipt_id, position)
);


CREATE TABLE IF NOT EXISTS decision_assessments (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    actor_json TEXT NOT NULL,
    signal TEXT NOT NULL,
    score INTEGER NOT NULL DEFAULT 0,
    summary TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_decision_assessments_workspace_decision
ON decision_assessments(workspace_id, decision_id, created_at DESC);

CREATE TABLE IF NOT EXISTS conflicts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    decision_id TEXT,
    proposal TEXT NOT NULL DEFAULT '',
    conflicting_with_id TEXT NOT NULL,
    summary TEXT NOT NULL,
    severity TEXT NOT NULL,
    confidence REAL NOT NULL,
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_conflicts_workspace_status
ON conflicts(workspace_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS conflict_resolutions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    conflict_id TEXT NOT NULL REFERENCES conflicts(id) ON DELETE CASCADE,
    actor_json TEXT NOT NULL,
    action TEXT NOT NULL,
    summary TEXT NOT NULL,
    decision_id TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_conflict_resolutions_conflict
ON conflict_resolutions(workspace_id, conflict_id, created_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS search_index USING fts5(
    workspace_id UNINDEXED,
    kind UNINDEXED,
    entity_id UNINDEXED,
    title,
    path UNINDEXED,
    body,
    tokenize='porter unicode61'
);


CREATE TABLE IF NOT EXISTS vector_index (
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    vector_json TEXT NOT NULL,
    PRIMARY KEY(workspace_id, kind, entity_id, path)
);

CREATE INDEX IF NOT EXISTS idx_vector_index_workspace_kind
ON vector_index(workspace_id, kind, entity_id);
