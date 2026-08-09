package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

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

func Serve(ctx context.Context, input io.Reader, output io.Writer, diagnostics io.Writer, core *store.Store) error {
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

		var next response
		switch item.Method {
		case "core.health":
			next = response{
				JSONRPC: "2.0",
				ID:      item.ID,
				Result: struct {
					RequestID string `json:"request_id"`
					store.Health
				}{RequestID: item.Meta.RequestID, Health: core.Health()},
			}
		default:
			next = errorResponse(item.ID, item.Meta.RequestID, "NOT_FOUND", "method is not available", false)
		}
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
