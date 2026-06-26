package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ActorKind string

const (
	ActorHuman  ActorKind = "human"
	ActorAgent  ActorKind = "agent"
	ActorSystem ActorKind = "system"
)

func ParseActorKind(value string) (ActorKind, error) {
	switch ActorKind(strings.ToLower(strings.TrimSpace(value))) {
	case ActorHuman:
		return ActorHuman, nil
	case ActorAgent:
		return ActorAgent, nil
	case ActorSystem:
		return ActorSystem, nil
	default:
		return "", fmt.Errorf("invalid actor kind %q", value)
	}
}

type Actor struct {
	Kind ActorKind `json:"kind"`
	Name string    `json:"name,omitempty"`
}

func (a Actor) Validate() error {
	if _, err := ParseActorKind(string(a.Kind)); err != nil {
		return err
	}
	return nil
}

type RelationshipType string

const (
	RelationshipFollows    RelationshipType = "follows"
	RelationshipExtends    RelationshipType = "extends"
	RelationshipDuplicates RelationshipType = "duplicates"
	RelationshipSupersedes RelationshipType = "supersedes"
	RelationshipConflicts  RelationshipType = "conflicts"
	RelationshipReconciles RelationshipType = "reconciles"
)

func ParseRelationshipType(value string) (RelationshipType, error) {
	switch RelationshipType(strings.ToLower(strings.TrimSpace(value))) {
	case RelationshipFollows:
		return RelationshipFollows, nil
	case RelationshipExtends:
		return RelationshipExtends, nil
	case RelationshipDuplicates:
		return RelationshipDuplicates, nil
	case RelationshipSupersedes:
		return RelationshipSupersedes, nil
	case RelationshipConflicts:
		return RelationshipConflicts, nil
	case RelationshipReconciles:
		return RelationshipReconciles, nil
	default:
		return "", fmt.Errorf("invalid relationship type %q", value)
	}
}

type ConflictSeverity string

const (
	SeverityLow      ConflictSeverity = "low"
	SeverityMedium   ConflictSeverity = "medium"
	SeverityHigh     ConflictSeverity = "high"
	SeverityCritical ConflictSeverity = "critical"
)

func ParseConflictSeverity(value string) (ConflictSeverity, error) {
	switch ConflictSeverity(strings.ToLower(strings.TrimSpace(value))) {
	case SeverityLow:
		return SeverityLow, nil
	case SeverityMedium:
		return SeverityMedium, nil
	case SeverityHigh:
		return SeverityHigh, nil
	case SeverityCritical:
		return SeverityCritical, nil
	default:
		return "", fmt.Errorf("invalid conflict severity %q", value)
	}
}

type ConflictStatus string

const (
	ConflictOpen      ConflictStatus = "open"
	ConflictResolved  ConflictStatus = "resolved"
	ConflictDismissed ConflictStatus = "dismissed"
	ConflictDeferred  ConflictStatus = "deferred"
)

func ParseConflictStatus(value string) (ConflictStatus, error) {
	if strings.TrimSpace(value) == "" {
		return ConflictOpen, nil
	}
	switch ConflictStatus(strings.ToLower(strings.TrimSpace(value))) {
	case ConflictOpen:
		return ConflictOpen, nil
	case ConflictResolved:
		return ConflictResolved, nil
	case ConflictDismissed:
		return ConflictDismissed, nil
	case ConflictDeferred:
		return ConflictDeferred, nil
	default:
		return "", fmt.Errorf("invalid conflict status %q", value)
	}
}

type DecisionStatus string

const (
	DecisionActive     DecisionStatus = "active"
	DecisionSuperseded DecisionStatus = "superseded"
	DecisionDeprecated DecisionStatus = "deprecated"
)

