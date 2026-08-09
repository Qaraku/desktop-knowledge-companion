package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"desktop-knowledge-companion/internal/store"
)

type Service struct {
	registry *Registry
	store    *store.Store
}

func (service *Service) RequestHighRiskApproval(ctx context.Context, toolName, caller, parameters string) (store.Approval, error) {
	tool, exists := service.registry.tools[toolName]
	if !exists || (tool.Risk != ConfirmedWrite && tool.Risk != Destructive) {
		return store.Approval{}, store.ErrInvalidState
	}
	canonical, err := canonicalParameters(parameters)
	if err != nil {
		return store.Approval{}, err
	}
	return service.store.RequestToolApproval(ctx, toolName, caller, canonical, time.Now().UTC().Add(5*time.Minute))
}

func (service *Service) ConsumeHighRiskApproval(ctx context.Context, toolName, caller, parameters, token string) error {
	canonical, err := canonicalParameters(parameters)
	if err != nil {
		return err
	}
	return service.store.ConsumeToolApproval(ctx, toolName, caller, canonical, token)
}

func NewService(registry *Registry, storage *store.Store) *Service {
	return &Service{registry: registry, store: storage}
}

func (service *Service) InspectTool(name string) Decision {
	return service.registry.Decide(name, false)
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

func canonicalParameters(parameters string) (string, error) {
	var value any
	if err := json.Unmarshal([]byte(parameters), &value); err != nil {
		return "", fmt.Errorf("tool parameters must be JSON: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("canonicalize tool parameters: %w", err)
	}
	return string(canonical), nil
}
