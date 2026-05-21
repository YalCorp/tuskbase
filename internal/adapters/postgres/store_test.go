package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

func TestPostgresDecisionLifecycleContract(t *testing.T) {
	dsn := os.Getenv("TUSKBASE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TUSKBASE_POSTGRES_DSN to run Postgres adapter contract")
	}
	driver := os.Getenv("TUSKBASE_POSTGRES_DRIVER")
	if driver == "" {
		driver = "pgx"
	}
	ctx := context.Background()
	store, err := Open(ctx, driver, dsn)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	now := app.SystemClock{}.Now()
	workspace := domain.Workspace{
		ID:              fmt.Sprintf("ws_postgres_contract_%d", now.UnixNano()),
		RepoRoot:        t.TempDir(),
		DisplayName:     "postgres contract",
		RepoFingerprint: "fingerprint",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	first, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID: workspace.ID,
		Actor:       domain.Actor{Kind: domain.ActorAgent},
		Type:        "architecture",
		Title:       "Use SQLite locally",
		Outcome:     "Use SQLite for local decision memory.",
		Confidence:  0.8,
	})
	if err != nil {
		t.Fatalf("Remember(first) error = %v", err)
	}
	second, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID:  workspace.ID,
		Actor:        domain.Actor{Kind: domain.ActorAgent},
		Type:         "architecture",
		Title:        "Use Postgres when shared",
		Outcome:      "Use Postgres for shared decision memory.",
		Rationale:    "A shared adapter lets multiple local agents use the same canonical records while keeping SQLite available for zero configuration use.",
		Confidence:   0.85,
		SupersedesID: first.Decision.ID,
		Alternatives: []domain.Alternative{{Title: "Keep SQLite only", Reason: "Rejected for shared store contract testing."}},
		Evidence:     []domain.Evidence{{Kind: "test", Snippet: "Postgres adapter contract."}},
	})
	if err != nil {
		t.Fatalf("Remember(second) error = %v", err)
	}
	retired, err := store.GetDecision(ctx, first.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision(first) error = %v", err)
	}
	if retired.ValidTo == nil {
		t.Fatal("superseded decision still has nil valid_to")
	}
	recent, err := service.Recent(ctx, workspace.ID, 10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if len(recent) != 1 || recent[0].ID != second.Decision.ID {
		t.Fatalf("Recent() = %#v, want only superseding decision", recent)
	}
}
