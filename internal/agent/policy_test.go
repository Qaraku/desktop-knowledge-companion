package agent

import (
	"context"
	"testing"

	"desktop-knowledge-companion/internal/store"
)

func TestRegistryEnforcesClosedToolAndApprovalPolicy(t *testing.T) {
	registry, err := NewRegistry([]Tool{{Name: "knowledge.read", Risk: Read}, {Name: "candidate.promote", Risk: ConfirmedWrite}, {Name: "search", Risk: Network}})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if decision := registry.Decide("missing", false); decision.Allowed || decision.ApprovalRequired {
		t.Fatalf("unregistered tool allowed: %#v", decision)
	}
	if decision := registry.Decide("candidate.promote", false); decision.Allowed || !decision.ApprovalRequired {
		t.Fatalf("confirmation policy missing: %#v", decision)
	}
	if decision := registry.Decide("search", false); decision.Allowed {
		t.Fatalf("unconfigured network tool allowed: %#v", decision)
	}
	if decision := registry.Decide("knowledge.read", false); !decision.Allowed {
		t.Fatalf("read tool rejected: %#v", decision)
	}
}

func TestServicePersistsDeniedAndApprovalRequiredAudit(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	registry, err := NewRegistry([]Tool{{Name: "promote", Risk: ConfirmedWrite}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, storage)
	if decision, err := service.RequestTool(context.Background(), "missing", "{}", false); err != nil || decision.Allowed {
		t.Fatalf("missing = %#v, %v", decision, err)
	}
	if decision, err := service.RequestTool(context.Background(), "promote", "{}", false); err != nil || !decision.ApprovalRequired {
		t.Fatalf("promote = %#v, %v", decision, err)
	}
	var count int
	if err := storage.DB().QueryRow("SELECT COUNT(*) FROM agent_tool_events WHERE state IN ('denied', 'approval_required')").Scan(&count); err != nil || count != 2 {
		t.Fatalf("audit count=%d err=%v", count, err)
	}
}

func TestHighRiskToolApprovalBindsParametersAndIsSingleUse(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	registry, err := NewRegistry([]Tool{{Name: "promote", Risk: ConfirmedWrite}})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(registry, storage)
	approval, err := service.RequestHighRiskApproval(context.Background(), "promote", "agent", `{"candidate":"a"}`)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := storage.ResolveApproval(context.Background(), approval.ID, "agent", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ConsumeHighRiskApproval(context.Background(), "promote", "agent", `{"candidate":"b"}`, resolved.Token); err == nil {
		t.Fatal("parameter replacement must be rejected")
	}
	if err := service.ConsumeHighRiskApproval(context.Background(), "promote", "agent", `{"candidate":"a"}`, resolved.Token); err != nil {
		t.Fatal(err)
	}
	if err := service.ConsumeHighRiskApproval(context.Background(), "promote", "agent", `{"candidate":"a"}`, resolved.Token); err == nil {
		t.Fatal("approval replay must be rejected")
	}
}
