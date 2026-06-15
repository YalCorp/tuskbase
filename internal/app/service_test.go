package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRememberUsesAuthenticatedPrincipalActor(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)

	principal := app.Principal{
		Subject:    "codex",
		Role:       app.RoleAgent,
		Actor:      domain.Actor{Kind: domain.ActorAgent, Name: "codex"},
		AuthSource: app.AuthSourceLocalSharedKey,
	}
	remembered, err := service.Remember(app.ContextWithPrincipal(ctx, principal), app.RememberInput{
		WorkspaceID: attached.Workspace.ID,
		Type:        "architecture",
		Title:       "Use auth-derived actors",
		Outcome:     "Authenticated writes derive actors from the transport identity.",
		Confidence:  0.9,
	})
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if remembered.Decision.Actor.Name != "codex" {
		t.Fatalf("actor = %+v, want codex", remembered.Decision.Actor)
	}
}

func TestRememberRejectsMismatchedPrincipalActor(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)

	principal := app.Principal{
		Subject:    "codex",
		Role:       app.RoleAgent,
		Actor:      domain.Actor{Kind: domain.ActorAgent, Name: "codex"},
		AuthSource: app.AuthSourceLocalSharedKey,
	}
	_, err := service.Remember(app.ContextWithPrincipal(ctx, principal), app.RememberInput{
		WorkspaceID: attached.Workspace.ID,
		Actor:       domain.Actor{Kind: domain.ActorAgent, Name: "claude"},
		Type:        "architecture",
		Title:       "Reject mismatched actor",
		Outcome:     "The request actor cannot disagree with the authenticated identity.",
		Confidence:  0.9,
	})
	if !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("Remember() error = %v, want forbidden", err)
	}
}

func TestAuthenticatedPermissions(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	reader := app.ContextWithPrincipal(ctx, app.Principal{
		Subject:    "reader",
		Role:       app.RoleReader,
		Actor:      domain.Actor{Kind: domain.ActorAgent, Name: "reader"},
		AuthSource: app.AuthSourceLocalSharedKey,
	})

	if _, err := service.Recent(reader, attached.Workspace.ID, 1); err != nil {
		t.Fatalf("Recent(reader) error = %v", err)
	}
	_, err := service.Remember(reader, app.RememberInput{
		WorkspaceID: attached.Workspace.ID,
		Type:        "architecture",
		Title:       "Reader cannot write",
		Outcome:     "Readers cannot mutate decisions.",
		Confidence:  0.9,
	})
	if !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("Remember(reader) error = %v, want forbidden", err)
	}
}

func TestNoAuthRememberStillRequiresActor(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)

	_, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID: attached.Workspace.ID,
		Type:        "architecture",
		Title:       "Missing actor",
		Outcome:     "Unauthenticated stdio calls still provide actor explicitly.",
		Confidence:  0.9,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid actor kind") {
		t.Fatalf("Remember() error = %v, want invalid actor", err)
	}
}

