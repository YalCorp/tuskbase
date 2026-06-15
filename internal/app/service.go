package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

const (
	defaultLimit             = 10
	defaultContextLimit      = 6
	contextDocumentScanLimit = 1000
)

type Store interface {
	ports.WorkspaceStore
	ports.DecisionStore
	ports.DocumentStore
	ports.ReceiptStore
	ports.AssessmentStore
	ports.ConflictStore
}

type Service struct {
	store     Store
	search    ports.SearchIndex
	ids       ports.IDGenerator
	clock     ports.Clock
	validator ports.RelationshipValidator
}

type Option func(*Service)

func WithRelationshipValidator(validator ports.RelationshipValidator) Option {
	return func(s *Service) {
		s.validator = validator
	}
}

func NewService(store Store, search ports.SearchIndex, ids ports.IDGenerator, clock ports.Clock, opts ...Option) *Service {
	if ids == nil {
		ids = UUIDGenerator{}
	}
	if clock == nil {
		clock = SystemClock{}
	}
	service := &Service{store: store, search: search, ids: ids, clock: clock}
	for _, opt := range opts {
		if opt != nil {
			opt(service)
		}
	}
	return service
}

type AttachInput struct {
	RepoPath    string `json:"repo_path"`
	DisplayName string `json:"display_name,omitempty"`
}

type AttachOutput struct {
	Workspace domain.Workspace `json:"workspace"`
}

func (s *Service) Attach(ctx context.Context, in AttachInput) (AttachOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return AttachOutput{}, err
	}
	if strings.TrimSpace(in.RepoPath) == "" {
		return AttachOutput{}, errors.New("repo_path is required")
	}
	root, err := resolveRepoRoot(in.RepoPath)
	if err != nil {
		return AttachOutput{}, err
	}
	docs, err := scanRepoDocuments(root)
	if err != nil {
		return AttachOutput{}, err
	}
	now := s.clock.Now()
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		displayName = filepath.Base(root)
	}
	workspace := domain.Workspace{
		ID:              workspaceID(root),
		RepoRoot:        root,
		DisplayName:     displayName,
		RepoFingerprint: repoFingerprint(root),
		DetectedDocs:    docs,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := workspace.Validate(); err != nil {
		return AttachOutput{}, err
	}
	for i := range docs {
		docs[i].WorkspaceID = workspace.ID
		docs[i].CreatedAt = now
		docs[i].UpdatedAt = now
	}
	workspace.DetectedDocs = docs
	saved, err := s.store.UpsertWorkspace(ctx, workspace)
	if err != nil {
		return AttachOutput{}, err
	}
	if err := s.store.ReplaceWorkspaceDocuments(ctx, saved.ID, docs); err != nil {
		return AttachOutput{}, err
	}
	for _, doc := range docs {
		if err := s.search.IndexDocument(ctx, doc); err != nil {
			return AttachOutput{}, fmt.Errorf("index repo document %s: %w", doc.Path, err)
		}
	}
	saved.DetectedDocs = docs
	return AttachOutput{Workspace: saved}, nil
}

type RememberInput struct {
	WorkspaceID   string                        `json:"workspace_id"`
	Actor         domain.Actor                  `json:"actor,omitempty"`
	Type          string                        `json:"type"`
	Title         string                        `json:"title"`
	Outcome       string                        `json:"outcome"`
	Rationale     string                        `json:"rationale,omitempty"`
	Confidence    float64                       `json:"confidence"`
	Status        domain.DecisionStatus         `json:"status,omitempty"`
	ValidFrom     time.Time                     `json:"valid_from,omitempty"`
	Alternatives  []domain.Alternative          `json:"alternatives,omitempty"`
	Claims        []domain.Claim                `json:"claims,omitempty"`
	Evidence      []domain.Evidence             `json:"evidence,omitempty"`
	Relationships []domain.DecisionRelationship `json:"relationships,omitempty"`
	PrecedentRef  string                        `json:"precedent_ref,omitempty"`
	SupersedesID  string                        `json:"supersedes_id,omitempty"`
	Metadata      map[string]any                `json:"metadata,omitempty"`
}

