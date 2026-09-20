// Package store persists imports, mapping decisions, previews and published
// graphs in SQLite. Publication runs inside a single transaction so a failure
// never leaves a partially migrated graph behind.
package store

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;"); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS imports (
  kind        TEXT NOT NULL,
  graph_iri   TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  loaded_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  PRIMARY KEY (kind, graph_iri)
);

CREATE TABLE IF NOT EXISTS mapping_state (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  version     INTEGER NOT NULL,
  payload     TEXT NOT NULL,
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS previews (
  preview_fp  TEXT PRIMARY KEY,
  content_fp  TEXT NOT NULL,
  rule_fp     TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  payload     TEXT NOT NULL,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS publications (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  preview_fp  TEXT NOT NULL,
  content_fp  TEXT NOT NULL,
  rule_fp     TEXT NOT NULL,
  rule_version INTEGER NOT NULL,
  graph_iri   TEXT NOT NULL,
  quads_json  TEXT NOT NULL,
  status      TEXT NOT NULL,
  published_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS affected_decisions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  round       INTEGER NOT NULL,
  payload     TEXT NOT NULL,
  resolved    INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
`

// HasImport deduplicates an import by (kind, content fingerprint).
func (s *Store) HasImport(ctx context.Context, kind, graphIRI, fp string) (bool, error) {
	var existing string
	err := s.db.QueryRowContext(ctx,
		`SELECT fingerprint FROM imports WHERE kind=? AND graph_iri=?`, kind, graphIRI).
		Scan(&existing)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return existing == fp, nil
}

func (s *Store) RecordImport(ctx context.Context, kind, graphIRI, fp string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO imports(kind, graph_iri, fingerprint)
		 VALUES(?,?,?)
		 ON CONFLICT(kind, graph_iri) DO UPDATE SET fingerprint=excluded.fingerprint,
		   loaded_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		kind, graphIRI, fp)
	return err
}

// ---- mapping state ----

func (s *Store) LoadMappingState(ctx context.Context) (version int, payload string, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT version, payload FROM mapping_state WHERE id=1`).Scan(&version, &payload)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return version, payload, err
}

func (s *Store) SaveMappingState(ctx context.Context, version int, payload string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mapping_state(id, version, payload)
		 VALUES(1,?,?)
		 ON CONFLICT(id) DO UPDATE SET version=excluded.version, payload=excluded.payload,
		   updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		version, payload)
	return err
}

// ---- previews ----

type PreviewRow struct {
	PreviewFP   string
	ContentFP   string
	RuleFP      string
	RuleVersion int
	Payload     string
}

func (s *Store) SavePreview(ctx context.Context, r PreviewRow) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO previews(preview_fp, content_fp, rule_fp, rule_version, payload)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(preview_fp) DO NOTHING`,
		r.PreviewFP, r.ContentFP, r.RuleFP, r.RuleVersion, r.Payload)
	return err
}

func (s *Store) GetPreview(ctx context.Context, fp string) (*PreviewRow, error) {
	row := &PreviewRow{}
	err := s.db.QueryRowContext(ctx,
		`SELECT preview_fp, content_fp, rule_fp, rule_version, payload FROM previews WHERE preview_fp=?`,
		fp).Scan(&row.PreviewFP, &row.ContentFP, &row.RuleFP, &row.RuleVersion, &row.Payload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

// ---- publication (atomic) ----

type Publication struct {
	ID          int64
	PreviewFP   string
	ContentFP   string
	RuleFP      string
	RuleVersion int
	GraphIRI    string
	QuadsJSON   string
}

// BeforeCommit is invoked inside the publication transaction just before COMMIT
// so tests (and failure injection) can prove no partial graph is persisted.
type BeforeCommit func(tx *sql.Tx) error

// Publish inserts the publication record in one transaction. If beforeCommit
// fails, everything rolls back, leaving no migrated graph.
func (s *Store) Publish(ctx context.Context, p Publication, hook BeforeCommit) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	// Idempotency: same content+rules fingerprint published before -> reuse.
	var existing int64
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM publications
		 WHERE content_fp=? AND rule_fp=? AND status='published'
		 ORDER BY id DESC LIMIT 1`, p.ContentFP, p.RuleFP).Scan(&existing)
	if err == nil {
		if hook != nil {
			if err := hook(tx); err != nil {
				return 0, err
			}
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		tx = nil
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO publications(preview_fp, content_fp, rule_fp, rule_version, graph_iri, quads_json, status)
		 VALUES(?,?,?,?,?,?,'published')`,
		p.PreviewFP, p.ContentFP, p.RuleFP, p.RuleVersion, p.GraphIRI, p.QuadsJSON)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if hook != nil {
		if err := hook(tx); err != nil {
			return 0, fmt.Errorf("publication aborted pre-commit: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return id, nil
}

func (s *Store) CountPublications(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM publications WHERE status='published'`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) LatestPublication(ctx context.Context) (*Publication, error) {
	p := &Publication{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, preview_fp, content_fp, rule_fp, rule_version, graph_iri, quads_json
		 FROM publications WHERE status='published' ORDER BY id DESC LIMIT 1`).
		Scan(&p.ID, &p.PreviewFP, &p.ContentFP, &p.RuleFP, &p.RuleVersion, &p.GraphIRI, &p.QuadsJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}
