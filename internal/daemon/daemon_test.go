package daemon_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
