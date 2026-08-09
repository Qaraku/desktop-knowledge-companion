package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"desktop-knowledge-companion/internal/agent"
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

func TestServerReplaysWriteResultAndRejectsIdempotencyConflict(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	imported, err := server.knowledge.Import(context.Background(), "text", "Original", "", "rpc-idempotency-import")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	item := request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "candidate.update",
		Params:  json.RawMessage(`{"id":"` + imported.Candidates[0].ID + `","expected_version":1,"content":"Updated"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-candidate-update-1"},
	}
	first := server.dispatch(context.Background(), item)
	if first.Error != nil {
		t.Fatalf("first update: %#v", first.Error)
	}
	second := server.dispatch(context.Background(), item)
	if second.Error != nil {
		t.Fatalf("replayed update: %#v", second.Error)
	}
	candidate, err := core.GetCandidate(context.Background(), imported.Candidates[0].ID)
	if err != nil || candidate.Version != 2 || candidate.Content != "Updated" {
		t.Fatalf("candidate after replay = %#v, %v", candidate, err)
	}
	item.Params = json.RawMessage(`{"id":"` + imported.Candidates[0].ID + `","expected_version":1,"content":"Changed"}`)
	conflict := server.dispatch(context.Background(), item)
	if conflict.Error == nil || conflict.Error.Data.Code != "IDEMPOTENCY_CONFLICT" {
		t.Fatalf("idempotency conflict = %#v", conflict.Error)
	}
}

func TestStateSnapshotIncludesRecoverableWorkspaceState(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	imported, err := server.knowledge.Import(context.Background(), "text", "Pending", "", "rpc-snapshot-import")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if _, err := server.agent.SuggestPrompt(context.Background(), "missing-evidence", "Import related material"); err != nil {
		t.Fatalf("suggest prompt: %v", err)
	}
	if _, err := core.DB().Exec(`INSERT INTO query_runs(id, question, mode, knowledge_version, profile_version, state, created_at) VALUES ('run-active', 'pending', 'strict', 0, 'local_v1', 'running', '2026-08-09T00:00:00Z')`); err != nil {
		t.Fatalf("create active run: %v", err)
	}
	snapshot, err := server.StateSnapshot(context.Background())
	if err != nil {
		t.Fatalf("state snapshot: %v", err)
	}
	if candidates := snapshot["pending_candidates"].([]domain.Candidate); len(candidates) != 1 || candidates[0].ID != imported.Candidates[0].ID {
		t.Fatalf("snapshot candidates = %#v", candidates)
	}
	if prompts := snapshot["pending_prompts"].([]store.PendingItem); len(prompts) != 1 {
		t.Fatalf("snapshot prompts = %#v", prompts)
	}
	if runs := snapshot["active_runs"].([]domain.QueryRun); len(runs) != 1 || runs[0].ID != "run-active" {
		t.Fatalf("snapshot active runs = %#v", runs)
	}
}

func TestServerCancelsActiveQueryRun(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	if _, err := core.DB().Exec(`INSERT INTO query_runs(id, question, mode, knowledge_version, profile_version, state, created_at) VALUES ('run-cancel', 'pending', 'strict', 0, 'local_v1', 'running', '2026-08-09T00:00:00Z')`); err != nil {
		t.Fatalf("create active run: %v", err)
	}
	response := NewServer(core).dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "query.cancel",
		Params:  json.RawMessage(`{"run_id":"run-cancel"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-query-cancel-1"},
	})
	if response.Error != nil {
		t.Fatalf("cancel response: %#v", response.Error)
	}
}

