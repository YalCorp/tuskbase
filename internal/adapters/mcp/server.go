package mcpadapter

import (
	"context"
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
		Name:        "tuskbase_remember",
		Description: "After work completes, record the final workspace decision with outcome, rationale, alternatives, evidence, precedent_ref, or supersedes_id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.RememberInput) (*mcp.CallToolResult, app.RememberOutput, error) {
		out, err := service.Remember(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_lookup",
		Description: "Before changing code, retrieve relevant active decisions, claims, evidence, and docs for this workspace; Tuskbase records a lookup receipt.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.LookupInput) (*mcp.CallToolResult, app.LookupOutput, error) {
		out, err := service.Lookup(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "tuskbase_preflight",
		Description: "Before committing to a plan, classify how a proposal follows, extends, duplicates, supersedes, or conflicts with active decisions.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in app.PreflightInput) (*mcp.CallToolResult, app.PreflightOutput, error) {
		out, err := service.Preflight(ctx, in)
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
