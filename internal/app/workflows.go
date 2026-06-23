package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

const statsScanLimit = 1000

type PrepareChangeInput struct {
	RepoPath    string `json:"repo_path"`
	DisplayName string `json:"display_name,omitempty"`
	Task        string `json:"task"`
	Plan        string `json:"plan,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type PrepareChangeOutput struct {
	Workspace            domain.Workspace  `json:"workspace"`
	Context              ContextOutput     `json:"context"`
	RecentDecisions      []domain.Decision `json:"recent_decisions"`
	OpenConflicts        []domain.Conflict `json:"open_conflicts"`
	Lookup               LookupOutput      `json:"lookup"`
	Preflight            *PreflightOutput  `json:"preflight,omitempty"`
	Verdict              string            `json:"verdict"`
	ShouldEdit           bool              `json:"should_edit"`
	RequiresUserApproval bool              `json:"requires_user_approval"`
	NextActions          []string          `json:"next_actions"`
}

type FinishChangeInput struct {
	WorkspaceID  string               `json:"workspace_id"`
	Summary      string               `json:"summary"`
	ChangedFiles []ChangedFileSummary `json:"changed_files,omitempty"`
	Tests        []TestSummary        `json:"tests,omitempty"`
	Decision     *RememberInput       `json:"decision,omitempty"`
}

type ChangedFileSummary struct {
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

type TestSummary struct {
	Command string `json:"command"`
	Status  string `json:"status,omitempty"`
	Output  string `json:"output,omitempty"`
}

type FinishChangeOutput struct {
	Status          string               `json:"status"`
	Reason          string               `json:"reason,omitempty"`
	Summary         string               `json:"summary"`
	ChangedFiles    []ChangedFileSummary `json:"changed_files,omitempty"`
	Tests           []TestSummary        `json:"tests,omitempty"`
	Decision        *domain.Decision     `json:"decision,omitempty"`
	IndexingStatus  string               `json:"indexing_status,omitempty"`
	IndexingError   string               `json:"indexing_error,omitempty"`
	QualityWarnings []string             `json:"quality_warnings,omitempty"`
}

func (s *Service) PrepareChange(ctx context.Context, in PrepareChangeInput) (PrepareChangeOutput, error) {
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return PrepareChangeOutput{}, errors.New("task is required")
	}
	attached, err := s.Attach(ctx, AttachInput{RepoPath: in.RepoPath, DisplayName: in.DisplayName})
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	workspaceID := attached.Workspace.ID
	limit := boundedLimit(in.Limit, defaultLimit)
	briefing, err := s.Context(ctx, ContextInput{WorkspaceID: workspaceID, Limit: limit})
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	recent, err := s.Recent(ctx, workspaceID, limit)
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	openConflicts, err := s.Conflicts(ctx, workspaceID)
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	lookup, err := s.Lookup(ctx, LookupInput{WorkspaceID: workspaceID, Query: task, Limit: limit})
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	out := PrepareChangeOutput{
		Workspace:       attached.Workspace,
		Context:         briefing,
		RecentDecisions: recent,
		OpenConflicts:   openConflicts,
		Lookup:          lookup,
		Verdict:         "needs_plan",
		ShouldEdit:      false,
		NextActions: []string{
			"Draft a concrete implementation plan, then call tuskbase_prepare_change again with plan before editing.",
			"Do not edit files until prepare_change returns should_edit=true.",
		},
	}
	plan := strings.TrimSpace(in.Plan)
	if plan == "" {
		return out, nil
	}
	preflight, err := s.Preflight(ctx, PreflightInput{WorkspaceID: workspaceID, Proposal: plan, Limit: limit})
	if err != nil {
		return PrepareChangeOutput{}, err
	}
	out.Preflight = &preflight
	if len(preflight.Conflicts) > 0 {
		out.Verdict = "conflict"
		out.ShouldEdit = false
		out.RequiresUserApproval = true
		out.NextActions = []string{
			"Stop before editing. Ask the user to approve a changed plan, conflict resolution, or reconciliation.",
			"Only call tuskbase_resolve_conflict or tuskbase_reconcile after explicit user approval.",
		}
		return out, nil
	}
	out.Verdict = "proceed"
	out.ShouldEdit = true
	out.NextActions = []string{
		"Proceed with the planned edits.",
		"After verification, call tuskbase_finish_change and include a durable decision only if one was actually made.",
	}
	if len(openConflicts) > 0 {
		out.NextActions = append(out.NextActions, "Existing open conflicts are included as context but are not a hard stop unless this plan conflicts.")
	}
	return out, nil
}

func (s *Service) FinishChange(ctx context.Context, in FinishChangeInput) (FinishChangeOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return FinishChangeOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return FinishChangeOutput{}, errors.New("workspace_id is required")
	}
	if _, err := s.store.GetWorkspace(ctx, workspaceID); err != nil {
		return FinishChangeOutput{}, err
	}
	out := FinishChangeOutput{
		Summary:      strings.TrimSpace(in.Summary),
		ChangedFiles: normalizeChangedFiles(in.ChangedFiles),
		Tests:        normalizeTestSummaries(in.Tests),
	}
	if in.Decision == nil {
		out.Status = "skipped"
		out.Reason = "no durable decision supplied"
		return out, nil
	}
	decisionInput := *in.Decision
	decisionWorkspaceID := strings.TrimSpace(decisionInput.WorkspaceID)
	if decisionWorkspaceID == "" {
		decisionInput.WorkspaceID = workspaceID
	} else if decisionWorkspaceID != workspaceID {
		return FinishChangeOutput{}, errors.New("decision workspace_id does not match finish workspace_id")
	}
	remembered, err := s.Remember(ctx, decisionInput)
	if err != nil {
		return FinishChangeOutput{}, err
	}
	out.Status = "remembered"
	out.Decision = &remembered.Decision
	out.IndexingStatus = remembered.IndexingStatus
	out.IndexingError = remembered.IndexingError
	out.QualityWarnings = remembered.QualityWarnings
	return out, nil
}

type CheckInput struct {
	WorkspaceID string `json:"workspace_id"`
	Proposal    string `json:"proposal"`
	Limit       int    `json:"limit,omitempty"`
}

type CheckOutput struct {
	Relationships []DecisionProposalRelation `json:"relationships"`
	Conflicts     []ConflictPreview          `json:"conflicts"`
	CanProceed    bool                       `json:"can_proceed"`
	NextActions   []string                   `json:"next_actions,omitempty"`
	CheckedAt     time.Time                  `json:"checked_at"`
}

type ConflictPreview struct {
	DecisionID string                  `json:"decision_id"`
	Title      string                  `json:"title"`
	Summary    string                  `json:"summary"`
	Severity   domain.ConflictSeverity `json:"severity"`
	Confidence float64                 `json:"confidence"`
}

func (s *Service) Check(ctx context.Context, in CheckInput) (CheckOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return CheckOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	proposal := strings.TrimSpace(in.Proposal)
	if workspaceID == "" {
		return CheckOutput{}, errors.New("workspace_id is required")
	}
	if proposal == "" {
		return CheckOutput{}, errors.New("proposal is required")
	}
	if _, err := s.store.GetWorkspace(ctx, workspaceID); err != nil {
		return CheckOutput{}, err
	}
	decisions, err := s.candidateDecisions(ctx, workspaceID, proposal, in.Limit)
	if err != nil {
		return CheckOutput{}, err
	}
	out := CheckOutput{CanProceed: true, CheckedAt: s.clock.Now()}
	for _, decision := range decisions {
		relation := s.classifyProposal(ctx, workspaceID, proposal, decision)
		out.Relationships = append(out.Relationships, relation)
		if relation.Type == domain.RelationshipConflicts {
			out.CanProceed = false
			out.Conflicts = append(out.Conflicts, ConflictPreview{
				DecisionID: decision.ID,
				Title:      decision.Title,
				Summary:    relation.Reason,
				Severity:   severityForConfidence(relation.Confidence),
				Confidence: relation.Confidence,
			})
		}
	}
	if !out.CanProceed {
		out.NextActions = append(out.NextActions, "Resolve or reconcile conflicting active decisions before proceeding.")
	}
	if len(out.Relationships) == 0 {
		out.NextActions = append(out.NextActions, "No active decision candidates were found; use tuskbase_lookup for broader repo context if the task is not purely mechanical.")
	}
	return out, nil
}

type AssessInput struct {
	WorkspaceID string         `json:"workspace_id"`
	DecisionID  string         `json:"decision_id"`
	Actor       domain.Actor   `json:"actor,omitempty"`
	Signal      string         `json:"signal"`
	Score       int            `json:"score,omitempty"`
	Summary     string         `json:"summary,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type AssessOutput struct {
	Assessment domain.Assessment `json:"assessment"`
	Decision   domain.Decision   `json:"decision"`
}

