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

func TestCandidateSplitKeepsSourceAndOriginalOrdering(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)
	imported, err := service.Import(context.Background(), "markdown", "# One\n\nAlpha\n\n# Two\n\nBeta\n\n# Three\n\nGamma", "notes.md", "split-candidate")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	split, err := service.SplitCandidate(context.Background(), imported.Candidates[1].ID, 1, []string{"Beta first", "Beta second"})
	if err != nil || len(split) != 2 || split[0].State != domain.CandidateEditing || split[1].State != domain.CandidateProposed {
		t.Fatalf("split candidate = %#v, %v", split, err)
	}
	items, err := storage.ListCandidates(context.Background(), imported.IngestionID)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(items) != 4 || items[0].Content != "Alpha" || items[1].Content != "Beta first" || items[2].Content != "Beta second" || items[3].Content != "Gamma" {
		t.Fatalf("split order = %#v", items)
	}
	for index, item := range items {
		if item.Ordinal != index || item.IngestionID != imported.IngestionID {
			t.Fatalf("split candidate metadata = %#v", item)
		}
	}
	if _, err := service.SplitCandidate(context.Background(), imported.Candidates[1].ID, 1, []string{"stale", "write"}); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("stale split = %v", err)
	}
	var source string
	if err := storage.DB().QueryRow("SELECT content FROM source_documents WHERE id = ?", imported.SourceID).Scan(&source); err != nil || source != "# One\n\nAlpha\n\n# Two\n\nBeta\n\n# Three\n\nGamma" {
		t.Fatalf("immutable source = %q, %v", source, err)
	}
}

func TestCandidateMergeSupersedesNonPrimaryCandidates(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)
	imported, err := service.Import(context.Background(), "markdown", "# First\n\nAlpha\n\n# Second\n\nBeta", "notes.md", "merge-candidates")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	merged, err := service.MergeCandidates(context.Background(), []domain.CandidateVersion{
		{ID: imported.Candidates[1].ID, ExpectedVersion: 1},
		{ID: imported.Candidates[0].ID, ExpectedVersion: 1},
	})
	if err != nil || merged.ID != imported.Candidates[0].ID || merged.Content != "Alpha\n\nBeta" || merged.Version != 2 || merged.State != domain.CandidateEditing {
		t.Fatalf("merge candidates = %#v, %v", merged, err)
	}
	superseded, err := storage.GetCandidate(context.Background(), imported.Candidates[1].ID)
	if err != nil || superseded.State != domain.CandidateSuperseded || superseded.Version != 2 {
		t.Fatalf("superseded candidate = %#v, %v", superseded, err)
	}
	pending, err := service.ListPendingCandidates(context.Background())
	if err != nil || len(pending) != 1 || pending[0].ID != merged.ID {
		t.Fatalf("pending candidates = %#v, %v", pending, err)
	}
	other, err := service.Import(context.Background(), "text", "Other", "", "merge-other")
	if err != nil {
		t.Fatalf("import other: %v", err)
	}
	if _, err := service.MergeCandidates(context.Background(), []domain.CandidateVersion{{ID: merged.ID, ExpectedVersion: merged.Version}, {ID: other.Candidates[0].ID, ExpectedVersion: 1}}); err == nil {
		t.Fatal("expected cross-ingestion merge rejection")
	}
}

func TestListPendingCandidatesExcludesRejectedAndPromotedItems(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)
	pending, err := service.Import(context.Background(), "text", "Pending", "", "import-pending")
	if err != nil {
		t.Fatalf("import pending: %v", err)
	}
	rejected, err := service.Import(context.Background(), "text", "Rejected", "", "import-rejected")
	if err != nil {
		t.Fatalf("import rejected: %v", err)
	}
	if _, err := service.RejectCandidate(context.Background(), rejected.Candidates[0].ID, 1); err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	items, err := service.ListPendingCandidates(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != pending.Candidates[0].ID {
		t.Fatalf("pending candidates = %#v, %v", items, err)
	}
}

func TestKnowledgeDetailRetainsRevisionsAndConflictRelations(t *testing.T) {
	storage, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer storage.Close()
	service := NewKnowledgeService(storage)
	promote := func(content, key string) domain.Knowledge {
		t.Helper()
		imported, err := service.Import(context.Background(), "text", content, "", key)
		if err != nil {
			t.Fatalf("import %s: %v", content, err)
		}
		approval, err := service.RequestCandidatePromotion(context.Background(), imported.Candidates[0].ID, "test")
		if err != nil {
			t.Fatalf("request approval: %v", err)
		}
		resolution, err := service.ResolveApproval(context.Background(), approval.ID, "test", true)
		if err != nil {
			t.Fatalf("resolve approval: %v", err)
		}
		knowledge, _, err := service.PromoteCandidate(context.Background(), imported.Candidates[0].ID, resolution.Token, "test")
		if err != nil {
			t.Fatalf("promote candidate: %v", err)
		}
		return knowledge
	}
	first := promote("First view", "detail-first")
	second := promote("Second view", "detail-second")
	if _, err := service.ReviseKnowledge(context.Background(), first.ID, first.CurrentRevisionID, "First view corrected", "fact_update"); err != nil {
		t.Fatalf("revise first knowledge: %v", err)
	}
	relation, err := service.LinkKnowledgeConflict(context.Background(), first.ID, second.ID)
	if err != nil || relation.Kind != "conflicts_with" {
		t.Fatalf("link conflict = %#v, %v", relation, err)
	}
	detail, err := service.GetKnowledge(context.Background(), first.ID)
	if err != nil {
		t.Fatalf("get knowledge: %v", err)
	}
	if detail.Knowledge.State != "conflicted" || len(detail.Revisions) != 2 || len(detail.Relations) != 1 || detail.Relations[0].ToKnowledgeID != second.ID {
		t.Fatalf("unexpected knowledge detail: %#v", detail)
	}
}
