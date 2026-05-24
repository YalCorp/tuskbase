package mcpadapter_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpadapter "github.com/priyavratuniyal/tuskbase/internal/adapters/mcp"
	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

func TestAttachTool(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer store.Close()
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	server := mcpadapter.NewServer(service)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer clientSession.Close()

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "tuskbase_attach",
		Arguments: map[string]any{"repo_path": newRepo(t)},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() result error: %v", result.GetError())
	}
	if result.StructuredContent == nil {
		t.Fatal("CallTool() returned no structured content")
	}
}

func TestNewServerWithVersionReportsVersion(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer store.Close()
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	server := mcpadapter.NewServerWithVersion(service, "v9.8.7")
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer clientSession.Close()

	got := clientSession.InitializeResult().ServerInfo.Version
	if got != "v9.8.7" {
		t.Fatalf("server version = %q, want %q", got, "v9.8.7")
	}
}

func TestFoundationTools(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "tuskbase.db"))
	if err != nil {
		t.Fatalf("sqlite.Open() error = %v", err)
	}
	defer store.Close()
	now := app.SystemClock{}.Now()
	workspace := domain.Workspace{
		ID:              "ws_mcp_tools",
		RepoRoot:        t.TempDir(),
		DisplayName:     "mcp tools",
		RepoFingerprint: "fingerprint",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatalf("UpsertWorkspace() error = %v", err)
	}
	service := app.NewService(store, store, app.UUIDGenerator{}, app.SystemClock{})
	server := mcpadapter.NewServer(service)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server.Connect() error = %v", err)
	}
	defer serverSession.Close()
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect() error = %v", err)
	}
	defer clientSession.Close()

	callToolOK(t, ctx, clientSession, "tuskbase_remember", map[string]any{
		"workspace_id": workspace.ID,
		"actor":        map[string]any{"kind": "agent", "name": "codex"},
		"type":         "architecture",
		"title":        "Use Redis for reset tokens",
		"outcome":      "Use Redis for password reset token storage.",
		"confidence":   0.9,
	})
	callToolOK(t, ctx, clientSession, "tuskbase_lookup", map[string]any{"workspace_id": workspace.ID, "query": "Redis reset tokens"})
	callToolOK(t, ctx, clientSession, "tuskbase_preflight", map[string]any{"workspace_id": workspace.ID, "proposal": "Avoid Redis for password reset tokens."})
	callToolOK(t, ctx, clientSession, "tuskbase_recent", map[string]any{"workspace_id": workspace.ID})
	callToolOK(t, ctx, clientSession, "tuskbase_conflicts", map[string]any{"workspace_id": workspace.ID})
}

func callToolOK(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) result error: %v", name, result.GetError())
	}
	if result.StructuredContent == nil {
		t.Fatalf("CallTool(%s) returned no structured content", name)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# MCP Repo\n\nLocal docs."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	return root
}
