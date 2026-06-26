package postgres

const candidateSelectColumns = `
id, workspace_id, type, title, outcome, rationale, confidence, source_path, source_title,
source_snippet, source_hash, detector, status, accepted_decision_id, rejection_summary, created_at, updated_at
`

const upsertDecisionCandidateSQL = `
INSERT INTO decision_candidates(
    id, workspace_id, type, title, outcome, rationale, confidence, source_path, source_title,
    source_snippet, source_hash, detector, status, accepted_decision_id, rejection_summary,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT(workspace_id, source_hash) DO UPDATE SET
    type = excluded.type,
    title = excluded.title,
    outcome = excluded.outcome,
    rationale = excluded.rationale,
    confidence = excluded.confidence,
    source_path = excluded.source_path,
    source_title = excluded.source_title,
    source_snippet = excluded.source_snippet,
    detector = excluded.detector,
    updated_at = excluded.updated_at
`

func getDecisionCandidateSQL() string {
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE id = $1`
}

func getDecisionCandidateBySourceHashSQL() string {
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE workspace_id = $1 AND source_hash = $2`
}

func listDecisionCandidatesSQL(whereClause, limitPlaceholder string) string {
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE ` + whereClause + ` ORDER BY updated_at DESC LIMIT ` + limitPlaceholder
}

const updateDecisionCandidateStatusSQL = `
UPDATE decision_candidates
SET status = $1, accepted_decision_id = $2, rejection_summary = $3, updated_at = $4
WHERE id = $5 AND workspace_id = $6
`
