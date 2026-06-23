package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

func TestSQLiteBackupRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	sourceDB := filepath.Join(root, "source.db")
	store, err := sqlite.Open(ctx, sourceDB)
	if err != nil {
		t.Fatalf("sqlite.Open(source) error = %v", err)
	}
	now := time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC)
	workspace := testWorkspace(t, now)
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	decision := testDecision(workspace.ID, now)
	if err := store.SaveDecision(ctx, decision); err != nil {
		t.Fatalf("SaveDecision() error = %v", err)
	}
	conflict := testConflict(workspace.ID, decision.ID, now)
	if err := store.SaveConflict(ctx, conflict); err != nil {
		t.Fatalf("SaveConflict() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(source) error = %v", err)
	}

	manager := testSQLiteManager(t, sourceDB, filepath.Join(root, "backups"), now)
	archivePath, manifest, err := manager.CreateManual(ctx)
	if err != nil {
		t.Fatalf("CreateManual() error = %v", err)
	}
	if manifest.Store.Type != StoreSQLite || manifest.Payload.Path != defaultSQLitePayload {
		t.Fatalf("manifest = %+v", manifest)
	}

	restoredDB := filepath.Join(root, "restored.db")
	restoreManager := testSQLiteManager(t, restoredDB, filepath.Join(root, "backups"), now.Add(time.Minute))
	restoredManifest, safetyPath, err := restoreManager.Restore(ctx, archivePath)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restoredManifest.Store.Type != StoreSQLite {
		t.Fatalf("restored manifest = %+v", restoredManifest)
	}
	if safetyPath != "" {
		t.Fatalf("safetyPath = %q, want empty for fresh target", safetyPath)
	}
	restored, err := sqlite.Open(ctx, restoredDB)
	if err != nil {
		t.Fatalf("sqlite.Open(restored) error = %v", err)
	}
	defer restored.Close()
	if got, err := restored.GetWorkspace(ctx, workspace.ID); err != nil || got.DisplayName != workspace.DisplayName {
		t.Fatalf("GetWorkspace() = %+v, %v", got, err)
	}
	if got, err := restored.GetDecision(ctx, decision.ID); err != nil || got.Title != decision.Title {
		t.Fatalf("GetDecision() = %+v, %v", got, err)
	}
	if got, err := restored.GetConflict(ctx, conflict.ID); err != nil || got.Summary != conflict.Summary {
		t.Fatalf("GetConflict() = %+v, %v", got, err)
	}
}

func TestAutoStoreTriggersDurableBackupsButNotLookupReceipts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "tuskbase.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC)
	workspace := testWorkspace(t, now)
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	manager := testSQLiteManager(t, dbPath, filepath.Join(root, "backups"), now)
	wrapped := WrapStore(store, manager, slog.Default())
	if err := wrapped.SaveDecision(ctx, testDecision(workspace.ID, now)); err != nil {
		t.Fatalf("SaveDecision() error = %v", err)
	}
	if err := wrapped.SaveLookupReceipt(ctx, domain.LookupReceipt{ID: "receipt_1", WorkspaceID: workspace.ID, Query: "backup", ResultCount: 0, CreatedAt: now}); err != nil {
		t.Fatalf("SaveLookupReceipt() error = %v", err)
	}
	entries, err := manager.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Manifest.Kind != KindAuto {
		t.Fatalf("entries after decision+receipt = %+v, want one auto backup", entries)
	}
	if err := wrapped.SaveConflict(ctx, testConflict(workspace.ID, "decision_1", now)); err != nil {
		t.Fatalf("SaveConflict() error = %v", err)
	}
	entries, err = manager.List(ctx)
	if err != nil {
		t.Fatalf("List() after conflict error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries after conflict = %d, want 2", len(entries))
	}
}

