package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDockerPostgresImage   = "pgvector/pgvector:pg16"
	defaultDockerPostgresPort    = 8766
	defaultDockerPostgresHost    = "127.0.0.1"
	defaultDockerPostgresDB      = "tuskbase"
	defaultDockerPostgresUser    = "tuskbase"
	defaultDockerPostgresProject = "tuskbase-local-shared"
	defaultDockerPostgresService = "postgres"
	defaultDockerPostgresVolume  = "tuskbase-local-shared-postgres"
)

type dockerPostgresProvisionRequest struct {
	Config   dockerPostgresConfig
	Password string
	RootDir  string
}

type dockerPostgresProvisionResult struct {
	Config  dockerPostgresConfig
	DSN     string
	Ready   bool
	Skipped bool
	Detail  string
}

type dockerPostgresProvisioner interface {
	Provision(context.Context, dockerPostgresProvisionRequest) (dockerPostgresProvisionResult, error)
}

var newDockerPostgresProvisioner = func() dockerPostgresProvisioner {
	return commandDockerPostgresProvisioner{runner: execCommandRunner{}, readyTimeout: 60 * time.Second, pollPeriod: time.Second}
}

func provisionDockerPostgresForSetup(pg postgresStoreConfig, opts setupStoreOptions) (dockerPostgresProvisionResult, error) {
	password := postgresPasswordFromDSN(pg.DSN)
	if password == "" {
		secret, err := generateSecret()
		if err != nil {
			return dockerPostgresProvisionResult{}, err
		}
		password = secret
	}
	root := localSharedDockerRoot(opts.ConfigPath)
	config := dockerPostgresConfig{
		Project:     defaultDockerPostgresProject,
		ComposePath: filepath.Join(root, "docker-compose.yml"),
		Service:     defaultDockerPostgresService,
		Image:       cleanDockerPostgresImage(opts.DockerPostgresImage),
		Host:        defaultDockerPostgresHost,
		Port:        opts.DockerPostgresPort,
		Database:    defaultDockerPostgresDB,
		User:        defaultDockerPostgresUser,
		Volume:      defaultDockerPostgresVolume,
	}
	if pg.Docker != nil {
		config = mergeDockerPostgresConfig(config, *pg.Docker)
		config.Image = cleanDockerPostgresImage(opts.DockerPostgresImage)
		config.Port = opts.DockerPostgresPort
	}
	if config.Port <= 0 || config.Port > 65535 {
		return dockerPostgresProvisionResult{}, fmt.Errorf("docker postgres port %d is invalid", config.Port)
	}
	if strings.TrimSpace(config.ComposePath) == "" {
		config.ComposePath = filepath.Join(root, "docker-compose.yml")
	}
	result := dockerPostgresProvisionResult{
		Config: config,
		DSN:    postgresDSN(config.Host, config.Port, config.User, password, config.Database),
	}
	if opts.PrintOnly {
		result.Skipped = true
		result.Detail = "skipped (--print-only)"
		return result, nil
	}
	if err := writeDockerPostgresFiles(root, config, password); err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	provisioned, err := newDockerPostgresProvisioner().Provision(context.Background(), dockerPostgresProvisionRequest{Config: config, Password: password, RootDir: root})
	if err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	provisioned.DSN = result.DSN
	return provisioned, nil
}

func localSharedDockerRoot(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		if root, err := os.UserConfigDir(); err == nil {
			return filepath.Join(root, "tuskbase", "local-shared")
		}
		return filepath.Join(".", ".tuskbase", "local-shared")
	}
	return filepath.Join(filepath.Dir(configPath), "local-shared")
}

func configuredDockerPostgresImage() string {
	return cleanDockerPostgresImage(os.Getenv("TUSKBASE_DOCKER_POSTGRES_IMAGE"))
}

func cleanDockerPostgresImage(value string) string {
	if image := strings.TrimSpace(value); image != "" {
		return image
	}
	return defaultDockerPostgresImage
}

func configuredDockerPostgresPort() int {
	raw := strings.TrimSpace(os.Getenv("TUSKBASE_DOCKER_POSTGRES_PORT"))
	if raw == "" {
		return defaultDockerPostgresPort
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return defaultDockerPostgresPort
	}
	return port
}

func mergeDockerPostgresConfig(base, existing dockerPostgresConfig) dockerPostgresConfig {
	if strings.TrimSpace(existing.Project) != "" {
		base.Project = existing.Project
	}
	if strings.TrimSpace(existing.ComposePath) != "" {
		base.ComposePath = existing.ComposePath
	}
	if strings.TrimSpace(existing.Service) != "" {
		base.Service = existing.Service
	}
	if strings.TrimSpace(existing.Host) != "" {
		base.Host = existing.Host
	}
	if strings.TrimSpace(existing.Database) != "" {
		base.Database = existing.Database
	}
	if strings.TrimSpace(existing.User) != "" {
		base.User = existing.User
	}
	if strings.TrimSpace(existing.Volume) != "" {
		base.Volume = existing.Volume
	}
	return base
}

