package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"desktop-knowledge-companion/internal/domain"
	"desktop-knowledge-companion/internal/organizer"
)

type ImportResult struct {
	SourceID    string             `json:"source_id"`
	IngestionID string             `json:"ingestion_id"`
	Candidates  []domain.Candidate `json:"candidates"`
	Replayed    bool               `json:"replayed"`
}

type Approval struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	TargetID  string    `json:"target_id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

type ApprovalResolution struct {
	Approval Approval `json:"approval"`
	Token    string   `json:"token,omitempty"`
}

func (store *Store) CreateImport(ctx context.Context, kind, content, displayName, idempotencyKey string, candidates []organizer.Candidate) (ImportResult, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return ImportResult{}, fmt.Errorf("idempotency key is required")
	}
	if len(candidates) == 0 {
		return ImportResult{}, fmt.Errorf("at least one candidate is required")
	}
	if result, found, err := store.importByIdempotency(ctx, idempotencyKey); err != nil || found {
		if err != nil {
			return ImportResult{}, err
		}
		result.Replayed = true
		return result, nil
	}

	now := time.Now().UTC()
	sourceID, err := domain.NewID(now)
	if err != nil {
		return ImportResult{}, err
	}
	ingestionID, err := domain.NewID(now)
	if err != nil {
		return ImportResult{}, err
	}
	prepared := make([]domain.Candidate, 0, len(candidates))
	for _, item := range candidates {
		id, err := domain.NewID(now)
		if err != nil {
			return ImportResult{}, err
		}
		prepared = append(prepared, domain.Candidate{ID: id, IngestionID: ingestionID, Ordinal: item.Ordinal, Version: 1, Content: item.Content, TitlePath: item.TitlePath, State: domain.CandidateProposed, UpdatedAt: now})
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin import: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO source_documents(id, kind, content, content_hash, display_name, input_at) VALUES (?, ?, ?, ?, ?, ?)", sourceID, kind, content, contentHash(content), nullIfEmpty(displayName), now.Format(time.RFC3339Nano)); err != nil {
		return ImportResult{}, fmt.Errorf("insert source: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO ingestions(id, source_id, idempotency_key, state, created_at) VALUES (?, ?, ?, 'candidates_ready', ?)", ingestionID, sourceID, idempotencyKey, now.Format(time.RFC3339Nano)); err != nil {
		return ImportResult{}, fmt.Errorf("insert ingestion: %w", err)
	}
	for _, item := range prepared {
		path, marshalErr := json.Marshal(item.TitlePath)
		if marshalErr != nil {
			return ImportResult{}, fmt.Errorf("marshal title path: %w", marshalErr)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO candidate_items(id, ingestion_id, ordinal, version, content, title_path_json, state, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)", item.ID, item.IngestionID, item.Ordinal, item.Version, item.Content, path, item.State, now.Format(time.RFC3339Nano)); err != nil {
			return ImportResult{}, fmt.Errorf("insert candidate: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("commit import: %w", err)
	}
	return ImportResult{SourceID: sourceID, IngestionID: ingestionID, Candidates: prepared}, nil
}

func (store *Store) ListCandidates(ctx context.Context, ingestionID string) ([]domain.Candidate, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, ingestion_id, ordinal, version, content, title_path_json, state, COALESCE(promoted_knowledge_id, ''), updated_at FROM candidate_items WHERE ingestion_id = ? ORDER BY ordinal`, ingestionID)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()
	var result []domain.Candidate
	for rows.Next() {
		item, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) ListPendingCandidates(ctx context.Context) ([]domain.Candidate, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, ingestion_id, ordinal, version, content, title_path_json, state, COALESCE(promoted_knowledge_id, ''), updated_at FROM candidate_items WHERE state IN ('proposed', 'editing') ORDER BY updated_at DESC, ingestion_id, ordinal`)
	if err != nil {
		return nil, fmt.Errorf("list pending candidates: %w", err)
	}
	defer rows.Close()
	result := make([]domain.Candidate, 0)
	for rows.Next() {
		item, err := scanCandidate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type KnowledgeSummary struct {
	Knowledge domain.Knowledge `json:"knowledge"`
	Content   string           `json:"content"`
}

type KnowledgeDetail struct {
	Knowledge domain.Knowledge           `json:"knowledge"`
	Revisions []domain.Revision          `json:"revisions"`
	Relations []domain.KnowledgeRelation `json:"relations"`
}

func (store *Store) GetKnowledgeSource(ctx context.Context, knowledgeID string) (domain.SourceDocument, error) {
	var source domain.SourceDocument
	var inputAt string
	err := store.db.QueryRowContext(ctx, `SELECT s.id, s.kind, s.content, COALESCE(s.display_name, ''), s.input_at
		FROM candidate_items c
		JOIN ingestions i ON i.id = c.ingestion_id
		JOIN source_documents s ON s.id = i.source_id
		WHERE c.promoted_knowledge_id = ?`, knowledgeID).Scan(&source.ID, &source.Kind, &source.Content, &source.DisplayName, &inputAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SourceDocument{}, ErrNotFound
	}
	if err != nil {
		return domain.SourceDocument{}, fmt.Errorf("get knowledge source: %w", err)
	}
	if source.InputAt, err = time.Parse(time.RFC3339Nano, inputAt); err != nil {
		return domain.SourceDocument{}, fmt.Errorf("parse source input time: %w", err)
	}
	return source, nil
}

func (store *Store) ListKnowledge(ctx context.Context) ([]KnowledgeSummary, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT k.id, k.state, k.current_revision_id, k.created_at, r.content FROM knowledge_items k JOIN knowledge_revisions r ON r.id = k.current_revision_id ORDER BY k.created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list knowledge: %w", err)
	}
	defer rows.Close()
	result := make([]KnowledgeSummary, 0)
	for rows.Next() {
		var item KnowledgeSummary
		var created string
		if err := rows.Scan(&item.Knowledge.ID, &item.Knowledge.State, &item.Knowledge.CurrentRevisionID, &created, &item.Content); err != nil {
			return nil, err
		}
		if item.Knowledge.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *Store) GetKnowledge(ctx context.Context, id string) (KnowledgeDetail, error) {
	var detail KnowledgeDetail
	var createdAt string
	err := store.db.QueryRowContext(ctx, `SELECT id, state, current_revision_id, created_at FROM knowledge_items WHERE id = ?`, id).Scan(&detail.Knowledge.ID, &detail.Knowledge.State, &detail.Knowledge.CurrentRevisionID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KnowledgeDetail{}, ErrNotFound
	}
	if err != nil {
		return KnowledgeDetail{}, fmt.Errorf("get knowledge: %w", err)
	}
	if detail.Knowledge.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return KnowledgeDetail{}, fmt.Errorf("parse knowledge creation time: %w", err)
	}
	revisions, err := store.db.QueryContext(ctx, `SELECT id, knowledge_id, parent_revision_id, content, reason, state, created_at FROM knowledge_revisions WHERE knowledge_id = ? ORDER BY created_at ASC`, id)
	if err != nil {
		return KnowledgeDetail{}, fmt.Errorf("list knowledge revisions: %w", err)
	}
	defer revisions.Close()
	for revisions.Next() {
		revision, err := scanRevision(revisions)
		if err != nil {
			return KnowledgeDetail{}, err
		}
		detail.Revisions = append(detail.Revisions, revision)
	}
	if err := revisions.Err(); err != nil {
		return KnowledgeDetail{}, err
	}
	relations, err := store.db.QueryContext(ctx, `SELECT id, from_knowledge_id, to_knowledge_id, kind, created_at FROM knowledge_relations WHERE from_knowledge_id = ? OR to_knowledge_id = ? ORDER BY created_at ASC`, id, id)
	if err != nil {
		return KnowledgeDetail{}, fmt.Errorf("list knowledge relations: %w", err)
	}
	defer relations.Close()
	for relations.Next() {
		relation, err := scanKnowledgeRelation(relations)
		if err != nil {
			return KnowledgeDetail{}, err
		}
		detail.Relations = append(detail.Relations, relation)
	}
	if err := relations.Err(); err != nil {
		return KnowledgeDetail{}, err
	}
	return detail, nil
}

func (store *Store) LinkKnowledgeConflict(ctx context.Context, fromKnowledgeID, toKnowledgeID string) (domain.KnowledgeRelation, error) {
	if fromKnowledgeID == "" || toKnowledgeID == "" || fromKnowledgeID == toKnowledgeID {
		return domain.KnowledgeRelation{}, ErrInvalidState
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("begin conflict relation: %w", err)
	}
	defer tx.Rollback()
	for _, id := range []string{fromKnowledgeID, toKnowledgeID} {
		var found string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM knowledge_items WHERE id = ?`, id).Scan(&found); errors.Is(err, sql.ErrNoRows) {
			return domain.KnowledgeRelation{}, ErrNotFound
		} else if err != nil {
			return domain.KnowledgeRelation{}, fmt.Errorf("read conflict knowledge: %w", err)
		}
	}
	var relation domain.KnowledgeRelation
	var createdAt string
	err = tx.QueryRowContext(ctx, `SELECT id, from_knowledge_id, to_knowledge_id, kind, created_at FROM knowledge_relations WHERE from_knowledge_id = ? AND to_knowledge_id = ? AND kind = 'conflicts_with'`, fromKnowledgeID, toKnowledgeID).Scan(&relation.ID, &relation.FromKnowledgeID, &relation.ToKnowledgeID, &relation.Kind, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		now := time.Now().UTC()
		relation.ID, err = domain.NewID(now)
		if err != nil {
			return domain.KnowledgeRelation{}, err
		}
		relation.FromKnowledgeID, relation.ToKnowledgeID, relation.Kind, relation.CreatedAt = fromKnowledgeID, toKnowledgeID, "conflicts_with", now
		if _, err = tx.ExecContext(ctx, `INSERT INTO knowledge_relations(id, from_knowledge_id, to_knowledge_id, kind, created_at) VALUES (?, ?, ?, 'conflicts_with', ?)`, relation.ID, relation.FromKnowledgeID, relation.ToKnowledgeID, now.Format(time.RFC3339Nano)); err != nil {
			return domain.KnowledgeRelation{}, fmt.Errorf("create conflict relation: %w", err)
		}
	} else if err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("read conflict relation: %w", err)
	} else if relation.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("parse conflict creation time: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE knowledge_items SET state = 'conflicted' WHERE id IN (?, ?)`, fromKnowledgeID, toKnowledgeID); err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("mark conflicted knowledge: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("commit conflict relation: %w", err)
	}
	return relation, nil
}

