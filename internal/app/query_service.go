package app

import (
	"context"

	"desktop-knowledge-companion/internal/domain"
	"desktop-knowledge-companion/internal/store"
)

type QueryService struct {
	store *store.Store
}

func NewQueryService(storage *store.Store) *QueryService {
	return &QueryService{store: storage}
}

func (service *QueryService) Ask(ctx context.Context, question, mode, profileVersion string) (domain.QueryRun, error) {
	return service.store.RunLocalQuery(ctx, question, mode, profileVersion)
}

func (service *QueryService) GetRun(ctx context.Context, runID string) (domain.QueryRun, error) {
	return service.store.GetQueryRun(ctx, runID)
}
