package store

import (
	"context"

	"gsb/internal/mapping"
)

// LoadMapping reconstructs the mapping set with rules and decision state.
func (s *Store) LoadMapping(ctx context.Context) (*mapping.Set, error) {
	set := mapping.NewSet()
	row := s.db.QueryRowContext(ctx,
		`SELECT version, version_fp, old_fp, new_fp FROM mapping_state WHERE id=1`)
	if err := row.Scan(&set.Version, &set.VersionFP, &set.OldOntologyFP, &set.NewOntologyFP); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT rule_fp,id,term_kind,relation,source,status,rationale,accepted_version
		 FROM mapping_rules ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	type head struct {
		fp, id, kind, rel, src, status, rat string
		ver                                 int
	}
	var heads []head
	for rows.Next() {
		var h head
		if err := rows.Scan(&h.fp, &h.id, &h.kind, &h.rel, &h.src, &h.status, &h.rat, &h.ver); err != nil {
			rows.Close()
			return nil, err
		}
		heads = append(heads, h)
	}
	rows.Close()
	for _, h := range heads {
		r := &mapping.Rule{
			RuleFingerprint: h.fp,
			ID:              h.id,
			TermKind:        h.kind,
			Relation:        h.rel,
			Source:          h.src,
			Status:          h.status,
			Rationale:       h.rat,
			AcceptedVersion: h.ver,
		}
		tg, err := s.loadTargets(ctx, h.fp)
		if err != nil {
			return nil, err
		}
		r.Targets = tg
		br, err := s.loadBranches(ctx, h.fp)
		if err != nil {
			return nil, err
		}
		r.Branches = br
		ev, err := s.loadEvidence(ctx, h.fp)
		if err != nil {
			return nil, err
		}
		r.Evidence = ev
		set.Rules = append(set.Rules, r)
	}
	set.Reindex()
	return set, nil
}

// SaveMapping persists the full mapping set (state + rules) in one
// transaction so readers never observe a half-written decision version.
func (s *Store) SaveMapping(ctx context.Context, set *mapping.Set) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE mapping_state SET version=?, version_fp=?, old_fp=?, new_fp=? WHERE id=1`,
		set.Version, set.VersionFP, set.OldOntologyFP, set.NewOntologyFP); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM mapping_rules`); err != nil {
		return err
	}
	for _, r := range set.Rules {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO mapping_rules (rule_fp,id,term_kind,relation,source,status,rationale,accepted_version)
			 VALUES (?,?,?,?,?,?,?,?)`,
			r.RuleFingerprint, r.ID, r.TermKind, r.Relation, r.Source, r.Status, r.Rationale, r.AcceptedVersion); err != nil {
			return err
		}
		for i, t := range r.Targets {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mapping_targets (rule_fp,pos,target) VALUES (?,?,?)`,
				r.RuleFingerprint, i, t); err != nil {
				return err
			}
		}
		for i, b := range r.Branches {
			vi := 0
			if b.Condition.ValueIRI {
				vi = 1
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mapping_branches (rule_fp,pos,target,predicate,value,value_iri)
				 VALUES (?,?,?,?,?,?)`,
				r.RuleFingerprint, i, b.Target, b.Condition.Predicate, b.Condition.Value, vi); err != nil {
				return err
			}
		}
		for i, e := range r.Evidence {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO mapping_evidence (rule_fp,pos,etype,detail,quad)
				 VALUES (?,?,?,?,?)`,
				r.RuleFingerprint, i, e.Type, e.Detail, e.Quad); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) loadTargets(ctx context.Context, fp string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target FROM mapping_targets WHERE rule_fp=? ORDER BY pos`, fp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) loadBranches(ctx context.Context, fp string) ([]mapping.Branch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT target,predicate,value,value_iri FROM mapping_branches WHERE rule_fp=? ORDER BY pos`, fp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mapping.Branch
	for rows.Next() {
		var b mapping.Branch
		var vi int
		if err := rows.Scan(&b.Target, &b.Condition.Predicate, &b.Condition.Value, &vi); err != nil {
			return nil, err
		}
		b.Condition.ValueIRI = vi == 1
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) loadEvidence(ctx context.Context, fp string) ([]mapping.Evidence, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT etype,detail,quad FROM mapping_evidence WHERE rule_fp=? ORDER BY pos`, fp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mapping.Evidence
	for rows.Next() {
		var e mapping.Evidence
		if err := rows.Scan(&e.Type, &e.Detail, &e.Quad); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
