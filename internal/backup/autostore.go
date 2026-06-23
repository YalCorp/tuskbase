package backup

import (
	"context"
	"log/slog"
	"time"

	"github.com/priyavratuniyal/tuskbase/internal/app"
	"github.com/priyavratuniyal/tuskbase/internal/domain"
)

type AutoStore struct {
	app.Store
	manager *Manager
	logger  *slog.Logger
}

func WrapStore(store app.Store, manager *Manager, logger *slog.Logger) app.Store {
	if store == nil || manager == nil {
		return store
	}
	return AutoStore{Store: store, manager: manager, logger: logger}
}

func (s AutoStore) SaveDecision(ctx context.Context, decision domain.Decision) error {
	if err := s.Store.SaveDecision(ctx, decision); err != nil {
		return err
	}
	s.trigger(ctx, "decision")
	return nil
}

func (s AutoStore) SaveAssessment(ctx context.Context, assessment domain.Assessment) error {
	if err := s.Store.SaveAssessment(ctx, assessment); err != nil {
		return err
	}
	s.trigger(ctx, "assessment")
	return nil
}

func (s AutoStore) SaveConflict(ctx context.Context, conflict domain.Conflict) error {
	if err := s.Store.SaveConflict(ctx, conflict); err != nil {
		return err
	}
	s.trigger(ctx, "conflict")
	return nil
}

func (s AutoStore) ResolveConflict(ctx context.Context, conflictID string, status domain.ConflictStatus, resolvedAt time.Time, resolution domain.ConflictResolution) (domain.Conflict, error) {
	conflict, err := s.Store.ResolveConflict(ctx, conflictID, status, resolvedAt, resolution)
	if err != nil {
		return domain.Conflict{}, err
	}
	s.trigger(ctx, "conflict_resolution")
	return conflict, nil
}

func (s AutoStore) trigger(ctx context.Context, reason string) {
	if _, _, err := s.manager.CreateAuto(ctx); err != nil && s.logger != nil {
		s.logger.Warn("automatic backup failed", "reason", reason, "error", err)
	}
}