func TestServerLinksAndReadsKnowledgeConflicts(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	promote := func(content, key string) domain.Knowledge {
		t.Helper()
		imported, err := server.knowledge.Import(context.Background(), "text", content, "", key)
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		approval, err := server.knowledge.RequestCandidatePromotion(context.Background(), imported.Candidates[0].ID, "test")
		if err != nil {
			t.Fatalf("request approval: %v", err)
		}
		resolution, err := server.knowledge.ResolveApproval(context.Background(), approval.ID, "test", true)
		if err != nil {
			t.Fatalf("resolve approval: %v", err)
		}
		knowledge, _, err := server.knowledge.PromoteCandidate(context.Background(), imported.Candidates[0].ID, resolution.Token, "test")
		if err != nil {
			t.Fatalf("promote: %v", err)
		}
		return knowledge
	}
	first, second := promote("First", "rpc-conflict-first"), promote("Second", "rpc-conflict-second")
	linked := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "knowledge.link_conflict",
		Params:  json.RawMessage(`{"from_knowledge_id":"` + first.ID + `","to_knowledge_id":"` + second.ID + `"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-conflict-link-1"},
	})
	if linked.Error != nil {
		t.Fatalf("link conflict: %#v", linked.Error)
	}
	detail, err := server.call(context.Background(), request{Method: "knowledge.get", Params: json.RawMessage(`{"id":"` + first.ID + `"}`)})
	if err != nil {
		t.Fatalf("get knowledge: %v", err)
	}
	value := detail.(store.KnowledgeDetail)
	if value.Knowledge.State != "conflicted" || len(value.Relations) != 1 || value.Relations[0].ToKnowledgeID != second.ID {
		t.Fatalf("knowledge detail: %#v", value)
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

func TestServerExposesAgentToolApproval(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	inspected, err := server.call(context.Background(), request{Method: "agent.tool.inspect", Params: json.RawMessage(`{"name":"network.search"}`)})
	if err != nil {
		t.Fatalf("inspect tool: %v", err)
	}
	if decision := inspected.(agent.Decision); decision.Allowed {
		t.Fatalf("unconfigured network tool allowed: %#v", decision)
	}
	approvalResponse := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "agent.tool.request_approval",
		Params:  json.RawMessage(`{"tool_name":"candidate.promote","parameters":{"candidate_id":"one"}}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-agent-approval-1"},
	})
	if approvalResponse.Error != nil {
		t.Fatalf("request approval: %#v", approvalResponse.Error)
	}
	approval := approvalResponse.Result.(struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}).Value.(store.Approval)
	resolved, err := core.ResolveApproval(context.Background(), approval.ID, "test", true)
	if err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	consumed := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "agent.tool.consume_approval",
		Params:  json.RawMessage(`{"tool_name":"candidate.promote","parameters":{ "candidate_id": "one" },"token":"` + resolved.Token + `"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-agent-consume-1"},
	})
	if consumed.Error != nil {
		t.Fatalf("consume approval: %#v", consumed.Error)
	}
}

func TestServerExposesAgentPromptLifecycle(t *testing.T) {
	core, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer core.Close()
	server := NewServer(core)
	suggested := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "agent.prompt.suggest",
		Params:  json.RawMessage(`{"topic":"missing-evidence","detail":"Import relevant material"}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-agent-prompt-1"},
	})
	if suggested.Error != nil {
		t.Fatalf("suggest prompt: %#v", suggested.Error)
	}
	suggestion := suggested.Result.(struct {
		RequestID string `json:"request_id"`
		Value     any    `json:"value"`
	}).Value.(agent.PromptSuggestion)
	if suggestion.PendingItem.State != "open" {
		t.Fatalf("unexpected suggestion: %#v", suggestion)
	}
	listed, err := server.call(context.Background(), request{Method: "agent.pending.list", Params: json.RawMessage(`{}`)})
	if err != nil || len(listed.([]store.PendingItem)) != 1 {
		t.Fatalf("pending prompts = %#v, %v", listed, err)
	}
	preference := server.dispatch(context.Background(), request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("2"),
		Method:  "agent.prompt.preference.set",
		Params:  json.RawMessage(`{"topic":"conflict","state":"deferred","defer_for_seconds":3600}`),
		Meta:    meta{ProtocolVersion: 1, RequestID: "0198c787-8bf0-7afe-8c7d-9a41c6671c23", Caller: "test", IdempotencyKey: "rpc-agent-preference-1"},
	})
	if preference.Error != nil {
		t.Fatalf("set preference: %#v", preference.Error)
	}
	suppressed, err := server.call(context.Background(), request{Method: "agent.prompt.suggest", Params: json.RawMessage(`{"topic":"conflict","detail":"Resolve conflict"}`)})
	if err != nil || !suppressed.(agent.PromptSuggestion).Suppressed {
		t.Fatalf("deferred prompt = %#v, %v", suppressed, err)
	}
}
