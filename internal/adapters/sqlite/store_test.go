package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
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
