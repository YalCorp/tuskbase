package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, driverName, dsn string) (*Store, error) {
	if strings.TrimSpace(driverName) == "" {
		return nil, errors.New("postgres driver name is required")
	}
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres dsn is required")
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func Wrap(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("postgres db is required")
	}
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	for _, statement := range strings.Split(schemaSQL, ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

const schemaSQL = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    repo_root TEXT NOT NULL,
    display_name TEXT NOT NULL,
    repo_fingerprint TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS decisions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    actor_json JSONB NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL,
    outcome TEXT NOT NULL,
    rationale TEXT NOT NULL DEFAULT '',
    confidence DOUBLE PRECISION NOT NULL,
    status TEXT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    valid_to TIMESTAMPTZ,
    transaction_time TIMESTAMPTZ NOT NULL,
    precedent_ref TEXT NOT NULL DEFAULT '',
    supersedes_id TEXT NOT NULL DEFAULT '',
    completeness_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    alternatives_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    claims_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    evidence_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    relationships_json JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
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
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS idx_decision_evidence_decision
ON decision_evidence(decision_id);

CREATE TABLE IF NOT EXISTS decision_relationships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    from_decision_id TEXT NOT NULL REFERENCES decisions(id) ON DELETE CASCADE,
    to_decision_id TEXT NOT NULL,
    type TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL,
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
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_repo_documents_workspace_path
ON repo_documents(workspace_id, path, chunk_index);

CREATE TABLE IF NOT EXISTS lookup_receipts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    query TEXT NOT NULL,
    result_count INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
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
    score DOUBLE PRECISION NOT NULL,
    PRIMARY KEY(receipt_id, position)
);

CREATE TABLE IF NOT EXISTS conflicts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    decision_id TEXT,
    proposal TEXT NOT NULL DEFAULT '',
    conflicting_with_id TEXT NOT NULL,
    summary TEXT NOT NULL,
    severity TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL,
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_conflicts_workspace_status
ON conflicts(workspace_id, status, created_at DESC);

CREATE TABLE IF NOT EXISTS search_index (
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_search_index_workspace_kind
ON search_index(workspace_id, kind, entity_id);

CREATE TABLE IF NOT EXISTS vector_index (
    workspace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    body TEXT NOT NULL DEFAULT '',
    embedding vector NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(workspace_id, kind, entity_id, path)
);

CREATE INDEX IF NOT EXISTS idx_vector_index_workspace_kind
ON vector_index(workspace_id, kind, entity_id);
`

func (s *Store) UpsertWorkspace(ctx context.Context, w domain.Workspace) (domain.Workspace, error) {
	if err := w.Validate(); err != nil {
		return domain.Workspace{}, err
	}
	existing, err := s.GetWorkspace(ctx, w.ID)
	if err == nil && !existing.CreatedAt.IsZero() {
		w.CreatedAt = existing.CreatedAt
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO workspaces(id, repo_root, display_name, repo_fingerprint, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT(id) DO UPDATE SET
    repo_root = excluded.repo_root,
    display_name = excluded.display_name,
    repo_fingerprint = excluded.repo_fingerprint,
    updated_at = excluded.updated_at
`, w.ID, w.RepoRoot, w.DisplayName, w.RepoFingerprint, w.CreatedAt.UTC(), w.UpdatedAt.UTC())
	if err != nil {
		return domain.Workspace{}, err
	}
	return w, nil
}

func (s *Store) GetWorkspace(ctx context.Context, id string) (domain.Workspace, error) {
	var w domain.Workspace
	err := s.db.QueryRowContext(ctx, `
SELECT id, repo_root, display_name, repo_fingerprint, created_at, updated_at
FROM workspaces WHERE id = $1
`, id).Scan(&w.ID, &w.RepoRoot, &w.DisplayName, &w.RepoFingerprint, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return domain.Workspace{}, err
	}
	return w, nil
}