func postgresPasswordFromDSN(dsn string) string {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || parsed.User == nil {
		return ""
	}
	password, ok := parsed.User.Password()
	if !ok {
		return ""
	}
	return password
}

func postgresDSN(host string, port int, user, password, database string) string {
	u := url.URL{
		Scheme: "postgres",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/" + strings.TrimPrefix(database, "/"),
		User:   url.UserPassword(user, password),
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}

func writeDockerPostgresFiles(root string, cfg dockerPostgresConfig, password string) error {
	initDir := filepath.Join(root, "initdb")
	if err := os.MkdirAll(initDir, 0o700); err != nil {
		return err
	}
	compose := dockerComposeYAML(cfg, password)
	if err := os.WriteFile(cfg.ComposePath, []byte(compose), 0o600); err != nil {
		return err
	}
	initSQL := "CREATE EXTENSION IF NOT EXISTS vector;\n"
	if err := os.WriteFile(filepath.Join(initDir, "001-vector.sql"), []byte(initSQL), 0o600); err != nil {
		return err
	}
	return nil
}

func dockerComposeYAML(cfg dockerPostgresConfig, password string) string {
	return fmt.Sprintf(`services:
  %s:
    image: %s
    restart: unless-stopped
    environment:
      POSTGRES_USER: %s
      POSTGRES_PASSWORD: %s
      POSTGRES_DB: %s
    ports:
      - %s
    volumes:
      - %s:/var/lib/postgresql/data
      - ./initdb:/docker-entrypoint-initdb.d:ro
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U %s -d %s"]
      interval: 5s
      timeout: 5s
      retries: 20
      start_period: 5s
volumes:
  %s:
    name: %s
`, cfg.Service, yamlQuote(cfg.Image), yamlQuote(cfg.User), yamlQuote(password), yamlQuote(cfg.Database), yamlQuote(cfg.Host+":"+strconv.Itoa(cfg.Port)+":5432"), cfg.Volume, cfg.User, cfg.Database, cfg.Volume, cfg.Volume)
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

type commandDockerPostgresProvisioner struct {
	runner       commandRunner
	readyTimeout time.Duration
	pollPeriod   time.Duration
}

func (p commandDockerPostgresProvisioner) Provision(ctx context.Context, req dockerPostgresProvisionRequest) (dockerPostgresProvisionResult, error) {
	if p.runner == nil {
		p.runner = execCommandRunner{}
	}
	compose, err := detectDockerCompose(ctx, p.runner)
	if err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	if _, err := compose.run(ctx, p.runner, req.Config, "up", "-d"); err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	if err := p.waitReady(ctx, compose, req.Config); err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	if _, err := compose.run(ctx, p.runner, req.Config, "exec", "-T", req.Config.Service, "psql", "-v", "ON_ERROR_STOP=1", "-U", req.Config.User, "-d", req.Config.Database, "-c", "CREATE EXTENSION IF NOT EXISTS vector;"); err != nil {
		return dockerPostgresProvisionResult{}, fmt.Errorf("enable pgvector extension: %w", err)
	}
	return dockerPostgresProvisionResult{Config: req.Config, Ready: true, Detail: "ready"}, nil
}

func (p commandDockerPostgresProvisioner) waitReady(ctx context.Context, compose dockerComposeCommand, cfg dockerPostgresConfig) error {
	timeout := p.readyTimeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	poll := p.pollPeriod
	if poll <= 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		_, err := compose.run(ctx, p.runner, cfg, "exec", "-T", cfg.Service, "pg_isready", "-U", cfg.User, "-d", cfg.Database)
		if err == nil {
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("docker postgres did not become ready within %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execCommandRunner struct{}

func (execCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		return output, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), detail)
	}
	return output, nil
}

type dockerComposeCommand struct {
	name   string
	prefix []string
}

func detectDockerCompose(ctx context.Context, runner commandRunner) (dockerComposeCommand, error) {
	if runner == nil {
		return dockerComposeCommand{}, errors.New("docker command runner is required")
	}
	if _, err := runner.Run(ctx, "docker", "compose", "version"); err == nil {
		return dockerComposeCommand{name: "docker", prefix: []string{"compose"}}, nil
	}
	if _, err := runner.Run(ctx, "docker-compose", "version"); err == nil {
		return dockerComposeCommand{name: "docker-compose"}, nil
	}
	return dockerComposeCommand{}, errors.New("Docker Compose is required for Local Shared Docker setup; install Docker Desktop or Docker Engine with the compose plugin, or pass --postgres-source existing --postgres-dsn <dsn>")
}

func (c dockerComposeCommand) run(ctx context.Context, runner commandRunner, cfg dockerPostgresConfig, args ...string) ([]byte, error) {
	composeArgs := append([]string{}, c.prefix...)
	composeArgs = append(composeArgs, "-f", cfg.ComposePath, "--project-name", cfg.Project)
	composeArgs = append(composeArgs, args...)
	return runner.Run(ctx, c.name, composeArgs...)
}
