package app

import (
	"context"
	"strings"
	"testing"

	"desktop-knowledge-companion/internal/store"
)

func TestStrictQueryProducesPersonalCitationAndImmutableRun(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	knowledge := NewKnowledgeService(storage)
	imported, err := knowledge.Import(context.Background(), "text", "Go builds a local desktop agent.", "", "query-import")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	approval, err := knowledge.RequestCandidatePromotion(context.Background(), imported.Candidates[0].ID, "gui")
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	resolution, err := knowledge.ResolveApproval(context.Background(), approval.ID, "gui", true)
	if err != nil {
		t.Fatalf("resolve approval: %v", err)
	}
	_, revision, err := knowledge.PromoteCandidate(context.Background(), imported.Candidates[0].ID, resolution.Token, "gui")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}

	queries := NewQueryService(storage)
	run, err := queries.Ask(context.Background(), "local agent", "strict", "local_v1")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if run.State != "completed" || len(run.Citations) != 1 || run.Citations[0].RevisionID != revision.ID || !strings.Contains(run.Answer, "local desktop agent") {
		t.Fatalf("unexpected query run: %#v", run)
	}
	reloaded, err := queries.GetRun(context.Background(), run.ID)
	if err != nil || len(reloaded.Trace) != 2 || reloaded.Citations[0].Origin != "personal" {
		t.Fatalf("run replay = %#v, %v", reloaded, err)
	}
	if _, err := storage.DB().Exec("UPDATE query_runs SET answer = 'changed' WHERE id = ?", run.ID); err == nil {
		t.Fatal("terminal query run must be immutable")
	}
}

func TestStrictQueryWithoutEvidenceRefusesWithoutCitation(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	run, err := NewQueryService(storage).Ask(context.Background(), "unrelated question", "strict", "local_v1")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	if run.RefusalReason != "no_local_evidence" || run.Answer != "" || len(run.Citations) != 0 {
		t.Fatalf("unexpected strict refusal: %#v", run)
	}
}