type RememberOutput struct {
	Decision        domain.Decision `json:"decision"`
	IndexingStatus  string          `json:"indexing_status"`
	IndexingError   string          `json:"indexing_error,omitempty"`
	QualityWarnings []string        `json:"quality_warnings,omitempty"`
}

// Remember stores the canonical decision first, then updates derived indexes. A retrieval failure should not erase the decision trail.
func (s *Service) Remember(ctx context.Context, in RememberInput) (RememberOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return RememberOutput{}, err
	}
	actor, err := ApplyPrincipalActor(ctx, in.Actor)
	if err != nil {
		return RememberOutput{}, err
	}
	status, err := domain.ParseDecisionStatus(string(in.Status))
	if err != nil {
		return RememberOutput{}, err
	}
	now := s.clock.Now()
	validFrom := in.ValidFrom
	if validFrom.IsZero() {
		validFrom = now
	}
	decision := domain.Decision{
		ID:              s.ids.NewID(),
		WorkspaceID:     strings.TrimSpace(in.WorkspaceID),
		Actor:           actor,
		Type:            strings.TrimSpace(in.Type),
		Title:           strings.TrimSpace(in.Title),
		Outcome:         strings.TrimSpace(in.Outcome),
		Rationale:       strings.TrimSpace(in.Rationale),
		Confidence:      in.Confidence,
		Status:          status,
		ValidFrom:       validFrom,
		TransactionTime: now,
		Alternatives:    in.Alternatives,
		Claims:          in.Claims,
		Evidence:        in.Evidence,
		PrecedentRef:    strings.TrimSpace(in.PrecedentRef),
		SupersedesID:    strings.TrimSpace(in.SupersedesID),
		Metadata:        in.Metadata,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if decision.Confidence == 0 {
		decision.Confidence = 0.7
	}
	for i := range decision.Alternatives {
		if decision.Alternatives[i].ID == "" {
			decision.Alternatives[i].ID = s.ids.NewID()
		}
		decision.Alternatives[i].DecisionID = decision.ID
		decision.Alternatives[i].Title = strings.TrimSpace(decision.Alternatives[i].Title)
		decision.Alternatives[i].Outcome = strings.TrimSpace(decision.Alternatives[i].Outcome)
		decision.Alternatives[i].Reason = strings.TrimSpace(decision.Alternatives[i].Reason)
	}
	for i := range decision.Claims {
		if decision.Claims[i].ID == "" {
			decision.Claims[i].ID = s.ids.NewID()
		}
		decision.Claims[i].DecisionID = decision.ID
		decision.Claims[i].Text = strings.TrimSpace(decision.Claims[i].Text)
	}
	for i := range decision.Evidence {
		if decision.Evidence[i].ID == "" {
			decision.Evidence[i].ID = s.ids.NewID()
		}
		decision.Evidence[i].DecisionID = decision.ID
		decision.Evidence[i].Kind = strings.TrimSpace(decision.Evidence[i].Kind)
		decision.Evidence[i].URI = strings.TrimSpace(decision.Evidence[i].URI)
		decision.Evidence[i].Title = strings.TrimSpace(decision.Evidence[i].Title)
		decision.Evidence[i].Snippet = strings.TrimSpace(decision.Evidence[i].Snippet)
	}
	decision.Relationships = normalizeRememberRelationships(decision, in.Relationships, s.ids)
	completenessScore, outWarnings := scoreCompleteness(decision)
	decision.CompletenessScore = completenessScore
	if err := decision.Validate(); err != nil {
		return RememberOutput{}, err
	}
	if _, err := s.store.GetWorkspace(ctx, decision.WorkspaceID); err != nil {
		return RememberOutput{}, err
	}
	if err := s.store.SaveDecision(ctx, decision); err != nil {
		return RememberOutput{}, err
	}
	for _, rel := range decision.Relationships {
		if rel.Type != domain.RelationshipConflicts {
			continue
		}
		conflict := domain.Conflict{
			ID:                s.ids.NewID(),
			WorkspaceID:       decision.WorkspaceID,
			DecisionID:        decision.ID,
			ConflictingWithID: rel.ToDecisionID,
			Summary:           relationshipReason(rel, decision),
			Severity:          severityForConfidence(rel.Confidence),
			Confidence:        rel.Confidence,
			Status:            domain.ConflictOpen,
			CreatedAt:         now,
		}
		if err := conflict.Validate(); err != nil {
			return RememberOutput{}, err
		}
		if err := s.store.SaveConflict(ctx, conflict); err != nil {
			return RememberOutput{}, err
		}
	}
	out := RememberOutput{Decision: decision, IndexingStatus: "indexed", QualityWarnings: outWarnings}
	if err := s.search.IndexDecision(ctx, decision); err != nil {
		out.IndexingStatus = "error"
		out.IndexingError = err.Error()
	}
	return out, nil
}

