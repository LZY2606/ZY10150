package store

import (
	"context"
	"database/sql"
	"errors"

	"gsb/internal/migrate"
	"gsb/internal/rdf"
	"gsb/internal/shacl"
)

// SavePreview persists a preview keyed by the normalized migrated graph
// fingerprint, together with steps and diagnostics.  It is idempotent:
// the same content+rules fingerprint is stored once.
func (s *Store) SavePreview(ctx context.Context, rep *migrate.Report, viols []shacl.Violation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM previews WHERE content_fp=?`, rep.ContentFP).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO previews (content_fp,source_fp,rule_fp,mapping_version)
		 VALUES (?,?,?,?)`,
		rep.ContentFP, rep.SourceFP, rep.RuleFP, rep.MappingVersion); err != nil {
		return err
	}
	for i, st := range rep.Steps {
		derived := ""
		if st.Derived != nil {
			derived = st.Derived.String()
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO preview_steps (content_fp,seq,step_kind,mapping_id,original,derived,explanation)
			 VALUES (?,?,?,?,?,?,?)`,
			rep.ContentFP, i, st.Kind, st.MappingID, st.Original.String(), derived, st.Explanation); err != nil {
			return err
		}
	}
	for _, v := range viols {
		ev := joinEvidence(v.Evidence)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO diagnostics (content_fp,fp,category,shape,node,path,value,message,mapping_cause,evidence)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			rep.ContentFP, v.Fingerprint, v.Category, v.Shape, v.Node, v.Path,
			v.Value, v.Message, v.MappingCause, ev); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Publish commits a preview as the current published graph in a single
// transaction.  Any failure rolls back so no partially migrated graph is
// observable.  The same content fingerprint cannot be published twice.
func (s *Store) Publish(ctx context.Context, rep *migrate.Report) (int64, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM publishes WHERE content_fp=?`, rep.ContentFP).Scan(&n); err != nil {
		return 0, false, err
	}
	if n > 0 {
		return 0, false, nil
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO publishes (content_fp,source_fp,rule_fp,mapping_version,status)
		 VALUES (?,?,?,?, 'published')`,
		rep.ContentFP, rep.SourceFP, rep.RuleFP, rep.MappingVersion)
	if err != nil {
		return 0, false, err
	}
	id, _ := res.LastInsertId()
	for i, q := range rep.MigratedQuads {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO published_quads (publish_id,seq,graph_iri,subject,predicate,object)
			 VALUES (?,?,?,?,?,?)`,
			id, i, rep.MigratedGraph, q.Subject.String(), q.Predicate.String(), q.Object.String()); err != nil {
			return 0, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// LatestPublished returns quads of the most recently published graph.
func (s *Store) LatestPublished(ctx context.Context) (string, []rdf.Quad, error) {
	var id int64
	var fp string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, content_fp FROM publishes WHERE status='published' ORDER BY id DESC LIMIT 1`).
		Scan(&id, &fp)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, nil
		}
		return "", nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT graph_iri,subject,predicate,object FROM published_quads WHERE publish_id=? ORDER BY seq`, id)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []rdf.Quad
	for rows.Next() {
		var g, sub, pred, obj string
		if err := rows.Scan(&g, &sub, &pred, &obj); err != nil {
			return "", nil, err
		}
		gt := rdf.DefaultGraph()
		if g != "" {
			gt = rdf.IRI(g)
		}
		st, _ := parseTerm(sub)
		pt, _ := parseTerm(pred)
		ot, _ := parseTerm(obj)
		out = append(out, rdf.Quad{Graph: gt, Subject: st, Predicate: pt, Object: ot})
	}
	return fp, out, rows.Err()
}

// Published reports whether a fingerprint was already published.
func (s *Store) Published(ctx context.Context, fp string) (bool, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM publishes WHERE content_fp=?`, fp).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func joinEvidence(e []string) string {
	out := ""
	for i, x := range e {
		if i > 0 {
			out += "\n"
		}
		out += x
	}
	return out
}
