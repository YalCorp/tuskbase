package ports

import (
	"context"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() string
}

type WorkspaceStore interface {
	UpsertWorkspace(context.Context, domain.Workspace) (domain.Workspace, error)
	GetWorkspace(context.Context, string) (domain.Workspace, error)
}

type DecisionStore interface {
	SaveDecision(context.Context, domain.Decision) error
	GetDecision(context.Context, string) (domain.Decision, error)
	RecentDecisions(context.Context, string, int) ([]domain.Decision, error)
}

type DocumentStore interface {
	ReplaceWorkspaceDocuments(context.Context, string, []domain.RepoDocument) error
}

type ReceiptStore interface {
	SaveLookupReceipt(context.Context, domain.LookupReceipt) error
}

type ConflictStore interface {
	SaveConflict(context.Context, domain.Conflict) error
	ListOpenConflicts(context.Context, string) ([]domain.Conflict, error)
}

type SearchIndex interface {
	IndexDecision(context.Context, domain.Decision) error
	IndexDocument(context.Context, domain.RepoDocument) error
	Search(context.Context, SearchQuery) ([]SearchResult, error)
}

type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

type SearchQuery struct {
	WorkspaceID string
	Query       string
	Limit       int
}

type SearchResult struct {
	Kind        string  `json:"kind"`
	EntityID    string  `json:"entity_id"`
	WorkspaceID string  `json:"workspace_id"`
	Title       string  `json:"title,omitempty"`
	Path        string  `json:"path,omitempty"`
	Snippet     string  `json:"snippet,omitempty"`
	Score       float64 `json:"score"`
}