type LookupInput struct {
	WorkspaceID string `json:"workspace_id"`
	Query       string `json:"query"`
	Limit       int    `json:"limit,omitempty"`
}

type LookupOutput struct {
	Results []ports.SearchResult `json:"results"`
	Receipt domain.LookupReceipt `json:"receipt"`
}

func (s *Service) Lookup(ctx context.Context, in LookupInput) (LookupOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return LookupOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	query := strings.TrimSpace(in.Query)
	if workspaceID == "" {
		return LookupOutput{}, errors.New("workspace_id is required")
	}
	if query == "" {
		return LookupOutput{}, errors.New("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	results, err := s.search.Search(ctx, ports.SearchQuery{WorkspaceID: workspaceID, Query: query, Limit: limit})
	if err != nil {
		return LookupOutput{}, err
	}
	receipt := domain.LookupReceipt{
		ID:          s.ids.NewID(),
		WorkspaceID: workspaceID,
		Query:       query,
		ResultCount: len(results),
		Results:     receiptResults(results),
		CreatedAt:   s.clock.Now(),
	}
	if err := s.store.SaveLookupReceipt(ctx, receipt); err != nil {
		return LookupOutput{}, err
	}
	return LookupOutput{Results: results, Receipt: receipt}, nil
}

type PreflightInput struct {
	WorkspaceID string `json:"workspace_id"`
	Proposal    string `json:"proposal"`
	Limit       int    `json:"limit,omitempty"`
}

type PreflightOutput struct {
	Lookup        LookupOutput               `json:"lookup"`
	Relationships []DecisionProposalRelation `json:"relationships"`
	Conflicts     []domain.Conflict          `json:"conflicts"`
}

type DecisionProposalRelation struct {
	DecisionID string                     `json:"decision_id"`
	Type       domain.RelationshipType    `json:"type"`
	Confidence float64                    `json:"confidence"`
	Reason     string                     `json:"reason,omitempty"`
	Subject    string                     `json:"subject,omitempty"`
	Evidence   []ports.RelationshipSignal `json:"evidence,omitempty"`
}

func (s *Service) Preflight(ctx context.Context, in PreflightInput) (PreflightOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return PreflightOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if strings.TrimSpace(in.Proposal) == "" {
		return PreflightOutput{}, errors.New("proposal is required")
	}
	lookup, err := s.Lookup(ctx, LookupInput{WorkspaceID: workspaceID, Query: in.Proposal, Limit: in.Limit})
	if err != nil {
		return PreflightOutput{}, err
	}
	var out PreflightOutput
	out.Lookup = lookup
	decisions, err := s.decisionsForResults(ctx, workspaceID, lookup.Results, in.Limit)
	if err != nil {
		return PreflightOutput{}, err
	}
	for _, decision := range decisions {
		relation := s.classifyProposal(ctx, workspaceID, in.Proposal, decision)
		out.Relationships = append(out.Relationships, relation)
		if relation.Type == domain.RelationshipConflicts {
			conflict := domain.Conflict{
				ID:                s.ids.NewID(),
				WorkspaceID:       workspaceID,
				Proposal:          strings.TrimSpace(in.Proposal),
				ConflictingWithID: decision.ID,
				Summary:           relation.Reason,
				Severity:          severityForConfidence(relation.Confidence),
				Confidence:        relation.Confidence,
				Status:            domain.ConflictOpen,
				CreatedAt:         s.clock.Now(),
			}
			if err := conflict.Validate(); err != nil {
				return PreflightOutput{}, err
			}
			if err := s.store.SaveConflict(ctx, conflict); err != nil {
				return PreflightOutput{}, err
			}
			out.Conflicts = append(out.Conflicts, conflict)
		}
	}
	sort.SliceStable(out.Relationships, func(i, j int) bool {
		return out.Relationships[i].Confidence > out.Relationships[j].Confidence
	})
	return out, nil
}

func (s *Service) Recent(ctx context.Context, workspaceID string, limit int) ([]domain.Decision, error) {
	if err := RequireRead(ctx); err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	return s.store.RecentDecisions(ctx, workspaceID, limit)
}

func (s *Service) Conflicts(ctx context.Context, workspaceID string) ([]domain.Conflict, error) {
	if err := RequireRead(ctx); err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, errors.New("workspace_id is required")
	}
	return s.store.ListOpenConflicts(ctx, workspaceID)
}

type ContextInput struct {
	WorkspaceID string `json:"workspace_id"`
	Limit       int    `json:"limit,omitempty"`
}

type ContextOutput struct {
	Workspace              ContextWorkspace      `json:"workspace"`
	ActiveDecisions        []ContextDecision     `json:"active_decisions"`
	OpenConflicts          []ContextConflict     `json:"open_conflicts"`
	RecentSupersessions    []ContextSupersession `json:"recent_supersessions,omitempty"`
	DegradedStates         []ContextDegraded     `json:"degraded_states,omitempty"`
	RecommendedNextActions []string              `json:"recommended_next_actions"`
	GeneratedAt            time.Time             `json:"generated_at"`
}

type ContextWorkspace struct {
	ID               string            `json:"id"`
	RepoRoot         string            `json:"repo_root"`
	DisplayName      string            `json:"display_name"`
	RepoFingerprint  string            `json:"repo_fingerprint"`
	DetectedDocCount int               `json:"detected_doc_count"`
	RepoDocuments    []ContextDocument `json:"repo_documents,omitempty"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type ContextDocument struct {
	Path   string `json:"path"`
	Title  string `json:"title,omitempty"`
	Chunks int    `json:"chunks"`
}

type ContextDecision struct {
	ID                string    `json:"id"`
	Type              string    `json:"type"`
	Title             string    `json:"title"`
	Outcome           string    `json:"outcome"`
	Confidence        float64   `json:"confidence"`
	CompletenessScore float64   `json:"completeness_score"`
	ClaimCount        int       `json:"claim_count"`
	EvidenceCount     int       `json:"evidence_count"`
	SupersedesID      string    `json:"supersedes_id,omitempty"`
	ValidFrom         time.Time `json:"valid_from"`
}

type ContextConflict struct {
	ID                string                  `json:"id"`
	DecisionID        string                  `json:"decision_id,omitempty"`
	Proposal          string                  `json:"proposal,omitempty"`
	ConflictingWithID string                  `json:"conflicting_with_id"`
	Summary           string                  `json:"summary"`
	Severity          domain.ConflictSeverity `json:"severity"`
	Confidence        float64                 `json:"confidence"`
	CreatedAt         time.Time               `json:"created_at"`
}

type ContextSupersession struct {
	DecisionID      string    `json:"decision_id"`
	Title           string    `json:"title"`
	SupersededID    string    `json:"superseded_id"`
	SupersededTitle string    `json:"superseded_title,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	TransactionTime time.Time `json:"transaction_time"`
}

type ContextDegraded struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
}