func (s *Store) SaveDecision(ctx context.Context, d domain.Decision) error {
	defaultDecisionTimes(&d)
	if d.Status == "" {
		d.Status = domain.DecisionActive
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if d.SupersedesID == d.ID {
		return errors.New("decision cannot supersede itself")
	}
	actor, err := marshalJSON(d.Actor, "{}")
	if err != nil {
		return err
	}
	metadata, err := marshalJSON(d.Metadata, "{}")
	if err != nil {
		return err
	}
	alternatives, err := marshalJSON(d.Alternatives, "[]")
	if err != nil {
		return err
	}
	claims, err := marshalJSON(d.Claims, "[]")
	if err != nil {
		return err
	}
	evidence, err := marshalJSON(d.Evidence, "[]")
	if err != nil {
		return err
	}
	relationships, err := marshalJSON(d.Relationships, "[]")
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if d.SupersedesID != "" {
		result, err := tx.ExecContext(ctx, `
UPDATE decisions
SET valid_to = $1, status = $2, updated_at = $3
WHERE id = $4 AND workspace_id = $5 AND valid_to IS NULL
`, d.ValidFrom.UTC(), domain.DecisionSuperseded, d.UpdatedAt.UTC(), d.SupersedesID, d.WorkspaceID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return fmt.Errorf("supersedes_id %q does not reference an active decision", d.SupersedesID)
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO decisions(
    id, workspace_id, actor_json, type, title, outcome, rationale, confidence, status,
    valid_from, valid_to, transaction_time, precedent_ref, supersedes_id, completeness_score, metadata_json,
    alternatives_json, claims_json, evidence_json, relationships_json, created_at, updated_at
)
VALUES ($1, $2, $3::jsonb, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb, $17::jsonb, $18::jsonb, $19::jsonb, $20::jsonb, $21, $22)
`, d.ID, d.WorkspaceID, actor, d.Type, d.Title, d.Outcome, d.Rationale, d.Confidence, d.Status, d.ValidFrom.UTC(), nullableTime(d.ValidTo), d.TransactionTime.UTC(), d.PrecedentRef, d.SupersedesID, d.CompletenessScore, metadata, alternatives, claims, evidence, relationships, d.CreatedAt.UTC(), d.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if err := replaceDecisionChildren(ctx, tx, d); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) GetDecision(ctx context.Context, id string) (domain.Decision, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+decisionSelectColumns+` FROM decisions WHERE id = $1`, id)
	d, err := scanDecisionBase(row)
	if err != nil {
		return domain.Decision{}, err
	}
	if err := s.loadDecisionChildren(ctx, &d); err != nil {
		return domain.Decision{}, err
	}
	return d, nil
}

func (s *Store) RecentDecisions(ctx context.Context, workspaceID string, limit int) ([]domain.Decision, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+decisionSelectColumns+` FROM decisions WHERE workspace_id = $1 AND valid_to IS NULL ORDER BY created_at DESC LIMIT $2`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var decisions []domain.Decision
	for rows.Next() {
		d, err := scanDecisionBase(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadDecisionChildren(ctx, &d); err != nil {
			return nil, err
		}
		decisions = append(decisions, d)
	}
	return decisions, rows.Err()
}

func replaceDecisionChildren(ctx context.Context, tx *sql.Tx, d domain.Decision) error {
	for _, target := range []struct{ table, column string }{
		{"decision_alternatives", "decision_id"},
		{"decision_claims", "decision_id"},
		{"decision_evidence", "decision_id"},
		{"decision_relationships", "from_decision_id"},
	} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = $1", target.table, target.column), d.ID); err != nil {
			return err
		}
	}
	for _, alternative := range d.Alternatives {
		_, err := tx.ExecContext(ctx, `INSERT INTO decision_alternatives(id, decision_id, title, outcome, reason) VALUES ($1, $2, $3, $4, $5)`, alternative.ID, d.ID, alternative.Title, alternative.Outcome, alternative.Reason)
		if err != nil {
			return err
		}
	}
	for _, claim := range d.Claims {
		if strings.TrimSpace(claim.Text) == "" {
			continue
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO decision_claims(id, decision_id, text) VALUES ($1, $2, $3)`, claim.ID, d.ID, claim.Text)
		if err != nil {
			return err
		}
	}
	for _, evidence := range d.Evidence {
		metadata, err := marshalJSON(evidence.Metadata, "{}")
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO decision_evidence(id, decision_id, kind, uri, title, snippet, metadata_json) VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)`, evidence.ID, d.ID, evidence.Kind, evidence.URI, evidence.Title, evidence.Snippet, metadata)
		if err != nil {
			return err
		}
	}
	for _, rel := range d.Relationships {
		_, err := tx.ExecContext(ctx, `INSERT INTO decision_relationships(id, workspace_id, from_decision_id, to_decision_id, type, confidence, reason) VALUES ($1, $2, $3, $4, $5, $6, $7)`, rel.ID, d.WorkspaceID, d.ID, rel.ToDecisionID, rel.Type, rel.Confidence, rel.Reason)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadDecisionChildren(ctx context.Context, d *domain.Decision) error {
	alternatives, err := s.listAlternatives(ctx, d.ID)
	if err != nil {
		return err
	}
	if len(alternatives) > 0 {
		d.Alternatives = alternatives
	}
	claims, err := s.listClaims(ctx, d.ID)
	if err != nil {
		return err
	}
	if len(claims) > 0 {
		d.Claims = claims
	}
	evidence, err := s.listEvidence(ctx, d.ID)
	if err != nil {
		return err
	}
	if len(evidence) > 0 {
		d.Evidence = evidence
	}
	relationships, err := s.listRelationships(ctx, d.ID)
	if err != nil {
		return err
	}
	if len(relationships) > 0 {
		d.Relationships = relationships
	}
	return nil
}

func (s *Store) listAlternatives(ctx context.Context, decisionID string) ([]domain.Alternative, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, decision_id, title, outcome, reason FROM decision_alternatives WHERE decision_id = $1 ORDER BY id`, decisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Alternative
	for rows.Next() {
		var a domain.Alternative
		if err := rows.Scan(&a.ID, &a.DecisionID, &a.Title, &a.Outcome, &a.Reason); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) listClaims(ctx context.Context, decisionID string) ([]domain.Claim, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, decision_id, text FROM decision_claims WHERE decision_id = $1 ORDER BY id`, decisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Claim
	for rows.Next() {
		var c domain.Claim
		if err := rows.Scan(&c.ID, &c.DecisionID, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) listEvidence(ctx context.Context, decisionID string) ([]domain.Evidence, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, decision_id, kind, uri, title, snippet, metadata_json FROM decision_evidence WHERE decision_id = $1 ORDER BY id`, decisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Evidence
	for rows.Next() {
		var e domain.Evidence
		var metadata []byte
		if err := rows.Scan(&e.ID, &e.DecisionID, &e.Kind, &e.URI, &e.Title, &e.Snippet, &metadata); err != nil {
			return nil, err
		}
		if err := unmarshalJSONBytes(metadata, &e.Metadata); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) listRelationships(ctx context.Context, decisionID string) ([]domain.DecisionRelationship, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, workspace_id, from_decision_id, to_decision_id, type, confidence, reason FROM decision_relationships WHERE from_decision_id = $1 ORDER BY id`, decisionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DecisionRelationship
	for rows.Next() {
		var r domain.DecisionRelationship
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.FromDecisionID, &r.ToDecisionID, &r.Type, &r.Confidence, &r.Reason); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceWorkspaceDocuments(ctx context.Context, workspaceID string, docs []domain.RepoDocument) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM repo_documents WHERE workspace_id = $1`, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND kind = 'document'`, workspaceID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vector_index WHERE workspace_id = $1 AND kind = 'document'`, workspaceID); err != nil {
		return err
	}
	for _, doc := range docs {
		_, err := tx.ExecContext(ctx, `
INSERT INTO repo_documents(id, workspace_id, path, title, content, chunk_index, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
`, doc.ID, workspaceID, doc.Path, doc.Title, doc.Content, doc.ChunkIndex, doc.CreatedAt.UTC(), doc.UpdatedAt.UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveLookupReceipt(ctx context.Context, r domain.LookupReceipt) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
INSERT INTO lookup_receipts(id, workspace_id, query, result_count, created_at)
VALUES ($1, $2, $3, $4, $5)
`, r.ID, r.WorkspaceID, r.Query, r.ResultCount, r.CreatedAt.UTC())
	if err != nil {
		return err
	}
	for i, result := range r.Results {
		_, err := tx.ExecContext(ctx, `
INSERT INTO lookup_receipt_results(receipt_id, position, kind, entity_id, workspace_id, title, path, snippet, score)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
`, r.ID, i, result.Kind, result.EntityID, result.WorkspaceID, result.Title, result.Path, result.Snippet, result.Score)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveConflict(ctx context.Context, c domain.Conflict) error {
	if err := c.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO conflicts(id, workspace_id, decision_id, proposal, conflicting_with_id, summary, severity, confidence, status, created_at, resolved_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, c.ID, c.WorkspaceID, c.DecisionID, c.Proposal, c.ConflictingWithID, c.Summary, c.Severity, c.Confidence, c.Status, c.CreatedAt.UTC(), nullableTime(c.ResolvedAt))
	return err
}

func (s *Store) ListOpenConflicts(ctx context.Context, workspaceID string) ([]domain.Conflict, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT c.id, c.workspace_id, c.decision_id, c.proposal, c.conflicting_with_id, c.summary, c.severity, c.confidence, c.status, c.created_at, c.resolved_at
FROM conflicts c
WHERE c.workspace_id = $1
  AND c.status = 'open'
  AND EXISTS (SELECT 1 FROM decisions d WHERE d.id = c.conflicting_with_id AND d.workspace_id = c.workspace_id AND d.valid_to IS NULL)
ORDER BY c.created_at DESC
`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conflicts []domain.Conflict
	for rows.Next() {
		var c domain.Conflict
		var resolved sql.NullTime
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.DecisionID, &c.Proposal, &c.ConflictingWithID, &c.Summary, &c.Severity, &c.Confidence, &c.Status, &c.CreatedAt, &resolved); err != nil {
			return nil, err
		}
		if resolved.Valid {
			t := resolved.Time
			c.ResolvedAt = &t
		}
		conflicts = append(conflicts, c)
	}
	return conflicts, rows.Err()
}

func (s *Store) IndexDecision(ctx context.Context, d domain.Decision) error {
	if !d.IsActive() {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM search_index WHERE workspace_id = $1 AND entity_id = $2 AND kind IN ('decision', 'claim', 'evidence', 'alternative')`, d.WorkspaceID, d.ID); err != nil {
		return err
	}
	text := strings.Join([]string{d.Type, d.Title, d.Outcome, d.Rationale, d.PrecedentRef, d.SupersedesID}, "\n")
	if err := s.insertSearch(ctx, d.WorkspaceID, "decision", d.ID, d.Title, "", text); err != nil {
		return err
	}
	for _, alternative := range d.Alternatives {
		text := strings.TrimSpace(strings.Join([]string{alternative.Title, alternative.Outcome, alternative.Reason}, "\n"))
		if text == "" {
			continue
		}
		if err := s.insertSearch(ctx, d.WorkspaceID, "alternative", d.ID, d.Title, "", text); err != nil {
			return err
		}
	}
	for _, claim := range d.Claims {
		if strings.TrimSpace(claim.Text) == "" {
			continue
		}
		if err := s.insertSearch(ctx, d.WorkspaceID, "claim", d.ID, d.Title, "", claim.Text); err != nil {
			return err
		}
	}
	for _, evidence := range d.Evidence {
		text := strings.TrimSpace(strings.Join([]string{evidence.Kind, evidence.URI, evidence.Title, evidence.Snippet}, "\n"))
		if text == "" {
			continue
		}
		if err := s.insertSearch(ctx, d.WorkspaceID, "evidence", d.ID, d.Title, evidence.URI, text); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) IndexDocument(ctx context.Context, doc domain.RepoDocument) error {
	return s.insertSearch(ctx, doc.WorkspaceID, "document", doc.ID, doc.Title, doc.Path, doc.Content)
}

func (s *Store) insertSearch(ctx context.Context, workspaceID, kind, entityID, title, path, body string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO search_index(workspace_id, kind, entity_id, title, path, body)
VALUES ($1, $2, $3, $4, $5, $6)
`, workspaceID, kind, entityID, title, path, body)
	return err
}

func (s *Store) Search(ctx context.Context, q ports.SearchQuery) ([]ports.SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	tokens := searchTokens(q.Query)
	if len(tokens) == 0 {
		return nil, nil
	}
	clauses := make([]string, 0, len(tokens))
	args := []any{q.WorkspaceID}
	for _, token := range tokens {
		args = append(args, "%"+token+"%")
		placeholder := fmt.Sprintf("$%d", len(args))
		clauses = append(clauses, "lower(title || ' ' || path || ' ' || body) LIKE "+placeholder)
	}
	args = append(args, max(200, limit))
	query := `
SELECT kind, entity_id, workspace_id, title, path, body
FROM search_index
WHERE workspace_id = $1
  AND (` + strings.Join(clauses, " OR ") + `)
  AND (
      kind = 'document'
      OR EXISTS (
          SELECT 1 FROM decisions d
          WHERE d.id = search_index.entity_id
            AND d.workspace_id = search_index.workspace_id
            AND d.valid_to IS NULL
      )
  )
LIMIT $` + fmt.Sprint(len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ports.SearchResult
	for rows.Next() {
		var r ports.SearchResult
		var body string
		if err := rows.Scan(&r.Kind, &r.EntityID, &r.WorkspaceID, &r.Title, &r.Path, &body); err != nil {
			return nil, err
		}
		r.Snippet = snippet(body)
		r.Score = tokenScore(tokens, strings.ToLower(strings.Join([]string{r.Title, r.Path, body}, " ")))
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (s *Store) UpsertVector(ctx context.Context, record ports.VectorRecord) error {
	if strings.TrimSpace(record.WorkspaceID) == "" || len(record.Vector) == 0 {
		return nil
	}
	vector, err := vectorLiteral(record.Vector)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO vector_index(workspace_id, kind, entity_id, title, path, body, embedding, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7::vector, $8)
ON CONFLICT(workspace_id, kind, entity_id, path) DO UPDATE SET
    title = excluded.title,
    body = excluded.body,
    embedding = excluded.embedding,
    updated_at = excluded.updated_at
`, record.WorkspaceID, record.Kind, record.EntityID, record.Title, record.Path, snippet(record.Text), vector, time.Now().UTC())
	return err
}

func (s *Store) SearchVector(ctx context.Context, q ports.VectorQuery) ([]ports.SearchResult, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 10
	}
	if len(q.Vector) == 0 || strings.TrimSpace(q.WorkspaceID) == "" {
		return nil, nil
	}
	vector, err := vectorLiteral(q.Vector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT kind, entity_id, workspace_id, title, path, body, 1 - (embedding <=> $2::vector) AS score
FROM vector_index
WHERE workspace_id = $1
  AND vector_dims(embedding) = vector_dims($2::vector)
  AND (
      kind = 'document'
      OR EXISTS (
          SELECT 1 FROM decisions d
          WHERE d.id = vector_index.entity_id
            AND d.workspace_id = vector_index.workspace_id
            AND d.valid_to IS NULL
      )
  )
ORDER BY embedding <=> $2::vector
LIMIT $3
`, q.WorkspaceID, vector, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []ports.SearchResult
	for rows.Next() {
		var r ports.SearchResult
		if err := rows.Scan(&r.Kind, &r.EntityID, &r.WorkspaceID, &r.Title, &r.Path, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

const decisionSelectColumns = `
id, workspace_id, actor_json, type, title, outcome, rationale, confidence, status,
valid_from, valid_to, transaction_time, precedent_ref, supersedes_id, completeness_score, metadata_json,
alternatives_json, claims_json, evidence_json, relationships_json, created_at, updated_at
`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDecisionBase(row rowScanner) (domain.Decision, error) {
	var d domain.Decision
	var actorJSON, metadataJSON, alternativesJSON, claimsJSON, evidenceJSON, relationshipsJSON []byte
	var validTo sql.NullTime
	if err := row.Scan(
		&d.ID, &d.WorkspaceID, &actorJSON, &d.Type, &d.Title, &d.Outcome, &d.Rationale, &d.Confidence, &d.Status,
		&d.ValidFrom, &validTo, &d.TransactionTime, &d.PrecedentRef, &d.SupersedesID, &d.CompletenessScore, &metadataJSON,
		&alternativesJSON, &claimsJSON, &evidenceJSON, &relationshipsJSON, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(actorJSON, &d.Actor); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(metadataJSON, &d.Metadata); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(alternativesJSON, &d.Alternatives); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(claimsJSON, &d.Claims); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(evidenceJSON, &d.Evidence); err != nil {
		return domain.Decision{}, err
	}
	if err := unmarshalJSONBytes(relationshipsJSON, &d.Relationships); err != nil {
		return domain.Decision{}, err
	}
	if validTo.Valid {
		t := validTo.Time
		d.ValidTo = &t
	}
	return d, nil
}

func defaultDecisionTimes(d *domain.Decision) {
	now := time.Now().UTC()
	if d.CreatedAt.IsZero() {
		d.CreatedAt = now
	}
	if d.UpdatedAt.IsZero() {
		d.UpdatedAt = d.CreatedAt
	}
	if d.ValidFrom.IsZero() {
		d.ValidFrom = d.CreatedAt
	}
	if d.TransactionTime.IsZero() {
		d.TransactionTime = d.CreatedAt
	}
}

func marshalJSON(value any, fallback string) (string, error) {
	if value == nil {
		return fallback, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(data)) == "null" {
		return fallback, nil
	}
	return string(data), nil
}

func unmarshalJSONBytes(value []byte, out any) error {
	if len(value) == 0 {
		return nil
	}
	return json.Unmarshal(value, out)
}

func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC()
}

var searchTokenRE = regexp.MustCompile(`[a-z0-9_]+`)

func searchTokens(query string) []string {
	raw := searchTokenRE.FindAllString(strings.ToLower(query), -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(raw))
	for _, token := range raw {
		if len(token) < 2 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
		if len(out) == 12 {
			break
		}
	}
	return out
}

func tokenScore(tokens []string, text string) float64 {
	var score float64
	for _, token := range tokens {
		if strings.Contains(text, token) {
			score++
		}
	}
	return score / float64(max(1, len(tokens)))
}

func vectorLiteral(vector []float32) (string, error) {
	if len(vector) == 0 {
		return "", errors.New("vector is required")
	}
	parts := make([]string, 0, len(vector))
	for _, value := range vector {
		f := float64(value)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", errors.New("vector values must be finite")
		}
		parts = append(parts, strconv.FormatFloat(f, 'g', -1, 32))
	}
	return "[" + strings.Join(parts, ",") + "]", nil
}

func snippet(body string) string {
	body = strings.TrimSpace(body)
	if len(body) <= 240 {
		return body
	}
	return body[:240] + "..."
}
