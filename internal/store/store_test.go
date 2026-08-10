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

	expectedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve expected data directory: %v", err)
	}
	if health := core.Health(); !health.Ready || health.SchemaVersion != 7 || health.DataDir != expectedRoot {
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
	if err != nil || len(entries) != 6 {
		t.Fatalf("expected six migration backups, entries=%d err=%v", len(entries), err)
	}

	core, err = Open(context.Background(), root)
	if err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	defer core.Close()
	entries, err = os.ReadDir(filepath.Join(root, "backups"))
	if err != nil || len(entries) != 6 {
		t.Fatalf("migration must not repeat, entries=%d err=%v", len(entries), err)
	}
}

func TestCandidateStateMigrationPreservesExistingRows(t *testing.T) {
	root := t.TempDir()
	legacy, err := openWithMigrations(context.Background(), root, defaultMigrations[:6])
	if err != nil {
		t.Fatalf("open schema six store: %v", err)
	}
	now := "2026-08-09T00:00:00Z"
	if _, err := legacy.DB().Exec(`INSERT INTO source_documents(id, kind, content, content_hash, input_at) VALUES ('source', 'text', 'Original', 'hash', ?);
INSERT INTO ingestions(id, source_id, idempotency_key, state, created_at) VALUES ('ingestion', 'source', 'legacy-key', 'candidates_ready', ?);
INSERT INTO candidate_items(id, ingestion_id, ordinal, version, content, title_path_json, state, updated_at) VALUES ('candidate', 'ingestion', 0, 1, 'Candidate', '[]', 'proposed', ?);`, now, now, now); err != nil {
		t.Fatalf("seed schema six candidate: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close schema six store: %v", err)
	}
	migrated, err := Open(context.Background(), root)
	if err != nil {
		t.Fatalf("migrate candidate state: %v", err)
	}
	defer migrated.Close()
	item, err := migrated.GetCandidate(context.Background(), "candidate")
	if err != nil || item.State != "proposed" {
		t.Fatalf("migrated candidate = %#v, %v", item, err)
	}
	if _, err := migrated.DB().Exec(`UPDATE candidate_items SET state = 'superseded' WHERE id = 'candidate'`); err != nil {
		t.Fatalf("superseded state should be accepted: %v", err)
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
