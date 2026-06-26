package ports

import (
	"context"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

// Clock lets application code stay deterministic in tests and independent of wall-clock calls.
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
	QueryDecisions(context.Context, DecisionQuery) ([]domain.Decision, error)
}

type DecisionCandidateStore interface {
	UpsertDecisionCandidate(context.Context, domain.DecisionCandidate) (domain.DecisionCandidate, error)
	GetDecisionCandidate(context.Context, string) (domain.DecisionCandidate, error)
	ListDecisionCandidates(context.Context, DecisionCandidateQuery) ([]domain.DecisionCandidate, error)
	UpdateDecisionCandidateStatus(context.Context, DecisionCandidateStatusUpdate) (domain.DecisionCandidate, error)
}

type DocumentStore interface {
	ReplaceWorkspaceDocuments(context.Context, string, []domain.RepoDocument) error
	ListWorkspaceDocuments(context.Context, string, int) ([]domain.RepoDocument, error)
}

type ReceiptStore interface {
	SaveLookupReceipt(context.Context, domain.LookupReceipt) error
}

type AssessmentStore interface {
	SaveAssessment(context.Context, domain.Assessment) error
	ListAssessments(context.Context, AssessmentQuery) ([]domain.Assessment, error)
}

type ConflictStore interface {
	SaveConflict(context.Context, domain.Conflict) error
	GetConflict(context.Context, string) (domain.Conflict, error)
	ListOpenConflicts(context.Context, string) ([]domain.Conflict, error)
	ListConflicts(context.Context, ConflictQuery) ([]domain.Conflict, error)
	ResolveConflict(context.Context, string, domain.ConflictStatus, time.Time, domain.ConflictResolution) (domain.Conflict, error)
}

type DecisionQuery struct {
	WorkspaceID      string
	Text             string
	Type             string
	Status           domain.DecisionStatus
	RelationshipTo   string
	RelationshipType domain.RelationshipType
	MinConfidence    float64
	Limit            int
}

type DecisionCandidateQuery struct {
	WorkspaceID string
	Status      domain.DecisionCandidateStatus
	AllStatuses bool
	Limit       int
}

type DecisionCandidateStatusUpdate struct {
	ID                 string
	WorkspaceID        string
	Status             domain.DecisionCandidateStatus
	AcceptedDecisionID string
	RejectionSummary   string
	UpdatedAt          time.Time
}

type AssessmentQuery struct {
	WorkspaceID string
	DecisionID  string
	Limit       int
}

type ConflictQuery struct {
	WorkspaceID string
	Status      domain.ConflictStatus
	Limit       int
}

type RelationshipValidationRequest struct {
	WorkspaceID string
	Proposal    string
	Decision    domain.Decision
	Relation    domain.RelationshipType
	Evidence    []RelationshipSignal
}

type RelationshipValidationResult struct {
	Relation   domain.RelationshipType
	Confidence float64
	Reason     string
}

type RelationshipValidator interface {
	ValidateRelationship(context.Context, RelationshipValidationRequest) (RelationshipValidationResult, error)
}

type RelationshipSignal struct {
	Name   string  `json:"name"`
	Detail string  `json:"detail"`
	Weight float64 `json:"weight"`
}

// SearchIndex is the retrieval boundary used by application workflows; text, vector, or hybrid adapters can sit behind it.
type SearchIndex interface {
	IndexDecision(context.Context, domain.Decision) error
	IndexDocument(context.Context, domain.RepoDocument) error
	Search(context.Context, SearchQuery) ([]SearchResult, error)
}

// EmbeddingProvider turns text into vectors. Implementations are optional adapters, not a requirement for default local use.
type EmbeddingProvider interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorStore stores derived vectors. Canonical decisions must remain durable even if this adapter is absent or failing.
type VectorStore interface {
	UpsertVector(context.Context, VectorRecord) error
	SearchVector(context.Context, VectorQuery) ([]SearchResult, error)
}

// VectorRecord is intentionally generic so SQLite, pgvector, Qdrant, or future stores can share the same indexing contract.
type VectorRecord struct {
	WorkspaceID string
	Kind        string
	EntityID    string
	Title       string
	Path        string
	Text        string
	Vector      []float32
}

type VectorQuery struct {
	WorkspaceID string
	Vector      []float32
	Limit       int
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
