package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"desktop-knowledge-companion/internal/app"
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
}

func NewServer(core *store.Store) *Server {
	return &Server{store: core, knowledge: app.NewKnowledgeService(core), query: app.NewQueryService(core)}
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
	result, err := server.call(ctx, item)
	if err != nil {
		return errorResponse(item.ID, item.Meta.RequestID, errorCode(err), "request could not be completed", false)
	}
	return response{JSONRPC: "2.0", ID: item.ID, Result: struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}{RequestID: item.Meta.RequestID, Value: result}}
}

func (server *Server) call(ctx context.Context, item request) (any, error) {
	switch item.Method {
	case "core.health":
		return server.store.Health(), nil
	case "core.state_snapshot":
		return map[string]any{"health": server.store.Health()}, nil
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
	default:
		return nil, errMethodNotFound
	}
}

var errMethodNotFound = errors.New("method not found")

func writeMethod(method string) bool {
	switch method {
	case "import.create", "candidate.update", "candidate.reject", "candidate.request_approval", "approval.resolve", "candidate.approve", "query.start":
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
	case errors.Is(err, store.ErrInvalidState):
		return "VALIDATION_FAILED"
	default:
		return "VALIDATION_FAILED"
	}
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
