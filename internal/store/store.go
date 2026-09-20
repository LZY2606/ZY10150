package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store is the SQLite persistence boundary.  Every import is deduped by
// content fingerprint and publishing happens in one transaction, so a
// failed publish cannot leave a partially migrated graph behind.
type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, pragmaSQL); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateDB(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

const pragmaSQL = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
`

const schemaSQL = `
CREATE TABLE IF NOT EXISTS documents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,           -- ontology_old|ontology_new|instances|shapes|candidates
	name TEXT NOT NULL,
	format TEXT NOT NULL,        -- turtle|trig|jsonld
	graph_iri TEXT NOT NULL DEFAULT '',
	content_fp TEXT NOT NULL UNIQUE,
	body TEXT NOT NULL,
	imported_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS quads (
	document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	graph_iri TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL,
	predicate TEXT NOT NULL,
	object TEXT NOT NULL,
	seq INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_quads_doc ON quads(document_id);

CREATE TABLE IF NOT EXISTS mapping_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL DEFAULT 0,
	version_fp TEXT NOT NULL DEFAULT '',
	old_fp TEXT NOT NULL DEFAULT '',
	new_fp TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO mapping_state (id, version, version_fp) VALUES (1, 0, '');

CREATE TABLE IF NOT EXISTS mapping_rules (
	rule_fp TEXT PRIMARY KEY,
	id TEXT NOT NULL,
	term_kind TEXT NOT NULL,
	relation TEXT NOT NULL,
	source TEXT NOT NULL,
	status TEXT NOT NULL,
	rationale TEXT NOT NULL DEFAULT '',
	accepted_version INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS mapping_targets (
	rule_fp TEXT NOT NULL REFERENCES mapping_rules(rule_fp) ON DELETE CASCADE,
	pos INTEGER NOT NULL,
	target TEXT NOT NULL,
	PRIMARY KEY (rule_fp, pos)
);
CREATE TABLE IF NOT EXISTS mapping_branches (
	rule_fp TEXT NOT NULL REFERENCES mapping_rules(rule_fp) ON DELETE CASCADE,
	pos INTEGER NOT NULL,
	target TEXT NOT NULL,
	predicate TEXT NOT NULL DEFAULT '',
	value TEXT NOT NULL DEFAULT '',
	value_iri INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (rule_fp, pos)
);
CREATE TABLE IF NOT EXISTS mapping_evidence (
	rule_fp TEXT NOT NULL REFERENCES mapping_rules(rule_fp) ON DELETE CASCADE,
	pos INTEGER NOT NULL,
	etype TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	quad TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (rule_fp, pos)
);

CREATE TABLE IF NOT EXISTS previews (
	content_fp TEXT PRIMARY KEY,   -- normalized migrated graph fingerprint
	source_fp TEXT NOT NULL,
	rule_fp TEXT NOT NULL,
	mapping_version INTEGER NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS preview_steps (
	content_fp TEXT NOT NULL REFERENCES previews(content_fp) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	step_kind TEXT NOT NULL,
	mapping_id TEXT NOT NULL DEFAULT '',
	original TEXT NOT NULL,
	derived TEXT NOT NULL DEFAULT '',
	explanation TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS diagnostics (
	content_fp TEXT NOT NULL,
	fp TEXT NOT NULL,
	category TEXT NOT NULL,
	shape TEXT NOT NULL DEFAULT '',
	node TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	value TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	mapping_cause TEXT NOT NULL DEFAULT '',
	evidence TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (content_fp, fp)
);

CREATE TABLE IF NOT EXISTS publishes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	content_fp TEXT NOT NULL UNIQUE,
	source_fp TEXT NOT NULL,
	rule_fp TEXT NOT NULL,
	mapping_version INTEGER NOT NULL,
	status TEXT NOT NULL,          -- published|failed
	created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS published_quads (
	publish_id INTEGER NOT NULL REFERENCES publishes(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL,
	graph_iri TEXT NOT NULL,
	subject TEXT NOT NULL,
	predicate TEXT NOT NULL,
	object TEXT NOT NULL
);
`

func migrateDB(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	return nil
}
