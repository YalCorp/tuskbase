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
	"unicode"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

const defaultLimit = 10

type Store interface {
	ports.WorkspaceStore
	ports.DecisionStore
	ports.DocumentStore
	ports.ReceiptStore
	ports.ConflictStore
}

type Service struct {
	store  Store
	search ports.SearchIndex
	ids    ports.IDGenerator
	clock  ports.Clock
}

func NewService(store Store, search ports.SearchIndex, ids ports.IDGenerator, clock ports.Clock) *Service {
	if ids == nil {
		ids = UUIDGenerator{}
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &Service{store: store, search: search, ids: ids, clock: clock}
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
			Status:            "open",
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
	DecisionID string                  `json:"decision_id"`
	Type       domain.RelationshipType `json:"type"`
	Confidence float64                 `json:"confidence"`
	Reason     string                  `json:"reason,omitempty"`
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
	seen := map[string]struct{}{}
	for _, result := range lookup.Results {
		if !isDecisionBoundResult(result.Kind) {
			continue
		}
		if _, ok := seen[result.EntityID]; ok {
			continue
		}
		seen[result.EntityID] = struct{}{}
		decision, err := s.store.GetDecision(ctx, result.EntityID)
		if err != nil {
			return PreflightOutput{}, err
		}
		if !decision.IsActive() {
			continue
		}
		relation := classifyProposal(in.Proposal, decision)
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
				Status:            "open",
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

func classifyProposal(proposal string, decision domain.Decision) DecisionProposalRelation {
	proposalText := strings.ToLower(proposal)
	decisionText := strings.ToLower(strings.Join([]string{decision.Title, decision.Outcome, decision.Rationale, claimsText(decision.Claims)}, " "))
	shared := sharedImportantTokens(proposalText, decisionText)
	proposalPolarity := polarityByToken(proposalText)
	decisionPolarity := polarityByToken(decisionText)
	for token := range shared {
		pp, pok := proposalPolarity[token]
		dp, dok := decisionPolarity[token]
		if pok && dok && pp != dp {
			return DecisionProposalRelation{
				DecisionID: decision.ID,
				Type:       domain.RelationshipConflicts,
				Confidence: 0.9,
				Reason:     fmt.Sprintf("Proposal direction for %q conflicts with active decision %q.", token, decision.Title),
			}
		}
	}
	overlap := float64(len(shared)) / float64(max(1, len(importantTokens(proposalText))))
	switch {
	case overlap >= 0.7:
		return DecisionProposalRelation{DecisionID: decision.ID, Type: domain.RelationshipDuplicates, Confidence: clamp(overlap), Reason: "Proposal substantially overlaps an active decision."}
	case overlap >= 0.35:
		return DecisionProposalRelation{DecisionID: decision.ID, Type: domain.RelationshipExtends, Confidence: clamp(overlap), Reason: "Proposal shares key terms with an active decision."}
	default:
		return DecisionProposalRelation{DecisionID: decision.ID, Type: domain.RelationshipFollows, Confidence: 0.35, Reason: "No deterministic contradiction found."}
	}
}

func claimsText(claims []domain.Claim) string {
	parts := make([]string, 0, len(claims))
	for _, claim := range claims {
		parts = append(parts, claim.Text)
	}
	return strings.Join(parts, " ")
}

func sharedImportantTokens(a, b string) map[string]struct{} {
	ta := importantTokens(a)
	tb := importantTokens(b)
	out := map[string]struct{}{}
	for token := range ta {
		if _, ok := tb[token]; ok {
			out[token] = struct{}{}
		}
	}
	return out
}

func importantTokens(text string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, token := range wordRE.FindAllString(strings.ToLower(text), -1) {
		token = strings.TrimFunc(token, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		if len(token) < 4 || stopWords[token] {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

var stopWords = map[string]bool{
	"about": true, "active": true, "after": true, "before": true, "decision": true,
	"first": true, "from": true, "have": true, "into": true, "local": true,
	"must": true, "need": true, "password": true, "reset": true, "should": true,
	"storage": true, "store": true, "that": true, "their": true, "this": true,
	"token": true, "tokens": true, "with": true, "without": false,
}

func polarityByToken(text string) map[string]int {
	tokens := wordRE.FindAllString(strings.ToLower(text), -1)
	out := map[string]int{}
	for i, token := range tokens {
		if len(token) < 4 || stopWords[token] {
			continue
		}
		start := max(0, i-4)
		window := strings.Join(tokens[start:i+1], " ")
		neg := strings.Contains(window, "avoid") ||
			strings.Contains(window, "do not") ||
			strings.Contains(window, "don't") ||
			strings.Contains(window, "never") ||
			strings.Contains(window, "without") ||
			strings.Contains(window, "remove") ||
			strings.Contains(window, "replace") ||
			strings.Contains(window, "reject")
		if neg {
			out[token] = -1
			continue
		}
		pos := strings.Contains(window, "use") ||
			strings.Contains(window, "adopt") ||
			strings.Contains(window, "keep") ||
			strings.Contains(window, "store") ||
			strings.Contains(window, "require") ||
			strings.Contains(window, "choose")
		if pos {
			out[token] = 1
		}
	}
	return out
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
