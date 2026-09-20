package store

import (
	"context"
	"database/sql"
	"errors"

	"gsb/internal/rdf"
)

var ErrDuplicate = errors.New("content already imported")

// Document is a stored parsed fixture.
type Document struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Format    string     `json:"format"`
	GraphIRI  string     `json:"graph_iri"`
	ContentFP string     `json:"content_fp"`
	Body      string     `json:"body"`
	Quads     []rdf.Quad `json:"-"`
}

// ImportDocument stores a parsed document.  Duplicate content (same
// fingerprint and kind) is rejected with ErrDuplicate so repeated
// imports are idempotent instead of stacking copies.
func (s *Store) ImportDocument(ctx context.Context, d *Document) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existing int64
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM documents WHERE content_fp = ? AND kind = ?`,
		d.ContentFP, d.Kind).Scan(&existing)
	if err != nil {
		return false, err
	}
	if existing > 0 {
		return false, nil
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO documents (kind,name,format,graph_iri,content_fp,body)
		 VALUES (?,?,?,?,?,?)`,
		d.Kind, d.Name, d.Format, d.GraphIRI, d.ContentFP, d.Body)
	if err != nil {
		return false, err
	}
	id, _ := res.LastInsertId()
	for i, q := range d.Quads {
		g := ""
		if q.Graph.Kind == rdf.KindIRI {
			g = q.Graph.Value
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO quads (document_id,graph_iri,subject,predicate,object,seq)
			 VALUES (?,?,?,?,?,?)`,
			id, g, q.Subject.String(), q.Predicate.String(), q.Object.String(), i); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	d.ID = id
	return true, nil
}

// ListDocuments returns imported document metadata.
func (s *Store) ListDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,kind,name,format,graph_iri,content_fp FROM documents ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Kind, &d.Name, &d.Format, &d.GraphIRI, &d.ContentFP); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DocumentByKind loads the most recent document of a kind with its quads.
func (s *Store) DocumentByKind(ctx context.Context, kind string) (*Document, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,kind,name,format,graph_iri,content_fp,body FROM documents
		 WHERE kind=? ORDER BY id DESC LIMIT 1`, kind)
	var d Document
	if err := row.Scan(&d.ID, &d.Kind, &d.Name, &d.Format, &d.GraphIRI, &d.ContentFP, &d.Body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	qs, err := s.quadsFor(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	d.Quads = qs
	return &d, nil
}

// QuadsByKind returns parsed quads for the latest document of a kind.
func (s *Store) QuadsByKind(ctx context.Context, kind string) ([]rdf.Quad, string, error) {
	d, err := s.DocumentByKind(ctx, kind)
	if err != nil || d == nil {
		return nil, "", err
	}
	return d.Quads, d.ContentFP, nil
}

func (s *Store) quadsFor(ctx context.Context, id int64) ([]rdf.Quad, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT graph_iri,subject,predicate,object FROM quads WHERE document_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rdf.Quad
	for rows.Next() {
		var g, sub, pred, obj string
		if err := rows.Scan(&g, &sub, &pred, &obj); err != nil {
			return nil, err
		}
		gt := rdf.DefaultGraph()
		if g != "" {
			gt = rdf.IRI(g)
		}
		st, err := parseTerm(sub)
		if err != nil {
			return nil, err
		}
		pt, err := parseTerm(pred)
		if err != nil {
			return nil, err
		}
		ot, err := parseTerm(obj)
		if err != nil {
			return nil, err
		}
		out = append(out, rdf.Quad{Graph: gt, Subject: st, Predicate: pt, Object: ot})
	}
	return out, rows.Err()
}

// QuadsByKindAll returns parsed quads across every document of a kind
// (e.g. several instance-graph fixtures), preserving each document's
// named graph provenance.
func (s *Store) QuadsByKindAll(ctx context.Context, kind string) ([]rdf.Quad, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM documents WHERE kind=? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []rdf.Quad
	for _, id := range ids {
		qs, err := s.quadsFor(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, qs...)
	}
	return out, nil
}
