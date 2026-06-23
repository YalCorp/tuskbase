package mcpadapter_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcpadapter "github.com/priyavratuniyal/tuskbase/internal/adapters/mcp"
	"github.com/priyavratuniyal/tuskbase/internal/adapters/sqlite"
	"github.com/priyavratuniyal/tuskbase/internal/app"
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
	repo := newRepo(t)

	prepareInitial := callToolOK(t, ctx, clientSession, "tuskbase_prepare_change", map[string]any{
		"repo_path": repo,
		"task":      "Change reset token storage.",
	})
	var initial struct {
		Workspace struct {
			ID       string `json:"id"`
			RepoRoot string `json:"repo_root"`
		} `json:"workspace"`
		Verdict string `json:"verdict"`
	}
	decodeStructured(t, prepareInitial.StructuredContent, &initial)
	if initial.Workspace.ID == "" || initial.Workspace.RepoRoot != repo || initial.Verdict != "needs_plan" {
		t.Fatalf("initial prepare_change structured content = %+v", initial)
	}
	workspaceID := initial.Workspace.ID

	callToolOK(t, ctx, clientSession, "tuskbase_remember", map[string]any{
		"workspace_id": workspaceID,
		"actor":        map[string]any{"kind": "agent", "name": "codex"},
		"type":         "architecture",
		"title":        "Use Redis for reset tokens",
		"outcome":      "Use Redis for password reset token storage.",
		"confidence":   0.9,
	})
	prepareResult := callToolOK(t, ctx, clientSession, "tuskbase_prepare_change", map[string]any{
		"repo_path": repo,
		"task":      "Change reset token storage.",
		"plan":      "Avoid Redis for password reset tokens.",
	})
	var prepared struct {
		Verdict              string `json:"verdict"`
		ShouldEdit           bool   `json:"should_edit"`
		RequiresUserApproval bool   `json:"requires_user_approval"`
	}
	decodeStructured(t, prepareResult.StructuredContent, &prepared)
	if prepared.Verdict != "conflict" || prepared.ShouldEdit || !prepared.RequiresUserApproval {
		t.Fatalf("prepare_change structured content = %+v", prepared)
	}
	finishResult := callToolOK(t, ctx, clientSession, "tuskbase_finish_change", map[string]any{
		"workspace_id": workspaceID,
		"summary":      "MCP smoke test completed.",
		"changed_files": []any{
			map[string]any{"path": "README.md", "summary": "Documented workflow."},
		},
		"tests": []any{
			map[string]any{"command": "go test ./internal/adapters/mcp", "status": "passed"},
		},
	})
	var finished struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	decodeStructured(t, finishResult.StructuredContent, &finished)
	if finished.Status != "skipped" || finished.Reason != "no durable decision supplied" {
		t.Fatalf("finish_change structured content = %+v", finished)
	}
	callToolOK(t, ctx, clientSession, "tuskbase_lookup", map[string]any{"workspace_id": workspaceID, "query": "Redis reset tokens"})
	callToolOK(t, ctx, clientSession, "tuskbase_check", map[string]any{"workspace_id": workspaceID, "proposal": "Avoid Redis for password reset tokens."})
	callToolOK(t, ctx, clientSession, "tuskbase_preflight", map[string]any{"workspace_id": workspaceID, "proposal": "Avoid Redis for password reset tokens."})
	callToolOK(t, ctx, clientSession, "tuskbase_context", map[string]any{"workspace_id": workspaceID})
	callToolOK(t, ctx, clientSession, "tuskbase_query", map[string]any{"workspace_id": workspaceID, "text": "Redis", "status": "active"})
	recent, err := service.Recent(ctx, workspaceID, 1)
	if err != nil || len(recent) != 1 {
		t.Fatalf("Recent() = %d, %v", len(recent), err)
	}
	callToolOK(t, ctx, clientSession, "tuskbase_assess", map[string]any{"workspace_id": workspaceID, "decision_id": recent[0].ID, "actor": map[string]any{"kind": "agent", "name": "codex"}, "signal": "helpful", "score": 5})
	toResolve, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: workspaceID, Proposal: "Avoid Redis for reset token storage."})
	if err != nil || len(toResolve.Conflicts) == 0 {
		t.Fatalf("Preflight(resolve seed) conflicts = %d, err = %v", len(toResolve.Conflicts), err)
	}
	callToolOK(t, ctx, clientSession, "tuskbase_resolve_conflict", map[string]any{"workspace_id": workspaceID, "conflict_id": toResolve.Conflicts[0].ID, "actor": map[string]any{"kind": "agent", "name": "codex"}, "action": "dismissed", "summary": "MCP smoke test resolution."})
	toReconcile, err := service.Preflight(ctx, app.PreflightInput{WorkspaceID: workspaceID, Proposal: "Avoid Redis for password reset tokens and use Postgres instead."})
	if err != nil || len(toReconcile.Conflicts) == 0 {
		t.Fatalf("Preflight(reconcile seed) conflicts = %d, err = %v", len(toReconcile.Conflicts), err)
	}
	callToolOK(t, ctx, clientSession, "tuskbase_reconcile", map[string]any{"workspace_id": workspaceID, "conflict_ids": []any{toReconcile.Conflicts[0].ID}, "actor": map[string]any{"kind": "agent", "name": "codex"}, "title": "Use Postgres for reset tokens", "outcome": "Use Postgres for password reset token storage.", "rationale": "MCP smoke test reconciliation records a durable decision.", "confidence": 0.8})
	callToolOK(t, ctx, clientSession, "tuskbase_stats", map[string]any{"workspace_id": workspaceID})
	callToolOK(t, ctx, clientSession, "tuskbase_recent", map[string]any{"workspace_id": workspaceID})
	callToolOK(t, ctx, clientSession, "tuskbase_conflicts", map[string]any{"workspace_id": workspaceID})

	resources, err := clientSession.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("ListResourceTemplates() error = %v", err)
	}
	if !containsResourceTemplate(resources.ResourceTemplates, "tuskbase_context", "tuskbase:///context/{workspace_id}") {
		t.Fatalf("context resource template missing: %+v", resources.ResourceTemplates)
	}
	resource, err := clientSession.ReadResource(ctx, &mcp.ReadResourceParams{URI: "tuskbase:///context/" + workspaceID})
	if err != nil {
		t.Fatalf("ReadResource(context) error = %v", err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].MIMEType != "application/json" || !strings.Contains(resource.Contents[0].Text, workspaceID) {
		t.Fatalf("context resource content = %+v", resource.Contents)
	}

	tools, err := clientSession.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_prepare_change"); !strings.Contains(desc, "Preferred automatic pre-edit workflow") || !strings.Contains(desc, "should_edit=false") {
		t.Fatalf("prepare_change description = %q", desc)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_finish_change"); !strings.Contains(desc, "Preferred conservative post-work workflow") || !strings.Contains(desc, "not auto-remembered") {
		t.Fatalf("finish_change description = %q", desc)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_lookup"); !strings.Contains(desc, "receipt.id") || !strings.Contains(desc, "tuskbase_prepare_change") {
		t.Fatalf("lookup description = %q", desc)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_remember"); !strings.Contains(desc, "durable decisions only") || !strings.Contains(desc, "supersedes_id") {
		t.Fatalf("remember description = %q", desc)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_preflight"); !strings.Contains(desc, "follows, extends, duplicates, supersedes, or conflicts") || !strings.Contains(desc, "stopping before file edits") {
		t.Fatalf("preflight description = %q", desc)
	}
	if desc := toolDescription(tools.Tools, "tuskbase_stats"); !strings.Contains(desc, "trail health stats") {
		t.Fatalf("stats description = %q", desc)
	}
}

func callToolOK(t *testing.T, ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
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
	return result
}

func decodeStructured(t *testing.T, content any, out any) {
	t.Helper()
	data, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
}

func toolDescription(tools []*mcp.Tool, name string) string {
	for _, tool := range tools {
		if tool.Name == name {
			return tool.Description
		}
	}
	return ""
}

func containsResourceTemplate(templates []*mcp.ResourceTemplate, name, uriTemplate string) bool {
	for _, template := range templates {
		if template.Name == name && template.URITemplate == uriTemplate {
			return true
		}
	}
	return false
}

func newRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# MCP Repo\n\nLocal docs."), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	return root
}
