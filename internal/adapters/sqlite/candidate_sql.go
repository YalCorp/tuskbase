package sqlite

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
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE id = ?`
}

func getDecisionCandidateBySourceHashSQL() string {
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE workspace_id = ? AND source_hash = ?`
}

func listDecisionCandidatesSQL(whereClause string) string {
	return `SELECT ` + candidateSelectColumns + ` FROM decision_candidates WHERE ` + whereClause + ` ORDER BY updated_at DESC LIMIT ?`
}

const updateDecisionCandidateStatusSQL = `
UPDATE decision_candidates
SET status = ?, accepted_decision_id = ?, rejection_summary = ?, updated_at = ?
WHERE id = ? AND workspace_id = ?
`