func ParseDecisionStatus(value string) (DecisionStatus, error) {
	if strings.TrimSpace(value) == "" {
		return DecisionActive, nil
	}
	switch DecisionStatus(strings.ToLower(strings.TrimSpace(value))) {
	case DecisionActive:
		return DecisionActive, nil
	case DecisionSuperseded:
		return DecisionSuperseded, nil
	case DecisionDeprecated:
		return DecisionDeprecated, nil
	default:
		return "", fmt.Errorf("invalid decision status %q", value)
	}
}

type DecisionCandidateStatus string

const (
	CandidatePending  DecisionCandidateStatus = "pending"
	CandidateAccepted DecisionCandidateStatus = "accepted"
	CandidateRejected DecisionCandidateStatus = "rejected"
)

func ParseDecisionCandidateStatus(value string) (DecisionCandidateStatus, error) {
	if strings.TrimSpace(value) == "" {
		return CandidatePending, nil
	}
	switch DecisionCandidateStatus(strings.ToLower(strings.TrimSpace(value))) {
	case CandidatePending:
		return CandidatePending, nil
	case CandidateAccepted:
		return CandidateAccepted, nil
	case CandidateRejected:
		return CandidateRejected, nil
	default:
		return "", fmt.Errorf("invalid decision candidate status %q", value)
	}
}

type Workspace struct {
	ID              string         `json:"id"`
	RepoRoot        string         `json:"repo_root"`
	DisplayName     string         `json:"display_name"`
	RepoFingerprint string         `json:"repo_fingerprint"`
	DetectedDocs    []RepoDocument `json:"detected_docs,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (w Workspace) Validate() error {
	if strings.TrimSpace(w.ID) == "" {
		return errors.New("workspace id is required")
	}
	if strings.TrimSpace(w.RepoRoot) == "" {
		return errors.New("workspace repo_root is required")
	}
	if strings.TrimSpace(w.RepoFingerprint) == "" {
		return errors.New("workspace repo_fingerprint is required")
	}
	return nil
}

type Decision struct {
	ID                string                 `json:"id"`
	WorkspaceID       string                 `json:"workspace_id"`
	Actor             Actor                  `json:"actor"`
	Type              string                 `json:"type"`
	Title             string                 `json:"title"`
	Outcome           string                 `json:"outcome"`
	Rationale         string                 `json:"rationale,omitempty"`
	Confidence        float64                `json:"confidence"`
	Status            DecisionStatus         `json:"status"`
	ValidFrom         time.Time              `json:"valid_from"`
	ValidTo           *time.Time             `json:"valid_to,omitempty"`
	TransactionTime   time.Time              `json:"transaction_time"`
	Alternatives      []Alternative          `json:"alternatives,omitempty"`
	Claims            []Claim                `json:"claims,omitempty"`
	Evidence          []Evidence             `json:"evidence,omitempty"`
	Relationships     []DecisionRelationship `json:"relationships,omitempty"`
	PrecedentRef      string                 `json:"precedent_ref,omitempty"`
	SupersedesID      string                 `json:"supersedes_id,omitempty"`
	CompletenessScore float64                `json:"completeness_score"`
	Metadata          map[string]any         `json:"metadata,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func (d Decision) Validate() error {
	if strings.TrimSpace(d.ID) == "" {
		return errors.New("decision id is required")
	}
	if strings.TrimSpace(d.WorkspaceID) == "" {
		return errors.New("decision workspace_id is required")
	}
	if err := d.Actor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Type) == "" {
		return errors.New("decision type is required")
	}
	if strings.TrimSpace(d.Title) == "" {
		return errors.New("decision title is required")
	}
	if strings.TrimSpace(d.Outcome) == "" {
		return errors.New("decision outcome is required")
	}
	if d.Confidence < 0 || d.Confidence > 1 {
		return errors.New("decision confidence must be between 0 and 1")
	}
	if _, err := ParseDecisionStatus(string(d.Status)); err != nil {
		return err
	}
	if d.CompletenessScore < 0 || d.CompletenessScore > 1 {
		return errors.New("decision completeness_score must be between 0 and 1")
	}
	for _, alternative := range d.Alternatives {
		if err := alternative.Validate(); err != nil {
			return err
		}
	}
	for _, rel := range d.Relationships {
		if err := rel.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func (d Decision) IsActive() bool {
	return d.ValidTo == nil
}

type DecisionCandidate struct {
	ID                 string                  `json:"id"`
	WorkspaceID        string                  `json:"workspace_id"`
	Type               string                  `json:"type"`
	Title              string                  `json:"title"`
	Outcome            string                  `json:"outcome"`
	Rationale          string                  `json:"rationale,omitempty"`
	Confidence         float64                 `json:"confidence"`
	SourcePath         string                  `json:"source_path"`
	SourceTitle        string                  `json:"source_title,omitempty"`
	SourceSnippet      string                  `json:"source_snippet"`
	SourceHash         string                  `json:"source_hash"`
	Detector           string                  `json:"detector"`
	Status             DecisionCandidateStatus `json:"status"`
	AcceptedDecisionID string                  `json:"accepted_decision_id,omitempty"`
	RejectionSummary   string                  `json:"rejection_summary,omitempty"`
	CreatedAt          time.Time               `json:"created_at"`
	UpdatedAt          time.Time               `json:"updated_at"`
}

func (c DecisionCandidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("decision candidate id is required")
	}
	if strings.TrimSpace(c.WorkspaceID) == "" {
		return errors.New("decision candidate workspace_id is required")
	}
	if strings.TrimSpace(c.Type) == "" {
		return errors.New("decision candidate type is required")
	}
	if strings.TrimSpace(c.Title) == "" {
		return errors.New("decision candidate title is required")
	}
	if strings.TrimSpace(c.Outcome) == "" {
		return errors.New("decision candidate outcome is required")
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("decision candidate confidence must be between 0 and 1")
	}
	if strings.TrimSpace(c.SourcePath) == "" {
		return errors.New("decision candidate source_path is required")
	}
	if strings.TrimSpace(c.SourceSnippet) == "" {
		return errors.New("decision candidate source_snippet is required")
	}
	if strings.TrimSpace(c.SourceHash) == "" {
		return errors.New("decision candidate source_hash is required")
	}
	if strings.TrimSpace(c.Detector) == "" {
		return errors.New("decision candidate detector is required")
	}
	if _, err := ParseDecisionCandidateStatus(string(c.Status)); err != nil {
		return err
	}
	if c.Status == CandidateAccepted && strings.TrimSpace(c.AcceptedDecisionID) == "" {
		return errors.New("accepted decision candidate requires accepted_decision_id")
	}
	return nil
}

