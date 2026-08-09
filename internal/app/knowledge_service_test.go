package app

import (
	"context"
	"errors"
	"testing"

	"desktop-knowledge-companion/internal/domain"
	"desktop-knowledge-companion/internal/store"
)

func TestImportApprovalPromotionAndRevisionAreTraceable(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)

	first, err := service.Import(context.Background(), "markdown", "# One\n\nAlpha\n\n# Two\n\nBeta", "notes.md", "import-001")
	if err != nil {
		t.Fatalf("import markdown: %v", err)
	}
	if len(first.Candidates) != 2 || first.Candidates[0].Ordinal != 0 || first.Candidates[1].Ordinal != 1 {
		t.Fatalf("unexpected candidates: %#v", first.Candidates)
	}
	replay, err := service.Import(context.Background(), "markdown", "# One\n\nAlpha\n\n# Two\n\nBeta", "notes.md", "import-001")
	if err != nil {
		t.Fatalf("replay import: %v", err)
	}
	if !replay.Replayed || replay.SourceID != first.SourceID || replay.IngestionID != first.IngestionID {
		t.Fatalf("unexpected replay: %#v", replay)
	}

	edited, err := service.EditCandidate(context.Background(), first.Candidates[0].ID, 1, "Alpha, edited")
	if err != nil || edited.Version != 2 || edited.State != domain.CandidateEditing {
		t.Fatalf("edit candidate = %#v, %v", edited, err)
	}
	approval, err := service.RequestCandidatePromotion(context.Background(), edited.ID, "gui")
	if err != nil {
		t.Fatalf("request approval: %v", err)
	}
	resolution, err := service.ResolveApproval(context.Background(), approval.ID, "gui", true)
	if err != nil || resolution.Token == "" {
		t.Fatalf("approve = %#v, %v", resolution, err)
	}
	knowledge, firstRevision, err := service.PromoteCandidate(context.Background(), edited.ID, resolution.Token, "gui")
	if err != nil {
		t.Fatalf("promote candidate: %v", err)
	}
	if knowledge.CurrentRevisionID != firstRevision.ID || firstRevision.Content != "Alpha, edited" {
		t.Fatalf("unexpected promoted knowledge/revision: %#v %#v", knowledge, firstRevision)
	}
	listed, err := service.ListKnowledge(context.Background())
	if err != nil || len(listed) != 1 || listed[0].Knowledge.ID != knowledge.ID || listed[0].Content != "Alpha, edited" {
		t.Fatalf("list knowledge = %#v, %v", listed, err)
	}
	replayedKnowledge, replayedRevision, err := service.PromoteCandidate(context.Background(), edited.ID, resolution.Token, "gui")
	if err != nil || replayedKnowledge.ID != knowledge.ID || replayedRevision.ID != firstRevision.ID {
		t.Fatalf("promotion replay = %#v %#v %v", replayedKnowledge, replayedRevision, err)
	}

	secondRevision, err := service.ReviseKnowledge(context.Background(), knowledge.ID, firstRevision.ID, "Alpha, corrected", "fact_update")
	if err != nil || secondRevision.ParentRevisionID != firstRevision.ID {
		t.Fatalf("revise knowledge = %#v, %v", secondRevision, err)
	}
	_, err = service.ReviseKnowledge(context.Background(), knowledge.ID, firstRevision.ID, "stale write", "fact_update")
	if !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
}

func TestRejectedCandidateRetainsImmutableSource(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)
	result, err := service.Import(context.Background(), "text", "Original text", "", "import-002")
	if err != nil {
		t.Fatalf("import text: %v", err)
	}
	rejected, err := service.RejectCandidate(context.Background(), result.Candidates[0].ID, 1)
	if err != nil || rejected.State != domain.CandidateRejected {
		t.Fatalf("reject candidate = %#v, %v", rejected, err)
	}
	var sourceContent string
	if err := storage.DB().QueryRow("SELECT content FROM source_documents WHERE id = ?", result.SourceID).Scan(&sourceContent); err != nil || sourceContent != "Original text" {
		t.Fatalf("source was not retained: %q, %v", sourceContent, err)
	}
}
