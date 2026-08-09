package agent

import "testing"

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