type Alternative struct {
	ID         string `json:"id,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
	Title      string `json:"title"`
	Outcome    string `json:"outcome,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (a Alternative) Validate() error {
	if strings.TrimSpace(a.Title) == "" && strings.TrimSpace(a.Outcome) == "" {
		return errors.New("alternative title or outcome is required")
	}
	return nil
}

type Claim struct {
	ID         string `json:"id,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
	Text       string `json:"text"`
}

type Evidence struct {
	ID         string         `json:"id,omitempty"`
	DecisionID string         `json:"decision_id,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	URI        string         `json:"uri,omitempty"`
	Title      string         `json:"title,omitempty"`
	Snippet    string         `json:"snippet,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

type DecisionRelationship struct {
	ID             string           `json:"id,omitempty"`
	WorkspaceID    string           `json:"workspace_id,omitempty"`
	FromDecisionID string           `json:"from_decision_id"`
	ToDecisionID   string           `json:"to_decision_id"`
	Type           RelationshipType `json:"type"`
	Confidence     float64          `json:"confidence"`
	Reason         string           `json:"reason,omitempty"`
}

func (r DecisionRelationship) Validate() error {
	if strings.TrimSpace(r.FromDecisionID) == "" {
		return errors.New("relationship from_decision_id is required")
	}
	if strings.TrimSpace(r.ToDecisionID) == "" {
		return errors.New("relationship to_decision_id is required")
	}
	if _, err := ParseRelationshipType(string(r.Type)); err != nil {
		return err
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return errors.New("relationship confidence must be between 0 and 1")
	}
	return nil
}

type RepoDocument struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Path        string    `json:"path"`
	Title       string    `json:"title,omitempty"`
	Content     string    `json:"content,omitempty"`
	ChunkIndex  int       `json:"chunk_index"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type LookupReceipt struct {
	ID          string                `json:"id"`
	WorkspaceID string                `json:"workspace_id"`
	Query       string                `json:"query"`
	ResultCount int                   `json:"result_count"`
	Results     []LookupReceiptResult `json:"results,omitempty"`
	CreatedAt   time.Time             `json:"created_at"`
}

type LookupReceiptResult struct {
	Kind        string  `json:"kind"`
	EntityID    string  `json:"entity_id"`
	WorkspaceID string  `json:"workspace_id"`
	Title       string  `json:"title,omitempty"`
	Path        string  `json:"path,omitempty"`
	Snippet     string  `json:"snippet,omitempty"`
	Score       float64 `json:"score"`
}

type Assessment struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	DecisionID  string         `json:"decision_id"`
	Actor       Actor          `json:"actor"`
	Signal      string         `json:"signal"`
	Score       int            `json:"score,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

func (a Assessment) Validate() error {
	if strings.TrimSpace(a.ID) == "" {
		return errors.New("assessment id is required")
	}
	if strings.TrimSpace(a.WorkspaceID) == "" {
		return errors.New("assessment workspace_id is required")
	}
	if strings.TrimSpace(a.DecisionID) == "" {
		return errors.New("assessment decision_id is required")
	}
	if err := a.Actor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(a.Signal) == "" {
		return errors.New("assessment signal is required")
	}
	if a.Score < 0 || a.Score > 5 {
		return errors.New("assessment score must be between 0 and 5")
	}
	return nil
}

type Conflict struct {
	ID                string           `json:"id"`
	WorkspaceID       string           `json:"workspace_id"`
	DecisionID        string           `json:"decision_id,omitempty"`
	Proposal          string           `json:"proposal,omitempty"`
	ConflictingWithID string           `json:"conflicting_with_id"`
	Summary           string           `json:"summary"`
	Severity          ConflictSeverity `json:"severity"`
	Confidence        float64          `json:"confidence"`
	Status            ConflictStatus   `json:"status"`
	CreatedAt         time.Time        `json:"created_at"`
	ResolvedAt        *time.Time       `json:"resolved_at,omitempty"`
}

func (c Conflict) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("conflict id is required")
	}
	if strings.TrimSpace(c.WorkspaceID) == "" {
		return errors.New("conflict workspace_id is required")
	}
	if strings.TrimSpace(c.ConflictingWithID) == "" {
		return errors.New("conflict conflicting_with_id is required")
	}
	if strings.TrimSpace(c.Summary) == "" {
		return errors.New("conflict summary is required")
	}
	if _, err := ParseConflictSeverity(string(c.Severity)); err != nil {
		return err
	}
	if c.Confidence < 0 || c.Confidence > 1 {
		return errors.New("conflict confidence must be between 0 and 1")
	}
	if _, err := ParseConflictStatus(string(c.Status)); err != nil {
		return err
	}
	return nil
}

type ConflictResolution struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ConflictID  string    `json:"conflict_id"`
	Actor       Actor     `json:"actor"`
	Action      string    `json:"action"`
	Summary     string    `json:"summary"`
	DecisionID  string    `json:"decision_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (r ConflictResolution) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("conflict resolution id is required")
	}
	if strings.TrimSpace(r.WorkspaceID) == "" {
		return errors.New("conflict resolution workspace_id is required")
	}
	if strings.TrimSpace(r.ConflictID) == "" {
		return errors.New("conflict resolution conflict_id is required")
	}
	if err := r.Actor.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.Action) == "" {
		return errors.New("conflict resolution action is required")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return errors.New("conflict resolution summary is required")
	}
	return nil
}
