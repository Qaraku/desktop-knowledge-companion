package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"desktop-knowledge-companion/internal/domain"
)

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
