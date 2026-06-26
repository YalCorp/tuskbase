package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

const (
	defaultImportLimit = 50
	importScanDocLimit = 2000
	ruleDetector       = "rule:v1"
	adrDetector        = "adr:v1"
)

type ImportScanInput struct {
	RepoPath    string `json:"repo_path"`
	DisplayName string `json:"display_name,omitempty"`
}

type ImportScanOutput struct {
	Workspace  domain.Workspace           `json:"workspace"`
	Candidates []domain.DecisionCandidate `json:"candidates"`
	Scanned    int                        `json:"scanned"`
	Created    int                        `json:"created"`
	Updated    int                        `json:"updated"`
	ByStatus   map[string]int             `json:"by_status"`
}

type ImportListInput struct {
	WorkspaceID string `json:"workspace_id"`
	Status      string `json:"status,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type ImportListOutput struct {
	Candidates []domain.DecisionCandidate `json:"candidates"`
	Count      int                        `json:"count"`
}

type ImportShowOutput struct {
	Candidate domain.DecisionCandidate `json:"candidate"`
}

type ImportAcceptOutput struct {
	Candidate       domain.DecisionCandidate `json:"candidate"`
	Decision        domain.Decision          `json:"decision"`
	IndexingStatus  string                   `json:"indexing_status"`
	IndexingError   string                   `json:"indexing_error,omitempty"`
	QualityWarnings []string                 `json:"quality_warnings,omitempty"`
}

type ImportRejectInput struct {
	CandidateID string `json:"candidate_id"`
	Summary     string `json:"summary,omitempty"`
}

type ImportRejectOutput struct {
	Candidate domain.DecisionCandidate `json:"candidate"`
}

type ImportDocument struct {
	WorkspaceID string
	Path        string
	Title       string
	Content     string
	ChunkIndex  int
}

type ExtractedCandidate struct {
	Type          string
	Title         string
	Outcome       string
	Rationale     string
	Confidence    float64
	SourceSnippet string
	Detector      string
}

type DecisionCandidateExtractor interface {
	ExtractCandidates(context.Context, ImportDocument) ([]ExtractedCandidate, error)
}

// TODO(importer): add an optional Ollama-backed extractor adapter after the
// offline review workflow is stable. V1 stays deterministic and conservative.
type RuleBasedExtractor struct{}

func (s *Service) ImportScan(ctx context.Context, in ImportScanInput) (ImportScanOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return ImportScanOutput{}, err
	}
	attached, err := s.Attach(ctx, AttachInput{RepoPath: in.RepoPath, DisplayName: in.DisplayName})
	if err != nil {
		return ImportScanOutput{}, err
	}
	docs, err := s.store.ListWorkspaceDocuments(ctx, attached.Workspace.ID, importScanDocLimit)
	if err != nil {
		return ImportScanOutput{}, err
	}
	extractor := RuleBasedExtractor{}
	now := s.clock.Now()
	out := ImportScanOutput{Workspace: attached.Workspace, ByStatus: map[string]int{}}
	for _, doc := range docs {
		if !isImportSourcePath(doc.Path) {
			continue
		}
		out.Scanned++
		extracted, err := extractor.ExtractCandidates(ctx, ImportDocument{
			WorkspaceID: doc.WorkspaceID,
			Path:        doc.Path,
			Title:       doc.Title,
			Content:     doc.Content,
			ChunkIndex:  doc.ChunkIndex,
		})
		if err != nil {
			return ImportScanOutput{}, err
		}
		for _, candidate := range extracted {
			sourceHash := candidateSourceHash(doc.WorkspaceID, doc.Path, doc.ChunkIndex, candidate.SourceSnippet)
			id := "cand_" + sourceHash[:24]
			pending := domain.DecisionCandidate{
				ID:            id,
				WorkspaceID:   doc.WorkspaceID,
				Type:          cleanDefault(candidate.Type, "architecture"),
				Title:         cleanDefault(candidate.Title, "Imported repo decision"),
				Outcome:       strings.TrimSpace(candidate.Outcome),
				Rationale:     strings.TrimSpace(candidate.Rationale),
				Confidence:    candidate.Confidence,
				SourcePath:    doc.Path,
				SourceTitle:   doc.Title,
				SourceSnippet: strings.TrimSpace(candidate.SourceSnippet),
				SourceHash:    sourceHash,
				Detector:      cleanDefault(candidate.Detector, ruleDetector),
				Status:        domain.CandidatePending,
				CreatedAt:     now,
				UpdatedAt:     now,
			}
			if pending.Confidence == 0 {
				pending.Confidence = 0.65
			}
			saved, err := s.store.UpsertDecisionCandidate(ctx, pending)
			if err != nil {
				return ImportScanOutput{}, err
			}
			if saved.ID == pending.ID && saved.CreatedAt.Equal(pending.CreatedAt) {
				out.Created++
			} else {
				out.Updated++
			}
			out.ByStatus[string(saved.Status)]++
			out.Candidates = append(out.Candidates, saved)
		}
	}
	sort.SliceStable(out.Candidates, func(i, j int) bool {
		if out.Candidates[i].SourcePath == out.Candidates[j].SourcePath {
			return out.Candidates[i].SourceSnippet < out.Candidates[j].SourceSnippet
		}
		return out.Candidates[i].SourcePath < out.Candidates[j].SourcePath
	})
	return out, nil
}

func (s *Service) ImportList(ctx context.Context, in ImportListInput) (ImportListOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return ImportListOutput{}, err
	}
	workspaceID := strings.TrimSpace(in.WorkspaceID)
	if workspaceID == "" {
		return ImportListOutput{}, errors.New("workspace_id is required")
	}
	if _, err := s.store.GetWorkspace(ctx, workspaceID); err != nil {
		return ImportListOutput{}, err
	}
	status, all, err := importListStatus(in.Status)
	if err != nil {
		return ImportListOutput{}, err
	}
	candidates, err := s.store.ListDecisionCandidates(ctx, ports.DecisionCandidateQuery{
		WorkspaceID: workspaceID,
		Status:      status,
		AllStatuses: all,
		Limit:       boundedLimit(in.Limit, defaultImportLimit),
	})
	if err != nil {
		return ImportListOutput{}, err
	}
	return ImportListOutput{Candidates: candidates, Count: len(candidates)}, nil
}

func (s *Service) ImportShow(ctx context.Context, candidateID string) (ImportShowOutput, error) {
	if err := RequireRead(ctx); err != nil {
		return ImportShowOutput{}, err
	}
	candidate, err := s.store.GetDecisionCandidate(ctx, strings.TrimSpace(candidateID))
	if err != nil {
		return ImportShowOutput{}, err
	}
	return ImportShowOutput{Candidate: candidate}, nil
}

func (s *Service) ImportAccept(ctx context.Context, candidateID string) (ImportAcceptOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return ImportAcceptOutput{}, err
	}
	candidate, err := s.store.GetDecisionCandidate(ctx, strings.TrimSpace(candidateID))
	if err != nil {
		return ImportAcceptOutput{}, err
	}
	if candidate.Status == domain.CandidateAccepted {
		decision, err := s.store.GetDecision(ctx, candidate.AcceptedDecisionID)
		if err != nil {
			return ImportAcceptOutput{}, err
		}
		return ImportAcceptOutput{Candidate: candidate, Decision: decision, IndexingStatus: "already_accepted"}, nil
	}
	if candidate.Status != domain.CandidatePending {
		return ImportAcceptOutput{}, fmt.Errorf("candidate status is %q, want pending", candidate.Status)
	}
	remembered, err := s.Remember(ctx, RememberInput{
		WorkspaceID: candidate.WorkspaceID,
		Type:        candidate.Type,
		Title:       candidate.Title,
		Outcome:     candidate.Outcome,
		Rationale:   candidate.Rationale,
		Confidence:  candidate.Confidence,
		Evidence: []domain.Evidence{{
			Kind:    "document",
			URI:     candidate.SourcePath,
			Title:   candidate.SourceTitle,
			Snippet: candidate.SourceSnippet,
			Metadata: map[string]any{
				"candidate_id": candidate.ID,
				"source_hash":  candidate.SourceHash,
				"detector":     candidate.Detector,
			},
		}},
		Metadata: map[string]any{
			"import_candidate_id": candidate.ID,
			"import_source_hash":  candidate.SourceHash,
			"import_detector":     candidate.Detector,
		},
	})
	if err != nil {
		return ImportAcceptOutput{}, err
	}
	updated, err := s.store.UpdateDecisionCandidateStatus(ctx, ports.DecisionCandidateStatusUpdate{
		ID:                 candidate.ID,
		WorkspaceID:        candidate.WorkspaceID,
		Status:             domain.CandidateAccepted,
		AcceptedDecisionID: remembered.Decision.ID,
		UpdatedAt:          s.clock.Now(),
	})
	if err != nil {
		return ImportAcceptOutput{}, err
	}
	return ImportAcceptOutput{
		Candidate:       updated,
		Decision:        remembered.Decision,
		IndexingStatus:  remembered.IndexingStatus,
		IndexingError:   remembered.IndexingError,
		QualityWarnings: remembered.QualityWarnings,
	}, nil
}

func (s *Service) ImportReject(ctx context.Context, in ImportRejectInput) (ImportRejectOutput, error) {
	if err := RequireWrite(ctx); err != nil {
		return ImportRejectOutput{}, err
	}
	candidate, err := s.store.GetDecisionCandidate(ctx, strings.TrimSpace(in.CandidateID))
	if err != nil {
		return ImportRejectOutput{}, err
	}
	if candidate.Status == domain.CandidateAccepted {
		return ImportRejectOutput{}, errors.New("accepted candidates cannot be rejected")
	}
	updated, err := s.store.UpdateDecisionCandidateStatus(ctx, ports.DecisionCandidateStatusUpdate{
		ID:               candidate.ID,
		WorkspaceID:      candidate.WorkspaceID,
		Status:           domain.CandidateRejected,
		RejectionSummary: strings.TrimSpace(in.Summary),
		UpdatedAt:        s.clock.Now(),
	})
	if err != nil {
		return ImportRejectOutput{}, err
	}
	return ImportRejectOutput{Candidate: updated}, nil
}

func (RuleBasedExtractor) ExtractCandidates(ctx context.Context, doc ImportDocument) ([]ExtractedCandidate, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if strings.TrimSpace(doc.Content) == "" {
		return nil, nil
	}
	if isADRPath(doc.Path) || looksLikeADR(doc) {
		if candidate, ok := extractADRCandidate(doc); ok {
			return []ExtractedCandidate{candidate}, nil
		}
	}
	return extractRuleCandidates(doc), nil
}

func extractADRCandidate(doc ImportDocument) (ExtractedCandidate, bool) {
	title := strings.TrimSpace(doc.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(doc.Path), filepath.Ext(doc.Path))
	}
	status := adrStatus(doc.Content)
	if status != "" && !strings.Contains(strings.ToLower(status), "accept") && !strings.Contains(strings.ToLower(status), "approved") {
		return ExtractedCandidate{}, false
	}
	snippet := firstMeaningfulParagraph(doc.Content)
	if snippet == "" {
		return ExtractedCandidate{}, false
	}
	outcome := extractDecisionSentence(doc.Content)
	if outcome == "" {
		outcome = "Follow " + title + "."
	}
	return ExtractedCandidate{
		Type:          "architecture",
		Title:         title,
		Outcome:       outcome,
		Rationale:     "Imported from an ADR-like document. Review before relying on it as canonical memory.",
		Confidence:    0.72,
		SourceSnippet: snippet,
		Detector:      adrDetector,
	}, true
}

func extractRuleCandidates(doc ImportDocument) []ExtractedCandidate {
	var out []ExtractedCandidate
	seen := map[string]struct{}{}
	for _, sentence := range candidateSentences(doc.Content) {
		if !decisionLanguage(sentence) {
			continue
		}
		snippet := normalizeSnippet(sentence)
		if snippet == "" {
			continue
		}
		key := strings.ToLower(snippet)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ExtractedCandidate{
			Type:          importCandidateType(doc.Path, snippet),
			Title:         titleFromSnippet(snippet),
			Outcome:       ensureSentence(snippet),
			Rationale:     "Imported from explicit repo documentation or agent rules. Review before relying on it as canonical memory.",
			Confidence:    confidenceForSource(doc.Path),
			SourceSnippet: snippet,
			Detector:      ruleDetector,
		})
		if len(out) >= 25 {
			break
		}
	}
	return out
}

func importListStatus(value string) (domain.DecisionCandidateStatus, bool, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return domain.CandidatePending, false, nil
	}
	if value == "all" {
		return "", true, nil
	}
	status, err := domain.ParseDecisionCandidateStatus(value)
	return status, false, err
}

func candidateSourceHash(workspaceID, sourcePath string, chunkIndex int, snippet string) string {
	normalized := normalizeSnippet(snippet)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(workspaceID),
		filepath.ToSlash(filepath.Clean(sourcePath)),
		fmt.Sprintf("%d", chunkIndex),
		normalized,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

func isImportSourcePath(path string) bool {
	path = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
	lower := strings.ToLower(path)
	if lower == "readme.md" || lower == "agents.md" || lower == "claude.md" || lower == ".github/copilot-instructions.md" {
		return true
	}
	if strings.HasPrefix(lower, "docs/") && strings.HasSuffix(lower, ".md") {
		return true
	}
	if strings.HasPrefix(lower, "adrs/") && strings.HasSuffix(lower, ".md") {
		return true
	}
	if strings.HasPrefix(lower, ".cursor/rules") || strings.HasPrefix(lower, ".windsurf/rules") {
		return true
	}
	return strings.Count(lower, "/") == 0 && strings.HasSuffix(lower, ".md")
}

func isADRPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	return strings.HasPrefix(lower, "adrs/") || strings.Contains(lower, "/adr/")
}

func looksLikeADR(doc ImportDocument) bool {
	text := strings.ToLower(doc.Title + "\n" + doc.Content)
	return strings.Contains(text, "architecture decision record") || strings.Contains(text, "\nstatus:")
}

var adrStatusRE = regexp.MustCompile(`(?im)^\s*status\s*:\s*(.+)$`)

func adrStatus(content string) string {
	matches := adrStatusRE.FindStringSubmatch(content)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func extractDecisionSentence(content string) string {
	for _, sentence := range candidateSentences(content) {
		lower := strings.ToLower(sentence)
		if strings.Contains(lower, "we choose") || strings.Contains(lower, "we decided") || strings.Contains(lower, "decision:") {
			return ensureSentence(normalizeSnippet(sentence))
		}
	}
	return ""
}

func firstMeaningfulParagraph(content string) string {
	for _, part := range strings.Split(content, "\n\n") {
		part = normalizeSnippet(part)
		if part == "" || strings.HasPrefix(part, "#") {
			continue
		}
		return part
	}
	return ""
}

func candidateSentences(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimPrefix(line, "* ")
		line = strings.TrimSpace(regexp.MustCompile(`^\d+\.\s+`).ReplaceAllString(line, ""))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "|") {
			continue
		}
		for _, part := range splitSentences(line) {
			if len(strings.Fields(part)) >= 3 {
				out = append(out, part)
			}
		}
	}
	return out
}

func splitSentences(line string) []string {
	line = strings.TrimSpace(line)
	if len(line) <= 220 {
		return []string{line}
	}
	parts := regexp.MustCompile(`(?i)(?:\.\s+|\;\s+)`).Split(line, -1)
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func decisionLanguage(sentence string) bool {
	lower := strings.ToLower(sentence)
	needles := []string{
		"we use ", "we choose ", "we chose ", "we require ", "we avoid ", "we do not ",
		"must use ", "must not ", "do not ", "avoid ", "required to ", "prefer ",
	}
	for _, needle := range needles {
		if strings.Contains(lower, needle) || strings.HasPrefix(lower, strings.TrimSpace(needle)) {
			return true
		}
	}
	return false
}

func normalizeSnippet(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	value = strings.Trim(value, "`*_ ")
	if len(value) > 500 {
		value = strings.TrimSpace(value[:500])
	}
	return value
}