func (s *Service) Context(ctx context.Context, in ContextInput) (ContextOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return ContextOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return ContextOutput{}, errors.New("workspace_id is required")
	}
	limit := contextLimit(in.Limit)
	workspace, err := s.store.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return ContextOutput{}, err
	}
	docs, err := s.store.ListWorkspaceDocuments(ctx, workspaceID, contextDocumentScanLimit)
	if err != nil {
		return ContextOutput{}, err
	}
	decisions, err := s.store.RecentDecisions(ctx, workspaceID, limit)
	if err != nil {
		return ContextOutput{}, err
	}
	conflicts, err := s.store.ListOpenConflicts(ctx, workspaceID)
	if err != nil {
		return ContextOutput{}, err
	}
	out := ContextOutput{
		Workspace:           contextWorkspace(workspace, docs, limit),
		ActiveDecisions:     contextDecisions(decisions),
		OpenConflicts:       contextConflicts(conflicts),
		RecentSupersessions: s.contextSupersessions(ctx, decisions, limit),
		GeneratedAt:         s.clock.Now(),
	}
	out.DegradedStates = contextDegradedStates(out)
	out.RecommendedNextActions = contextRecommendedNextActions(out)
	return out, nil
}

func contextLimit(limit int) int {
	if limit <= 0 {
		return defaultContextLimit
	}
	if limit > defaultLimit {
		return defaultLimit
	}
	return limit
}

