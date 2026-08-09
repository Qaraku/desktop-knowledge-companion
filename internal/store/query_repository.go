package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"desktop-knowledge-companion/internal/domain"
)

var queryTokenPattern = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

func (store *Store) RunLocalQuery(ctx context.Context, question, mode, profileVersion string) (domain.QueryRun, error) {
	if strings.TrimSpace(question) == "" || !validQueryMode(mode) || strings.TrimSpace(profileVersion) == "" {
		return domain.QueryRun{}, fmt.Errorf("question, mode, and profile version are required")
	}
	now := time.Now().UTC()
	runID, err := domain.NewID(now)
	if err != nil {
		return domain.QueryRun{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.QueryRun{}, fmt.Errorf("begin query run: %w", err)
	}
	defer tx.Rollback()
	var knowledgeVersion int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM knowledge_revisions WHERE state = 'current'").Scan(&knowledgeVersion); err != nil {
		return domain.QueryRun{}, fmt.Errorf("freeze knowledge version: %w", err)
	}
	run := domain.QueryRun{ID: runID, Question: question, Mode: mode, KnowledgeVersion: knowledgeVersion, ProfileVersion: profileVersion, State: "running", CreatedAt: now}
	if _, err = tx.ExecContext(ctx, "INSERT INTO query_runs(id, question, mode, knowledge_version, profile_version, state, created_at) VALUES (?, ?, ?, ?, ?, 'queued', ?)", run.ID, run.Question, run.Mode, run.KnowledgeVersion, run.ProfileVersion, now.Format(time.RFC3339Nano)); err != nil {
		return domain.QueryRun{}, fmt.Errorf("create query run: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE query_runs SET state = 'running' WHERE id = ?", run.ID); err != nil {
		return domain.QueryRun{}, fmt.Errorf("start query run: %w", err)
	}
	if err = insertTrace(ctx, tx, run.ID, 1, "retrieval_started", map[string]any{"query": question, "knowledge_version": knowledgeVersion}, now); err != nil {
		return domain.QueryRun{}, err
	}

	hits, err := findHits(ctx, tx, question)
	if err != nil {
		return domain.QueryRun{}, err
	}
	if err = insertTrace(ctx, tx, run.ID, 2, "retrieval_completed", map[string]any{"hit_count": len(hits)}, now); err != nil {
		return domain.QueryRun{}, err
	}
	for ordinal, hit := range hits {
		if _, err = tx.ExecContext(ctx, "INSERT INTO query_citations(run_id, ordinal, origin, knowledge_id, revision_id, excerpt) VALUES (?, ?, 'personal', ?, ?, ?)", run.ID, ordinal, hit.knowledgeID, hit.revisionID, hit.content); err != nil {
			return domain.QueryRun{}, fmt.Errorf("write query citation: %w", err)
		}
		run.Citations = append(run.Citations, domain.Citation{Ordinal: ordinal, Origin: "personal", KnowledgeID: hit.knowledgeID, RevisionID: hit.revisionID, Excerpt: hit.content})
	}
	run.Trace = []domain.TraceEvent{
		{Sequence: 1, Stage: "retrieval_started", Payload: `{"knowledge_version":` + fmt.Sprint(knowledgeVersion) + `}`, OccurredAt: now},
		{Sequence: 2, Stage: "retrieval_completed", Payload: `{"hit_count":` + fmt.Sprint(len(hits)) + `}`, OccurredAt: now},
	}
	if len(hits) == 0 {
		switch mode {
		case "clarify":
			run.Answer = "个人知识不足；请补充相关背景或导入资料。"
		case "augment":
			run.Answer = "未配置补充来源；无法基于个人知识回答。"
		default:
			run.RefusalReason = "no_local_evidence"
		}
	} else {
		parts := make([]string, 0, len(hits))
		for _, hit := range hits {
			parts = append(parts, hit.content)
		}
		run.Answer = "根据个人知识：\n" + strings.Join(parts, "\n")
	}
	run.State = "completed"
	run.CompletedAt = now
	if _, err = tx.ExecContext(ctx, "UPDATE query_runs SET state = 'completed', answer = ?, refusal_reason = ?, completed_at = ? WHERE id = ?", nullIfEmpty(run.Answer), nullIfEmpty(run.RefusalReason), now.Format(time.RFC3339Nano), run.ID); err != nil {
		return domain.QueryRun{}, fmt.Errorf("complete query run: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return domain.QueryRun{}, fmt.Errorf("commit query run: %w", err)
	}
	return run, nil
}

func (store *Store) GetQueryRun(ctx context.Context, runID string) (domain.QueryRun, error) {
	var run domain.QueryRun
	var created string
	var completed sql.NullString
	var answer, refusal sql.NullString
	err := store.db.QueryRowContext(ctx, `SELECT id, question, mode, knowledge_version, profile_version, state, answer, refusal_reason, created_at, completed_at FROM query_runs WHERE id = ?`, runID).Scan(&run.ID, &run.Question, &run.Mode, &run.KnowledgeVersion, &run.ProfileVersion, &run.State, &answer, &refusal, &created, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.QueryRun{}, ErrNotFound
	}
	if err != nil {
		return domain.QueryRun{}, fmt.Errorf("read query run: %w", err)
	}
	run.Answer, run.RefusalReason = answer.String, refusal.String
	if run.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return domain.QueryRun{}, err
	}
	if completed.Valid {
		if run.CompletedAt, err = time.Parse(time.RFC3339Nano, completed.String); err != nil {
			return domain.QueryRun{}, err
		}
	}
	rows, err := store.db.QueryContext(ctx, "SELECT ordinal, origin, COALESCE(knowledge_id, ''), COALESCE(revision_id, ''), excerpt FROM query_citations WHERE run_id = ? ORDER BY ordinal", runID)
	if err != nil {
		return domain.QueryRun{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var citation domain.Citation
		if err := rows.Scan(&citation.Ordinal, &citation.Origin, &citation.KnowledgeID, &citation.RevisionID, &citation.Excerpt); err != nil {
			return domain.QueryRun{}, err
		}
		run.Citations = append(run.Citations, citation)
	}
	if err := rows.Err(); err != nil {
		return domain.QueryRun{}, err
	}
	traces, err := store.db.QueryContext(ctx, "SELECT sequence, stage, payload_json, occurred_at FROM trace_events WHERE run_id = ? ORDER BY sequence", runID)
	if err != nil {
		return domain.QueryRun{}, err
	}
	defer traces.Close()
	for traces.Next() {
		var trace domain.TraceEvent
		var occurred string
		if err := traces.Scan(&trace.Sequence, &trace.Stage, &trace.Payload, &occurred); err != nil {
			return domain.QueryRun{}, err
		}
		if trace.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred); err != nil {
			return domain.QueryRun{}, err
		}
		run.Trace = append(run.Trace, trace)
	}
	return run, traces.Err()
}

func (store *Store) CancelQueryRun(ctx context.Context, runID string) (domain.QueryRun, error) {
	result, err := store.db.ExecContext(ctx, `UPDATE query_runs SET state = 'cancelled', refusal_reason = 'cancelled', completed_at = ? WHERE id = ? AND state IN ('queued', 'running')`, time.Now().UTC().Format(time.RFC3339Nano), runID)
	if err != nil {
		return domain.QueryRun{}, fmt.Errorf("cancel query run: %w", err)
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		if _, err := store.GetQueryRun(ctx, runID); errors.Is(err, ErrNotFound) {
			return domain.QueryRun{}, ErrNotFound
		}
		return domain.QueryRun{}, ErrInvalidState
	}
	return store.GetQueryRun(ctx, runID)
}

func (store *Store) ListActiveQueryRuns(ctx context.Context) ([]domain.QueryRun, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id, question, mode, knowledge_version, profile_version, state, created_at FROM query_runs WHERE state IN ('queued', 'running') ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list active query runs: %w", err)
	}
	defer rows.Close()
	runs := make([]domain.QueryRun, 0)
	for rows.Next() {
		var run domain.QueryRun
		var createdAt string
		if err := rows.Scan(&run.ID, &run.Question, &run.Mode, &run.KnowledgeVersion, &run.ProfileVersion, &run.State, &createdAt); err != nil {
			return nil, fmt.Errorf("scan active query run: %w", err)
		}
		if run.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil {
			return nil, fmt.Errorf("parse active query creation time: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

type queryHit struct {
	knowledgeID string
	revisionID  string
	content     string
}

func findHits(ctx context.Context, tx *sql.Tx, question string) ([]queryHit, error) {
	query := ftsQuery(question)
	if query == "" {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT knowledge_id, revision_id, content FROM knowledge_fts WHERE knowledge_fts MATCH ? ORDER BY rank LIMIT 8", query)
	if err != nil {
		return nil, fmt.Errorf("FTS5 lookup: %w", err)
	}
	defer rows.Close()
	var hits []queryHit
	for rows.Next() {
		var hit queryHit
		if err := rows.Scan(&hit.knowledgeID, &hit.revisionID, &hit.content); err != nil {
			return nil, err
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func ftsQuery(question string) string {
	tokens := queryTokenPattern.FindAllString(question, -1)
	if len(tokens) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(tokens))
	for _, token := range tokens {
		quoted = append(quoted, `"`+strings.ReplaceAll(token, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " AND ")
}

func insertTrace(ctx context.Context, tx *sql.Tx, runID string, sequence int, stage string, payload any, occurredAt time.Time) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode trace payload: %w", err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO trace_events(run_id, sequence, stage, payload_json, occurred_at) VALUES (?, ?, ?, ?, ?)", runID, sequence, stage, encoded, occurredAt.Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("write trace event: %w", err)
	}
	return nil
}

func validQueryMode(mode string) bool {
	return mode == "strict" || mode == "augment" || mode == "clarify"
}
