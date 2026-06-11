package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerComposeYAMLRendersSinglePostgresVolumeTarget(t *testing.T) {
	cfg := dockerPostgresConfig{
		Service:  "postgres",
		Image:    "pgvector/pgvector:pg16",
		Host:     "127.0.0.1",
		Port:     8766,
		Database: "tuskbase",
		User:     "tuskbase",
		Volume:   "tuskbase-local-shared-postgres",
	}
	compose := dockerComposeYAML(cfg, "secret")
	want := "- tuskbase-local-shared-postgres:/var/lib/postgresql/data"
	if !strings.Contains(compose, want) {
		t.Fatalf("compose volume missing %q:\n%s", want, compose)
	}
	if strings.Contains(compose, ":/var/lib/postgresql/data:/var/lib/postgresql/data") {
		t.Fatalf("compose volume target rendered twice:\n%s", compose)
	}
}

func TestDockerComposeCommandUsesSelectedContext(t *testing.T) {
	runner := newScriptedCommandRunner()
	compose, err := detectDockerCompose(context.Background(), runner, dockerDesktopContext)
	if err != nil {
		t.Fatalf("detectDockerCompose() error = %v", err)
	}
	if _, err := compose.run(context.Background(), runner, testDockerPostgresConfig(), "up", "-d"); err != nil {
		t.Fatalf("compose.run() error = %v", err)
	}
	want := "docker --context desktop-linux compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test up -d"
	if !runner.called(want) {
		t.Fatalf("missing command %q; calls=%v", want, runner.calls)
	}
}

func TestDockerSetupReusesExistingComposePassword(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	composePath := filepath.Join(root, "local-shared", "docker-compose.yml")
	if err := os.MkdirAll(filepath.Dir(composePath), 0o700); err != nil {
		t.Fatalf("mkdir compose dir: %v", err)
	}
	if err := os.WriteFile(composePath, []byte("services:\n  postgres:\n    environment:\n      POSTGRES_PASSWORD: \"existing-secret\"\n"), 0o600); err != nil {
		t.Fatalf("write compose: %v", err)
	}
	cfg := userConfig{Mode: modeLocalShared}
	_, err := applySetupStoreConfig(&cfg, setupStoreOptions{PostgresSource: postgresSourceDocker, DockerPostgresPort: 8766, DockerPostgresImage: defaultDockerPostgresImage, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("applySetupStoreConfig() error = %v", err)
	}
	if got := postgresPasswordFromDSN(cfg.Store.Postgres.DSN); got != "existing-secret" {
		t.Fatalf("postgres password = %q, want existing compose password", got)
	}
}

func TestDockerProvisionerRepairsPasswordMismatch(t *testing.T) {
	runner := newScriptedCommandRunner()
	attempts := 0
	provisioner := commandDockerPostgresProvisioner{
		runner: runner,
		verifyDSN: func(context.Context, string) error {
			attempts++
			if attempts == 1 {
				return errors.New(`failed SASL auth: FATAL: password authentication failed for user "tuskbase" (SQLSTATE 28P01)`)
			}
			return nil
		},
	}
	result, err := provisioner.Provision(context.Background(), dockerPostgresProvisionRequest{Config: testDockerPostgresConfig(), Password: "new-secret"})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if !strings.Contains(result.Detail, "repaired") {
		t.Fatalf("Provision() detail = %q, want repaired password detail", result.Detail)
	}
	if !runner.calledContaining(`ALTER USER "tuskbase" WITH PASSWORD 'new-secret';`) {
		t.Fatalf("missing password repair command; calls=%v", runner.calls)
	}
}

func TestDockerDefaultSocketFailureSuggestsDesktopContext(t *testing.T) {
	runner := newScriptedCommandRunner()
	runner.fail("docker compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test up -d", errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock"))
	provisioner := testCommandDockerPostgresProvisioner(runner)
	_, err := provisioner.Provision(context.Background(), dockerPostgresProvisionRequest{Config: testDockerPostgresConfig(), Password: "secret"})
	if err == nil {
		t.Fatal("Provision() error = nil, want Docker context hint")
	}
	got := err.Error()
	for _, want := range []string{"--docker-context desktop-linux", "did not switch contexts automatically"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Provision() error missing %q: %v", want, err)
		}
	}
	if runner.called("docker --context desktop-linux compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test up -d") {
		t.Fatalf("default setup silently switched to desktop context; calls=%v", runner.calls)
	}
}

func TestDockerContextAutoSelectsDesktopLinuxWhenDefaultFails(t *testing.T) {
	runner := newScriptedCommandRunner()
	runner.fail("docker info", errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"))
	cfg := testDockerPostgresConfig()
	cfg.Context = dockerContextAuto
	provisioner := testCommandDockerPostgresProvisioner(runner)
	result, err := provisioner.Provision(context.Background(), dockerPostgresProvisionRequest{Config: cfg, Password: "secret"})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Config.Context != dockerDesktopContext {
		t.Fatalf("resolved context = %q, want %q", result.Config.Context, dockerDesktopContext)
	}
	want := "docker --context desktop-linux compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test up -d"
	if !runner.called(want) {
		t.Fatalf("missing command %q; calls=%v", want, runner.calls)
	}
}

func TestDockerContextAutoKeepsStandaloneDockerCompose(t *testing.T) {
	runner := newScriptedCommandRunner()
	runner.fail("docker compose version", errors.New(`exec: "docker": executable file not found in $PATH`))
	runner.fail("docker info", errors.New(`exec: "docker": executable file not found in $PATH`))
	runner.fail("docker --context desktop-linux info", errors.New(`exec: "docker": executable file not found in $PATH`))
	cfg := testDockerPostgresConfig()
	cfg.Context = dockerContextAuto
	provisioner := testCommandDockerPostgresProvisioner(runner)
	result, err := provisioner.Provision(context.Background(), dockerPostgresProvisionRequest{Config: cfg, Password: "secret"})
	if err != nil {
		t.Fatalf("Provision() error = %v", err)
	}
	if result.Config.Context != "" {
		t.Fatalf("resolved context = %q, want empty standalone docker-compose context", result.Config.Context)
	}
	if runner.called("docker info") {
		t.Fatalf("auto mode should not require docker CLI after finding standalone docker-compose; calls=%v", runner.calls)
	}
	want := "docker-compose -f /tmp/tuskbase-compose.yml --project-name tuskbase-test up -d"
	if !runner.called(want) {
		t.Fatalf("missing command %q; calls=%v", want, runner.calls)
	}
}

func TestDockerDesktopUnavailableSuggestsStartOrExistingPostgres(t *testing.T) {
	runner := newScriptedCommandRunner()
	runner.fail("docker --context desktop-linux info", errors.New("Cannot connect to the Docker daemon at unix:///Users/example/.docker/run/docker.sock"))
	cfg := testDockerPostgresConfig()
	cfg.Context = dockerDesktopContext
	provisioner := testCommandDockerPostgresProvisioner(runner)
	_, err := provisioner.Provision(context.Background(), dockerPostgresProvisionRequest{Config: cfg, Password: "secret"})
	if err == nil {
		t.Fatal("Provision() error = nil, want Docker Desktop guidance")
	}
	got := err.Error()
	for _, want := range []string{"start Docker Desktop", "--postgres-source existing --postgres-dsn <dsn>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Provision() error missing %q: %v", want, err)
		}
	}
}