func contextWorkspace(workspace domain.Workspace, docs []domain.RepoDocument, limit int) ContextWorkspace {
	return ContextWorkspace{
		ID:               workspace.ID,
		RepoRoot:         workspace.RepoRoot,
		DisplayName:      workspace.DisplayName,
		RepoFingerprint:  workspace.RepoFingerprint,
		DetectedDocCount: uniqueDocumentCount(docs),
		RepoDocuments:    summarizeDocuments(docs, limit),
		UpdatedAt:        workspace.UpdatedAt,
	}
}

func contextDecisions(decisions []domain.Decision) []ContextDecision {
	out := make([]ContextDecision, 0, len(decisions))
	for _, decision := range decisions {
		out = append(out, ContextDecision{
			ID:                decision.ID,
			Type:              decision.Type,
			Title:             decision.Title,
			Outcome:           decision.Outcome,
			Confidence:        decision.Confidence,
			CompletenessScore: decision.CompletenessScore,
			ClaimCount:        len(decision.Claims),
			EvidenceCount:     len(decision.Evidence),
			SupersedesID:      decision.SupersedesID,
			ValidFrom:         decision.ValidFrom,
		})
	}
	return out
}

func contextConflicts(conflicts []domain.Conflict) []ContextConflict {
	out := make([]ContextConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		out = append(out, ContextConflict{
			ID:                conflict.ID,
			DecisionID:        conflict.DecisionID,
			Proposal:          conflict.Proposal,
			ConflictingWithID: conflict.ConflictingWithID,
			Summary:           conflict.Summary,
			Severity:          conflict.Severity,
			Confidence:        conflict.Confidence,
			CreatedAt:         conflict.CreatedAt,
		})
	}
	return out
}

func summarizeDocuments(docs []domain.RepoDocument, limit int) []ContextDocument {
	if limit <= 0 {
		limit = defaultContextLimit
	}
	titles := map[string]string{}
	chunks := map[string]int{}
	paths := make([]string, 0, len(docs))
	for _, doc := range docs {
		path := strings.TrimSpace(doc.Path)
		if path == "" {
			continue
		}
		if _, ok := chunks[path]; !ok {
			paths = append(paths, path)
			titles[path] = strings.TrimSpace(doc.Title)
		}
		chunks[path]++
	}
	sort.Strings(paths)
	if len(paths) > limit {
		paths = paths[:limit]
	}
	out := make([]ContextDocument, 0, len(paths))
	for _, path := range paths {
		out = append(out, ContextDocument{Path: path, Title: titles[path], Chunks: chunks[path]})
	}
	return out
}