func (store *Store) GetCandidate(ctx context.Context, id string) (domain.Candidate, error) {
	row := store.db.QueryRowContext(ctx, `SELECT id, ingestion_id, ordinal, version, content, title_path_json, state, COALESCE(promoted_knowledge_id, ''), updated_at FROM candidate_items WHERE id = ?`, id)
	item, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Candidate{}, ErrNotFound
	}
	return item, err
}

func (store *Store) UpdateCandidate(ctx context.Context, id string, expectedVersion int, content string) (domain.Candidate, error) {
	if strings.TrimSpace(content) == "" {
		return domain.Candidate{}, fmt.Errorf("candidate content is empty")
	}
	result, err := store.db.ExecContext(ctx, `UPDATE candidate_items SET content = ?, version = version + 1, state = 'editing', updated_at = ? WHERE id = ? AND version = ? AND state IN ('proposed', 'editing')`, content, time.Now().UTC().Format(time.RFC3339Nano), id, expectedVersion)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("update candidate: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		if _, err := store.GetCandidate(ctx, id); errors.Is(err, ErrNotFound) {
			return domain.Candidate{}, ErrNotFound
		}
		return domain.Candidate{}, ErrVersionConflict
	}
	return store.GetCandidate(ctx, id)
}

func (store *Store) RejectCandidate(ctx context.Context, id string, expectedVersion int) (domain.Candidate, error) {
	result, err := store.db.ExecContext(ctx, `UPDATE candidate_items SET version = version + 1, state = 'rejected', updated_at = ? WHERE id = ? AND version = ? AND state IN ('proposed', 'editing')`, time.Now().UTC().Format(time.RFC3339Nano), id, expectedVersion)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("reject candidate: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		if _, err := store.GetCandidate(ctx, id); errors.Is(err, ErrNotFound) {
			return domain.Candidate{}, ErrNotFound
		}
		return domain.Candidate{}, ErrInvalidState
	}
	return store.GetCandidate(ctx, id)
}

