package store

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

var defaultMigrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);`,
	},
	{
		version: 2,
		sql: `
CREATE TABLE core_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL
);`,
	},
	{
		version: 3,
		sql: `
CREATE TABLE source_documents (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL CHECK(kind IN ('text', 'markdown')),
  content TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  display_name TEXT,
  input_at TEXT NOT NULL
);
CREATE TABLE ingestions (
  id TEXT PRIMARY KEY,
  source_id TEXT NOT NULL REFERENCES source_documents(id),
  idempotency_key TEXT NOT NULL UNIQUE,
  state TEXT NOT NULL CHECK(state IN ('received', 'processing', 'candidates_ready', 'failed', 'cancelled')),
  created_at TEXT NOT NULL,
  error_summary TEXT
);
CREATE TABLE candidate_items (
  id TEXT PRIMARY KEY,
  ingestion_id TEXT NOT NULL REFERENCES ingestions(id),
  ordinal INTEGER NOT NULL,
  version INTEGER NOT NULL,
  content TEXT NOT NULL,
  title_path_json TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('proposed', 'editing', 'rejected', 'promoted')),
  promoted_knowledge_id TEXT,
  updated_at TEXT NOT NULL,
  UNIQUE(ingestion_id, ordinal)
);
CREATE TABLE knowledge_items (
  id TEXT PRIMARY KEY,
  state TEXT NOT NULL CHECK(state IN ('active', 'conflicted', 'archived')),
  current_revision_id TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE knowledge_revisions (
  id TEXT PRIMARY KEY,
  knowledge_id TEXT NOT NULL REFERENCES knowledge_items(id),
  parent_revision_id TEXT REFERENCES knowledge_revisions(id),
  content TEXT NOT NULL,
  reason TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('current', 'historical')),
  created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX one_current_revision_per_knowledge ON knowledge_revisions(knowledge_id) WHERE state = 'current';
CREATE TABLE approval_requests (
  id TEXT PRIMARY KEY,
  action TEXT NOT NULL,
  target_id TEXT NOT NULL,
  parameter_hash TEXT NOT NULL,
  caller TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('pending', 'approved', 'denied', 'consumed', 'expired')),
  approval_token TEXT UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE knowledge_relations (
  id TEXT PRIMARY KEY,
  from_knowledge_id TEXT NOT NULL REFERENCES knowledge_items(id),
  to_knowledge_id TEXT NOT NULL REFERENCES knowledge_items(id),
  kind TEXT NOT NULL CHECK(kind = 'conflicts_with'),
  created_at TEXT NOT NULL,
  CHECK(from_knowledge_id <> to_knowledge_id),
  UNIQUE(from_knowledge_id, to_knowledge_id, kind)
);`,
	},
	{
		version: 4,
		sql: `
CREATE VIRTUAL TABLE knowledge_fts USING fts5(
  content,
  knowledge_id UNINDEXED,
  revision_id UNINDEXED,
  tokenize = 'unicode61'
);
INSERT INTO knowledge_fts(content, knowledge_id, revision_id)
SELECT r.content, r.knowledge_id, r.id
FROM knowledge_revisions r
WHERE r.state = 'current';
CREATE TABLE query_runs (
  id TEXT PRIMARY KEY,
  question TEXT NOT NULL,
  mode TEXT NOT NULL CHECK(mode IN ('strict', 'augment', 'clarify')),
  knowledge_version INTEGER NOT NULL,
  profile_version TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('queued', 'running', 'completed', 'failed', 'cancelled')),
  answer TEXT,
  refusal_reason TEXT,
  created_at TEXT NOT NULL,
  completed_at TEXT
);
CREATE TABLE query_citations (
  run_id TEXT NOT NULL REFERENCES query_runs(id),
  ordinal INTEGER NOT NULL,
  origin TEXT NOT NULL CHECK(origin IN ('personal', 'web', 'model')),
  knowledge_id TEXT REFERENCES knowledge_items(id),
  revision_id TEXT REFERENCES knowledge_revisions(id),
  external_ref TEXT,
  excerpt TEXT NOT NULL,
  PRIMARY KEY(run_id, ordinal),
  CHECK((origin = 'personal' AND revision_id IS NOT NULL AND external_ref IS NULL) OR (origin = 'web' AND external_ref IS NOT NULL) OR (origin = 'model' AND revision_id IS NULL AND external_ref IS NULL))
);
CREATE TABLE trace_events (
  run_id TEXT NOT NULL REFERENCES query_runs(id),
  sequence INTEGER NOT NULL,
  stage TEXT NOT NULL,
  payload_json TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  PRIMARY KEY(run_id, sequence)
);
CREATE TRIGGER query_runs_terminal_immutable
BEFORE UPDATE ON query_runs
WHEN OLD.state IN ('completed', 'failed', 'cancelled')
BEGIN
  SELECT RAISE(ABORT, 'terminal query run is immutable');
END;
CREATE TRIGGER query_citations_terminal_immutable
BEFORE INSERT ON query_citations
WHEN (SELECT state FROM query_runs WHERE id = NEW.run_id) IN ('completed', 'failed', 'cancelled')
BEGIN
  SELECT RAISE(ABORT, 'terminal query citations are immutable');
END;
CREATE TRIGGER trace_events_terminal_immutable
BEFORE INSERT ON trace_events
WHEN (SELECT state FROM query_runs WHERE id = NEW.run_id) IN ('completed', 'failed', 'cancelled')
BEGIN
  SELECT RAISE(ABORT, 'terminal query trace is immutable');
END;`,
	},
	{
		version: 5,
		sql: `
CREATE TABLE agent_tool_events (
  id TEXT PRIMARY KEY,
  run_id TEXT REFERENCES query_runs(id),
  tool_name TEXT NOT NULL,
  risk_level TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('requested', 'denied', 'approval_required', 'executed')),
  parameter_hash TEXT NOT NULL,
  detail TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE TABLE prompt_preferences (
  topic TEXT PRIMARY KEY,
  state TEXT NOT NULL CHECK(state IN ('ignored', 'deferred', 'closed')),
  deferred_until TEXT,
  updated_at TEXT NOT NULL
);
CREATE TABLE pending_items (
  id TEXT PRIMARY KEY,
  topic TEXT NOT NULL,
  detail TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('open', 'ignored', 'deferred', 'closed')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);`,
	},
}

func currentSchemaVersion(migrations []migration) int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].version
}

func applyMigrations(ctx context.Context, db *sql.DB, root string, migrations []migration) error {
	for _, item := range migrations {
		applied, err := migrationApplied(ctx, db, item.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := backupBeforeMigration(ctx, db, root, item.version); err != nil {
			return err
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", item.version, err)
		}
		if _, err = tx.ExecContext(ctx, item.sql); err == nil {
			_, err = tx.ExecContext(ctx,
				"INSERT INTO schema_migrations(version, applied_at) VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))",
				item.version,
			)
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", item.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", item.version, err)
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var found int
	err := db.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = ?", version).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil && isMissingMigrationsTable(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read schema migrations: %w", err)
	}
	return true, nil
}

func isMissingMigrationsTable(err error) bool {
	return err != nil && (contains(err.Error(), "no such table: schema_migrations") || contains(err.Error(), "no such table: main.schema_migrations"))
}