func uniqueDocumentCount(docs []domain.RepoDocument) int {
	seen := map[string]struct{}{}
	for _, doc := range docs {
		path := strings.TrimSpace(doc.Path)
		if path != "" {
			seen[path] = struct{}{}
		}
	}
	return len(seen)
}

func (s *Service) contextSupersessions(ctx context.Context, decisions []domain.Decision, limit int) []ContextSupersession {
	out := make([]ContextSupersession, 0, len(decisions))
	seen := map[string]struct{}{}
	add := func(decision domain.Decision, supersededID, reason string) {
		if len(out) >= limit {
			return
		}
		supersededID = strings.TrimSpace(supersededID)
		if supersededID == "" {
			return
		}
		key := decision.ID + "\x00" + supersededID
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		supersession := ContextSupersession{
			DecisionID:      decision.ID,
			Title:           decision.Title,
			SupersededID:    supersededID,
			Reason:          strings.TrimSpace(reason),
			TransactionTime: decision.TransactionTime,
		}
		if superseded, err := s.store.GetDecision(ctx, supersededID); err == nil {
			supersession.SupersededTitle = superseded.Title
		}
		out = append(out, supersession)
	}
	for _, decision := range decisions {
		add(decision, decision.SupersedesID, "Supersedes prior active decision.")
		for _, rel := range decision.Relationships {
			if rel.Type == domain.RelationshipSupersedes {
				add(decision, rel.ToDecisionID, rel.Reason)
			}
		}
	}
	return out
}

func contextDegradedStates(out ContextOutput) []ContextDegraded {
	var states []ContextDegraded
	if out.Workspace.DetectedDocCount == 0 {
		states = append(states, ContextDegraded{Code: "repo_docs_missing", Severity: "warning", Detail: "No repo documents are attached for this workspace."})
	}
	if len(out.ActiveDecisions) == 0 {
		states = append(states, ContextDegraded{Code: "no_active_decisions", Severity: "info", Detail: "No active decisions are recorded for this workspace."})
	}
	if len(out.OpenConflicts) > 0 {
		states = append(states, ContextDegraded{Code: "open_conflicts", Severity: "warning", Detail: fmt.Sprintf("%d open conflict(s) need review before relying on affected decisions.", len(out.OpenConflicts))})
	}
	var thin int
	for _, decision := range out.ActiveDecisions {
		if decision.CompletenessScore > 0 && decision.CompletenessScore < 0.55 {
			thin++
		}
	}
	if thin > 0 {
		states = append(states, ContextDegraded{Code: "low_completeness", Severity: "info", Detail: fmt.Sprintf("%d active decision(s) have low completeness.", thin)})
	}
	return states
}

func contextRecommendedNextActions(out ContextOutput) []string {
	actions := make([]string, 0, 4)
	if out.Workspace.DetectedDocCount == 0 {
		actions = append(actions, "Run tuskbase_attach for this repo before relying on repo document context.")
	}
	if len(out.OpenConflicts) > 0 {
		actions = append(actions, "Review open conflicts before relying on affected decisions.")
	}
	if len(out.ActiveDecisions) == 0 {
		actions = append(actions, "Use tuskbase_remember after the work to record durable decisions worth preserving.")
	} else {
		actions = append(actions, "Use tuskbase_lookup with the task-specific question before editing, then cite the lookup receipt when reporting what context you used.")
	}
	if hasLowCompleteness(out.ActiveDecisions) {
		actions = append(actions, "Enrich low-completeness active decisions with rationale, evidence, alternatives, claims, or supersedes links before treating them as strong precedent.")
	}
	actions = append(actions, "Use tuskbase_preflight before meaningful plans and report whether the plan follows, extends, duplicates, supersedes, or conflicts.")
	actions = append(actions, "Use tuskbase_remember only for completed decisions with rationale, evidence, alternatives, claims, and supersedes links.")
	return actions
}

