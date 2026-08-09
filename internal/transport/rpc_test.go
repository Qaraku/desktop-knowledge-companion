package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"desktop-knowledge-companion/internal/domain"
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

func TestServerDispatchesIdempotentImportAndCandidateRead(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	item := request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "import.create", Params: json.RawMessage(`{"kind":"text","content":"Go Agent","display_name":"note"}`), Meta: meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-import-1"}}
	response := server.dispatch(context.Background(), item)
	if response.Error != nil {
		t.Fatalf("import response: %#v", response.Error)
	}
	value := response.Result.(struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}).Value.(store.ImportResult)
	if len(value.Candidates) != 1 {
		t.Fatalf("unexpected import result: %#v", value)
	}
	result, err := server.call(context.Background(), request{Method: "candidate.list", Params: json.RawMessage(`{"ingestion_id":"` + value.IngestionID + `"}`)})
	if err != nil {
		t.Fatalf("candidate list: %v", err)
	}
	if candidates := result.([]domain.Candidate); len(candidates) != 1 || candidates[0].Content != "Go Agent" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	pending, err := server.call(context.Background(), request{Method: "candidate.pending.list", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("pending candidates: %v", err)
	}
	if candidates := pending.([]domain.Candidate); len(candidates) != 1 || candidates[0].ID != value.Candidates[0].ID {
		t.Fatalf("unexpected pending candidates: %#v", candidates)
	}
}

func TestServerDispatchesKnowledgeRevision(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	knowledge := server.knowledge
	imported, err := knowledge.Import(context.Background(), "text", "Original", "", "rpc-revision-import")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	approval, err := knowledge.RequestCandidatePromotion(context.Background(), imported.Candidates[0].ID, "test")
	if err != nil {
		t.Fatalf("approval request: %v", err)
	}
	resolution, err := knowledge.ResolveApproval(context.Background(), approval.ID, "test", true)
	if err != nil {
		t.Fatalf("approval resolution: %v", err)
	}
	item, firstRevision, err := knowledge.PromoteCandidate(context.Background(), imported.Candidates[0].ID, resolution.Token, "test")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	response := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "knowledge.revise",
		Params:  json.RawMessage(`{"knowledge_id":"` + item.ID + `","expected_revision_id":"` + firstRevision.ID + `","content":"Corrected","reason":"fact_update"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-revision-1"},
	})
	if response.Error != nil {
		t.Fatalf("revision response: %#v", response.Error)
	}
	value := response.Result.(struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}).Value.(domain.Revision)
	if value.ParentRevisionID != firstRevision.ID || value.Content != "Corrected" {
		t.Fatalf("unexpected revision: %#v", value)
	}
}
