package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	ManifestVersion = 1
	StoreSQLite     = "sqlite"
	StorePostgres   = "postgres"
	SourceDocker    = "docker"
	SourceExisting  = "existing"
	KindManual      = "manual"
	KindAuto        = "auto"

	defaultSQLitePayload   = "tuskbase.sqlite"
	defaultPostgresPayload = "tuskbase.dump"
	statusFileName         = "backup-status.json"
)

type Config struct {
	Dir             string
	StatusPath      string
	Mode            string
	StoreType       string
	SQLitePath      string
	PostgresSource  string
	Docker          DockerPostgres
	Retention       int
	TuskbaseVersion string
	Now             func() time.Time
	Runner          CommandRunner
}

type DockerPostgres struct {
	Project     string
	ComposePath string
	Context     string
	Service     string
	Database    string
	User        string
}

type Manifest struct {
	Version         int             `json:"version"`
	CreatedAt       time.Time       `json:"created_at"`
	Kind            string          `json:"kind"`
	TuskbaseVersion string          `json:"tuskbase_version,omitempty"`
	Mode            string          `json:"mode,omitempty"`
	Store           ManifestStore   `json:"store"`
	Payload         ManifestPayload `json:"payload"`
}

type ManifestStore struct {
	Type           string `json:"type"`
	PostgresSource string `json:"postgres_source,omitempty"`
}

type ManifestPayload struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

type Entry struct {
	Path     string
	Size     int64
	Modified time.Time
	Manifest Manifest
}