func (s *Service) Assess(ctx context.Context, in AssessInput) (AssessOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return AssessOutput{}, err
	}
	actor, err := ApplyPrincipalActor(ctx, in.Actor)
	if err != nil {
		return AssessOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	decisionID := strings.TrimSpace(in.DecisionID)
	if workspaceID == "" {
		return AssessOutput{}, errors.New("workspace_id is required")
	}
	if decisionID == "" {
		return AssessOutput{}, errors.New("decision_id is required")
	}
	decision, err := s.store.GetDecision(ctx, decisionID)
	if err != nil {
		return AssessOutput{}, err
	}
	if decision.WorkspaceID != workspaceID {
		return AssessOutput{}, errors.New("decision does not belong to workspace")
	}
	assessment := domain.Assessment{
		ID:          s.ids.NewID(),
		WorkspaceID: workspaceID,
		DecisionID:  decisionID,
		Actor:       actor,
		Signal:      strings.TrimSpace(in.Signal),
		Score:       in.Score,
		Summary:     strings.TrimSpace(in.Summary),
		Metadata:    in.Metadata,
		CreatedAt:   s.clock.Now(),
	}
	if err := assessment.Validate(); err != nil {
		return AssessOutput{}, err
	}
	if err := s.store.SaveAssessment(ctx, assessment); err != nil {
		return AssessOutput{}, err
	}
	return AssessOutput{Assessment: assessment, Decision: decision}, nil
}

