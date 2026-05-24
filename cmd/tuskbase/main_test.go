package main

import (
	"bytes"
	"context"
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

func TestInitMCPDemoCodex(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"init-mcp", "codex", "--mode", "demo"}, &out, &errb); err != nil {
		t.Fatalf("execute(init-mcp demo) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "[mcp_servers.tuskbase]") || !strings.Contains(got, "args = [\"serve\"]") {
		t.Fatalf("demo config output = %q", got)
	}
}

func TestInitMCPLocalBasic(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"init-mcp", "codex", "--mode", "local-basic"}, &out, &errb); err != nil {
		t.Fatalf("execute(init-mcp local-basic) error = %v", err)
	}
	if !strings.Contains(out.String(), "url = \"http://127.0.0.1:8765/mcp\"") {
		t.Fatalf("local-basic config output = %q", out.String())
	}
}

func TestInitExplainsModes(t *testing.T) {
	var out, errb bytes.Buffer
	if err := execute(context.Background(), []string{"init"}, &out, &errb); err != nil {
		t.Fatalf("execute(init) error = %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Demo") || !strings.Contains(got, "Local Basic") {
		t.Fatalf("init output = %q", got)
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
