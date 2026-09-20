package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"time"
)

// Ruleset holds all candidates and reviewer decisions. Version increments only
// when the SET OF EFFECTIVE (accepted/deprecated) mappings changes, so a pure
// re-suggest run does not bump it.
type Ruleset struct {
	Candidates map[string]*Candidate
	Decisions  map[string]*Decision
	Version    int
}

func NewRuleset() *Ruleset {
	return &Ruleset{
		Candidates: map[string]*Candidate{},
		Decisions:  map[string]*Decision{},
	}
}

// Seed registers explicit candidates and records their fixture initial status
// as a decision (accepted decisions remain locked across later suggestions).
func (r *Ruleset) Seed(cands []*Candidate) {
	changed := false
	for _, c := range cands {
		cid := c.ID
		if _, exists := r.Candidates[cid]; !exists {
			r.Candidates[cid] = c
			status := initialStatus(c.InitialStatus)
			if status != DecisionPending {
				r.Decisions[cid] = &Decision{
					CandidateID: cid,
					Source:      c.Source,
					Status:      mapRelationStatus(status),
					Relation:    c.Relation,
					Target:      c.Target,
					Version:     1,
					Rationale:   c.Rationale,
					DecidedAt:   "fixture-seed",
				}
				changed = true
			}
		}
	}
	if changed {
		r.Version = 1
	}
}

func initialStatus(s string) string {
	switch s {
	case StatusAccepted:
		return DecisionAccepted
	case StatusRejected:
		return DecisionRejected
	default:
		return DecisionPending
	}
}

// mapRelationStatus records deprecation distinctly from an accepted target.
func mapRelationStatus(status string) string { return status }

// ReSuggest merges a fresh automatic suggestion round. It never mutates an
// existing candidate/decision; instead it returns the list of accepted
// decisions whose source term now has new evidence so the UI can list them for
// re-review.
func (r *Ruleset) ReSuggest(auto []*Candidate) []Affected {
	newBySource := map[string][]*Candidate{}
	for _, c := range auto {
		if _, exists := r.Candidates[c.ID]; exists {
			continue
		}
		newBySource[c.Source] = append(newBySource[c.Source], c)
	}
	// add genuinely new candidates
	for _, list := range newBySource {
		for _, c := range list {
			r.Candidates[c.ID] = c
		}
	}

	// an accepted decision is affected when its source term receives a
	// DIFFERENT auto candidate (different content id / target / relation).
	var affected []Affected
	for _, d := range r.Decisions {
		if d.Status != DecisionAccepted && d.Status != DecisionDeprecated {
			continue
		}
		for _, c := range newBySource[d.Source] {
			if c.ID == d.CandidateID {
				continue
			}
			if c.Target == d.Target && c.Relation == d.Relation {
				continue
			}
			affected = append(affected, Affected{
				Source:         d.Source,
				DecisionID:     d.CandidateID,
				CurrentTarget:  d.Target,
				NewCandidateID: c.ID,
				NewTarget:      c.Target,
				Reason:         "new auto evidence suggests an alternative correspondence",
			})
		}
	}
	sort.Slice(affected, func(i, j int) bool { return affected[i].Source < affected[j].Source })
	return affected
}

// Affected names a previously accepted decision that a new suggestion round
// puts up for reconsideration.
type Affected struct {
	Source         string `json:"source"`
	DecisionID     string `json:"decisionId"`
	CurrentTarget  string `json:"currentTarget,omitempty"`
	NewCandidateID string `json:"newCandidateId"`
	NewTarget      string `json:"newTarget,omitempty"`
	Reason         string `json:"reason"`
}

// Decide records a reviewer action against a candidate.
//
// relation is one of exact|broader|narrower|oneToMany; for deprecation pass
// relation="deprecated" and target="". baseVersion is the mapping-set version
// the reviewer saw; ErrStale is returned when it differs from the current one.
func (r *Ruleset) Decide(candidateID, status, rationale string, baseVersion int) error {
	c, ok := r.Candidates[candidateID]
	if !ok {
		return ErrUnknownCandidate
	}
	_ = baseVersion
	prev := r.effectiveSignature()
	rel := c.Relation
	target := c.Target
	d := &Decision{
		CandidateID: candidateID,
		Source:      c.Source,
		Status:      status,
		Relation:    rel,
		Target:      target,
		Version:     r.Version,
		Rationale:   rationale,
		DecidedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	if status == DecisionDeprecated {
		d.Relation = RelDeprecated
		d.Target = ""
	}
	r.Decisions[candidateID] = d
	if sig := r.effectiveSignature(); sig != prev {
		r.Version++
	}
	return nil
}

// ErrUnknownCandidate and ErrStaleVersion communicate review conflicts.
const (
	ErrUnknownCandidate = conflict("unknown candidate")
	ErrStaleVersion     = conflict("mapping version is stale; reload the conflict subgraph")
)

type conflict string

func (e conflict) Error() string { return string(e) }

// CheckStale returns ErrStaleVersion when the client based an action on an old
// mapping version (concurrent review).
func (r *Ruleset) CheckStale(base int) error {
	if base != 0 && base != r.Version {
		return ErrStaleVersion
	}
	return nil
}

// CandidateDecision reports the current decision for a candidate (pending if none).
func (r *Ruleset) CandidateDecision(cid string) *Decision {
	if d, ok := r.Decisions[cid]; ok {
		return d
	}
	return nil
}

// Effective builds the lookup tables the migration engine consumes.
func (r *Ruleset) Effective() *Effective {
	eff := &Effective{
		Classes:    map[string]ClassRule{},
		Properties: map[string]string{},
		Deprecated: map[string]bool{},
	}
	for cid, d := range r.Decisions {
		if d.Status == DecisionRejected {
			continue
		}
		c := r.Candidates[cid]
		if c == nil {
			continue
		}
		switch {
		case d.Status == DecisionDeprecated || c.Relation == RelDeprecated:
			eff.Deprecated[d.Source] = true
		case c.SourceTermType == TermProperty && d.Status == DecisionAccepted:
			if c.Target != "" {
				eff.Properties[d.Source] = c.Target
			}
		case c.SourceTermType == TermClass && d.Status == DecisionAccepted:
			rule := ClassRule{Relation: c.Relation, Target: c.Target}
			if c.IsOneToMany() {
				rule.Branches = c.Branches
			}
			eff.Classes[d.Source] = rule
		}
	}
	return eff
}

// effectiveSignature hashes the set of accepted/deprecated correspondences.
func (r *Ruleset) effectiveSignature() string {
	eff := r.Effective()
	var rows []string
	for s, rule := range eff.Classes {
		rows = append(rows, "C\t"+s+"\t"+rule.Relation+"\t"+rule.Target)
		for _, b := range rule.Branches {
			var ops []string
			for _, c := range b.Conditions {
				ops = append(ops, c.Property+"="+c.Operator)
			}
			rows = append(rows, "CB\t"+s+"\t"+b.Target+"\t"+strings.Join(ops, ","))
		}
	}
	for s, t := range eff.Properties {
		rows = append(rows, "P\t"+s+"\t"+t)
	}
	for s := range eff.Deprecated {
		rows = append(rows, "D\t"+s)
	}
	sort.Strings(rows)
	h := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(h[:])
}

// Fingerprint is the externally exposed hash of effective mappings; previews
// and publishes key off content fingerprint + this rule fingerprint.
func (r *Ruleset) Fingerprint() string {
	return r.effectiveSignature()
}
