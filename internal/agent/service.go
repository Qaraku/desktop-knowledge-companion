package agent

import (
	"context"

	"desktop-knowledge-companion/internal/store"
)

type Service struct {
	registry *Registry
	store    *store.Store
}

func NewService(registry *Registry, storage *store.Store) *Service {
	return &Service{registry: registry, store: storage}
}

func (service *Service) RequestTool(ctx context.Context, name, parameters string, networkConfigured bool) (Decision, error) {
	decision := service.registry.Decide(name, networkConfigured)
	risk := "unregistered"
	if tool, ok := service.registry.tools[name]; ok {
		risk = string(tool.Risk)
	}
	state := "denied"
	if decision.Allowed {
		state = "executed"
	} else if decision.ApprovalRequired {
		state = "approval_required"
	}
	if _, err := service.store.RecordToolEvent(ctx, name, risk, state, parameters, decision.Reason); err != nil {
		return Decision{}, err
	}
	return decision, nil
}
