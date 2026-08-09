package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type RPCIdempotencyRecord struct {
	ParametersHash string
	ResultJSON     string
}

func (store *Store) GetRPCIdempotencyRecord(ctx context.Context, caller, method, key string) (RPCIdempotencyRecord, bool, error) {
	var record RPCIdempotencyRecord
	err := store.db.QueryRowContext(ctx, `SELECT parameters_hash, result_json FROM rpc_idempotency_records WHERE caller = ? AND method = ? AND idempotency_key = ?`, caller, method, key).Scan(&record.ParametersHash, &record.ResultJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return RPCIdempotencyRecord{}, false, nil
	}
	if err != nil {
		return RPCIdempotencyRecord{}, false, fmt.Errorf("get RPC idempotency record: %w", err)
	}
	return record, true, nil
}

func (store *Store) SaveRPCIdempotencyRecord(ctx context.Context, caller, method, key, parametersHash, resultJSON string) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO rpc_idempotency_records(caller, method, idempotency_key, parameters_hash, result_json, completed_at) VALUES (?, ?, ?, ?, ?, ?)`, caller, method, key, parametersHash, resultJSON, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save RPC idempotency record: %w", err)
	}
	return nil
}
