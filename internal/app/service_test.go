package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

func TestAttachRememberLookup(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	repo := newRepo(t)

	attached, err := service.Attach(ctx, app.AttachInput{RepoPath: repo})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if len(attached.Workspace.DetectedDocs) == 0 {
		t.Fatal("Attach() did not detect repo docs")
	}

	remembered, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID: attached.Workspace.ID,
		Actor:       domain.Actor{Kind: domain.ActorAgent, Name: "codex"},
		Type:        "architecture",
		Title:       "Use SQLite first",
		Outcome:     "Use SQLite for local durable decision records.",
		Rationale:   "The first slice should be local-first and zero-config.",
		Confidence:  0.9,
		Claims:      []domain.Claim{{Text: "SQLite stores canonical local decisions."}},
	})
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if remembered.IndexingStatus != "indexed" {
		t.Fatalf("IndexingStatus = %q", remembered.IndexingStatus)
	}

	lookup, err := service.Lookup(ctx, app.LookupInput{WorkspaceID: attached.Workspace.ID, Query: "SQLite decision records"})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(lookup.Results) == 0 {
		t.Fatal("Lookup() returned no results")
	}
}

func TestDecisionLifecycleSupersedes(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	defer store.Close()
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	attached := attachRepo(t, ctx, service)

	old := rememberDecision(t, ctx, service, attached.Workspace.ID, "Use SQLite first", "Use SQLite for local durable decision records.")
	newer, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID:  attached.Workspace.ID,
		Actor:        domain.Actor{Kind: domain.ActorAgent},
		Type:         "architecture",
		Title:        "Use Postgres for shared decision records",
		Outcome:      "Use Postgres when multiple agents need the same durable decision store.",
		Rationale:    "The shared adapter keeps the canonical write path durable while SQLite remains the zero configuration local default for single developer usage.",
		Confidence:   0.85,
		SupersedesID: old.Decision.ID,
		Alternatives: []domain.Alternative{{Title: "Keep SQLite only", Reason: "SQLite stays local-first but does not satisfy shared adapter needs."}},
		Evidence:     []domain.Evidence{{Kind: "plan", URI: "memory://foundation", Snippet: "Postgres is a required foundation adapter."}},
	})
	if err != nil {
		t.Fatalf("Remember() supersede error = %v", err)
	}

	retired, err := store.GetDecision(ctx, old.Decision.ID)
	if err != nil {
		t.Fatalf("GetDecision(old) error = %v", err)
	}
	if retired.ValidTo == nil {
		t.Fatal("superseded decision still has nil valid_to")
	}
	if retired.Status != domain.DecisionSuperseded {
		t.Fatalf("superseded decision status = %q", retired.Status)
	}

	recent, err := service.Recent(ctx, attached.Workspace.ID, 10)
	if err != nil {
		t.Fatalf("Recent() error = %v", err)
	}
	if containsDecision(recent, old.Decision.ID) {
		t.Fatal("Recent() returned superseded decision")
	}
	if !containsDecision(recent, newer.Decision.ID) {
		t.Fatal("Recent() did not return active superseding decision")
	}

	lookup, err := service.Lookup(ctx, app.LookupInput{WorkspaceID: attached.Workspace.ID, Query: "SQLite durable decision records"})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	for _, result := range lookup.Results {
		if result.EntityID == old.Decision.ID {
			t.Fatal("Lookup() returned superseded decision context")
		}
	}
}

func TestPreflightIgnoresSupersededDecision(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	old := rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Redis for reset tokens", "Use Redis for password reset token storage.")
	if _, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID:  attached.Workspace.ID,
		Actor:        domain.Actor{Kind: domain.ActorAgent},
		Type:         "architecture",
		Title:        "Use Postgres for reset tokens",
		Outcome:      "Use Postgres for password reset token storage.",
		Rationale:    "Postgres keeps reset token state in the same transactional store as user records and avoids a second local service dependency.",
		Confidence:   0.85,
		SupersedesID: old.Decision.ID,
		Alternatives: []domain.Alternative{{Title: "Keep Redis", Reason: "Rejected to reduce local service footprint."}},
		Evidence:     []domain.Evidence{{Kind: "decision", Snippet: "The reset token storage decision has changed."}},
	}); err != nil {
		t.Fatalf("Remember() supersede error = %v", err)
	}

	out, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for password reset tokens."})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(out.Conflicts) != 0 {
		t.Fatalf("Preflight() conflicts against superseded decision = %d, want 0", len(out.Conflicts))
	}
}

