package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/backup"
)

const defaultAutoBackupRetention = 20

func runBackupCommand(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stdout, "Usage: tuskbase backup <create|list|restore>")
		return nil
	}
	switch args[0] {
	case "create":
		return runBackupCreate(ctx, args[1:], stdout, stderr)
	case "list":
		return runBackupList(ctx, args[1:], stdout, stderr)
	case "restore":
		return runBackupRestore(ctx, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown backup command %q", args[0])
	}
}

func runBackupCreate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", configuredBackupDir(), "backup directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	manager, err := backupManagerFromRuntime(*dir)
	if err != nil {
		return err
	}
	path, manifest, err := manager.CreateManual(ctx)
	if err != nil {
		return err
	}
	p := newPresenter(stdout)
	p.KV("backup", path)
	p.KV("kind", manifest.Kind)
	p.KV("store", manifest.Store.Type)
	p.KV("created_at", manifest.CreatedAt.Format(time.RFC3339))
	return nil
}

func runBackupList(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", configuredBackupDir(), "backup directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	manager, err := backupManagerFromRuntime(*dir)
	if err != nil {
		return err
	}
	entries, err := manager.List(ctx)
	if err != nil {
		return err
	}
	p := newPresenter(stdout)
	if len(entries) == 0 {
		p.KV("backups", "none")
		return nil
	}
	for _, entry := range entries {
		p.Line("%s", fmt.Sprintf("%s kind=%s store=%s size=%d created_at=%s", entry.Path, entry.Manifest.Kind, entry.Manifest.Store.Type, entry.Size, entry.Manifest.CreatedAt.Format(time.RFC3339)))
	}
	return nil
}

func runBackupRestore(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "confirm destructive restore")
	stopDaemon := fs.Bool("stop-daemon", false, "stop the local daemon before restoring when it is reachable")
	dir := fs.String("dir", configuredBackupDir(), "backup directory")
	archivePath, flagArgs, err := splitRestoreArgs(args)
	if err != nil {
		return err
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if archivePath == "" || fs.NArg() != 0 {
		return errors.New("usage: tuskbase backup restore <file> --yes")
	}
	if !*yes {
		return errors.New("restore rewrites the current Tuskbase store; rerun with --yes to confirm")
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		return err
	}
	if !found {
		return errors.New("no Tuskbase setup found; run `tuskbase setup` first")
	}
	cfg = normalizedDaemonConfig(cfg)
	controller := newLifecycleController()
	status := controller.Status(ctx, cfg)
	if status.Health != nil {
		if !*stopDaemon {
			return errors.New("refusing to restore while the Tuskbase daemon is reachable; run `tuskbase daemon stop` first or pass --stop-daemon")
		}
		result := controller.Stop(ctx, cfg)
		printLifecycleResult(stdout, "service", result)
		if result.Err != nil {
			return fmt.Errorf("daemon stop: %w", result.Err)
		}
		if result.Degraded {
			return fmt.Errorf("daemon stop degraded: %s", emptyDefault(result.Detail, "unknown"))
		}
		status = controller.Status(ctx, cfg)
		if status.Health != nil {
			return errors.New("daemon is still reachable after stop; restore refused")
		}
	}
	manager, err := backupManagerFromRuntime(*dir)
	if err != nil {
		return err
	}
	manifest, safetyPath, err := manager.Restore(ctx, archivePath)
	if err != nil {
		return err
	}
	p := newPresenter(stdout)
	p.KV("restore", "ok")
	p.KV("store", manifest.Store.Type)
	if safetyPath != "" {
		p.KV("safety_copy", safetyPath)
	}
	p.KV("restart", "tuskbase daemon restart")
	return nil
}

func splitRestoreArgs(args []string) (string, []string, error) {
	var archivePath string
	var flags []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--dir" || arg == "-dir":
			if i+1 >= len(args) {
				return "", nil, errors.New("flag needs an argument: --dir")
			}
			flags = append(flags, arg, args[i+1])
			i++
		case strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-"):
			flags = append(flags, arg)
		case archivePath == "":
			archivePath = arg
		default:
			return "", nil, errors.New("usage: tuskbase backup restore <file> --yes")
		}
	}
	return archivePath, flags, nil
}

func backupManagerFromRuntime(dir string) (*backup.Manager, error) {
	store, err := loadRuntimeStoreConfig(configuredDBPath())
	if err != nil {
		return nil, err
	}
	cfg, _ := configuredBackupConfig(store)
	cfg.Dir = dir
	return backup.NewManager(cfg)
}

func configuredBackupConfig(store runtimeStoreConfig) (backup.Config, bool) {
	cfg := backup.Config{
		Dir:             configuredBackupDir(),
		StatusPath:      filepath.Join(filepath.Dir(defaultDBPath()), "backup-status.json"),
		StoreType:       store.Type,
		SQLitePath:      store.SQLitePath,
		Retention:       configuredAutoBackupRetention(),
		TuskbaseVersion: version,
	}
	if userCfg, found, err := loadUserConfig(); err == nil && found {
		cfg.Mode = userCfg.Mode
		if userCfg.Store.Postgres != nil {
			cfg.PostgresSource = userCfg.Store.Postgres.Source
			if userCfg.Store.Postgres.Docker != nil {
				docker := userCfg.Store.Postgres.Docker
				cfg.Docker = backup.DockerPostgres{
					Project:     docker.Project,
					ComposePath: docker.ComposePath,
					Context:     docker.Context,
					Service:     docker.Service,
					Database:    docker.Database,
					User:        docker.User,
				}
			}
		}
	}
	if cfg.StoreType == storePostgres && strings.TrimSpace(cfg.PostgresSource) == "" {
		cfg.PostgresSource = postgresSourceExisting
	}
	if cfg.StoreType == "" {
		cfg.StoreType = storeSQLite
	}
	autoEnabled := configuredAutoBackupEnabled()
	if cfg.StoreType == storePostgres && cfg.PostgresSource != postgresSourceDocker {
		autoEnabled = false
	}
	return cfg, autoEnabled
}

func configuredBackupDir() string {
	if value := strings.TrimSpace(os.Getenv("TUSKBASE_BACKUP_DIR")); value != "" {
		return value
	}
	return filepath.Join(filepath.Dir(defaultDBPath()), "backups")
}

func configuredAutoBackupEnabled() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("TUSKBASE_BACKUP_AUTO")))
	return value != "false" && value != "0" && value != "no"
}

func configuredAutoBackupRetention() int {
	value := strings.TrimSpace(os.Getenv("TUSKBASE_BACKUP_AUTO_RETENTION"))
	if value == "" {
		return defaultAutoBackupRetention
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return defaultAutoBackupRetention
	}
	return parsed
}