func hasLowCompleteness(decisions []ContextDecision) bool {
	for _, decision := range decisions {
		if decision.CompletenessScore > 0 && decision.CompletenessScore < 0.55 {
			return true
		}
	}
	return false
}

func normalizeRememberRelationships(decision domain.Decision, input []domain.DecisionRelationship, ids ports.IDGenerator) []domain.DecisionRelationship {
	relationships := make([]domain.DecisionRelationship, 0, len(input)+1)
	for _, rel := range input {
		if rel.ID == "" {
			rel.ID = ids.NewID()
		}
		rel.WorkspaceID = decision.WorkspaceID
		rel.FromDecisionID = decision.ID
		if rel.Confidence == 0 {
			rel.Confidence = 0.75
		}
		rel.Reason = strings.TrimSpace(rel.Reason)
		relationships = append(relationships, rel)
	}
	if decision.SupersedesID != "" && !hasRelationship(relationships, decision.SupersedesID, domain.RelationshipSupersedes) {
		relationships = append(relationships, domain.DecisionRelationship{
			ID:             ids.NewID(),
			WorkspaceID:    decision.WorkspaceID,
			FromDecisionID: decision.ID,
			ToDecisionID:   decision.SupersedesID,
			Type:           domain.RelationshipSupersedes,
			Confidence:     1,
			Reason:         "Supersedes prior active decision.",
		})
	}
	return relationships
}

func hasRelationship(relationships []domain.DecisionRelationship, toDecisionID string, relType domain.RelationshipType) bool {
	for _, rel := range relationships {
		if rel.ToDecisionID == toDecisionID && rel.Type == relType {
			return true
		}
	}
	return false
}

func relationshipReason(rel domain.DecisionRelationship, decision domain.Decision) string {
	if strings.TrimSpace(rel.Reason) != "" {
		return strings.TrimSpace(rel.Reason)
	}
	return fmt.Sprintf("Decision %q conflicts with prior active decision %s.", decision.Title, rel.ToDecisionID)
}

func receiptResults(results []ports.SearchResult) []domain.LookupReceiptResult {
	out := make([]domain.LookupReceiptResult, 0, len(results))
	for _, result := range results {
		out = append(out, domain.LookupReceiptResult{
			Kind:        result.Kind,
			EntityID:    result.EntityID,
			WorkspaceID: result.WorkspaceID,
			Title:       result.Title,
			Path:        result.Path,
			Snippet:     result.Snippet,
			Score:       result.Score,
		})
	}
	return out
}

func scoreCompleteness(decision domain.Decision) (float64, []string) {
	var score float64
	var warnings []string
	if strings.TrimSpace(decision.Type) != "" && strings.TrimSpace(decision.Title) != "" && strings.TrimSpace(decision.Outcome) != "" {
		score += 0.2
	}
	rationaleLen := len(strings.Fields(decision.Rationale))
	switch {
	case rationaleLen >= 20:
		score += 0.25
	case rationaleLen > 0:
		score += 0.12
		warnings = append(warnings, "Rationale is present but thin; future agents may not understand the tradeoff.")
	default:
		warnings = append(warnings, "Rationale is missing; include the reasoning behind the decision.")
	}
	if len(decision.Alternatives) > 0 {
		score += 0.15
	} else {
		warnings = append(warnings, "Alternatives are missing; record at least one rejected option when possible.")
	}
	if len(decision.Evidence) > 0 {
		score += 0.2
	} else {
		warnings = append(warnings, "Evidence is missing; link code, docs, tests, issues, or snippets that support the decision.")
	}
	if decision.Confidence > 0 && decision.Confidence <= 1 {
		score += 0.1
	}
	if decision.PrecedentRef != "" || decision.SupersedesID != "" || len(decision.Relationships) > 0 {
		score += 0.1
	}
	if decision.Confidence >= 0.9 && len(decision.Evidence) == 0 && rationaleLen < 20 {
		warnings = append(warnings, "High confidence without evidence or substantial rationale is weak precedent.")
	}
	score = clamp(score)
	if score < 0.55 {
		warnings = append(warnings, "Decision was stored with low completeness; enrich it before treating it as strong precedent.")
	}
	return float64(int(score*100+0.5)) / 100, warnings
}