type QueryInput struct {
	WorkspaceID      string  `json:"workspace_id"`
	Text             string  `json:"text,omitempty"`
	Type             string  `json:"type,omitempty"`
	Status           string  `json:"status,omitempty"`
	RelationshipTo   string  `json:"relationship_to,omitempty"`
	RelationshipType string  `json:"relationship_type,omitempty"`
	MinConfidence    float64 `json:"min_confidence,omitempty"`
	Limit            int     `json:"limit,omitempty"`
}

type QueryOutput struct {
	Decisions []domain.Decision `json:"decisions"`
	Count     int               `json:"count"`
}

func (s *Service) Query(ctx context.Context, in QueryInput) (QueryOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return QueryOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return QueryOutput{}, errors.New("workspace_id is required")
	}
	status, err := domain.ParseDecisionStatus(in.Status)
	if err != nil {
		return QueryOutput{}, err
	}
	if strings.TrimSpace(in.Status) == "" {
		status = ""
	}
	var relType domain.RelationshipType
	if strings.TrimSpace(in.RelationshipType) != "" {
		relType, err = domain.ParseRelationshipType(in.RelationshipType)
		if err != nil {
			return QueryOutput{}, err
		}
	}
	decisions, err := s.store.QueryDecisions(ctx, ports.DecisionQuery{
		WorkspaceID:      workspaceID,
		Text:             strings.TrimSpace(in.Text),
		Type:             strings.TrimSpace(in.Type),
		Status:           status,
		RelationshipTo:   strings.TrimSpace(in.RelationshipTo),
		RelationshipType: relType,
		MinConfidence:    in.MinConfidence,
		Limit:            boundedLimit(in.Limit, defaultLimit),
	})
	if err != nil {
		return QueryOutput{}, err
	}
	return QueryOutput{Decisions: decisions, Count: len(decisions)}, nil
}

type ResolveConflictInput struct {
	WorkspaceID string       `json:"workspace_id"`
	ConflictID  string       `json:"conflict_id"`
	Actor       domain.Actor `json:"actor,omitempty"`
	Action      string       `json:"action"`
	Status      string       `json:"status,omitempty"`
	Summary     string       `json:"summary"`
	DecisionID  string       `json:"decision_id,omitempty"`
}

type ResolveConflictOutput struct {
	Conflict   domain.Conflict           `json:"conflict"`
	Resolution domain.ConflictResolution `json:"resolution"`
}

