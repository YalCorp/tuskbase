package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

func NewServer(service *app.Service) *mcp.Server {
	return NewServerWithVersion(service, "dev")
}

func NewServerWithVersion(service *app.Service, version string) *mcp.Server {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "tuskbase", Version: version}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_attach",
		Description: "Attach or refresh a local repo workspace.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.AttachInput) (*mcp.CallToolResult, app.AttachOutput, error) {
		out, err := service.Attach(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_context",
		Description: "Start here for a compact workspace briefing: attached repo docs, recent active decisions, open conflicts, recent supersessions, degraded states, and recommended next actions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.ContextInput) (*mcp.CallToolResult, app.ContextOutput, error) {
		out, err := service.Context(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_remember",
		Description: "After work completes, store durable decisions only: include outcome, rationale, evidence, alternatives, claims, and relationships such as supersedes_id; skip chat logs, transient status, and unchosen plans.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.RememberInput) (*mcp.CallToolResult, app.RememberOutput, error) {
		out, err := service.Remember(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_lookup",
		Description: "Use for task-specific repo memory before editing or explaining a choice. Skip for purely mechanical work or when tuskbase_context shows no recorded memory. Cite receipt.id and relevant result ids when reporting context used.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.LookupInput) (*mcp.CallToolResult, app.LookupOutput, error) {
		out, err := service.Lookup(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_check",
		Description: "Run a non-mutating proposal check against active decisions. Use before preflight when you want relationship evidence without recording a lookup receipt or opening conflicts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.CheckInput) (*mcp.CallToolResult, app.CheckOutput, error) {
		out, err := service.Check(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_query",
		Description: "Query decisions with structured filters such as text, type, status, relationship target/type, confidence, and limit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.QueryInput) (*mcp.CallToolResult, app.QueryOutput, error) {
		out, err := service.Query(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_preflight",
		Description: "Use before meaningful implementation plans, especially architecture, security, data, setup, or behavior changes. Report whether the proposal follows, extends, duplicates, supersedes, or conflicts with active decisions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.PreflightInput) (*mcp.CallToolResult, app.PreflightOutput, error) {
		out, err := service.Preflight(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_assess",
		Description: "Append feedback to a decision trail, such as helpful, stale, incomplete, or incorrect. Assessments are additive and do not rewrite decisions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.AssessInput) (*mcp.CallToolResult, app.AssessOutput, error) {
		out, err := service.Assess(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_resolve_conflict",
		Description: "Close, dismiss, or defer an existing conflict with an append-only resolution note and optional decision reference.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.ResolveConflictInput) (*mcp.CallToolResult, app.ResolveConflictOutput, error) {
		out, err := service.ResolveConflict(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_reconcile",
		Description: "Record a reconciliation decision that relates to conflicting decisions and resolves the specified conflicts.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.ReconcileInput) (*mcp.CallToolResult, app.ReconcileOutput, error) {
		out, err := service.Reconcile(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_stats",
		Description: "Return aggregate trail health stats for a workspace: decision counts, conflict lifecycle counts, assessment counts, and completeness health.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.StatsInput) (*mcp.CallToolResult, app.StatsOutput, error) {
		out, err := service.Stats(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_recent",
		Description: "List recent active decisions for a workspace; superseded decisions are hidden by default.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recentInput) (*mcp.CallToolResult, recentOutput, error) {
		decisions, err := service.Recent(ctx, in.WorkspaceID, in.Limit)
		return nil, recentOutput{Decisions: decisions}, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_conflicts",
		Description: "List open conflicts for a workspace, scoped to conflicts against active decisions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in conflictsInput) (*mcp.CallToolResult, conflictsOutput, error) {
		conflicts, err := service.Conflicts(ctx, in.WorkspaceID)
		return nil, conflictsOutput{Conflicts: conflicts}, err
	})

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "tuskbase_context",
		Title:       "Tuskbase Context",
		Description: "Compact workspace briefing for an attached Tuskbase workspace.",
		MIMEType:    "application/json",
		URITemplate: "tuskbase:///context/{workspace_id}",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		if req == nil || req.Params == nil {
			return nil, errors.New("resource URI is required")
		}
		uri := strings.TrimSpace(req.Params.URI)
		workspaceID := strings.TrimPrefix(uri, "tuskbase:///context/")
		if workspaceID == uri || strings.TrimSpace(workspaceID) == "" {
			return nil, errors.New("expected tuskbase:///context/{workspace_id}")
		}
		out, err := service.Context(ctx, app.ContextInput{WorkspaceID: workspaceID})
		if err != nil {
			return nil, err
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: "application/json", Text: string(data)}}}, nil
	})

	return server
}

type recentInput struct {
	WorkspaceID string `json:"workspace_id"`
	Limit       int    `json:"limit,omitempty"`
}

type recentOutput struct {
	Decisions []domain.Decision `json:"decisions"`
}

type conflictsInput struct {
	WorkspaceID string `json:"workspace_id"`
}

type conflictsOutput struct {
	Conflicts []domain.Conflict `json:"conflicts"`
}