func titleFromSnippet(snippet string) string {
	snippet = strings.TrimSpace(snippet)
	snippet = strings.TrimRight(snippet, ".")
	if len(snippet) <= 80 {
		return snippet
	}
	return strings.TrimSpace(snippet[:80]) + "..."
}

func ensureSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.HasSuffix(value, ".") || strings.HasSuffix(value, "!") || strings.HasSuffix(value, "?") {
		return value
	}
	return value + "."
}

func importCandidateType(path, snippet string) string {
	lower := strings.ToLower(path + " " + snippet)
	switch {
	case strings.Contains(lower, "security") || strings.Contains(lower, "auth"):
		return "security"
	case strings.Contains(lower, "deploy") || strings.Contains(lower, "docker"):
		return "deployment"
	case strings.Contains(lower, "workflow") || strings.Contains(lower, "agent") || strings.Contains(lower, "rules"):
		return "workflow"
	default:
		return "architecture"
	}
}

func confidenceForSource(path string) float64 {
	lower := strings.ToLower(filepath.ToSlash(path))
	switch {
	case strings.HasPrefix(lower, ".cursor/rules") || strings.HasPrefix(lower, ".windsurf/rules") || lower == "agents.md" || lower == "claude.md":
		return 0.7
	case strings.HasPrefix(lower, "adrs/"):
		return 0.72
	default:
		return 0.64
	}
}

func cleanDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
