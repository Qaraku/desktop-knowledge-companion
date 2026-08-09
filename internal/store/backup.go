package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func backupBeforeMigration(ctx context.Context, db *sql.DB, root string, version int) error {
	databasePath := filepath.Join(root, "knowledge.db")
	info, err := os.Stat(databasePath)
	if errorsIsNotExist(err) || (err == nil && info.Size() == 0) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat database before migration: %w", err)
	}

	backupPath := filepath.Join(root, "backups", fmt.Sprintf("knowledge-before-v%d-%s.db", version, time.Now().UTC().Format("20060102T150405.000000000Z")))
	quotedPath := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := db.ExecContext(ctx, "VACUUM INTO '"+quotedPath+"'"); err != nil {
		return fmt.Errorf("create pre-migration backup: %w", err)
	}
	return nil
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