func (store *Store) SplitCandidate(ctx context.Context, id string, expectedVersion int, parts []string) ([]domain.Candidate, error) {
	parts = normalizedCandidateParts(parts)
	if len(parts) < 2 {
		return nil, fmt.Errorf("at least two non-empty candidate parts are required")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin candidate split: %w", err)
	}
	defer tx.Rollback()
	candidate, err := getCandidateTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if !candidateEditable(candidate.State) {
		return nil, ErrInvalidState
	}
	if candidate.Version != expectedVersion {
		return nil, ErrVersionConflict
	}
	now := time.Now().UTC()
	shift := len(parts) - 1
	if _, err = tx.ExecContext(ctx, `UPDATE candidate_items SET ordinal = -ordinal - 1 WHERE ingestion_id = ? AND ordinal > ?`, candidate.IngestionID, candidate.Ordinal); err != nil {
		return nil, fmt.Errorf("temporarily shift candidate ordinals: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE candidate_items SET ordinal = -ordinal - 1 + ? WHERE ingestion_id = ? AND ordinal < 0`, shift, candidate.IngestionID); err != nil {
		return nil, fmt.Errorf("restore candidate ordinals: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE candidate_items SET content = ?, version = version + 1, state = 'editing', updated_at = ? WHERE id = ? AND version = ? AND state IN ('proposed', 'editing')`, parts[0], now.Format(time.RFC3339Nano), candidate.ID, expectedVersion)
	if err != nil {
		return nil, fmt.Errorf("update split candidate: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return nil, ErrVersionConflict
	}
	candidate.Content = parts[0]
	candidate.Version++
	candidate.State = domain.CandidateEditing
	candidate.UpdatedAt = now
	created := []domain.Candidate{candidate}
	titlePath, err := json.Marshal(candidate.TitlePath)
	if err != nil {
		return nil, fmt.Errorf("marshal split candidate title path: %w", err)
	}
	for index, content := range parts[1:] {
		item := candidate
		item.ID, err = domain.NewID(now)
		if err != nil {
			return nil, err
		}
		item.Ordinal = candidate.Ordinal + index + 1
		item.Version = 1
		item.Content = content
		item.State = domain.CandidateProposed
		item.PromotedKnowledgeID = ""
		if _, err = tx.ExecContext(ctx, `INSERT INTO candidate_items(id, ingestion_id, ordinal, version, content, title_path_json, state, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, item.ID, item.IngestionID, item.Ordinal, item.Version, item.Content, titlePath, item.State, now.Format(time.RFC3339Nano)); err != nil {
			return nil, fmt.Errorf("insert split candidate: %w", err)
		}
		created = append(created, item)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit candidate split: %w", err)
	}
	return created, nil
}

func (store *Store) MergeCandidates(ctx context.Context, candidates []domain.CandidateVersion) (domain.Candidate, error) {
	if len(candidates) < 2 {
		return domain.Candidate{}, fmt.Errorf("at least two candidates are required to merge")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("begin candidate merge: %w", err)
	}
	defer tx.Rollback()
	items := make([]domain.Candidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, reference := range candidates {
		if reference.ID == "" || reference.ExpectedVersion < 1 {
			return domain.Candidate{}, fmt.Errorf("candidate ID and expected version are required")
		}
		if _, duplicate := seen[reference.ID]; duplicate {
			return domain.Candidate{}, fmt.Errorf("candidate IDs must be unique")
		}
		seen[reference.ID] = struct{}{}
		item, err := getCandidateTx(ctx, tx, reference.ID)
		if err != nil {
			return domain.Candidate{}, err
		}
		if !candidateEditable(item.State) {
			return domain.Candidate{}, ErrInvalidState
		}
		if item.Version != reference.ExpectedVersion {
			return domain.Candidate{}, ErrVersionConflict
		}
		if len(items) > 0 && items[0].IngestionID != item.IngestionID {
			return domain.Candidate{}, fmt.Errorf("candidates must share an ingestion")
		}
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Ordinal < items[right].Ordinal })
	contents := make([]string, 0, len(items))
	for _, item := range items {
		contents = append(contents, item.Content)
	}
	now := time.Now().UTC()
	primary := items[0]
	mergedContent := strings.Join(contents, "\n\n")
	result, err := tx.ExecContext(ctx, `UPDATE candidate_items SET content = ?, version = version + 1, state = 'editing', updated_at = ? WHERE id = ? AND version = ? AND state IN ('proposed', 'editing')`, mergedContent, now.Format(time.RFC3339Nano), primary.ID, primary.Version)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("update merged candidate: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return domain.Candidate{}, ErrVersionConflict
	}
	for _, item := range items[1:] {
		result, err = tx.ExecContext(ctx, `UPDATE candidate_items SET state = 'superseded', version = version + 1, updated_at = ? WHERE id = ? AND version = ? AND state IN ('proposed', 'editing')`, now.Format(time.RFC3339Nano), item.ID, item.Version)
		if err != nil {
			return domain.Candidate{}, fmt.Errorf("supersede merged candidate: %w", err)
		}
		if count, _ := result.RowsAffected(); count != 1 {
			return domain.Candidate{}, ErrVersionConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return domain.Candidate{}, fmt.Errorf("commit candidate merge: %w", err)
	}
	primary.Content = mergedContent
	primary.Version++
	primary.State = domain.CandidateEditing
	primary.UpdatedAt = now
	return primary, nil
}

func normalizedCandidateParts(parts []string) []string {
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func candidateEditable(state domain.CandidateState) bool {
	return state == domain.CandidateProposed || state == domain.CandidateEditing
}

func (store *Store) RequestCandidateApproval(ctx context.Context, candidateID, caller string, expiresAt time.Time) (Approval, error) {
	candidate, err := store.GetCandidate(ctx, candidateID)
	if err != nil {
		return Approval{}, err
	}
	if candidate.State != domain.CandidateProposed && candidate.State != domain.CandidateEditing {
		return Approval{}, ErrInvalidState
	}
	if caller == "" || !expiresAt.After(time.Now().UTC()) {
		return Approval{}, fmt.Errorf("caller and future expiry are required")
	}
	id, err := domain.NewID(time.Now().UTC())
	if err != nil {
		return Approval{}, err
	}
	approval := Approval{ID: id, Action: "candidate.promote", TargetID: candidateID, State: "pending", ExpiresAt: expiresAt.UTC()}
	_, err = store.db.ExecContext(ctx, `INSERT INTO approval_requests(id, action, target_id, parameter_hash, caller, state, expires_at, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`, approval.ID, approval.Action, candidateID, actionHash(approval.Action, candidateID), caller, approval.ExpiresAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Approval{}, fmt.Errorf("create approval: %w", err)
	}
	return approval, nil
}

func (store *Store) ResolveApproval(ctx context.Context, approvalID, caller string, approve bool) (ApprovalResolution, error) {
	approval, err := store.getApproval(ctx, approvalID)
	if err != nil {
		return ApprovalResolution{}, err
	}
	if approval.State != "pending" || !approval.ExpiresAt.After(time.Now().UTC()) || caller == "" {
		return ApprovalResolution{}, ErrApprovalInvalid
	}
	state, token := "denied", ""
	if approve {
		state = "approved"
		token, err = domain.NewID(time.Now().UTC())
		if err != nil {
			return ApprovalResolution{}, err
		}
	}
	result, err := store.db.ExecContext(ctx, `UPDATE approval_requests SET state = ?, approval_token = ? WHERE id = ? AND state = 'pending' AND caller = ? AND expires_at > ?`, state, nullIfEmpty(token), approvalID, caller, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ApprovalResolution{}, fmt.Errorf("resolve approval: %w", err)
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ApprovalResolution{}, ErrApprovalInvalid
	}
	approval.State = state
	return ApprovalResolution{Approval: approval, Token: token}, nil
}

func (store *Store) PromoteCandidate(ctx context.Context, candidateID, approvalToken, caller string) (domain.Knowledge, domain.Revision, error) {
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("begin candidate promotion: %w", err)
	}
	defer tx.Rollback()
	candidate, err := getCandidateTx(ctx, tx, candidateID)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, err
	}
	if candidate.State == domain.CandidatePromoted {
		knowledge, revision, err := getPromotedKnowledgeTx(ctx, tx, candidate.PromotedKnowledgeID)
		return knowledge, revision, err
	}
	if candidate.State != domain.CandidateProposed && candidate.State != domain.CandidateEditing {
		return domain.Knowledge{}, domain.Revision{}, ErrInvalidState
	}
	result, err := tx.ExecContext(ctx, `UPDATE approval_requests SET state = 'consumed' WHERE action = 'candidate.promote' AND target_id = ? AND parameter_hash = ? AND caller = ? AND approval_token = ? AND state = 'approved' AND expires_at > ?`, candidateID, actionHash("candidate.promote", candidateID), caller, approvalToken, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("consume approval: %w", err)
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return domain.Knowledge{}, domain.Revision{}, ErrApprovalInvalid
	}
	now := time.Now().UTC()
	knowledgeID, err := domain.NewID(now)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, err
	}
	revisionID, err := domain.NewID(now)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, err
	}
	knowledge := domain.Knowledge{ID: knowledgeID, State: "active", CurrentRevisionID: revisionID, CreatedAt: now}
	revision := domain.Revision{ID: revisionID, KnowledgeID: knowledgeID, Content: candidate.Content, Reason: "candidate_promotion", State: "current", CreatedAt: now}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_items(id, state, current_revision_id, created_at) VALUES (?, ?, ?, ?)", knowledge.ID, knowledge.State, knowledge.CurrentRevisionID, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("insert knowledge: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_revisions(id, knowledge_id, parent_revision_id, content, reason, state, created_at) VALUES (?, ?, NULL, ?, ?, ?, ?)", revision.ID, revision.KnowledgeID, revision.Content, revision.Reason, revision.State, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("insert first revision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_fts(content, knowledge_id, revision_id) VALUES (?, ?, ?)", revision.Content, knowledge.ID, revision.ID); err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("index first revision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE candidate_items SET state = 'promoted', promoted_knowledge_id = ?, version = version + 1, updated_at = ? WHERE id = ?", knowledge.ID, now.Format(time.RFC3339Nano), candidateID); err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("mark candidate promoted: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("commit candidate promotion: %w", err)
	}
	return knowledge, revision, nil
}

func (store *Store) ReviseKnowledge(ctx context.Context, knowledgeID, expectedRevisionID, content, reason string) (domain.Revision, error) {
	if strings.TrimSpace(content) == "" || !validRevisionReason(reason) {
		return domain.Revision{}, fmt.Errorf("invalid revision content or reason")
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Revision{}, err
	}
	defer tx.Rollback()
	var currentID string
	if err = tx.QueryRowContext(ctx, "SELECT current_revision_id FROM knowledge_items WHERE id = ?", knowledgeID).Scan(&currentID); errors.Is(err, sql.ErrNoRows) {
		return domain.Revision{}, ErrNotFound
	} else if err != nil {
		return domain.Revision{}, fmt.Errorf("read current revision: %w", err)
	}
	if currentID != expectedRevisionID {
		return domain.Revision{}, ErrVersionConflict
	}
	now := time.Now().UTC()
	revisionID, err := domain.NewID(now)
	if err != nil {
		return domain.Revision{}, err
	}
	revision := domain.Revision{ID: revisionID, KnowledgeID: knowledgeID, ParentRevisionID: currentID, Content: content, Reason: reason, State: "current", CreatedAt: now}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_revisions SET state = 'historical' WHERE id = ? AND state = 'current'", currentID); err != nil {
		return domain.Revision{}, fmt.Errorf("archive current revision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_revisions(id, knowledge_id, parent_revision_id, content, reason, state, created_at) VALUES (?, ?, ?, ?, ?, 'current', ?)", revision.ID, knowledgeID, currentID, content, reason, now.Format(time.RFC3339Nano)); err != nil {
		return domain.Revision{}, fmt.Errorf("insert revision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM knowledge_fts WHERE revision_id = ?", currentID); err != nil {
		return domain.Revision{}, fmt.Errorf("remove historical search index: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_fts(content, knowledge_id, revision_id) VALUES (?, ?, ?)", revision.Content, knowledgeID, revision.ID); err != nil {
		return domain.Revision{}, fmt.Errorf("index revision: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE knowledge_items SET current_revision_id = ? WHERE id = ?", revision.ID, knowledgeID); err != nil {
		return domain.Revision{}, fmt.Errorf("set current revision: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return domain.Revision{}, fmt.Errorf("commit revision: %w", err)
	}
	return revision, nil
}

func (store *Store) importByIdempotency(ctx context.Context, key string) (ImportResult, bool, error) {
	var result ImportResult
	err := store.db.QueryRowContext(ctx, "SELECT id, source_id FROM ingestions WHERE idempotency_key = ?", key).Scan(&result.IngestionID, &result.SourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return ImportResult{}, false, nil
	}
	if err != nil {
		return ImportResult{}, false, fmt.Errorf("read idempotent import: %w", err)
	}
	items, err := store.ListCandidates(ctx, result.IngestionID)
	if err != nil {
		return ImportResult{}, false, err
	}
	result.Candidates = items
	return result, true, nil
}

func (store *Store) getApproval(ctx context.Context, id string) (Approval, error) {
	var approval Approval
	var expires string
	err := store.db.QueryRowContext(ctx, "SELECT id, action, target_id, state, expires_at FROM approval_requests WHERE id = ?", id).Scan(&approval.ID, &approval.Action, &approval.TargetID, &approval.State, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	if err != nil {
		return Approval{}, fmt.Errorf("read approval: %w", err)
	}
	approval.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return Approval{}, fmt.Errorf("parse approval expiry: %w", err)
	}
	return approval, nil
}

type candidateScanner interface {
	Scan(...any) error
}

func scanCandidate(scanner candidateScanner) (domain.Candidate, error) {
	var item domain.Candidate
	var pathJSON, updated string
	err := scanner.Scan(&item.ID, &item.IngestionID, &item.Ordinal, &item.Version, &item.Content, &pathJSON, &item.State, &item.PromotedKnowledgeID, &updated)
	if err != nil {
		return domain.Candidate{}, err
	}
	if err := json.Unmarshal([]byte(pathJSON), &item.TitlePath); err != nil {
		return domain.Candidate{}, fmt.Errorf("decode candidate title path: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return domain.Candidate{}, fmt.Errorf("parse candidate time: %w", err)
	}
	return item, nil
}

type revisionScanner interface {
	Scan(...any) error
}

func scanRevision(scanner revisionScanner) (domain.Revision, error) {
	var revision domain.Revision
	var parent sql.NullString
	var createdAt string
	if err := scanner.Scan(&revision.ID, &revision.KnowledgeID, &parent, &revision.Content, &revision.Reason, &revision.State, &createdAt); err != nil {
		return domain.Revision{}, err
	}
	var err error
	revision.ParentRevisionID = parent.String
	if revision.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return domain.Revision{}, fmt.Errorf("parse revision creation time: %w", err)
	}
	return revision, nil
}

func scanKnowledgeRelation(scanner revisionScanner) (domain.KnowledgeRelation, error) {
	var relation domain.KnowledgeRelation
	var createdAt string
	if err := scanner.Scan(&relation.ID, &relation.FromKnowledgeID, &relation.ToKnowledgeID, &relation.Kind, &createdAt); err != nil {
		return domain.KnowledgeRelation{}, err
	}
	var err error
	if relation.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return domain.KnowledgeRelation{}, fmt.Errorf("parse relation creation time: %w", err)
	}
	return relation, nil
}

func getCandidateTx(ctx context.Context, tx *sql.Tx, id string) (domain.Candidate, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, ingestion_id, ordinal, version, content, title_path_json, state, COALESCE(promoted_knowledge_id, ''), updated_at FROM candidate_items WHERE id = ?`, id)
	item, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Candidate{}, ErrNotFound
	}
	return item, err
}

func getPromotedKnowledgeTx(ctx context.Context, tx *sql.Tx, id string) (domain.Knowledge, domain.Revision, error) {
	var knowledge domain.Knowledge
	var created string
	err := tx.QueryRowContext(ctx, "SELECT id, state, current_revision_id, created_at FROM knowledge_items WHERE id = ?", id).Scan(&knowledge.ID, &knowledge.State, &knowledge.CurrentRevisionID, &created)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("read promoted knowledge: %w", err)
	}
	knowledge.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, err
	}
	var revision domain.Revision
	var parent sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT id, knowledge_id, parent_revision_id, content, reason, state, created_at FROM knowledge_revisions WHERE id = ?", knowledge.CurrentRevisionID).Scan(&revision.ID, &revision.KnowledgeID, &parent, &revision.Content, &revision.Reason, &revision.State, &created)
	if err != nil {
		return domain.Knowledge{}, domain.Revision{}, fmt.Errorf("read promoted revision: %w", err)
	}
	revision.ParentRevisionID = parent.String
	revision.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	return knowledge, revision, err
}

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func actionHash(action, targetID string) string {
	sum := sha256.Sum256([]byte(action + "\x00" + targetID))
	return hex.EncodeToString(sum[:])
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func validRevisionReason(value string) bool {
	switch value {
	case "typo", "format", "entry_error", "opinion_change", "fact_update", "time_change", "correction":
		return true
	default:
		return false
	}
}