func TestAutoRetentionPrunesOnlyAutoBackups(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "tuskbase.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	store.Close()
	base := time.Date(2026, 6, 24, 1, 0, 0, 0, time.UTC)
	clock := base
	manager, err := NewManager(Config{Dir: filepath.Join(root, "backups"), StoreType: StoreSQLite, SQLitePath: dbPath, Retention: 2, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	if _, _, err := manager.CreateManual(ctx); err != nil {
		t.Fatalf("CreateManual() error = %v", err)
	}
	for i := 0; i < 4; i++ {
		clock = base.Add(time.Duration(i+1) * time.Second)
		if _, _, err := manager.CreateAuto(ctx); err != nil {
			t.Fatalf("CreateAuto(%d) error = %v", i, err)
		}
	}
	entries, err := manager.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	var manual, auto int
	for _, entry := range entries {
		switch entry.Manifest.Kind {
		case KindManual:
			manual++
		case KindAuto:
			auto++
		}
	}
	if manual != 1 || auto != 2 {
		t.Fatalf("manual=%d auto=%d entries=%+v, want manual=1 auto=2", manual, auto, entries)
	}
}

func TestAutoBackupFailureDoesNotFailDurableWrite(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "tuskbase.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer store.Close()
	now := time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC)
	workspace := testWorkspace(t, now)
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	manager, err := NewManager(Config{
		Dir:        filepath.Join(root, "backups"),
		StoreType:  StoreSQLite,
		SQLitePath: filepath.Join(root, "missing.db"),
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	wrapped := WrapStore(store, manager, slog.Default())
	decision := testDecision(workspace.ID, now)
	if err := wrapped.SaveDecision(ctx, decision); err != nil {
		t.Fatalf("SaveDecision() error = %v, backup failure must be non-fatal", err)
	}
	if got, err := store.GetDecision(ctx, decision.ID); err != nil || got.Title != decision.Title {
		t.Fatalf("GetDecision() = %+v, %v", got, err)
	}
	status, err := manager.Status()
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.LastAutoError == "" {
		t.Fatal("LastAutoError is empty after failed automatic backup")
	}
}

func TestDockerPostgresBackupAndRestoreCommands(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runner := &recordingRunner{outputs: map[string][]byte{}}
	cfg := Config{
		Dir:            filepath.Join(root, "backups"),
		StoreType:      StorePostgres,
		PostgresSource: SourceDocker,
		Docker: DockerPostgres{
			Project:     "tuskbase-test",
			ComposePath: "/tmp/tuskbase-compose.yml",
			Context:     "desktop-linux",
			Service:     "postgres",
			Database:    "tuskbase",
			User:        "tuskbase",
		},
		Now:    func() time.Time { return time.Date(2026, 6, 24, 1, 2, 3, 0, time.UTC) },
		Runner: runner,
	}
	manager, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	archivePath, _, err := manager.CreateManual(ctx)
	if err != nil {
		t.Fatalf("CreateManual() error = %v", err)
	}
	if !runner.called("docker --context desktop-linux compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test exec -T postgres pg_dump -Fc -U tuskbase -d tuskbase") {
		t.Fatalf("missing pg_dump command; calls=%v", runner.calls)
	}
	if _, _, err := manager.Restore(ctx, archivePath); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if !runner.calledContaining(" cp ") || !runner.called("docker --context desktop-linux compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test exec -T postgres pg_restore --clean --if-exists -U tuskbase -d tuskbase /tmp/tuskbase-restore.dump") {
		t.Fatalf("missing restore commands; calls=%v", runner.calls)
	}
}

func TestExistingPostgresGetsToolingGuidance(t *testing.T) {
	manager, err := NewManager(Config{Dir: t.TempDir(), StoreType: StorePostgres, PostgresSource: SourceExisting})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	_, _, err = manager.CreateManual(context.Background())
	if err == nil || !strings.Contains(err.Error(), "use your database backup tooling") {
		t.Fatalf("CreateManual() error = %v, want existing Postgres guidance", err)
	}
}

func testSQLiteManager(t *testing.T, dbPath, backupDir string, now time.Time) *Manager {
	t.Helper()
	manager, err := NewManager(Config{Dir: backupDir, StoreType: StoreSQLite, SQLitePath: dbPath, Now: func() time.Time { return now }, Retention: 20})
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	return manager
}

func testWorkspace(t *testing.T, now time.Time) domain.Workspace {
	t.Helper()
	return domain.Workspace{ID: "workspace_1", RepoRoot: t.TempDir(), DisplayName: "repo", RepoFingerprint: "fingerprint", CreatedAt: now, UpdatedAt: now}
}

func testDecision(workspaceID string, now time.Time) domain.Decision {
	return domain.Decision{
		ID:                "decision_1",
		WorkspaceID:       workspaceID,
		Actor:             domain.Actor{Kind: domain.ActorAgent, Name: "codex"},
		Type:              "architecture",
		Title:             "Back up local memory",
		Outcome:           "Store compressed local backups outside Docker volumes.",
		Rationale:         "Disaster recovery should survive volume loss.",
		Confidence:        0.9,
		Status:            domain.DecisionActive,
		ValidFrom:         now,
		TransactionTime:   now,
		CompletenessScore: 0.8,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func testConflict(workspaceID, decisionID string, now time.Time) domain.Conflict {
	return domain.Conflict{
		ID:                "conflict_1",
		WorkspaceID:       workspaceID,
		DecisionID:        decisionID,
		ConflictingWithID: "prior_decision",
		Summary:           "Backup behavior changed.",
		Severity:          domain.SeverityMedium,
		Confidence:        0.8,
		Status:            domain.ConflictOpen,
		CreatedAt:         now,
	}
}

type recordingRunner struct {
	calls   []string
	outputs map[string][]byte
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.calls = append(r.calls, call)
	if out := r.outputs[call]; out != nil {
		return out, nil
	}
	return []byte("PGDMP"), nil
}

func (r *recordingRunner) called(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

func (r *recordingRunner) calledContaining(want string) bool {
	for _, call := range r.calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
