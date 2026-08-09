package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"desktop-knowledge-companion/internal/domain"
	"desktop-knowledge-companion/internal/organizer"
	"desktop-knowledge-companion/internal/store"
)

type KnowledgeService struct {
	store *store.Store
	now   func() time.Time
}

func NewKnowledgeService(storage *store.Store) *KnowledgeService {
	return &KnowledgeService{store: storage, now: time.Now}
}

func (service *KnowledgeService) Import(ctx context.Context, kind, content, displayName, idempotencyKey string) (store.ImportResult, error) {
	candidates, err := organizer.Organize(kind, content)
	if err != nil {
		return store.ImportResult{}, err
	}
	return service.store.CreateImport(ctx, kind, content, displayName, idempotencyKey, candidates)
}

func (service *KnowledgeService) EditCandidate(ctx context.Context, id string, expectedVersion int, content string) (domain.Candidate, error) {
	return service.store.UpdateCandidate(ctx, id, expectedVersion, content)
}

func (service *KnowledgeService) RejectCandidate(ctx context.Context, id string, expectedVersion int) (domain.Candidate, error) {
	return service.store.RejectCandidate(ctx, id, expectedVersion)
}

func (service *KnowledgeService) ListPendingCandidates(ctx context.Context) ([]domain.Candidate, error) {
	return service.store.ListPendingCandidates(ctx)
}

func (service *KnowledgeService) RequestCandidatePromotion(ctx context.Context, candidateID, caller string) (store.Approval, error) {
	if strings.TrimSpace(caller) == "" {
		return store.Approval{}, fmt.Errorf("caller is required")
	}
	return service.store.RequestCandidateApproval(ctx, candidateID, caller, service.now().UTC().Add(5*time.Minute))
}

func (service *KnowledgeService) ResolveApproval(ctx context.Context, approvalID, caller string, approve bool) (store.ApprovalResolution, error) {
	return service.store.ResolveApproval(ctx, approvalID, caller, approve)
}

func (service *KnowledgeService) PromoteCandidate(ctx context.Context, candidateID, token, caller string) (domain.Knowledge, domain.Revision, error) {
	return service.store.PromoteCandidate(ctx, candidateID, token, caller)
}

func (service *KnowledgeService) ReviseKnowledge(ctx context.Context, knowledgeID, expectedRevisionID, content, reason string) (domain.Revision, error) {
	return service.store.ReviseKnowledge(ctx, knowledgeID, expectedRevisionID, content, reason)
}

func (service *KnowledgeService) ListKnowledge(ctx context.Context) ([]store.KnowledgeSummary, error) {
	return service.store.ListKnowledge(ctx)
}

func (service *KnowledgeService) GetKnowledgeSource(ctx context.Context, knowledgeID string) (domain.SourceDocument, error) {
	return service.store.GetKnowledgeSource(ctx, knowledgeID)
}

func (service *KnowledgeService) GetKnowledge(ctx context.Context, id string) (store.KnowledgeDetail, error) {
	return service.store.GetKnowledge(ctx, id)
}

func (service *KnowledgeService) LinkKnowledgeConflict(ctx context.Context, fromKnowledgeID, toKnowledgeID string) (domain.KnowledgeRelation, error) {
	return service.store.LinkKnowledgeConflict(ctx, fromKnowledgeID, toKnowledgeID)
}