func (s *Service) ResolveConflict(ctx context.Context, in ResolveConflictInput) (ResolveConflictOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return ResolveConflictOutput{}, err
	}
	actor, err := ApplyPrincipalActor(ctx, in.Actor)
	if err != nil {
		return ResolveConflictOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	conflictID := strings.TrimSpace(in.ConflictID)
	if workspaceID == "" {
		return ResolveConflictOutput{}, errors.New("workspace_id is required")
	}
	if conflictID == "" {
		return ResolveConflictOutput{}, errors.New("conflict_id is required")
	}
	conflict, err := s.store.GetConflict(ctx, conflictID)
	if err != nil {
		return ResolveConflictOutput{}, err
	}
	if conflict.WorkspaceID != workspaceID {
		return ResolveConflictOutput{}, errors.New("conflict does not belong to workspace")
	}
	status, action, err := conflictResolutionStatus(in.Status, in.Action)
	if err != nil {
		return ResolveConflictOutput{}, err
	}
	resolution := domain.ConflictResolution{
		ID:          s.ids.NewID(),
		WorkspaceID: workspaceID,
		ConflictID:  conflictID,
		Actor:       actor,
		Action:      action,
		Summary:     strings.TrimSpace(in.Summary),
		DecisionID:  strings.TrimSpace(in.DecisionID),
		CreatedAt:   s.clock.Now(),
	}
	if resolution.Summary == "" {
		resolution.Summary = fmt.Sprintf("Conflict marked %s.", status)
	}
	resolved, err := s.store.ResolveConflict(ctx, conflictID, status, resolution.CreatedAt, resolution)
	if err != nil {
		return ResolveConflictOutput{}, err
	}
	return ResolveConflictOutput{Conflict: resolved, Resolution: resolution}, nil
}

type ReconcileInput struct {
	WorkspaceID  string               `json:"workspace_id"`
	Actor        domain.Actor         `json:"actor,omitempty"`
	ConflictIDs  []string             `json:"conflict_ids"`
	Type         string               `json:"type,omitempty"`
	Title        string               `json:"title"`
	Outcome      string               `json:"outcome"`
	Rationale    string               `json:"rationale,omitempty"`
	Confidence   float64              `json:"confidence,omitempty"`
	Alternatives []domain.Alternative `json:"alternatives,omitempty"`
	Claims       []domain.Claim       `json:"claims,omitempty"`
	Evidence     []domain.Evidence    `json:"evidence,omitempty"`
	Metadata     map[string]any       `json:"metadata,omitempty"`
}

type ReconcileOutput struct {
	Decision        domain.Decision   `json:"decision"`
	Conflicts       []domain.Conflict `json:"conflicts"`
	IndexingStatus  string            `json:"indexing_status"`
	IndexingError   string            `json:"indexing_error,omitempty"`
	QualityWarnings []string          `json:"quality_warnings,omitempty"`
}

func (s *Service) Reconcile(ctx context.Context, in ReconcileInput) (ReconcileOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return ReconcileOutput{}, err
	}
	if _, err := ApplyPrincipalActor(ctx, in.Actor); err != nil {
		return ReconcileOutput{}, err
	}
	if len(in.ConflictIDs) == 0 {
		return ReconcileOutput{}, errors.New("conflict_ids is required")
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return ReconcileOutput{}, errors.New("workspace_id is required")
	}
	conflicts := make([]domain.Conflict, 0, len(in.ConflictIDs))
	relationships := make([]domain.DecisionRelationship, 0, len(in.ConflictIDs)*2)
	seenRel := map[string]struct{}{}
	for _, id := range in.ConflictIDs {
		conflict, err := s.store.GetConflict(ctx, strings.TrimSpace(id))
		if err != nil {
			return ReconcileOutput{}, err
		}
		if conflict.WorkspaceID != workspaceID {
			return ReconcileOutput{}, errors.New("conflict does not belong to workspace")
		}
		conflicts = append(conflicts, conflict)
		for _, target := range []string{conflict.ConflictingWithID, conflict.DecisionID} {
			target = strings.TrimSpace(target)
			if target == "" {
				continue
			}
			key := target + "\x00" + string(domain.RelationshipReconciles)
			if _, ok := seenRel[key]; ok {
				continue
			}
			seenRel[key] = struct{}{}
			relationships = append(relationships, domain.DecisionRelationship{ToDecisionID: target, Type: domain.RelationshipReconciles, Confidence: 0.9, Reason: "Reconciles an open conflict in the decision trail."})
		}
	}
	decisionType := strings.TrimSpace(in.Type)
	if decisionType == "" {
		decisionType = "reconciliation"
	}
	remembered, err := s.Remember(ctx, RememberInput{
		WorkspaceID:   workspaceID,
		Actor:         in.Actor,
		Type:          decisionType,
		Title:         in.Title,
		Outcome:       in.Outcome,
		Rationale:     in.Rationale,
		Confidence:    in.Confidence,
		Alternatives:  in.Alternatives,
		Claims:        in.Claims,
		Evidence:      in.Evidence,
		Relationships: relationships,
		Metadata:      in.Metadata,
	})
	if err != nil {
		return ReconcileOutput{}, err
	}
	resolved := make([]domain.Conflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		out, err := s.ResolveConflict(ctx, ResolveConflictInput{
			WorkspaceID: workspaceID,
			ConflictID:  conflict.ID,
			Actor:       in.Actor,
			Action:      "reconciled",
			Summary:     fmt.Sprintf("Reconciled by decision %s.", remembered.Decision.ID),
			DecisionID:  remembered.Decision.ID,
		})
		if err != nil {
			return ReconcileOutput{}, err
		}
		resolved = append(resolved, out.Conflict)
	}
	return ReconcileOutput{Decision: remembered.Decision, Conflicts: resolved, IndexingStatus: remembered.IndexingStatus, IndexingError: remembered.IndexingError, QualityWarnings: remembered.QualityWarnings}, nil
}

