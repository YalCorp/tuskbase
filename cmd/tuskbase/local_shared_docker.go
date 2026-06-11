package main

import (
	"context"
	"database/sql"
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
	dockerContextAuto            = "auto"
	dockerDesktopContext         = "desktop-linux"
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
	if opts.DockerContextSet {
		contextName, err := normalizeDockerContext(opts.DockerContext)
		if err != nil {
			return dockerPostgresProvisionResult{}, err
		}
		config.Context = contextName
	}
	if config.Port <= 0 || config.Port > 65535 {
		return dockerPostgresProvisionResult{}, fmt.Errorf("docker postgres port %d is invalid", config.Port)
	}
	if strings.TrimSpace(config.ComposePath) == "" {
		config.ComposePath = filepath.Join(root, "docker-compose.yml")
	}
	password := postgresPasswordFromDSN(pg.DSN)
	if password == "" {
		password = dockerPostgresPasswordFromCompose(config.ComposePath)
	}
	if password == "" {
		secret, err := generateSecret()
		if err != nil {
			return dockerPostgresProvisionResult{}, err
		}
		password = secret
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

func configuredDockerContext() string {
	return strings.TrimSpace(os.Getenv("TUSKBASE_DOCKER_CONTEXT"))
}

func normalizeDockerContext(value string) (string, error) {
	contextName := strings.TrimSpace(value)
	if contextName == "" {
		return "", nil
	}
	if strings.EqualFold(contextName, dockerContextAuto) {
		return dockerContextAuto, nil
	}
	if strings.ContainsAny(contextName, " \t\n\r") {
		return "", fmt.Errorf("docker context %q is invalid; expected a context name or auto", value)
	}
	return contextName, nil
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
	if strings.TrimSpace(existing.Context) != "" {
		base.Context = existing.Context
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

func dockerPostgresPasswordFromCompose(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "POSTGRES_PASSWORD:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "POSTGRES_PASSWORD:"))
		if raw == "" {
			return ""
		}
		if unquoted, err := strconv.Unquote(raw); err == nil {
			return unquoted
		}
		return strings.Trim(raw, "'\"")
	}
	return ""
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
	verifyDSN    func(context.Context, string) error
}

func (p commandDockerPostgresProvisioner) Provision(ctx context.Context, req dockerPostgresProvisionRequest) (dockerPostgresProvisionResult, error) {
	if p.runner == nil {
		p.runner = execCommandRunner{}
	}
	if p.verifyDSN == nil {
		p.verifyDSN = verifyPostgresDSN
	}
	config := req.Config
	compose, err := detectDockerCompose(ctx, p.runner, config.Context)
	if err != nil {
		return dockerPostgresProvisionResult{}, err
	}
	config.Context = compose.context
	if _, err := compose.run(ctx, p.runner, config, "up", "-d"); err != nil {
		return dockerPostgresProvisionResult{}, diagnoseDockerProvisionError(ctx, p.runner, config, err)
	}
	if err := p.waitReady(ctx, compose, config); err != nil {
		return dockerPostgresProvisionResult{}, diagnoseDockerProvisionError(ctx, p.runner, config, err)
	}
	if _, err := compose.run(ctx, p.runner, config, "exec", "-T", config.Service, "psql", "-v", "ON_ERROR_STOP=1", "-U", config.User, "-d", config.Database, "-c", "CREATE EXTENSION IF NOT EXISTS vector;"); err != nil {
		return dockerPostgresProvisionResult{}, fmt.Errorf("enable pgvector extension: %w", diagnoseDockerProvisionError(ctx, p.runner, config, err))
	}
	dsn := postgresDSN(config.Host, config.Port, config.User, req.Password, config.Database)
	if err := p.waitDSNReady(ctx, dsn); err != nil {
		if !isPostgresAuthError(err) {
			return dockerPostgresProvisionResult{}, diagnoseDockerPostgresDSNError(config, err)
		}
		if repairErr := p.reconcilePassword(ctx, compose, config, req.Password); repairErr != nil {
			return dockerPostgresProvisionResult{}, fmt.Errorf("%w; password repair failed: %v", diagnoseDockerPostgresDSNError(config, err), repairErr)
		}
		if err := p.waitDSNReady(ctx, dsn); err != nil {
			return dockerPostgresProvisionResult{}, diagnoseDockerPostgresDSNError(config, err)
		}
		return dockerPostgresProvisionResult{Config: config, Ready: true, Detail: "ready (repaired stored password)"}, nil
	}
	return dockerPostgresProvisionResult{Config: config, Ready: true, Detail: "ready"}, nil
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

func (p commandDockerPostgresProvisioner) waitDSNReady(ctx context.Context, dsn string) error {
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
		err := p.verifyDSN(ctx, dsn)
		if err == nil {
			return nil
		}
		if isPostgresAuthError(err) {
			return err
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("docker postgres TCP connection did not become ready within %s: %w", timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

func (p commandDockerPostgresProvisioner) reconcilePassword(ctx context.Context, compose dockerComposeCommand, cfg dockerPostgresConfig, password string) error {
	statement := fmt.Sprintf("ALTER USER %s WITH PASSWORD %s;", postgresQuoteIdentifier(cfg.User), postgresQuoteLiteral(password))
	_, err := compose.run(ctx, p.runner, cfg, "exec", "-T", cfg.Service, "psql", "-v", "ON_ERROR_STOP=1", "-U", cfg.User, "-d", cfg.Database, "-c", statement)
	return err
}

var verifyPostgresDSN = verifyPostgresDSNDefault

func verifyPostgresDSNDefault(ctx context.Context, dsn string) error {
	db, err := sql.Open(defaultPostgresDriver, dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return db.PingContext(pingCtx)
}

func isPostgresAuthError(err error) bool {
	if err == nil {
		return false
	}
	detail := strings.ToLower(err.Error())
	return strings.Contains(detail, "password authentication failed") || strings.Contains(detail, "sqlstate 28p01") || strings.Contains(detail, "failed sasl auth")
}

func diagnoseDockerPostgresDSNError(cfg dockerPostgresConfig, err error) error {
	addr := net.JoinHostPort(emptyDefault(cfg.Host, defaultDockerPostgresHost), strconv.Itoa(cfg.Port))
	if isPostgresAuthError(err) {
		return fmt.Errorf("Docker-managed Postgres is running at %s, but it rejected the configured Tuskbase password; an existing Docker volume may have been reused with an older password: %w", addr, err)
	}
	return fmt.Errorf("Docker-managed Postgres did not accept the configured Tuskbase DSN at %s: %w", addr, err)
}

func postgresQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func postgresQuoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
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
	name    string
	prefix  []string
	context string
}

func detectDockerCompose(ctx context.Context, runner commandRunner, dockerContext string) (dockerComposeCommand, error) {
	if runner == nil {
		return dockerComposeCommand{}, errors.New("docker command runner is required")
	}
	contextName, err := normalizeDockerContext(dockerContext)
	if err != nil {
		return dockerComposeCommand{}, err
	}
	switch {
	case contextName == dockerContextAuto:
		return detectDockerComposeAuto(ctx, runner)
	case contextName != "":
		if err := requireDockerContextReachable(ctx, runner, contextName); err != nil {
			return dockerComposeCommand{}, err
		}
		return dockerComposeCommand{name: "docker", prefix: []string{"--context", contextName, "compose"}, context: contextName}, nil
	default:
		return detectDefaultDockerCompose(ctx, runner)
	}
}

func detectDefaultDockerCompose(ctx context.Context, runner commandRunner) (dockerComposeCommand, error) {
	if _, err := runner.Run(ctx, "docker", "compose", "version"); err == nil {
		return dockerComposeCommand{name: "docker", prefix: []string{"compose"}}, nil
	}
	if _, err := runner.Run(ctx, "docker-compose", "version"); err == nil {
		return dockerComposeCommand{name: "docker-compose"}, nil
	}
	return dockerComposeCommand{}, errors.New("Docker Compose is required for Local Shared Docker setup; install Docker Desktop or Docker Engine with the compose plugin, or pass --postgres-source existing --postgres-dsn <dsn>")
}

func detectDockerComposeAuto(ctx context.Context, runner commandRunner) (dockerComposeCommand, error) {
	compose, composeErr := detectDefaultDockerCompose(ctx, runner)
	var defaultErr error
	if composeErr == nil {
		if compose.name == "docker-compose" {
			return compose, nil
		}
		if _, defaultErr = runner.Run(ctx, "docker", "info"); defaultErr == nil {
			return compose, nil
		}
	} else {
		defaultErr = composeErr
	}
	if err := dockerContextReachable(ctx, runner, dockerDesktopContext); err == nil {
		return dockerComposeCommand{name: "docker", prefix: []string{"--context", dockerDesktopContext, "compose"}, context: dockerDesktopContext}, nil
	} else {
		return dockerComposeCommand{}, fmt.Errorf("Docker default context is not reachable (%v) and Docker Desktop context %q is unavailable (%v); start Docker Desktop or Docker Engine, fix Docker daemon access, or pass --postgres-source existing --postgres-dsn <dsn>", defaultErr, dockerDesktopContext, err)
	}
}

func requireDockerContextReachable(ctx context.Context, runner commandRunner, contextName string) error {
	if err := dockerContextReachable(ctx, runner, contextName); err != nil {
		if contextName == dockerDesktopContext {
			return fmt.Errorf("Docker context %q is not reachable; start Docker Desktop and rerun setup, or pass --postgres-source existing --postgres-dsn <dsn>: %w", contextName, err)
		}
		return fmt.Errorf("Docker context %q is not reachable; start Docker for that context and rerun setup, or pass --postgres-source existing --postgres-dsn <dsn>: %w", contextName, err)
	}
	return nil
}

func dockerContextReachable(ctx context.Context, runner commandRunner, contextName string) error {
	if _, err := runner.Run(ctx, "docker", "--context", contextName, "info"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, "docker", "--context", contextName, "compose", "version"); err != nil {
		return err
	}
	return nil
}

func diagnoseDockerProvisionError(ctx context.Context, runner commandRunner, cfg dockerPostgresConfig, err error) error {
	contextName := strings.TrimSpace(cfg.Context)
	if contextName == "" && isDockerDaemonAccessError(err) {
		if reachableErr := dockerContextReachable(ctx, runner, dockerDesktopContext); reachableErr == nil {
			return fmt.Errorf("Docker default context failed: %w. Docker Desktop context %q is reachable, but Tuskbase did not switch contexts automatically; rerun with `tuskbase setup --mode local-shared --docker-context desktop-linux --yes`, or pass --postgres-source existing --postgres-dsn <dsn>", err, dockerDesktopContext)
		}
		return fmt.Errorf("Docker default context failed: %w. Start Docker Desktop or Docker Engine, fix access to the Docker daemon socket, add your user to the docker group for system Docker, or pass --postgres-source existing --postgres-dsn <dsn>", err)
	}
	if contextName == dockerDesktopContext && isDockerDaemonAccessError(err) {
		return fmt.Errorf("Docker context %q failed: %w. Start Docker Desktop and rerun setup, or pass --postgres-source existing --postgres-dsn <dsn>", contextName, err)
	}
	return err
}

func isDockerDaemonAccessError(err error) bool {
	if err == nil {
		return false
	}
	detail := strings.ToLower(err.Error())
	for _, marker := range []string{
		"/var/run/docker.sock",
		"permission denied",
		"cannot connect to the docker daemon",
		"is the docker daemon running",
		"docker daemon is not running",
		"docker_engine",
		"error during connect",
		"access is denied",
	} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func (c dockerComposeCommand) run(ctx context.Context, runner commandRunner, cfg dockerPostgresConfig, args ...string) ([]byte, error) {
	composeArgs := append([]string{}, c.prefix...)
	composeArgs = append(composeArgs, "-f", cfg.ComposePath, "--project-name", cfg.Project)
	composeArgs = append(composeArgs, args...)
	return runner.Run(ctx, c.name, composeArgs...)
}
