package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

func TestPresenterPlainForNonTTYWriter(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "")
	t.Setenv("TUSKBASE_PLAIN", "")
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "")
	t.Setenv("CI", "")
	var out bytes.Buffer
	p := newPresenter(&out)
	if p.pretty {
		t.Fatal("newPresenter(bytes.Buffer).pretty = true, want false")
	}
	p.KV("write", "skipped")
	if got := out.String(); got != "write: skipped\n" {
		t.Fatalf("plain output = %q", got)
	}
}

func TestPresenterPrettyEnvironmentOverrides(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "pretty", env: map[string]string{"TUSKBASE_PRETTY": "1"}, want: true},
		{name: "plain", env: map[string]string{"TUSKBASE_PRETTY": "1", "TUSKBASE_PLAIN": "1"}, want: false},
		{name: "no color", env: map[string]string{"TUSKBASE_PRETTY": "", "NO_COLOR": "1"}, want: false},
		{name: "dumb term", env: map[string]string{"TUSKBASE_PRETTY": "", "TERM": "dumb"}, want: false},
		{name: "ci", env: map[string]string{"TUSKBASE_PRETTY": "", "CI": "true"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"TUSKBASE_PRETTY", "TUSKBASE_PLAIN", "NO_COLOR", "TERM", "CI"} {
				t.Setenv(key, "")
			}
			for key, value := range tt.env {
				t.Setenv(key, value)
			}
			var out bytes.Buffer
			if got := newPresenter(&out).pretty; got != tt.want {
				t.Fatalf("pretty = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestPresenterStatusColors(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "1")
	var out bytes.Buffer
	p := newPresenter(&out)
	for _, status := range []string{"ready", "down", "degraded", "skipped", "configured", "missing"} {
		got := p.Status(status)
		if !strings.Contains(got, status) || !strings.Contains(got, "\x1b[") {
			t.Fatalf("Status(%q) = %q, want colored label", status, got)
		}
	}
	if got := statusLabel("ok"); got != "ready" {
		t.Fatalf("statusLabel(ok) = %q, want ready", got)
	}
}

func TestPrettySetupPrintOnly(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "1")
	t.Setenv("TUSKBASE_CONFIG_PATH", filepath.Join(t.TempDir(), "config.json"))
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"setup", "--print-only", "--mode", "local-basic"}, &out, &errb); err != nil {
		t.Fatalf("execute(setup --print-only) error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"tuskbase", "Setup", "Store", "Service", "Next", "write: skipped (--print-only)", "service: skipped (--print-only)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pretty setup output missing %q: %s", want, got)
		}
	}
}

func TestPrettyStatusDoctorConnectAndAuthList(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "1")
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("TUSKBASE_CONFIG_PATH", path)
	t.Setenv("TUSKBASE_API_KEY", "")
	t.Setenv("TUSKBASE_AGENT_KEYS", "")
	cfg := userConfig{
		Mode:      modeLocalShared,
		Addr:      defaultAddr,
		AgentKeys: []daemon.LocalSharedKey{{Name: "codex", Role: "agent", Key: "secret"}},
		Store: storeConfig{Type: storePostgres, Postgres: &postgresStoreConfig{
			Source: postgresSourceExisting,
			Driver: defaultPostgresDriver,
			DSN:    "postgres://tuskbase:secret@localhost:5432/tuskbase?sslmode=disable",
		}},
	}
	if err := saveUserConfig(path, cfg); err != nil {
		t.Fatalf("saveUserConfig() error = %v", err)
	}
	oldVerify := verifyPostgresDSN
	verifyPostgresDSN = func(context.Context, string) error { return nil }
	t.Cleanup(func() { verifyPostgresDSN = oldVerify })

	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "status", args: []string{"status"}, want: []string{"Service", "daemon: ready", "auth_policy: local-api-key"}},
		{name: "doctor", args: []string{"doctor"}, want: []string{"Health", "Store", "Runtime", "postgres_connect: ok"}},
		{name: "connect", args: []string{"connect", "codex"}, want: []string{"Connect", "tuskbase bridge --client codex"}},
		{name: "auth list", args: []string{"auth", "list"}, want: []string{"Auth", "Keys", "codex", "<hidden>"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			if err := execute(context.Background(), tc.args, &out, &errb); err != nil {
				t.Fatalf("execute(%v) error = %v", tc.args, err)
			}
			got := stripANSI(out.String())
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("%s output missing %q: %s", tc.name, want, got)
				}
			}
		})
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(value string) string {
	return ansiPattern.ReplaceAllString(value, "")
}

func TestPrettyDoesNotAffectBridgeDiagnosticsText(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "1")
	got := bridgeDiagnosticText(bridgeDiagnosticOutput{Status: "not-ready", Mode: modeLocalBasic, Detail: "down"})
	if strings.Contains(got, "\x1b[") || strings.Contains(got, "local-first repo memory") {
		t.Fatalf("bridge diagnostics text should remain protocol-safe, got %q", got)
	}
}

func TestTTYFileWithoutCharDeviceIsPlain(t *testing.T) {
	t.Setenv("TUSKBASE_PRETTY", "")
	file, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()
	if newPresenter(file).pretty {
		t.Fatal("newPresenter(temp file).pretty = true, want false")
	}
}