type StatsInput struct {
	WorkspaceID string `json:"workspace_id"`
}

type StatsOutput struct {
	WorkspaceID      string          `json:"workspace_id"`
	DecisionStats    DecisionStats   `json:"decision_stats"`
	ConflictStats    ConflictStats   `json:"conflict_stats"`
	AssessmentStats  AssessmentStats `json:"assessment_stats"`
	TrailHealthScore float64         `json:"trail_health_score"`
	GeneratedAt      time.Time       `json:"generated_at"`
}

type DecisionStats struct {
	Total               int     `json:"total"`
	Active              int     `json:"active"`
	Superseded          int     `json:"superseded"`
	Deprecated          int     `json:"deprecated"`
	LowCompleteness     int     `json:"low_completeness"`
	AverageCompleteness float64 `json:"average_completeness"`
}

type ConflictStats struct {
	Total     int `json:"total"`
	Open      int `json:"open"`
	Resolved  int `json:"resolved"`
	Dismissed int `json:"dismissed"`
	Deferred  int `json:"deferred"`
}

type AssessmentStats struct {
	Total        int     `json:"total"`
	AverageScore float64 `json:"average_score,omitempty"`
}

func (s *Service) Stats(ctx context.Context, in StatsInput) (StatsOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return StatsOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return StatsOutput{}, errors.New("workspace_id is required")
	}
	if _, err := s.store.GetWorkspace(ctx, workspaceID); err != nil {
		return StatsOutput{}, err
	}
	decisions, err := s.store.QueryDecisions(ctx, ports.DecisionQuery{WorkspaceID: workspaceID, Limit: statsScanLimit})
	if err != nil {
		return StatsOutput{}, err
	}
	conflicts, err := s.store.ListConflicts(ctx, ports.ConflictQuery{WorkspaceID: workspaceID, Limit: statsScanLimit})
	if err != nil {
		return StatsOutput{}, err
	}
	assessments, err := s.store.ListAssessments(ctx, ports.AssessmentQuery{WorkspaceID: workspaceID, Limit: statsScanLimit})
	if err != nil {
		return StatsOutput{}, err
	}
	out := StatsOutput{WorkspaceID: workspaceID, GeneratedAt: s.clock.Now()}
	var completenessTotal float64
	for _, decision := range decisions {
		out.DecisionStats.Total++
		completenessTotal += decision.CompletenessScore
		if decision.CompletenessScore > 0 && decision.CompletenessScore < 0.55 {
			out.DecisionStats.LowCompleteness++
		}
		switch decision.Status {
		case domain.DecisionSuperseded:
			out.DecisionStats.Superseded++
		case domain.DecisionDeprecated:
			out.DecisionStats.Deprecated++
		default:
			if decision.IsActive() {
				out.DecisionStats.Active++
			}
		}
	}
	if out.DecisionStats.Total > 0 {
		out.DecisionStats.AverageCompleteness = round2(completenessTotal / float64(out.DecisionStats.Total))
	}
	for _, conflict := range conflicts {
		out.ConflictStats.Total++
		switch conflict.Status {
		case domain.ConflictResolved:
			out.ConflictStats.Resolved++
		case domain.ConflictDismissed:
			out.ConflictStats.Dismissed++
		case domain.ConflictDeferred:
			out.ConflictStats.Deferred++
		default:
			out.ConflictStats.Open++
		}
	}
	var scoreTotal, scored int
	for _, assessment := range assessments {
		out.AssessmentStats.Total++
		if assessment.Score > 0 {
			scoreTotal += assessment.Score
			scored++
		}
	}
	if scored > 0 {
		out.AssessmentStats.AverageScore = round2(float64(scoreTotal) / float64(scored))
	}
	out.TrailHealthScore = trailHealth(out)
	return out, nil
}

