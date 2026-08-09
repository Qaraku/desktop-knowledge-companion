package agent

import "fmt"

type Risk string

const (
	Read           Risk = "read"
	Network        Risk = "network"
	DraftWrite     Risk = "draft_write"
	ConfirmedWrite Risk = "confirmed_write"
	Destructive    Risk = "destructive"
)

type Tool struct {
	Name string
	Risk Risk
}

type Decision struct {
	Allowed          bool
	ApprovalRequired bool
	Reason           string
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(tools []Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, tool := range tools {
		if tool.Name == "" || !validRisk(tool.Risk) {
			return nil, fmt.Errorf("invalid tool declaration")
		}
		if _, exists := registry.tools[tool.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", tool.Name)
		}
		registry.tools[tool.Name] = tool
	}
	return registry, nil
}

func (registry *Registry) Decide(name string, networkConfigured bool) Decision {
	tool, exists := registry.tools[name]
	if !exists {
		return Decision{Reason: "tool is not registered"}
	}
	switch tool.Risk {
	case Read, DraftWrite:
		return Decision{Allowed: true}
	case Network:
		if !networkConfigured {
			return Decision{Reason: "network tool is not configured"}
		}
		return Decision{Allowed: true}
	case ConfirmedWrite, Destructive:
		return Decision{ApprovalRequired: true, Reason: "matching user approval is required"}
	default:
		return Decision{Reason: "unknown tool risk"}
	}
}

func validRisk(risk Risk) bool {
	return risk == Read || risk == Network || risk == DraftWrite || risk == ConfirmedWrite || risk == Destructive
}
