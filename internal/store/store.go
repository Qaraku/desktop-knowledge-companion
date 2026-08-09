package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const CoreVersion = "0.1.0"

type Store struct {
	db     *sql.DB
	root   string
	lock   *processLock
	schema int
}

type Health struct {
	CoreVersion   string `json:"core_version"`
	SchemaVersion int    `json:"schema_version"`
	DataDir       string `json:"data_dir"`
	Ready         bool   `json:"ready"`
}

func Open(ctx context.Context, dataDir string) (*Store, error) {
	return openWithMigrations(ctx, dataDir, defaultMigrations)
}

func openWithMigrations(ctx context.Context, dataDir string, migrations []migration) (_ *Store, err error) {
	root, err := ValidateDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	for _, directory := range []string{"backups", "cache"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			return nil, fmt.Errorf("create %s directory: %w", directory, err)
		}
	}

	lock, err := acquireProcessLock(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = lock.release()
		}
	}()

	db, err := sql.Open("sqlite", filepath.Join(root, "knowledge.db"))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	defer func() {
		if err != nil {
			_ = db.Close()
		}
	}()
	if _, err = db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	if err = applyMigrations(ctx, db, root, migrations); err != nil {
		return nil, err
	}

	return &Store{db: db, root: root, lock: lock, schema: currentSchemaVersion(migrations)}, nil
}

func (store *Store) Health() Health {
	return Health{CoreVersion: CoreVersion, SchemaVersion: store.schema, DataDir: store.root, Ready: true}
}

func (store *Store) DB() *sql.DB {
	return store.db
}

func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	return errors.Join(store.db.Close(), store.lock.release())
}
