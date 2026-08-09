package transport

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"desktop-knowledge-companion/internal/agent"
	"desktop-knowledge-companion/internal/app"
	"desktop-knowledge-companion/internal/domain"
	"desktop-knowledge-companion/internal/store"
)

const protocolVersion = 1

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Meta    meta            `json:"meta"`
}

type meta struct {
	ProtocolVersion int    `json:"protocol_version"`
	RequestID       string `json:"request_id"`
	IdempotencyKey  string `json:"idempotency_key"`
	Caller          string `json:"caller"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    rpcErrorDetail `json:"data"`
}

type rpcErrorDetail struct {
	Code        string `json:"code"`
	RequestID   string `json:"request_id"`
	Retryable   bool   `json:"retryable"`
	UserMessage string `json:"user_message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Server struct {
	store     *store.Store
	knowledge *app.KnowledgeService
	query     *app.QueryService
	agent     *agent.Service
}

func NewServer(core *store.Store) *Server {
	return &Server{store: core, knowledge: app.NewKnowledgeService(core), query: app.NewQueryService(core), agent: agent.NewService(agent.DefaultRegistry(), core)}
}

func (server *Server) StateSnapshot(ctx context.Context) (map[string]any, error) {
	pendingCandidates, err := server.knowledge.ListPendingCandidates(ctx)
	if err != nil {
		return nil, err
	}
	pendingPrompts, err := server.agent.ListPendingPrompts(ctx)
	if err != nil {
		return nil, err
	}
	activeRuns, err := server.store.ListActiveQueryRuns(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"health":             server.store.Health(),
		"pending_candidates": pendingCandidates,
		"pending_prompts":    pendingPrompts,
		"active_runs":        activeRuns,
	}, nil
}

func Serve(ctx context.Context, input io.Reader, output io.Writer, diagnostics io.Writer, server *Server) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	encoder := json.NewEncoder(output)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item request
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			if err := encoder.Encode(errorResponse(nil, "", "VALIDATION_FAILED", "invalid JSON-RPC request", false)); err != nil {
				return fmt.Errorf("write malformed request response: %w", err)
			}
			continue
		}
		if response, handled := validateRequest(item); handled {
			if err := encoder.Encode(response); err != nil {
				return fmt.Errorf("write validation response: %w", err)
			}
			continue
		}

		next := server.dispatch(ctx, item)
		if err := encoder.Encode(next); err != nil {
			return fmt.Errorf("write JSON-RPC response: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = fmt.Fprintf(diagnostics, "JSON-RPC input stopped: %v\n", err)
		return fmt.Errorf("read JSON-RPC input: %w", err)
	}
	return ctx.Err()
}

func (server *Server) dispatch(ctx context.Context, item request) response {
	if writeMethod(item.Method) && item.Meta.IdempotencyKey == "" {
		return errorResponse(item.ID, item.Meta.RequestID, "VALIDATION_FAILED", "idempotency_key is required for writes", false)
	}
	var parametersHash string
	if writeMethod(item.Method) {
		canonical, err := canonicalJSON(item.Params)
		if err != nil {
			return errorResponse(item.ID, item.Meta.RequestID, "VALIDATION_FAILED", "invalid JSON-RPC parameters", false)
		}
		sum := sha256.Sum256(canonical)
		parametersHash = fmt.Sprintf("%x", sum[:])
		record, found, err := server.store.GetRPCIdempotencyRecord(ctx, item.Meta.Caller, item.Method, item.Meta.IdempotencyKey)
		if err != nil {
			return errorResponse(item.ID, item.Meta.RequestID, "STORAGE_UNAVAILABLE", "request could not be completed", false)
		}
		if found {
			if record.ParametersHash != parametersHash {
				return errorResponse(item.ID, item.Meta.RequestID, "IDEMPOTENCY_CONFLICT", "request could not be completed", false)
			}
			return successResponse(item.ID, item.Meta.RequestID, json.RawMessage(record.ResultJSON))
		}
	}
	result, err := server.call(ctx, item)
	if err != nil {
		return errorResponse(item.ID, item.Meta.RequestID, errorCode(err), "request could not be completed", false)
	}
	if writeMethod(item.Method) {
		encoded, err := json.Marshal(result)
		if err != nil {
			return errorResponse(item.ID, item.Meta.RequestID, "STORAGE_UNAVAILABLE", "request could not be completed", false)
		}
		if err := server.store.SaveRPCIdempotencyRecord(ctx, item.Meta.Caller, item.Method, item.Meta.IdempotencyKey, parametersHash, string(encoded)); err != nil {
			return errorResponse(item.ID, item.Meta.RequestID, "STORAGE_UNAVAILABLE", "request could not be completed", false)
		}
	}
	return successResponse(item.ID, item.Meta.RequestID, result)
}

