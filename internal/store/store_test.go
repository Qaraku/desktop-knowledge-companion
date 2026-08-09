package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestValidateDataDirRejectsRelativePath(t *testing.T) {
	_, err := ValidateDataDir("relative")
	if !errors.Is(err, ErrDataDirNotAbsolute) {
		t.Fatalf("expected ErrDataDirNotAbsolute, got %v", err)
	}
}

func TestOpenCreatesManagedDirectoriesAndSQLiteFeatures(t *testing.T) {
	root := t.TempDir()
	core, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })

	if health := core.Health(); !health.Ready || health.SchemaVersion != 5 || health.DataDir != root {
		t.Fatalf("unexpected health: %#v", health)
	}
	for _, name := range []string{"knowledge.db", "backups", "cache", "core.lock"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("managed path %s missing: %v", name, err)
		}
	}
	if _, err := core.DB().Exec("CREATE VIRTUAL TABLE fts_probe USING fts5(content)"); err != nil {
		t.Fatalf("FTS5 must be available: %v", err)
	}
	var foreignKeys int
	if err := core.DB().QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign keys = %d, err = %v", foreignKeys, err)
	}
}

func TestSecondProcessIsRejectedAndCloseReleasesLock(t *testing.T) {
	root := t.TempDir()
	first, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	_, err = Open(context.Background(), root)
	if !errors.Is(err, ErrInstanceLocked) {
		t.Fatalf("expected process lock error, got %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}
	second, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("reopen after close: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second store: %v", err)
	}
}

func TestMigrationCreatesBackupAndDoesNotRepeat(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "knowledge.db")
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("open seed database: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL); INSERT INTO schema_migrations VALUES (1, 'seed')"); err != nil {
		t.Fatalf("seed migration: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	core, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("migrate seed database: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatalf("close migrated store: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "backups"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("expected four migration backups, entries=%d err=%v", len(entries), err)
	}

	core, err = Open(context.Background(), root)
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	defer core.Close()
	entries, err = os.ReadDir(filepath.Join(root, "backups"))
	if err != nil || len(entries) != 4 {
		t.Fatalf("migration must not repeat, entries=%d err=%v", len(entries), err)
	}
}

func TestFailedMigrationReleasesLockAndRollsBack(t *testing.T) {
	root := t.TempDir()
	migrations := []migration{
		{version: 1, sql: "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);"},
		{version: 2, sql: "CREATE TABLE broken ("},
	}
	_, err := openWithMigrations(context.Background(), root, migrations)
	if err == nil {
		t.Fatal("expected migration failure")
	}
	if _, err := os.Stat(filepath.Join(root, "core.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock must be released after migration failure: %v", err)
	}

	core, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("default migrations should recover: %v", err)
	}
	defer core.Close()
}