type Status struct {
	LastAutoAt    time.Time `json:"last_auto_at,omitempty"`
	LastAutoError string    `json:"last_auto_error,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return output, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), detail)
	}
	return output, nil
}

type Manager struct {
	cfg Config
}

func NewManager(cfg Config) (*Manager, error) {
	cfg.Dir = strings.TrimSpace(cfg.Dir)
	if cfg.Dir == "" {
		return nil, errors.New("backup dir is required")
	}
	cfg.StoreType = strings.ToLower(strings.TrimSpace(cfg.StoreType))
	if cfg.StoreType == "" {
		cfg.StoreType = StoreSQLite
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	if cfg.Retention < 0 {
		cfg.Retention = 0
	}
	return &Manager{cfg: cfg}, nil
}

func (m *Manager) CreateManual(ctx context.Context) (string, Manifest, error) {
	return m.create(ctx, KindManual)
}

func (m *Manager) CreateAuto(ctx context.Context) (string, Manifest, error) {
	path, manifest, err := m.create(ctx, KindAuto)
	if err != nil {
		_ = m.writeStatus(Status{LastAutoError: err.Error(), UpdatedAt: m.now()})
		return "", Manifest{}, err
	}
	if m.cfg.Retention > 0 {
		if err := m.PruneAuto(ctx, m.cfg.Retention); err != nil {
			_ = m.writeStatus(Status{LastAutoAt: manifest.CreatedAt, LastAutoError: err.Error(), UpdatedAt: m.now()})
			return path, manifest, err
		}
	}
	_ = m.writeStatus(Status{LastAutoAt: manifest.CreatedAt, UpdatedAt: m.now()})
	return path, manifest, nil
}

func (m *Manager) List(ctx context.Context) ([]Entry, error) {
	_ = ctx
	entries, err := os.ReadDir(m.cfg.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		path := filepath.Join(m.cfg.Dir, entry.Name())
		manifest, err := ReadManifest(path)
		if err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Entry{Path: path, Size: info.Size(), Modified: info.ModTime(), Manifest: manifest})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Manifest.CreatedAt.After(out[j].Manifest.CreatedAt)
	})
	return out, nil
}

func (m *Manager) PruneAuto(ctx context.Context, keep int) error {
	if keep <= 0 {
		return nil
	}
	entries, err := m.List(ctx)
	if err != nil {
		return err
	}
	var auto []Entry
	for _, entry := range entries {
		if entry.Manifest.Kind == KindAuto {
			auto = append(auto, entry)
		}
	}
	for i := keep; i < len(auto); i++ {
		if err := os.Remove(auto[i].Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (m *Manager) Restore(ctx context.Context, archivePath string) (Manifest, string, error) {
	manifest, payloadPath, cleanup, err := extractArchive(archivePath)
	if err != nil {
		return Manifest{}, "", err
	}
	defer cleanup()
	if err := m.Validate(manifest); err != nil {
		return Manifest{}, "", err
	}
	switch manifest.Store.Type {
	case StoreSQLite:
		safetyPath, err := m.restoreSQLite(payloadPath)
		if err != nil {
			return Manifest{}, "", err
		}
		return manifest, safetyPath, nil
	case StorePostgres:
		if err := m.restoreDockerPostgres(ctx, payloadPath); err != nil {
			return Manifest{}, "", err
		}
		return manifest, "", nil
	default:
		return Manifest{}, "", fmt.Errorf("unsupported backup store %q", manifest.Store.Type)
	}
}

func (m *Manager) Validate(manifest Manifest) error {
	if manifest.Version != ManifestVersion {
		return fmt.Errorf("backup manifest version %d is not supported by this Tuskbase", manifest.Version)
	}
	if manifest.Store.Type != m.cfg.StoreType {
		return fmt.Errorf("backup store %q is not compatible with current store %q", manifest.Store.Type, m.cfg.StoreType)
	}
	if manifest.Store.Type == StorePostgres {
		if manifest.Store.PostgresSource != SourceDocker || strings.TrimSpace(m.cfg.PostgresSource) != SourceDocker {
			return errors.New("restore is only supported for Docker-managed Local Shared Postgres in this version; use your database backup tooling for existing Postgres")
		}
	}
	return nil
}

func (m *Manager) Status() (Status, error) {
	data, err := os.ReadFile(m.statusPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Status{}, nil
		}
		return Status{}, err
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (m *Manager) create(ctx context.Context, kind string) (string, Manifest, error) {
	if err := os.MkdirAll(m.cfg.Dir, 0o700); err != nil {
		return "", Manifest{}, err
	}
	createdAt := m.now()
	manifest := Manifest{
		Version:         ManifestVersion,
		CreatedAt:       createdAt,
		Kind:            kind,
		TuskbaseVersion: strings.TrimSpace(m.cfg.TuskbaseVersion),
		Mode:            strings.TrimSpace(m.cfg.Mode),
		Store:           ManifestStore{Type: m.cfg.StoreType},
	}
	prefix := fmt.Sprintf("tuskbase-%s-%s", kind, createdAt.UTC().Format("20060102T150405Z"))
	switch m.cfg.StoreType {
	case StoreSQLite:
		payload, cleanup, err := m.snapshotSQLite(ctx)
		if err != nil {
			return "", Manifest{}, err
		}
		defer cleanup()
		manifest.Payload = ManifestPayload{Path: defaultSQLitePayload, Format: "sqlite"}
		path := nextArchivePath(m.cfg.Dir, prefix+"-sqlite")
		if err := writeArchive(path, manifest, payload, defaultSQLitePayload); err != nil {
			return "", Manifest{}, err
		}
		return path, manifest, nil
	case StorePostgres:
		if strings.TrimSpace(m.cfg.PostgresSource) != SourceDocker {
			return "", Manifest{}, errors.New("automatic Postgres backup is only supported for Docker-managed Local Shared; use your database backup tooling for existing Postgres")
		}
		dump, err := m.dumpDockerPostgres(ctx)
		if err != nil {
			return "", Manifest{}, err
		}
		manifest.Store.PostgresSource = SourceDocker
		manifest.Payload = ManifestPayload{Path: defaultPostgresPayload, Format: "pg_dump custom"}
		path := nextArchivePath(m.cfg.Dir, prefix+"-postgres")
		if err := writeArchiveBytes(path, manifest, dump, defaultPostgresPayload); err != nil {
			return "", Manifest{}, err
		}
		return path, manifest, nil
	default:
		return "", Manifest{}, fmt.Errorf("unsupported store %q", m.cfg.StoreType)
	}
}

func nextArchivePath(dir, base string) string {
	path := filepath.Join(dir, base+".tar.gz")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	for i := 2; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s-%d.tar.gz", base, i))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func (m *Manager) snapshotSQLite(ctx context.Context) (string, func(), error) {
	source := strings.TrimSpace(m.cfg.SQLitePath)
	if source == "" {
		return "", nil, errors.New("sqlite path is required")
	}
	if _, err := os.Stat(source); err != nil {
		return "", nil, err
	}
	tmpDir, err := os.MkdirTemp("", "tuskbase-backup-sqlite-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	out := filepath.Join(tmpDir, defaultSQLitePayload)
	db, err := sql.Open("sqlite", source)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+strings.ReplaceAll(out, "'", "''")+"'"); err != nil {
		cleanup()
		return "", nil, err
	}
	return out, cleanup, nil
}

func (m *Manager) restoreSQLite(payloadPath string) (string, error) {
	target := strings.TrimSpace(m.cfg.SQLitePath)
	if target == "" {
		return "", errors.New("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return "", err
	}
	var safetyPath string
	if _, err := os.Stat(target); err == nil {
		safetyPath = target + ".pre-restore-" + m.now().UTC().Format("20060102T150405Z")
		if err := copyFile(target, safetyPath, 0o600); err != nil {
			return "", fmt.Errorf("write safety copy: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := copyFile(payloadPath, target, 0o600); err != nil {
		return "", err
	}
	return safetyPath, nil
}

func (m *Manager) dumpDockerPostgres(ctx context.Context) ([]byte, error) {
	cfg := m.cfg.Docker
	if strings.TrimSpace(cfg.ComposePath) == "" || strings.TrimSpace(cfg.Service) == "" {
		return nil, errors.New("docker compose path and service are required for Postgres backup")
	}
	args := composeArgs(cfg, "exec", "-T", cfg.Service, "pg_dump", "-Fc", "-U", cfg.User, "-d", cfg.Database)
	name, rest := composeCommand(cfg, args)
	return m.cfg.Runner.Run(ctx, name, rest...)
}

func (m *Manager) restoreDockerPostgres(ctx context.Context, payloadPath string) error {
	cfg := m.cfg.Docker
	if strings.TrimSpace(cfg.ComposePath) == "" || strings.TrimSpace(cfg.Service) == "" {
		return errors.New("docker compose path and service are required for Postgres restore")
	}
	containerPath := "/tmp/tuskbase-restore.dump"
	if err := m.runCompose(ctx, cfg, "cp", payloadPath, cfg.Service+":"+containerPath); err != nil {
		return err
	}
	if err := m.runCompose(ctx, cfg, "exec", "-T", cfg.Service, "pg_restore", "--clean", "--if-exists", "-U", cfg.User, "-d", cfg.Database, containerPath); err != nil {
		return err
	}
	cleanupName, cleanupArgs := composeCommand(cfg, composeArgs(cfg, "exec", "-T", cfg.Service, "rm", "-f", containerPath))
	_, _ = m.cfg.Runner.Run(ctx, cleanupName, cleanupArgs...)
	return nil
}

func (m *Manager) runCompose(ctx context.Context, cfg DockerPostgres, args ...string) error {
	name, rest := composeCommand(cfg, composeArgs(cfg, args...))
	_, err := m.cfg.Runner.Run(ctx, name, rest...)
	return err
}

func (m *Manager) now() time.Time {
	return m.cfg.Now().UTC()
}

func (m *Manager) writeStatus(status Status) error {
	path := m.statusPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func (m *Manager) statusPath() string {
	if path := strings.TrimSpace(m.cfg.StatusPath); path != "" {
		return path
	}
	return filepath.Join(m.cfg.Dir, statusFileName)
}

func composeCommand(cfg DockerPostgres, args []string) (string, []string) {
	if strings.TrimSpace(cfg.Context) != "" {
		return "docker", append([]string{"--context", cfg.Context, "compose"}, args...)
	}
	return "docker", append([]string{"compose"}, args...)
}

func composeArgs(cfg DockerPostgres, args ...string) []string {
	out := []string{}
	if strings.TrimSpace(cfg.ComposePath) != "" {
		out = append(out, "-f", cfg.ComposePath)
	}
	if strings.TrimSpace(cfg.Project) != "" {
		out = append(out, "--project-name", cfg.Project)
	}
	return append(out, args...)
}

func writeArchive(path string, manifest Manifest, payloadPath, payloadName string) error {
	file, err := os.Open(payloadPath)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeArchiveReader(path, manifest, file, payloadName)
}

func writeArchiveBytes(path string, manifest Manifest, payload []byte, payloadName string) error {
	return writeArchiveReader(path, manifest, bytes.NewReader(payload), payloadName)
}

func writeArchiveReader(path string, manifest Manifest, payload io.Reader, payloadName string) error {
	tmp := path + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := writeTarBytes(tw, "manifest.json", append(data, '\n'), manifest.CreatedAt); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := writeTarReader(tw, payloadName, payload, manifest.CreatedAt); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	err = errors.Join(tw.Close(), gz.Close(), out.Close())
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func writeTarBytes(tw *tar.Writer, name string, data []byte, modTime time.Time) error {
	return writeTarReaderWithSize(tw, name, int64(len(data)), strings.NewReader(string(data)), modTime)
}

func writeTarReader(tw *tar.Writer, name string, r io.Reader, modTime time.Time) error {
	tmp, err := os.CreateTemp("", "tuskbase-archive-payload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	size, err := io.Copy(tmp, r)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	file, err := os.Open(tmpName)
	if err != nil {
		return err
	}
	defer file.Close()
	return writeTarReaderWithSize(tw, name, size, file, modTime)
}

func writeTarReaderWithSize(tw *tar.Writer, name string, size int64, r io.Reader, modTime time.Time) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: modTime}); err != nil {
		return err
	}
	_, err := io.Copy(tw, r)
	return err
}

func ReadManifest(path string) (Manifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return Manifest{}, errors.New("backup manifest missing")
		}
		if err != nil {
			return Manifest{}, err
		}
		if filepath.Clean(header.Name) != "manifest.json" {
			continue
		}
		var manifest Manifest
		if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
			return Manifest{}, err
		}
		if manifest.Version == 0 {
			return Manifest{}, errors.New("backup manifest missing")
		}
		return manifest, nil
	}
}

func extractArchive(path string) (Manifest, string, func(), error) {
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, "", nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, "", nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	tmpDir, err := os.MkdirTemp("", "tuskbase-restore-*")
	if err != nil {
		return Manifest{}, "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmpDir) }
	var manifest Manifest
	var payloadPath string
	var payloadName string
	payloadCount := 0
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			cleanup()
			return Manifest{}, "", nil, err
		}
		name := filepath.Clean(header.Name)
		if strings.Contains(name, "..") || filepath.IsAbs(name) {
			cleanup()
			return Manifest{}, "", nil, fmt.Errorf("unsafe archive path %q", header.Name)
		}
		switch name {
		case "manifest.json":
			if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
				cleanup()
				return Manifest{}, "", nil, err
			}
		default:
			payloadCount++
			if payloadCount > 1 {
				cleanup()
				return Manifest{}, "", nil, errors.New("backup archive contains more than one payload")
			}
			outPath := filepath.Join(tmpDir, filepath.Base(name))
			out, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				cleanup()
				return Manifest{}, "", nil, err
			}
			_, copyErr := io.Copy(out, tr)
			closeErr := out.Close()
			if copyErr != nil || closeErr != nil {
				cleanup()
				return Manifest{}, "", nil, errors.Join(copyErr, closeErr)
			}
			payloadPath = outPath
			payloadName = name
		}
	}
	if manifest.Version == 0 {
		cleanup()
		return Manifest{}, "", nil, errors.New("backup manifest missing")
	}
	if payloadPath == "" {
		cleanup()
		return Manifest{}, "", nil, errors.New("backup payload missing")
	}
	if filepath.Clean(manifest.Payload.Path) != payloadName {
		cleanup()
		return Manifest{}, "", nil, fmt.Errorf("backup payload %q does not match manifest path %q", payloadName, manifest.Payload.Path)
	}
	return manifest, payloadPath, cleanup, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}