func TestCompletenessScoringWarnings(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)

	sparse := rememberDecision(t, ctx, service, attached.Workspace.ID, "Approve PR", "Approve the PR.")
	if sparse.Decision.CompletenessScore >= 0.55 {
		t.Fatalf("sparse completeness score = %.2f, want low", sparse.Decision.CompletenessScore)
	}
	if len(sparse.QualityWarnings) == 0 {
		t.Fatal("sparse decision returned no quality warnings")
	}

	rich, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID:  attached.Workspace.ID,
		Actor:        domain.Actor{Kind: domain.ActorAgent},
		Type:         "architecture",
		Title:        "Use Postgres adapter behind ports",
		Outcome:      "Add a Postgres store adapter while keeping SQLite as the zero configuration default.",
		Rationale:    "The adapter boundary lets canonical writes stay durable before indexing while preserving the local-first developer experience and avoiding a product dependency on one database.",
		Confidence:   0.82,
		PrecedentRef: sparse.Decision.ID,
		Alternatives: []domain.Alternative{{Title: "Replace SQLite", Reason: "Rejected because local-first setup should remain zero configuration."}},
		Evidence:     []domain.Evidence{{Kind: "plan", URI: "memory://foundation", Snippet: "Add Postgres behind existing store ports while keeping SQLite as the default."}},
	})
	if err != nil {
		t.Fatalf("Remember() rich error = %v", err)
	}
	if rich.Decision.CompletenessScore <= sparse.Decision.CompletenessScore {
		t.Fatalf("rich score %.2f <= sparse score %.2f", rich.Decision.CompletenessScore, sparse.Decision.CompletenessScore)
	}
}

func containsDecision(decisions []domain.Decision, id string) bool {
	for _, decision := range decisions {
		if decision.ID == id {
			return true
		}
	}
	return false
}

func TestRememberSurvivesIndexFailure(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	defer store.Close()
	service := app.NewService(store, failingSearch{SearchIndex: store}, app.UUIDGenerator{}, app.SystemClock{})
	workspace := domain.Workspace{
		ID:              "ws_test",
		RepoRoot:        t.TempDir(),
		DisplayName:     "test",
		RepoFingerprint: "fingerprint",
	}
	workspace.CreatedAt = app.SystemClock{}.Now()
	workspace.UpdatedAt = workspace.CreatedAt
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}

	remembered, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID: workspace.ID,
		Actor:       domain.Actor{Kind: domain.ActorAgent},
		Type:        "architecture",
		Title:       "Keep canonical writes",
		Outcome:     "Decision writes succeed even when indexing fails.",
		Confidence:  0.8,
	})
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if remembered.IndexingStatus != "error" {
		t.Fatalf("IndexingStatus = %q", remembered.IndexingStatus)
	}
	if _, err := store.GetDecision(ctx, remembered.Decision.ID); err != nil {
		t.Fatalf("decision was not persisted: %v", err)
	}
}

func TestPreflightRedisContradiction(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Redis for reset tokens", "Use Redis for password reset token storage.")

	out, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for password reset tokens and store them elsewhere."})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(out.Conflicts) == 0 {
		t.Fatal("Preflight() did not detect Redis contradiction")
	}
}

func TestPreflightCompatiblePostgresTokens(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Postgres for refresh tokens", "Use Postgres for refresh token storage.")

	out, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Use Postgres for password reset tokens too."})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(out.Conflicts) != 0 {
		t.Fatalf("Preflight() conflicts = %d, want 0", len(out.Conflicts))
	}
}

type failingSearch struct {
	ports.SearchIndex
}

func (f failingSearch) IndexDecision(context.Context, domain.Decision) error {
	return errors.New("forced index failure")
}

func newTestService(t *testing.T) (*app.Service, func()) {
	t.Helper()
	store := newStore(t)
	return app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{}), func() { _ = store.Close() }
}

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	return store
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Test Repo\n\nUses local docs."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "architecture.md"), []byte("# Architecture\n\nDecision memory matters."), 0o644); err != nil {
		t.Fatalf("write docs: %v", err)
	}
	return root
}

func attachRepo(t *testing.T, ctx context.Context, service *app.Service) app.AttachOutput {
	t.Helper()
	attached, err := service.Attach(ctx, app.AttachInput{RepoPath: newRepo(t)})
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	return attached
}

func rememberDecision(t *testing.T, ctx context.Context, service *app.Service, workspaceID, title, outcome string) app.RememberOutput {
	t.Helper()
	remembered, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID: workspaceID,
		Actor:       domain.Actor{Kind: domain.ActorAgent},
		Type:        "architecture",
		Title:       title,
		Outcome:     outcome,
		Confidence:  0.9,
	})
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	return remembered
}
