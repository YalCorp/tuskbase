package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

func TestOpenCreatesSchema(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	var count int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name = 'workspaces'`).Scan(&count); err != nil {
		t.Fatalf("schema query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("workspaces table count = %d", count)
	}
}

func TestOpenBackfillsDecisionChildrenWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "tuskbase.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		ID:              "ws_backfill",
		RepoRoot:        t.TempDir(),
		DisplayName:     "backfill",
		RepoFingerprint: "fingerprint",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	decision := domain.Decision{
		ID:                "d_backfill",
		WorkspaceID:       workspace.ID,
		Actor:             domain.Actor{Kind: domain.ActorAgent},
		Type:              "architecture",
		Title:             "Keep decisions canonical",
		Outcome:           "Store canonical decisions before derived child rows.",
		Rationale:         "Backfill should keep older records readable.",
		Confidence:        0.9,
		Status:            domain.DecisionActive,
		ValidFrom:         now,
		TransactionTime:   now,
		CompletenessScore: 0.5,
		Alternatives:      []domain.Alternative{{ID: "a_backfill", DecisionID: "d_backfill", Title: "Skip backfill", Reason: "Would lose compatibility."}},
		Claims:            []domain.Claim{{ID: "c_backfill", DecisionID: "d_backfill", Text: "Startup backfill must not block."}},
		Evidence:          []domain.Evidence{{ID: "e_backfill", DecisionID: "d_backfill", Kind: "test", URI: "store_test.go", Snippet: "Regression coverage."}},
		Relationships: []domain.DecisionRelationship{{
			ID:             "r_backfill",
			WorkspaceID:    workspace.ID,
			FromDecisionID: "d_backfill",
			ToDecisionID:   "prior_decision",
			Type:           domain.RelationshipFollows,
			Confidence:     0.9,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveDecision(ctx, decision); err != nil {
		t.Fatalf("SaveDecision() error = %v", err)
	}
	for _, stmt := range []string{
		`DELETE FROM decision_relationships`,
		`DELETE FROM decision_evidence`,
		`DELETE FROM decision_claims`,
		`DELETE FROM decision_alternatives`,
	} {
		if _, err := store.DB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s failed: %v", stmt, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	openCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reopened, err := Open(openCtx, dbPath)
	if err != nil {
		t.Fatalf("Open() with child backfill error = %v", err)
	}
	defer reopened.Close()
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "alternatives", query: `SELECT count(*) FROM decision_alternatives WHERE decision_id = 'd_backfill'`},
		{name: "claims", query: `SELECT count(*) FROM decision_claims WHERE decision_id = 'd_backfill'`},
		{name: "evidence", query: `SELECT count(*) FROM decision_evidence WHERE decision_id = 'd_backfill'`},
		{name: "relationships", query: `SELECT count(*) FROM decision_relationships WHERE from_decision_id = 'd_backfill'`},
	} {
		var count int
		if err := reopened.DB().QueryRowContext(ctx, tc.query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.name, err)
		}
		if count != 1 {
			t.Fatalf("%s count = %d, want 1", tc.name, count)
		}
	}
}

func TestSaveDecisionNormalizesChildrenAndSupersedes(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		ID:              "ws_store",
		RepoRoot:        t.TempDir(),
		DisplayName:     "store",
		RepoFingerprint: "fingerprint",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	first := domain.Decision{
		ID:                "d1",
		WorkspaceID:       workspace.ID,
		Actor:             domain.Actor{Kind: domain.ActorAgent},
		Type:              "architecture",
		Title:             "Use SQLite",
		Outcome:           "Use SQLite for local decisions.",
		Rationale:         "SQLite keeps local setup simple.",
		Confidence:        0.8,
		Status:            domain.DecisionActive,
		ValidFrom:         now,
		TransactionTime:   now,
		CompletenessScore: 0.5,
		Alternatives:      []domain.Alternative{{ID: "a1", DecisionID: "d1", Title: "Use Postgres", Reason: "Deferred for local setup."}},
		Claims:            []domain.Claim{{ID: "c1", DecisionID: "d1", Text: "SQLite is zero configuration."}},
		Evidence:          []domain.Evidence{{ID: "e1", DecisionID: "d1", Kind: "doc", URI: "README.md", Snippet: "Local-first."}},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := store.SaveDecision(ctx, first); err != nil {
		t.Fatalf("SaveDecision(first) error = %v", err)
	}
	second := domain.Decision{
		ID:                "d2",
		WorkspaceID:       workspace.ID,
		Actor:             domain.Actor{Kind: domain.ActorAgent},
		Type:              "architecture",
		Title:             "Use Postgres",
		Outcome:           "Use Postgres for shared decisions.",
		Rationale:         "Shared stores need a networked adapter.",
		Confidence:        0.85,
		Status:            domain.DecisionActive,
		ValidFrom:         now.Add(time.Minute),
		TransactionTime:   now.Add(time.Minute),
		SupersedesID:      first.ID,
		CompletenessScore: 0.5,
		Relationships: []domain.DecisionRelationship{{
			ID:             "r1",
			WorkspaceID:    workspace.ID,
			FromDecisionID: "d2",
			ToDecisionID:   "d1",
			Type:           domain.RelationshipSupersedes,
			Confidence:     1,
		}},
		CreatedAt: now.Add(time.Minute),
		UpdatedAt: now.Add(time.Minute),
	}
	if err := store.SaveDecision(ctx, second); err != nil {
		t.Fatalf("SaveDecision(second) error = %v", err)
	}
	retired, err := store.GetDecision(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetDecision(first) error = %v", err)
	}
	if retired.ValidTo == nil {
		t.Fatal("first decision was not retired")
	}
	var alternatives, claims, evidence, relationships int
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM decision_alternatives WHERE decision_id = 'd1'`).Scan(&alternatives); err != nil {
		t.Fatalf("count alternatives: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM decision_claims WHERE decision_id = 'd1'`).Scan(&claims); err != nil {
		t.Fatalf("count claims: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM decision_evidence WHERE decision_id = 'd1'`).Scan(&evidence); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if err := store.DB().QueryRowContext(ctx, `SELECT count(*) FROM decision_relationships WHERE from_decision_id = 'd2'`).Scan(&relationships); err != nil {
		t.Fatalf("count relationships: %v", err)
	}
	if alternatives != 1 || claims != 1 || evidence != 1 || relationships != 1 {
		t.Fatalf("normalized counts alternatives=%d claims=%d evidence=%d relationships=%d", alternatives, claims, evidence, relationships)
	}
}

func TestDecisionCandidateLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		ID:              "ws_candidates",
		RepoRoot:        t.TempDir(),
		DisplayName:     "candidates",
		RepoFingerprint: "fingerprint",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	candidate := domain.DecisionCandidate{
		ID:            "cand_1",
		WorkspaceID:   workspace.ID,
		Type:          "architecture",
		Title:         "Use SQLite locally",
		Outcome:       "Use SQLite for local memory.",
		Rationale:     "Source-backed import.",
		Confidence:    0.7,
		SourcePath:    "README.md",
		SourceSnippet: "We use SQLite for local memory.",
		SourceHash:    "hash_1",
		Detector:      "rule:v1",
		Status:        domain.CandidatePending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	inserted, err := store.UpsertDecisionCandidate(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertDecisionCandidate() error = %v", err)
	}
	if inserted.Status != domain.CandidatePending {
		t.Fatalf("status = %q, want pending", inserted.Status)
	}
	candidate.ID = "cand_duplicate"
	candidate.Title = "Use SQLite for local development"
	candidate.UpdatedAt = now.Add(time.Minute)
	updated, err := store.UpsertDecisionCandidate(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertDecisionCandidate(update) error = %v", err)
	}
	if updated.ID != inserted.ID {
		t.Fatalf("idempotent candidate id = %q, want %q", updated.ID, inserted.ID)
	}
	if updated.Title != "Use SQLite for local development" {
		t.Fatalf("updated title = %q", updated.Title)
	}
	rejected, err := store.UpdateDecisionCandidateStatus(ctx, ports.DecisionCandidateStatusUpdate{
		ID:               inserted.ID,
		WorkspaceID:      workspace.ID,
		Status:           domain.CandidateRejected,
		RejectionSummary: "Too generic.",
		UpdatedAt:        now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("UpdateDecisionCandidateStatus(reject) error = %v", err)
	}
	if rejected.Status != domain.CandidateRejected || rejected.RejectionSummary == "" {
		t.Fatalf("rejected candidate = %+v", rejected)
	}
	pending, err := store.ListDecisionCandidates(ctx, ports.DecisionCandidateQuery{WorkspaceID: workspace.ID})
	if err != nil {
		t.Fatalf("ListDecisionCandidates(pending) error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending candidates after reject = %d, want 0", len(pending))
	}
	candidate.Title = "Use SQLite after rejection refresh"
	candidate.UpdatedAt = now.Add(3 * time.Minute)
	refreshed, err := store.UpsertDecisionCandidate(ctx, candidate)
	if err != nil {
		t.Fatalf("UpsertDecisionCandidate(refresh rejected) error = %v", err)
	}
	if refreshed.Status != domain.CandidateRejected {
		t.Fatalf("refreshed status = %q, want rejected", refreshed.Status)
	}
	all, err := store.ListDecisionCandidates(ctx, ports.DecisionCandidateQuery{WorkspaceID: workspace.ID, AllStatuses: true})
	if err != nil {
		t.Fatalf("ListDecisionCandidates(all) error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("all candidates = %d, want 1", len(all))
	}
}
