package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"version"}, &out, &errb); err != nil {
		t.Fatalf("execute(version) error = %v", err)
	}
	if !strings.Contains(out.String(), "tuskbase ") || !strings.Contains(out.String(), "go:") {
		t.Fatalf("version output = %q", out.String())
	}
}

func TestUsagePrioritizesFriendlyCommands(t *testing.T) {
	var out bytes.Buffer
	printUsage(&out)
	got := out.String()
	for _, want := range []string{"setup", "start", "status", "connect [client]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q: %s", want, got)
		}
	}
	if strings.Index(got, "setup") > strings.Index(got, "serve") {
		t.Fatalf("usage should show setup before advanced serve: %s", got)
	}
}

func TestInitMCPDemoCodexCompatibility(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"init-mcp", "codex", "--mode", "demo"}, &out, &errb); err != nil {
		t.Fatalf("execute(init-mcp demo) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "codex mcp add tuskbase") || !strings.Contains(got, "tuskbase serve") {
		t.Fatalf("demo config output = %q", got)
	}
}

func TestConnectClaudeLocalBasic(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"connect", "claude", "--mode", "local-basic"}, &out, &errb); err != nil {
		t.Fatalf("execute(connect claude) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "claude mcp add") || !strings.Contains(got, "Authorization: Bearer <tuskbase-key>") {
		t.Fatalf("connect output = %q", got)
	}
}

func TestInitExplainsSetup(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"init"}, &out, &errb); err != nil {
		t.Fatalf("execute(init) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "tuskbase setup") || !strings.Contains(got, "Local Basic") {
		t.Fatalf("init output = %q", got)
	}
}

func TestSetupPrintOnlyDoesNotWriteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TUSKBASE_CONFIG_PATH", path)
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"setup", "--print-only", "--mode", "local-basic", "--client", "all"}, &out, &errb); err != nil {
		t.Fatalf("execute(setup --print-only) error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config file exists after print-only, stat err = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "write: skipped") || !strings.Contains(got, "Codex MCP config") || !strings.Contains(got, "Cursor MCP config") {
		t.Fatalf("setup print output = %q", got)
	}
}

func TestSetupWritesGeneratedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TUSKBASE_CONFIG_PATH", path)
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"setup", "--mode", "local-basic", "--yes"}, &out, &errb); err != nil {
		t.Fatalf("execute(setup) error = %v", err)
	}
	cfg, found, err := loadUserConfig()
	if err != nil {
		t.Fatalf("loadUserConfig() error = %v", err)
	}
	if !found {
		t.Fatal("loadUserConfig() found = false")
	}
	if cfg.Mode != modeLocalBasic || !strings.HasPrefix(cfg.APIKey, "tbk_") {
		t.Fatalf("config = %+v", cfg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config permissions = %o, want 600", got)
	}
	if !strings.Contains(out.String(), "secret: generated and stored locally") {
		t.Fatalf("setup output = %q", out.String())
	}
}

func TestEnvExampleDocumentsSupportedVariables(t *testing.T) {
	data, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatalf("read .env.example: %v", err)
	}
	got := string(data)
	for _, want := range []string{"TUSKBASE_API_KEY", "TUSKBASE_AGENT_KEYS", "TUSKBASE_ADDR", "TUSKBASE_DB_PATH", "TUSKBASE_CONFIG_PATH", "OPENAI_API_KEY"} {
		if !strings.Contains(got, want) {
			t.Fatalf(".env.example missing %q", want)
		}
	}
}

func TestLoadRuntimeConfigUsesLocalAPIKeyAuth(t *testing.T) {
	t.Setenv("TUSKBASE_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("TUSKBASE_AGENT_KEYS", "")
	t.Setenv("TUSKBASE_API_KEY", "local-secret")
	cfg, err := loadRuntimeConfig("127.0.0.1:8765", "tuskbase.db", true, false)
	if err != nil {
		t.Fatalf("loadRuntimeConfig() error = %v", err)
	}
	if got := cfg.Auth.Name(); got != "local-api-key" {
		t.Fatalf("auth policy = %q, want local-api-key", got)
	}
}

func TestLoadRuntimeConfigUsesStoredConfigAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TUSKBASE_CONFIG_PATH", path)
	t.Setenv("TUSKBASE_API_KEY", "")
	t.Setenv("TUSKBASE_AGENT_KEYS", "")
	if err := saveUserConfig(path, userConfig{Mode: modeLocalBasic, Addr: defaultAddr, APIKey: "stored-secret"}); err != nil {
		t.Fatalf("saveUserConfig() error = %v", err)
	}
	cfg, err := loadRuntimeConfig("127.0.0.1:8765", "tuskbase.db", true, false)
	if err != nil {
		t.Fatalf("loadRuntimeConfig() error = %v", err)
	}
	if got := cfg.Auth.Name(); got != "local-api-key" {
		t.Fatalf("auth policy = %q, want local-api-key", got)
	}
}

func TestLoadRuntimeConfigUsesLocalSharedKeys(t *testing.T) {
	t.Setenv("TUSKBASE_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("TUSKBASE_API_KEY", "")
	t.Setenv("TUSKBASE_AGENT_KEYS", "codex:agent:codex-secret,claude:reader:claude-secret")
	cfg, err := loadRuntimeConfig("127.0.0.1:8765", "tuskbase.db", true, false)
	if err != nil {
		t.Fatalf("loadRuntimeConfig() error = %v", err)
	}
	if got := cfg.Auth.Name(); got != "local-shared-keys" {
		t.Fatalf("auth policy = %q, want local-shared-keys", got)
	}
}

func TestLoadRuntimeConfigRequiresHTTPAuth(t *testing.T) {
	t.Setenv("TUSKBASE_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("TUSKBASE_API_KEY", "")
	t.Setenv("TUSKBASE_AGENT_KEYS", "")
	_, err := loadRuntimeConfig("127.0.0.1:8765", "tuskbase.db", true, false)
	if err == nil {
		t.Fatal("loadRuntimeConfig() error = nil, want missing auth error")
	}
	if !strings.Contains(err.Error(), "run `tuskbase setup`") {
		t.Fatalf("loadRuntimeConfig() error = %v", err)
	}
}

func TestLoadRuntimeConfigDefaultsToNoAuthForStdio(t *testing.T) {
	t.Setenv("TUSKBASE_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.json"))
	t.Setenv("TUSKBASE_API_KEY", "")
	t.Setenv("TUSKBASE_AGENT_KEYS", "")
	cfg, err := loadRuntimeConfig("127.0.0.1:8765", "tuskbase.db", false, false)
	if err != nil {
		t.Fatalf("loadRuntimeConfig() error = %v", err)
	}
	if got := cfg.Auth.Name(); got != "none" {
		t.Fatalf("auth policy = %q, want none", got)
	}
}

func TestLegacyBothEnablesHTTPMCPAndREST(t *testing.T) {
	args, err := legacyServeArgs("both")
	if err != nil {
		t.Fatalf("legacyServeArgs(both) error = %v", err)
	}
	got := strings.Join(args, " ")
	if got != "--http-mcp --rest" {
		t.Fatalf("legacy both args = %q", got)
	}
}
