package postgres

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

func TestVectorLiteral(t *testing.T) {
	got, err := vectorLiteral([]float32{0.1, -2, 3.25})
	if err != nil {
		t.Fatalf("vectorLiteral() error = %v", err)
	}
	if got != "[0.1,-2,3.25]" {
		t.Fatalf("vectorLiteral() = %q", got)
	}
}

func TestVectorLiteralRejectsNonFinite(t *testing.T) {
	if _, err := vectorLiteral([]float32{float32(math.Inf(1))}); err == nil {
		t.Fatal("vectorLiteral() error = nil, want non-finite rejection")
	}
}

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
	if err := store.UpsertVector(ctx, ports.VectorRecord{WorkspaceID: workspace.ID, Kind: "decision", EntityID: second.Decision.ID, Title: second.Decision.Title, Text: second.Decision.Rationale, Vector: []float32{0.9, 0.1, 0.1}}); err != nil {
		t.Fatalf("UpsertVector() error = %v", err)
	}
	results, err := store.SearchVector(ctx, ports.VectorQuery{WorkspaceID: workspace.ID, Vector: []float32{0.8, 0.1, 0.1}, Limit: 5})
	if err != nil {
		t.Fatalf("SearchVector() error = %v", err)
	}
	if len(results) == 0 || results[0].EntityID != second.Decision.ID || results[0].Score <= 0 {
		t.Fatalf("SearchVector() = %#v, want semantic match for second decision", results)
	}
	candidate := domain.DecisionCandidate{
		ID:            "cand_postgres_contract",
		WorkspaceID:   workspace.ID,
		Type:          "architecture",
		Title:         "Use source-backed imports",
		Outcome:       "Imported decisions remain candidates until accepted.",
		Rationale:     "Contract coverage for Local Shared.",
		Confidence:    0.7,
		SourcePath:    "README.md",
		SourceSnippet: "We require source-backed imports.",
		SourceHash:    "hash_postgres_contract",
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
		t.Fatalf("candidate status = %q, want pending", inserted.Status)
	}
	rejected, err := store.UpdateDecisionCandidateStatus(ctx, ports.DecisionCandidateStatusUpdate{ID: inserted.ID, WorkspaceID: workspace.ID, Status: domain.CandidateRejected, RejectionSummary: "contract", UpdatedAt: now})
	if err != nil {
		t.Fatalf("UpdateDecisionCandidateStatus() error = %v", err)
	}
	if rejected.Status != domain.CandidateRejected {
		t.Fatalf("rejected status = %q", rejected.Status)
	}
}
