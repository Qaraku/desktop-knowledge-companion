package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"desktop-knowledge-companion/internal/store"
)

func TestServeReturnsHealthAndProtocolFailure(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()

	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"core.health\",\"params\":{},\"meta\":{\"protocol_version\":1,\"request_id\":\"0198c787-8bf0-7afe-8c7d-9a41c6671c23\",\"caller\":\"test\"}}\n{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"core.health\",\"meta\":{\"protocol_version\":2,\"request_id\":\"0198c787-8bf0-7afe-8c7d-9a41c6671c23\",\"caller\":\"test\"}}\n")
	var output, diagnostics bytes.Buffer
	if err := Serve(context.Background(), input, &output, &diagnostics, NewServer(core)); err != nil {
		t.Fatalf("serve: %v", err)
	}

	decoder := json.NewDecoder(&output)
	var healthy, unsupported map[string]any
	if err := decoder.Decode(&healthy); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	result, ok := healthy["result"].(map[string]any)
	value, valueOK := result["value"].(map[string]any)
	if !ok || !valueOK || value["ready"] != true {
		t.Fatalf("unexpected health response: %#v", healthy)
	}
	if err := decoder.Decode(&unsupported); err != nil {
		t.Fatalf("decode protocol response: %v", err)
	}
	errorBody, ok := unsupported["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response: %#v", unsupported)
	}
	detail, ok := errorBody["data"].(map[string]any)
	if !ok || detail["code"] != "PROTOCOL_UNSUPPORTED" {
		t.Fatalf("unexpected protocol error: %#v", unsupported)
	}
}