func isDecisionBoundResult(kind string) bool {
	switch kind {
	case "decision", "claim", "evidence", "alternative":
		return true
	default:
		return false
	}
}

func resolveRepoRoot(repoPath string) (string, error) {
	expanded := strings.TrimSpace(repoPath)
	if strings.HasPrefix(expanded, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			expanded = filepath.Join(home, strings.TrimPrefix(expanded, "~/"))
		}
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("repo_path %q is not a directory", repoPath)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}

func workspaceID(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return "ws_" + hex.EncodeToString(sum[:])[:24]
}

func repoFingerprint(root string) string {
	h := sha256.New()
	h.Write([]byte(filepath.Clean(root)))
	for _, rel := range []string{".git/config", "go.mod", "README.md", "AGENTS.md"} {
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			h.Write([]byte(rel))
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func scanRepoDocuments(root string) ([]domain.RepoDocument, error) {
	var docs []domain.RepoDocument
	seen := map[string]struct{}{}
	add := func(rel string) error {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if _, ok := seen[rel]; ok {
			return nil
		}
		seen[rel] = struct{}{}
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		text := string(data)
		for i, chunk := range chunkText(text, 6000) {
			idHash := sha256.Sum256([]byte(rel + fmt.Sprintf("#%d", i)))
			docs = append(docs, domain.RepoDocument{
				ID:         "doc_" + hex.EncodeToString(idHash[:])[:24],
				Path:       rel,
				Title:      documentTitle(rel, text),
				Content:    chunk,
				ChunkIndex: i,
			})
		}
		return nil
	}
	for _, rel := range []string{"README.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil {
			if err := add(rel); err != nil {
				return nil, err
			}
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		if err := add(entry.Name()); err != nil {
			return nil, err
		}
	}
	docsRoot := filepath.Join(root, "docs")
	if _, err := os.Stat(docsRoot); err == nil {
		err = filepath.WalkDir(docsRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return add(rel)
		})
		if err != nil {
			return nil, err
		}
	}
	sort.SliceStable(docs, func(i, j int) bool {
		if docs[i].Path == docs[j].Path {
			return docs[i].ChunkIndex < docs[j].ChunkIndex
		}
		return docs[i].Path < docs[j].Path
	})
	return docs, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", ".local", "node_modules", "vendor", "dist", "build", "out", "target", ".next":
		return true
	default:
		return false
	}
}

func documentTitle(rel, text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	return rel
}

func chunkText(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}
	var chunks []string
	for len(text) > max {
		cut := strings.LastIndex(text[:max], "\n\n")
		if cut < max/2 {
			cut = strings.LastIndex(text[:max], "\n")
		}
		if cut < max/2 {
			cut = max
		}
		chunks = append(chunks, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	return append(chunks, text)
}

var wordRE = regexp.MustCompile(`[a-z0-9][a-z0-9_+\-./]*`)

func claimsText(claims []domain.Claim) string {
	parts := make([]string, 0, len(claims))
	for _, claim := range claims {
		parts = append(parts, claim.Text)
	}
	return strings.Join(parts, " ")
}

func severityForConfidence(confidence float64) domain.ConflictSeverity {
	switch {
	case confidence >= 0.9:
		return domain.SeverityHigh
	case confidence >= 0.75:
		return domain.SeverityMedium
	default:
		return domain.SeverityLow
	}
}

func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