func testCommandDockerPostgresProvisioner(runner commandRunner) commandDockerPostgresProvisioner {
	return commandDockerPostgresProvisioner{runner: runner, verifyDSN: func(context.Context, string) error { return nil }}
}

func testDockerPostgresConfig() dockerPostgresConfig {
	return dockerPostgresConfig{
		Project:     "tuskbase-test",
		ComposePath: "/tmp/tuskbase-compose.yml",
		Service:     "postgres",
		Image:       "pgvector/pgvector:pg16",
		Host:        "127.0.0.1",
		Port:        8766,
		Database:    "tuskbase",
		User:        "tuskbase",
		Volume:      "tuskbase-test-postgres",
	}
}

type scriptedCommandRunner struct {
	calls []string
	errs  map[string]error
}

func newScriptedCommandRunner() *scriptedCommandRunner {
	return &scriptedCommandRunner{errs: map[string]error{}}
}

func (r *scriptedCommandRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	call := commandLine(name, args...)
	r.calls = append(r.calls, call)
	if err := r.errs[call]; err != nil {
		return nil, err
	}
	return []byte("ok"), nil
}

func (r *scriptedCommandRunner) fail(call string, err error) {
	r.errs[call] = err
}

func (r *scriptedCommandRunner) called(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

func (r *scriptedCommandRunner) calledContaining(want string) bool {
	for _, call := range r.calls {
		if strings.Contains(call, want) {
			return true
		}
	}
	return false
}

func commandLine(name string, args ...string) string {
	parts := append([]string{name}, args...)
	return strings.Join(parts, " ")
}