func TestContextReturnsCompactWorkspaceBriefing(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)

	old := rememberDecision(t, ctx, service, attached.Workspace.ID, "Use SQLite for shared memory", "Use SQLite for shared agent memory.")
	newer, err := service.Remember(ctx, app.RememberInput{
		WorkspaceID:  attached.Workspace.ID,
		Actor:        domain.Actor{Kind: domain.ActorAgent},
		Type:         "architecture",
		Title:        "Use Postgres for shared memory",
		Outcome:      "Use Postgres for shared agent memory while SQLite remains the local basic default.",
		Rationale:    "Shared local memory needs a multi-client store, but the local basic path should stay zero configuration and keep the same application service boundaries.",
		Confidence:   0.86,
		SupersedesID: old.Decision.ID,
		Alternatives: []domain.Alternative{{Title: "Keep SQLite for shared mode", Reason: "Rejected because concurrent shared agent access needs a server database."}},
		Evidence:     []domain.Evidence{{Kind: "plan", URI: "docs/03_product_tiers.md", Snippet: "Local Shared uses Postgres with pgvector."}},
	})
	if err != nil {
		t.Fatalf("Remember() supersession error = %v", err)
	}
	redis := rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Redis for cache backend", "Use Redis for cache backend storage.")
	if _, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for cache backend storage."}); err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}

	out, err := service.Context(ctx, app.ContextInput{WorkspaceID: attached.Workspace.ID, Limit: 5})
	if err != nil {
		t.Fatalf("Context() error = %v", err)
	}
	if out.Workspace.ID != attached.Workspace.ID {
		t.Fatalf("workspace id = %q, want %q", out.Workspace.ID, attached.Workspace.ID)
	}
	if out.Workspace.DetectedDocCount < 2 {
		t.Fatalf("DetectedDocCount = %d, want at least 2", out.Workspace.DetectedDocCount)
	}
	if !containsContextDocument(out.Workspace.RepoDocuments, "README.md") {
		t.Fatalf("repo document summary missing README.md: %+v", out.Workspace.RepoDocuments)
	}
	if containsContextDecision(out.ActiveDecisions, old.Decision.ID) {
		t.Fatal("Context() returned superseded decision as active")
	}
	if !containsContextDecision(out.ActiveDecisions, newer.Decision.ID) || !containsContextDecision(out.ActiveDecisions, redis.Decision.ID) {
		t.Fatalf("active decisions missing expected ids: %+v", out.ActiveDecisions)
	}
	if len(out.OpenConflicts) == 0 {
		t.Fatal("Context() returned no open conflicts")
	}
	if len(out.RecentSupersessions) == 0 || out.RecentSupersessions[0].SupersededID != old.Decision.ID {
		t.Fatalf("RecentSupersessions = %+v, want superseded id %s", out.RecentSupersessions, old.Decision.ID)
	}
	if out.RecentSupersessions[0].SupersededTitle != old.Decision.Title {
		t.Fatalf("superseded title = %q, want %q", out.RecentSupersessions[0].SupersededTitle, old.Decision.Title)
	}
	if !containsDegradedState(out.DegradedStates, "open_conflicts") {
		t.Fatalf("degraded states missing open_conflicts: %+v", out.DegradedStates)
	}
	if !containsAction(out.RecommendedNextActions, "tuskbase_lookup") || !containsAction(out.RecommendedNextActions, "tuskbase_preflight") {
		t.Fatalf("recommended actions missing lookup/preflight guidance: %+v", out.RecommendedNextActions)
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

func containsContextDocument(docs []app.ContextDocument, path string) bool {
	for _, doc := range docs {
		if doc.Path == path {
			return true
		}
	}
	return false
}

func containsContextDecision(decisions []app.ContextDecision, id string) bool {
	for _, decision := range decisions {
		if decision.ID == id {
			return true
		}
	}
	return false
}

func containsDegradedState(states []app.ContextDegraded, code string) bool {
	for _, state := range states {
		if state.Code == code {
			return true
		}
	}
	return false
}

func containsAction(actions []string, needle string) bool {
	for _, action := range actions {
		if strings.Contains(action, needle) {
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

func TestPreflightRedisConflict(t *testing.T) {
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
		t.Fatal("Preflight() did not detect Redis conflict")
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

func TestAssessQueryStatsAndResolveConflict(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	defer store.Close()
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	attached := attachRepo(t, ctx, service)
	redis := rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Redis for cache backend", "Use Redis for cache backend storage.")

	if _, err := service.Assess(ctx, app.AssessInput{WorkspaceID: attached.Workspace.ID, DecisionID: redis.Decision.ID, Actor: domain.Actor{Kind: domain.ActorAgent}, Signal: "helpful", Score: 5, Summary: "This made the cache dependency explicit."}); err != nil {
		t.Fatalf("Assess(helpful) error = %v", err)
	}
	if _, err := service.Assess(ctx, app.AssessInput{WorkspaceID: attached.Workspace.ID, DecisionID: redis.Decision.ID, Actor: domain.Actor{Kind: domain.ActorAgent}, Signal: "stale", Score: 2, Summary: "May need revisiting after Postgres shared mode."}); err != nil {
		t.Fatalf("Assess(stale) error = %v", err)
	}
	assessments, err := store.ListAssessments(ctx, ports.AssessmentQuery{WorkspaceID: attached.Workspace.ID, DecisionID: redis.Decision.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListAssessments() error = %v", err)
	}
	if len(assessments) != 2 {
		t.Fatalf("assessment count = %d, want 2", len(assessments))
	}

	query, err := service.Query(ctx, app.QueryInput{WorkspaceID: attached.Workspace.ID, Text: "cache", Type: "architecture", Status: string(domain.DecisionActive)})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if !containsDecision(query.Decisions, redis.Decision.ID) {
		t.Fatalf("Query() missing Redis decision: %+v", query.Decisions)
	}

	preflight, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for cache backend storage."})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(preflight.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(preflight.Conflicts))
	}
	resolved, err := service.ResolveConflict(ctx, app.ResolveConflictInput{WorkspaceID: attached.Workspace.ID, ConflictID: preflight.Conflicts[0].ID, Actor: domain.Actor{Kind: domain.ActorAgent}, Action: "dismissed", Summary: "Cache work was out of scope for the current task."})
	if err != nil {
		t.Fatalf("ResolveConflict() error = %v", err)
	}
	if resolved.Conflict.Status != domain.ConflictDismissed || resolved.Conflict.ResolvedAt == nil {
		t.Fatalf("resolved conflict = %+v", resolved.Conflict)
	}

	stats, err := service.Stats(ctx, app.StatsInput{WorkspaceID: attached.Workspace.ID})
	if err != nil {
		t.Fatalf("Stats() error = %v", err)
	}
	if stats.AssessmentStats.Total != 2 || stats.ConflictStats.Dismissed != 1 || stats.DecisionStats.Active == 0 {
		t.Fatalf("Stats() = %+v", stats)
	}
}

func TestReconcileCreatesDecisionAndClosesConflict(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Redis for reset tokens", "Use Redis for password reset token storage.")

	preflight, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for password reset tokens and use Postgres instead."})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if len(preflight.Conflicts) == 0 {
		t.Fatal("Preflight() returned no conflict to reconcile")
	}
	reconciled, err := service.Reconcile(ctx, app.ReconcileInput{
		WorkspaceID: attached.Workspace.ID,
		Actor:       domain.Actor{Kind: domain.ActorAgent},
		ConflictIDs: []string{preflight.Conflicts[0].ID},
		Title:       "Use Postgres for reset tokens",
		Outcome:     "Use Postgres for password reset token storage and retire the Redis direction for this scope.",
		Rationale:   "Postgres keeps reset token state in the same transactional store as users while preserving one local service boundary for the basic setup.",
		Confidence:  0.86,
		Evidence:    []domain.Evidence{{Kind: "test", Snippet: "Reconciliation closes the conflict."}},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if reconciled.Decision.Type != "reconciliation" {
		t.Fatalf("reconciliation type = %q", reconciled.Decision.Type)
	}
	if len(reconciled.Conflicts) != 1 || reconciled.Conflicts[0].Status != domain.ConflictResolved {
		t.Fatalf("reconciled conflicts = %+v", reconciled.Conflicts)
	}
	open, err := service.Conflicts(ctx, attached.Workspace.ID)
	if err != nil {
		t.Fatalf("Conflicts() error = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open conflicts after reconcile = %+v", open)
	}
	query, err := service.Query(ctx, app.QueryInput{WorkspaceID: attached.Workspace.ID, RelationshipType: string(domain.RelationshipReconciles)})
	if err != nil {
		t.Fatalf("Query(reconciles) error = %v", err)
	}
	if !containsDecision(query.Decisions, reconciled.Decision.ID) {
		t.Fatalf("reconciliation query missing decision: %+v", query.Decisions)
	}
}

func TestCheckAvoidsDifferentMechanismFalsePositive(t *testing.T) {
	ctx := context.Background()
	service, closeStore := newTestService(t)
	defer closeStore()
	attached := attachRepo(t, ctx, service)
	rememberDecision(t, ctx, service, attached.Workspace.ID, "Use Postgres for reset tokens", "Use Postgres for password reset token storage.")

	out, err := service.Check(ctx, app.CheckInput{WorkspaceID: attached.Workspace.ID, Proposal: "Avoid Redis for password reset tokens."})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !out.CanProceed || len(out.Conflicts) != 0 {
		t.Fatalf("Check() false-positive conflict = %+v", out)
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
