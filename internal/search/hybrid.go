package search

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/domain"
	"github.com/priyavratuniyal/tuskbase/internal/ports"
)

// HybridIndex combines deterministic text lookup with optional semantic lookup. If vectors fail, text remains the product baseline.
type HybridIndex struct {
	Text       ports.SearchIndex
	Vectors    ports.VectorStore
	Embeddings ports.EmbeddingProvider
	Logger     *slog.Logger
}

// NewHybridIndex returns the plain text index until both vector storage and embeddings are configured.
func NewHybridIndex(text ports.SearchIndex, vectors ports.VectorStore, embeddings ports.EmbeddingProvider, logger *slog.Logger) ports.SearchIndex {
	if embeddings == nil || vectors == nil {
		return text
	}
	return &HybridIndex{Text: text, Vectors: vectors, Embeddings: embeddings, Logger: logger}
}

func (h *HybridIndex) IndexDecision(ctx context.Context, d domain.Decision) error {
	if err := h.Text.IndexDecision(ctx, d); err != nil {
		return err
	}
	if !d.IsActive() {
		return nil
	}
	for _, record := range decisionRecords(d) {
		h.indexVector(ctx, record)
	}
	return nil
}

func (h *HybridIndex) IndexDocument(ctx context.Context, doc domain.RepoDocument) error {
	if err := h.Text.IndexDocument(ctx, doc); err != nil {
		return err
	}
	h.indexVector(ctx, ports.VectorRecord{WorkspaceID: doc.WorkspaceID, Kind: "document", EntityID: doc.ID, Title: doc.Title, Path: doc.Path, Text: doc.Content})
	return nil
}

// Search prefers semantic matches when available, then fills gaps with text matches so Local Basic has a graceful fallback.
func (h *HybridIndex) Search(ctx context.Context, q ports.SearchQuery) ([]ports.SearchResult, error) {
	textResults, err := h.Text.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	if h.Embeddings == nil || h.Vectors == nil || strings.TrimSpace(q.Query) == "" {
		return textResults, nil
	}
	vector, err := h.Embeddings.Embed(ctx, q.Query)
	if err != nil {
		h.log("embedding query failed", err)
		return textResults, nil
	}
	vectorResults, err := h.Vectors.SearchVector(ctx, ports.VectorQuery{WorkspaceID: q.WorkspaceID, Vector: vector, Limit: q.Limit})
	if err != nil {
		h.log("vector search failed", err)
		return textResults, nil
	}
	return merge(textResults, vectorResults, q.Limit), nil
}

func (h *HybridIndex) indexVector(ctx context.Context, record ports.VectorRecord) {
	if strings.TrimSpace(record.Text) == "" || h.Embeddings == nil || h.Vectors == nil {
		return
	}
	vector, err := h.Embeddings.Embed(ctx, record.Text)
	if err != nil {
		h.log("embedding index failed", err)
		return
	}
	record.Vector = vector
	if err := h.Vectors.UpsertVector(ctx, record); err != nil {
		h.log("vector upsert failed", err)
	}
}

func (h *HybridIndex) log(msg string, err error) {
	if h.Logger != nil && err != nil && !errors.Is(err, context.Canceled) {
		h.Logger.Debug(msg, "error", err)
	}
}

// decisionRecords indexes the decision plus supporting claims/evidence/alternatives because future agents search for reasons, not only titles.
func decisionRecords(d domain.Decision) []ports.VectorRecord {
	base := ports.VectorRecord{
		WorkspaceID: d.WorkspaceID,
		Kind:        "decision",
		EntityID:    d.ID,
		Title:       d.Title,
		Text:        strings.Join([]string{d.Type, d.Title, d.Outcome, d.Rationale, d.PrecedentRef, d.SupersedesID}, "\n"),
	}
	records := []ports.VectorRecord{base}
	for _, claim := range d.Claims {
		if strings.TrimSpace(claim.Text) != "" {
			records = append(records, ports.VectorRecord{WorkspaceID: d.WorkspaceID, Kind: "claim", EntityID: d.ID, Title: d.Title, Text: claim.Text})
		}
	}
	for _, evidence := range d.Evidence {
		text := strings.TrimSpace(strings.Join([]string{evidence.Kind, evidence.URI, evidence.Title, evidence.Snippet}, "\n"))
		if text != "" {
			records = append(records, ports.VectorRecord{WorkspaceID: d.WorkspaceID, Kind: "evidence", EntityID: d.ID, Title: d.Title, Path: evidence.URI, Text: text})
		}
	}
	for _, alternative := range d.Alternatives {
		text := strings.TrimSpace(strings.Join([]string{alternative.Title, alternative.Outcome, alternative.Reason}, "\n"))
		if text != "" {
			records = append(records, ports.VectorRecord{WorkspaceID: d.WorkspaceID, Kind: "alternative", EntityID: d.ID, Title: d.Title, Text: text})
		}
	}
	return records
}

func merge(textResults, vectorResults []ports.SearchResult, limit int) []ports.SearchResult {
	if limit <= 0 {
		limit = 10
	}
	out := make([]ports.SearchResult, 0, limit)
	seen := map[string]struct{}{}
	add := func(r ports.SearchResult) {
		if len(out) >= limit {
			return
		}
		key := r.Kind + "\x00" + r.EntityID + "\x00" + r.Path
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	for _, r := range vectorResults {
		add(r)
	}
	for _, r := range textResults {
		add(r)
	}
	return out
}
