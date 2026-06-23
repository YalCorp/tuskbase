package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

func TestBackupCreateAndListSQLite(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	dbPath := filepath.Join(root, "tuskbase.db")
	backupDir := filepath.Join(root, "backups")
	t.Setenv("TUSKBASE_CONFIG_PATH", configPath)
	t.Setenv("TUSKBASE_BACKUP_DIR", backupDir)
	if err := saveUserConfig(configPath, userConfig{Mode: modeLocalBasic, Addr: defaultAddr, DBPath: dbPath, APIKey: "secret", Store: storeConfig{Type: storeSQLite}}); err != nil {
		t.Fatalf("saveUserConfig() error = %v", err)
	}
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	controller := &testLifecycleController{status: lifecycleStatus{Backend: "test", State: "stopped", Autostart: "enabled"}}
	withLifecycle(t, controller)
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"backup", "create"}, &out, &errb); err != nil {
		t.Fatalf("backup create error = %v stderr=%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "backup:") || !strings.Contains(out.String(), "store: sqlite") {
		t.Fatalf("backup create output = %q", out.String())
	}
	out.Reset()
	if err := execute(context.Background(), []string{"backup", "list"}, &out, &errb); err != nil {
		t.Fatalf("backup list error = %v", err)
	}
	if !strings.Contains(out.String(), "kind=manual") || !strings.Contains(out.String(), "store=sqlite") {
		t.Fatalf("backup list output = %q", out.String())
	}
}

func TestBackupRestoreRequiresYes(t *testing.T) {
	var out, errb bytes.Buffer
	err := execute(context.Background(), []string{"backup", "restore", filepath.Join(t.TempDir(), "missing.tar.gz")}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("backup restore error = %v, want --yes requirement", err)
	}
}

func TestBackupRestoreRefusesReachableDaemon(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	t.Setenv("TUSKBASE_CONFIG_PATH", configPath)
	if err := saveUserConfig(configPath, testLocalConfig()); err != nil {
		t.Fatalf("saveUserConfig() error = %v", err)
	}
	controller := &testLifecycleController{status: lifecycleStatus{Backend: "test", State: "running", Health: &daemon.Health{Status: "ok", MCP: true}}}
	withLifecycle(t, controller)
	var out, errb bytes.Buffer
	err := execute(context.Background(), []string{"backup", "restore", filepath.Join(root, "missing.tar.gz"), "--yes"}, &out, &errb)
	if err == nil || !strings.Contains(err.Error(), "daemon is reachable") {
		t.Fatalf("backup restore error = %v, want daemon refusal", err)
	}
}

func TestBackupRestoreStopDaemonFlagStopsBeforeRestore(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	sourceDB := filepath.Join(root, "source.db")
	targetDB := filepath.Join(root, "target.db")
	backupDir := filepath.Join(root, "backups")
	t.Setenv("TUSKBASE_CONFIG_PATH", configPath)
	t.Setenv("TUSKBASE_BACKUP_DIR", backupDir)
	store, err := sqlite.Open(context.Background(), sourceDB)
	if err != nil {
		t.Fatalf("sqlite.Open(source) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close(source) error = %v", err)
	}
	if err := saveUserConfig(configPath, userConfig{Mode: modeLocalBasic, Addr: defaultAddr, DBPath: sourceDB, APIKey: "secret", Store: storeConfig{Type: storeSQLite}}); err != nil {
		t.Fatalf("save source config: %v", err)
	}
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"backup", "create"}, &out, &errb); err != nil {
		t.Fatalf("backup create error = %v", err)
	}
	archive := strings.TrimSpace(strings.TrimPrefix(firstOutputLine(out.String()), "backup:"))
	if archive == "" {
		t.Fatalf("backup create output = %q", out.String())
	}
	if err := saveUserConfig(configPath, userConfig{Mode: modeLocalBasic, Addr: defaultAddr, DBPath: targetDB, APIKey: "secret", Store: storeConfig{Type: storeSQLite}}); err != nil {
		t.Fatalf("save target config: %v", err)
	}
	controller := &flippingStopLifecycle{result: lifecycleResult{Backend: "test", State: "stopped"}}
	withLifecycle(t, controller)
	out.Reset()
	if err := execute(context.Background(), []string{"backup", "restore", archive, "--yes", "--stop-daemon"}, &out, &errb); err != nil {
		t.Fatalf("backup restore error = %v output=%s", err, out.String())
	}
	if got := atomic.LoadInt32(&controller.stopCalls); got != 1 {
		t.Fatalf("stop calls = %d, want 1", got)
	}
	if !strings.Contains(out.String(), "restore: ok") || !strings.Contains(out.String(), "restart: tuskbase daemon restart") {
		t.Fatalf("restore output = %q", out.String())
	}
}

func firstOutputLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

type flippingStopLifecycle struct {
	stopCalls int32
	result    lifecycleResult
}

func (c *flippingStopLifecycle) EnsureReady(context.Context, userConfig) error { return nil }
func (c *flippingStopLifecycle) InstallAndStart(context.Context, userConfig) lifecycleResult {
	return c.result
}
func (c *flippingStopLifecycle) Uninstall(context.Context, userConfig) lifecycleResult {
	return c.result
}
func (c *flippingStopLifecycle) Restart(context.Context, userConfig) lifecycleResult { return c.result }
func (c *flippingStopLifecycle) Stop(context.Context, userConfig) lifecycleResult {
	atomic.AddInt32(&c.stopCalls, 1)
	return c.result
}
func (c *flippingStopLifecycle) Status(context.Context, userConfig) lifecycleStatus {
	if atomic.LoadInt32(&c.stopCalls) == 0 {
		return lifecycleStatus{Backend: "test", State: "running", Health: &daemon.Health{Status: "ok", MCP: true}}
	}
	return lifecycleStatus{Backend: "test", State: "stopped"}
}
