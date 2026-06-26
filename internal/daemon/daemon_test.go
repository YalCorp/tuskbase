package daemon_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/priyavratuniyal/tuskbase/internal/daemon"
)

func TestHTTPMCPHandler(t *testing.T) {
	ctx := context.Background()
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp", Version: "v9.8.7"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, daemon.NoAuthPolicy{})
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer session.Close()
	if got := session.InitializeResult().ServerInfo.Version; got != "v9.8.7" {
		t.Fatalf("server version = %q, want %q", got, "v9.8.7")
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("ListTools() returned no tools")
	}
}

func TestHTTPMCPRequiresLocalAPIKey(t *testing.T) {
	ctx := context.Background()
	authPolicy, err := daemon.NewLocalAPIKeyPolicy("local-secret")
	if err != nil {
		t.Fatalf("NewLocalAPIKeyPolicy() error = %v", err)
	}
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, authPolicy)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	resp := httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated MCP status = %d, want 401", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "no bearer token") {
		t.Fatalf("unauthenticated MCP body = %q", resp.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	resp = httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("bad-token MCP status = %d, want 401", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), "invalid token") {
		t.Fatalf("bad-token MCP body = %q", resp.Body.String())
	}
}

func TestHTTPMCPAcceptsLocalAPIKey(t *testing.T) {
	ctx := context.Background()
	authPolicy, err := daemon.NewLocalAPIKeyPolicy("local-secret")
	if err != nil {
		t.Fatalf("NewLocalAPIKeyPolicy() error = %v", err)
	}
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp", Version: "v9.8.7"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, authPolicy)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: "local-secret"}}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer session.Close()
	if got := session.InitializeResult().ServerInfo.Version; got != "v9.8.7" {
		t.Fatalf("server version = %q, want %q", got, "v9.8.7")
	}
	if got := d.Health().AuthPolicy; got != "local-api-key" {
		t.Fatalf("auth policy = %q, want local-api-key", got)
	}
}

func TestHTTPMCPAcceptsLocalSharedAgentKey(t *testing.T) {
	ctx := context.Background()
	authPolicy, err := daemon.NewLocalSharedKeyPolicy([]daemon.LocalSharedKey{
		{Name: "codex", Role: "agent", Key: "codex-secret"},
		{Name: "claude", Role: "reader", Key: "claude-secret"},
	})
	if err != nil {
		t.Fatalf("NewLocalSharedKeyPolicy() error = %v", err)
	}
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp", Version: "v9.8.7"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, authPolicy)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	server := httptest.NewServer(d.Handler())
	defer server.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	httpClient := &http.Client{Transport: bearerTransport{token: "codex-secret"}}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer session.Close()
	if got := session.InitializeResult().ServerInfo.Version; got != "v9.8.7" {
		t.Fatalf("server version = %q, want %q", got, "v9.8.7")
	}
	if got := d.Health().AuthPolicy; got != "local-shared-keys" {
		t.Fatalf("auth policy = %q, want local-shared-keys", got)
	}
}

func TestRESTNotMountedByDefault(t *testing.T) {
	ctx := context.Background()
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, daemon.NoAuthPolicy{})
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	req := httptest.NewRequest(http.MethodPost, "/v1/lookup", nil)
	resp := httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("REST route status = %d, want 404", resp.Code)
	}
}

func TestControlAPIMountedByDefaultWithAuth(t *testing.T) {
	ctx := context.Background()
	authPolicy, err := daemon.NewLocalAPIKeyPolicy("local-secret")
	if err != nil {
		t.Fatalf("NewLocalAPIKeyPolicy() error = %v", err)
	}
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, authPolicy)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# Control Repo\n\nWe require reviewable imports."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	body := []byte(`{"repo_path":` + strconv.Quote(repo) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/control/v1/import/scan", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated control status = %d, want 401", resp.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/control/v1/import/scan", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer local-secret")
	req.Header.Set("Content-Type", "application/json")
	resp = httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("authenticated control status = %d body = %q", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"candidates"`) {
		t.Fatalf("control body = %q", resp.Body.String())
	}
}

func TestHealth(t *testing.T) {
	ctx := context.Background()
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, EnableREST: true, MCPPath: "/mcp"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, daemon.NoAuthPolicy{})
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("health status = %d", resp.Code)
	}
}

func TestHealthDoesNotRequireLocalAPIKey(t *testing.T) {
	ctx := context.Background()
	authPolicy, err := daemon.NewLocalAPIKeyPolicy("local-secret")
	if err != nil {
		t.Fatalf("NewLocalAPIKeyPolicy() error = %v", err)
	}
	d, err := daemon.New(ctx, daemon.Config{EnableMCP: true, MCPPath: "/mcp"}, daemon.SQLiteStoreFactory{Path: filepath.Join(t.TempDir(), "tuskbase.db")}, authPolicy)
	if err != nil {
		t.Fatalf("daemon.New() error = %v", err)
	}
	defer d.Close()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	resp := httptest.NewRecorder()
	d.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.Code)
	}
	if !strings.Contains(resp.Body.String(), `"auth_policy":"local-api-key"`) {
		t.Fatalf("health body = %q", resp.Body.String())
	}
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return base.RoundTrip(clone)
}