func (s *Service) candidateDecisions(ctx context.Context, workspaceID, text string, limit int) ([]domain.Decision, error) {
	limit = boundedLimit(limit, defaultLimit)
	results, err := s.search.Search(ctx, ports.SearchQuery{WorkspaceID: workspaceID, Query: text, Limit: limit})
	if err != nil {
		return nil, err
	}
	return s.decisionsForResults(ctx, workspaceID, results, limit)
}

func (s *Service) decisionsForResults(ctx context.Context, workspaceID string, results []ports.SearchResult, limit int) ([]domain.Decision, error) {
	limit = boundedLimit(limit, defaultLimit)
	seen := map[string]struct{}{}
	decisions := make([]domain.Decision, 0, limit)
	for _, result := range results {
		if !isDecisionBoundResult(result.Kind) {
			continue
		}
		if _, ok := seen[result.EntityID]; ok {
			continue
		}
		seen[result.EntityID] = struct{}{}
		decision, err := s.store.GetDecision(ctx, result.EntityID)
		if err != nil {
			return nil, err
		}
		if decision.WorkspaceID == workspaceID && decision.IsActive() {
			decisions = append(decisions, decision)
		}
		if len(decisions) >= limit {
			return decisions, nil
		}
	}
	if len(decisions) < limit {
		recent, err := s.store.RecentDecisions(ctx, workspaceID, limit)
		if err != nil {
			return nil, err
		}
		for _, decision := range recent {
			if _, ok := seen[decision.ID]; ok {
				continue
			}
			seen[decision.ID] = struct{}{}
			if decision.IsActive() {
				decisions = append(decisions, decision)
			}
			if len(decisions) >= limit {
				break
			}
		}
	}
	return decisions, nil
}

func conflictResolutionStatus(statusInput, actionInput string) (domain.ConflictStatus, string, error) {
	action := strings.ToLower(strings.TrimSpace(actionInput))
	if action == "" {
		action = "resolved"
	}
	if strings.TrimSpace(statusInput) != "" {
		status, err := domain.ParseConflictStatus(statusInput)
		return status, action, err
	}
	switch action {
	case "resolved", "accepted", "reconciled":
		return domain.ConflictResolved, action, nil
	case "dismissed", "false_positive":
		return domain.ConflictDismissed, action, nil
	case "deferred":
		return domain.ConflictDeferred, action, nil
	default:
		return "", "", fmt.Errorf("invalid conflict resolution action %q", actionInput)
	}
}

func boundedLimit(limit, fallback int) int {
	if limit <= 0 {
		limit = fallback
	}
	if limit > statsScanLimit {
		limit = statsScanLimit
	}
	return limit
}

func normalizeChangedFiles(files []ChangedFileSummary) []ChangedFileSummary {
	out := make([]ChangedFileSummary, 0, len(files))
	for _, file := range files {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		out = append(out, ChangedFileSummary{Path: path, Summary: strings.TrimSpace(file.Summary)})
	}
	return out
}

func normalizeTestSummaries(tests []TestSummary) []TestSummary {
	out := make([]TestSummary, 0, len(tests))
	for _, test := range tests {
		command := strings.TrimSpace(test.Command)
		if command == "" {
			continue
		}
		out = append(out, TestSummary{
			Command: command,
			Status:  strings.TrimSpace(test.Status),
			Output:  strings.TrimSpace(test.Output),
		})
	}
	return out
}

func trailHealth(out StatsOutput) float64 {
	var decisionScore float64 = 1
	if out.DecisionStats.Total > 0 {
		decisionScore = out.DecisionStats.AverageCompleteness
	}
	conflictScore := 1.0
	if out.ConflictStats.Total > 0 {
		conflictScore = 1 - float64(out.ConflictStats.Open)/float64(out.ConflictStats.Total)
	}
	assessmentScore := 0.5
	if out.AssessmentStats.Total > 0 {
		assessmentScore = 1
	}
	return round2(clamp(decisionScore*0.5 + conflictScore*0.35 + assessmentScore*0.15))
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
