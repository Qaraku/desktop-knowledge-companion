package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"desktop-knowledge-companion/internal/domain"
)

func (store *Store) RequestToolApproval(ctx context.Context, toolName, caller, parameters string, expiresAt time.Time) (Approval, error) {
	if toolName == "" || caller == "" || !expiresAt.After(time.Now().UTC()) {
		return Approval{}, fmt.Errorf("tool, caller, and future expiry are required")
	}
	id, err := domain.NewID(time.Now().UTC())
	if err != nil {
		return Approval{}, err
	}
	action := "agent.tool.execute"
	approval := Approval{ID: id, Action: action, TargetID: toolName, State: "pending", ExpiresAt: expiresAt.UTC()}
	_, err = store.db.ExecContext(ctx, `INSERT INTO approval_requests(id, action, target_id, parameter_hash, caller, state, expires_at, created_at) VALUES (?, ?, ?, ?, ?, 'pending', ?, ?)`, id, action, toolName, parameterHash(parameters), caller, approval.ExpiresAt.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return Approval{}, fmt.Errorf("create tool approval: %w", err)
	}
	return approval, nil
}

func (store *Store) ConsumeToolApproval(ctx context.Context, toolName, caller, parameters, token string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE approval_requests SET state = 'consumed' WHERE action = 'agent.tool.execute' AND target_id = ? AND parameter_hash = ? AND caller = ? AND approval_token = ? AND state = 'approved' AND expires_at > ?`, toolName, parameterHash(parameters), caller, token, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("consume tool approval: %w", err)
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ErrApprovalInvalid
	}
	return nil
}

type ToolAuditEvent struct {
	ID        string    `json:"id"`
	ToolName  string    `json:"tool_name"`
	RiskLevel string    `json:"risk_level"`
	State     string    `json:"state"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

func (store *Store) RecordToolEvent(ctx context.Context, toolName, riskLevel, state, parameters, detail string) (ToolAuditEvent, error) {
	now := time.Now().UTC()
	id, err := domain.NewID(now)
	if err != nil {
		return ToolAuditEvent{}, err
	}
	sum := sha256.Sum256([]byte(parameters))
	event := ToolAuditEvent{ID: id, ToolName: toolName, RiskLevel: riskLevel, State: state, Detail: detail, CreatedAt: now}
	_, err = store.db.ExecContext(ctx, `INSERT INTO agent_tool_events(id, tool_name, risk_level, state, parameter_hash, detail, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, toolName, riskLevel, state, hex.EncodeToString(sum[:]), detail, now.Format(time.RFC3339Nano))
	if err != nil {
		return ToolAuditEvent{}, fmt.Errorf("record tool event: %w", err)
	}
	return event, nil
}

func (store *Store) SetPromptPreference(ctx context.Context, topic, state string, deferredUntil *time.Time) error {
	if topic == "" || (state != "ignored" && state != "deferred" && state != "closed") {
		return fmt.Errorf("invalid prompt preference")
	}
	var until any
	if deferredUntil != nil {
		until = deferredUntil.UTC().Format(time.RFC3339Nano)
	}
	_, err := store.db.ExecContext(ctx, `INSERT INTO prompt_preferences(topic, state, deferred_until, updated_at) VALUES (?, ?, ?, ?) ON CONFLICT(topic) DO UPDATE SET state = excluded.state, deferred_until = excluded.deferred_until, updated_at = excluded.updated_at`, topic, state, until, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (store *Store) GetPromptPreference(ctx context.Context, topic string) (string, *time.Time, error) {
	var state string
	var deferred sql.NullString
	err := store.db.QueryRowContext(ctx, "SELECT state, deferred_until FROM prompt_preferences WHERE topic = ?", topic).Scan(&state, &deferred)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	if !deferred.Valid {
		return state, nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, deferred.String)
	if err != nil {
		return "", nil, err
	}
	return state, &value, nil
}

func parameterHash(parameters string) string {
	sum := sha256.Sum256([]byte(parameters))
	return hex.EncodeToString(sum[:])
}