func successResponse(id json.RawMessage, requestID string, value any) response {
	return response{JSONRPC: "2.0", ID: id, Result: struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}{RequestID: requestID, Value: value}}
}

func (server *Server) call(ctx context.Context, item request) (any, error) {
	switch item.Method {
	case "core.health":
		return server.store.Health(), nil
	case "core.state_snapshot":
		return server.StateSnapshot(ctx)
	case "import.create":
		var p struct {
			Kind        string `json:"kind"`
			Content     string `json:"content"`
			DisplayName string `json:"display_name"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.Import(ctx, p.Kind, p.Content, p.DisplayName, item.Meta.IdempotencyKey)
	case "candidate.list":
		var p struct {
			IngestionID string `json:"ingestion_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.store.ListCandidates(ctx, p.IngestionID)
	case "candidate.get":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.store.GetCandidate(ctx, p.ID)
	case "candidate.pending.list":
		return server.knowledge.ListPendingCandidates(ctx)
	case "candidate.update":
		var p struct {
			ID              string
			ExpectedVersion int `json:"expected_version"`
			Content         string
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.EditCandidate(ctx, p.ID, p.ExpectedVersion, p.Content)
	case "candidate.reject":
		var p struct {
			ID              string
			ExpectedVersion int `json:"expected_version"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.RejectCandidate(ctx, p.ID, p.ExpectedVersion)
	case "candidate.split":
		var p struct {
			ID              string   `json:"id"`
			ExpectedVersion int      `json:"expected_version"`
			Parts           []string `json:"parts"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.SplitCandidate(ctx, p.ID, p.ExpectedVersion, p.Parts)
	case "candidate.merge":
		var p struct {
			Candidates []domain.CandidateVersion `json:"candidates"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.MergeCandidates(ctx, p.Candidates)
	case "candidate.request_approval":
		var p struct {
			CandidateID string `json:"candidate_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.RequestCandidatePromotion(ctx, p.CandidateID, item.Meta.Caller)
	case "approval.resolve":
		var p struct {
			ApprovalID string `json:"approval_id"`
			Approve    bool
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.ResolveApproval(ctx, p.ApprovalID, item.Meta.Caller, p.Approve)
	case "candidate.approve":
		var p struct {
			CandidateID string `json:"candidate_id"`
			Token       string
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		knowledge, revision, err := server.knowledge.PromoteCandidate(ctx, p.CandidateID, p.Token, item.Meta.Caller)
		return map[string]any{"knowledge": knowledge, "revision": revision}, err
	case "knowledge.list":
		return server.knowledge.ListKnowledge(ctx)
	case "knowledge.get":
		var p struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.GetKnowledge(ctx, p.ID)
	case "knowledge.source":
		var p struct {
			KnowledgeID string `json:"knowledge_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.GetKnowledgeSource(ctx, p.KnowledgeID)
	case "knowledge.revise":
		var p struct {
			KnowledgeID        string `json:"knowledge_id"`
			ExpectedRevisionID string `json:"expected_revision_id"`
			Content            string `json:"content"`
			Reason             string `json:"reason"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.ReviseKnowledge(ctx, p.KnowledgeID, p.ExpectedRevisionID, p.Content, p.Reason)
	case "knowledge.link_conflict":
		var p struct {
			FromKnowledgeID string `json:"from_knowledge_id"`
			ToKnowledgeID   string `json:"to_knowledge_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.knowledge.LinkKnowledgeConflict(ctx, p.FromKnowledgeID, p.ToKnowledgeID)
	case "agent.tool.inspect":
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.agent.InspectTool(p.Name), nil
	case "agent.tool.request_approval":
		var p struct {
			ToolName   string          `json:"tool_name"`
			Parameters json.RawMessage `json:"parameters"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil || len(p.Parameters) == 0 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("tool parameters are required")
		}
		return server.agent.RequestHighRiskApproval(ctx, p.ToolName, item.Meta.Caller, string(p.Parameters))
	case "agent.tool.consume_approval":
		var p struct {
			ToolName   string          `json:"tool_name"`
			Parameters json.RawMessage `json:"parameters"`
			Token      string          `json:"token"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil || len(p.Parameters) == 0 {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("tool parameters are required")
		}
		if err := server.agent.ConsumeHighRiskApproval(ctx, p.ToolName, item.Meta.Caller, string(p.Parameters), p.Token); err != nil {
			return nil, err
		}
		return map[string]bool{"authorized": true}, nil
	case "agent.prompt.suggest":
		var p struct {
			Topic  string `json:"topic"`
			Detail string `json:"detail"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.agent.SuggestPrompt(ctx, p.Topic, p.Detail)
	case "agent.prompt.preference.set":
		var p struct {
			Topic         string `json:"topic"`
			State         string `json:"state"`
			DeferredUntil string `json:"deferred_until"`
			DeferForSecs  int    `json:"defer_for_seconds"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		var deferredUntil *time.Time
		if p.DeferredUntil != "" && p.DeferForSecs > 0 {
			return nil, fmt.Errorf("only one deferred time may be provided")
		}
		if p.DeferredUntil != "" {
			value, err := time.Parse(time.RFC3339Nano, p.DeferredUntil)
			if err != nil {
				return nil, fmt.Errorf("invalid deferred_until: %w", err)
			}
			deferredUntil = &value
		} else if p.State == "deferred" && p.DeferForSecs > 0 {
			value := time.Now().UTC().Add(time.Duration(p.DeferForSecs) * time.Second)
			deferredUntil = &value
		}
		if err := server.agent.SetPromptPreference(ctx, p.Topic, p.State, deferredUntil); err != nil {
			return nil, err
		}
		return map[string]bool{"saved": true}, nil
	case "agent.pending.list":
		return server.agent.ListPendingPrompts(ctx)
	case "agent.pending.resolve":
		var p struct {
			ID    string `json:"id"`
			State string `json:"state"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		if err := server.agent.ResolvePendingPrompt(ctx, p.ID, p.State); err != nil {
			return nil, err
		}
		return map[string]bool{"resolved": true}, nil
	case "query.start":
		var p struct {
			Question       string `json:"question"`
			Mode           string `json:"mode"`
			ProfileVersion string `json:"profile_version"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.query.Ask(ctx, p.Question, p.Mode, p.ProfileVersion)
	case "query.get":
		var p struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.query.GetRun(ctx, p.RunID)
	case "query.cancel":
		var p struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal(item.Params, &p); err != nil {
			return nil, err
		}
		return server.query.CancelRun(ctx, p.RunID)
	default:
		return nil, errMethodNotFound
	}
}

var errMethodNotFound = errors.New("method not found")

func writeMethod(method string) bool {
	switch method {
	case "import.create", "candidate.update", "candidate.reject", "candidate.split", "candidate.merge", "candidate.request_approval", "approval.resolve", "candidate.approve", "knowledge.revise", "knowledge.link_conflict", "agent.tool.request_approval", "agent.tool.consume_approval", "agent.prompt.suggest", "agent.prompt.preference.set", "agent.pending.resolve", "query.start", "query.cancel":
		return true
	}
	return false
}

func errorCode(err error) string {
	switch {
	case errors.Is(err, errMethodNotFound):
		return "NOT_FOUND"
	case errors.Is(err, store.ErrNotFound):
		return "NOT_FOUND"
	case errors.Is(err, store.ErrVersionConflict):
		return "VERSION_CONFLICT"
	case errors.Is(err, store.ErrApprovalInvalid):
		return "APPROVAL_INVALID"
	case errors.Is(err, store.ErrIdempotencyConflict):
		return "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, store.ErrInvalidState):
		return "VALIDATION_FAILED"
	default:
		return "VALIDATION_FAILED"
	}
}

func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateRequest(item request) (response, bool) {
	if item.JSONRPC != "2.0" || len(item.ID) == 0 || item.Method == "" {
		return errorResponse(item.ID, item.Meta.RequestID, "VALIDATION_FAILED", "invalid JSON-RPC envelope", false), true
	}
	if item.Meta.ProtocolVersion != protocolVersion {
		return errorResponse(item.ID, item.Meta.RequestID, "PROTOCOL_UNSUPPORTED", "protocol version is not supported", false), true
	}
	if !uuidPattern.MatchString(item.Meta.RequestID) {
		return errorResponse(item.ID, item.Meta.RequestID, "VALIDATION_FAILED", "request_id must be a UUID", false), true
	}
	if item.Meta.Caller == "" {
		return errorResponse(item.ID, item.Meta.RequestID, "VALIDATION_FAILED", "caller is required", false), true
	}
	return response{}, false
}

func errorResponse(id json.RawMessage, requestID, code, message string, retryable bool) response {
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    -32000,
			Message: message,
			Data: rpcErrorDetail{
				Code:        code,
				RequestID:   requestID,
				Retryable:   retryable,
				UserMessage: message,
			},
		},
	}
}
