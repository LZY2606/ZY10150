package mapping

import (
	"errors"
	"sort"

	"gsb/internal/rdf"
	"gsb/internal/spec"
)

// Set holds candidate and accepted mapping rules and the decision version.
type Set struct {
	Rules         []*Rule `json:"rules"`
	Version       int     `json:"version"`
	VersionFP     string  `json:"version_fp"`
	OldOntologyFP string  `json:"old_ontology_fp"`
	NewOntologyFP string  `json:"new_ontology_fp"`
	byFP          map[string]*Rule
	bySrc         map[string][]*Rule
}

func NewSet() *Set {
	return &Set{byFP: map[string]*Rule{}, bySrc: map[string][]*Rule{}}
}

func (s *Set) reindex() {
	s.byFP = map[string]*Rule{}
	s.bySrc = map[string][]*Rule{}
	for _, r := range s.Rules {
		s.byFP[r.RuleFingerprint] = r
		s.bySrc[r.Source] = append(s.bySrc[r.Source], r)
	}
}

// AddCandidate inserts a candidate rule unless the same content (rule
// fingerprint) already exists.  Existing accepted rules are never
// overwritten; it returns false when deduplicated.
func (s *Set) AddCandidate(r *Rule) bool {
	if s.byFP == nil {
		s.reindex()
	}
	r.RuleFingerprint = r.Fingerprint()
	r.ID = RuleID(r.RuleFingerprint)
	if _, ok := s.byFP[r.RuleFingerprint]; ok {
		return false
	}
	if r.Status == "" {
		r.Status = "candidate"
	}
	s.Rules = append(s.Rules, r)
	s.byFP[r.RuleFingerprint] = r
	s.bySrc[r.Source] = append(s.bySrc[r.Source], r)
	return true
}

// ErrConflict indicates the client based its decision on a stale mapping
// version; the response carries the conflicting subgraph.
var ErrConflict = errors.New("mapping version conflict")

// Conflict carries the accepted rule that changed plus its evidence
// subgraph, rendered for the review page.
type Conflict struct {
	Rule          *Rule    `json:"rule"`
	ConflictQuads []string `json:"conflict_subgraph"`
	CurrentFP     string   `json:"current_fp"`
}

// Decide binds a decision to a candidate: relation, targets, condition,
// rationale and the effective mapping version.  expectedVersion implements
// optimistic concurrency: deciding against an outdated rule set returns
// ErrConflict plus the conflicting accepted rule.
func (s *Set) Decide(ruleFP string, in DecisionInput, expectedVersion int) (*Rule, *Conflict, error) {
	if s.byFP == nil {
		s.reindex()
	}
	if expectedVersion != s.Version {
		if c := s.acceptedForSource(in.Source); c != nil {
			return nil, s.conflictFor(c), ErrConflict
		}
		return nil, nil, ErrConflict
	}
	r := s.byFP[ruleFP]
	if r == nil {
		// Allow deciding on an ad-hoc rule built by the client.
		r = &Rule{
			TermKind: in.TermKind,
			Relation: in.Relation,
			Source:   in.Source,
			Targets:  in.Targets,
			Branches: in.Branches,
			Evidence: in.Evidence,
		}
		r.RuleFingerprint = r.Fingerprint()
		r.ID = RuleID(r.RuleFingerprint)
		ruleFP = r.RuleFingerprint
		s.byFP[ruleFP] = r
		s.Rules = append(s.Rules, r)
	}
	if err := validateDecision(in); err != nil {
		return nil, nil, err
	}
	// Retire any previous accepted rule on the same source.
	for _, old := range s.bySrc[in.Source] {
		if old != r && old.Status == "accepted" {
			old.Status = "superseded"
		}
	}
	r.Relation = in.Relation
	r.Targets = in.Targets
	r.Branches = in.Branches
	if in.Relation == Deprecated {
		r.Targets = nil
		r.Status = "deprecated"
	} else {
		r.Status = "accepted"
	}
	r.Rationale = in.Rationale
	s.Version++
	r.AcceptedVersion = s.Version
	r.RuleFingerprint = r.Fingerprint()
	r.ID = RuleID(r.RuleFingerprint)
	// A decision changes the rule content (and therefore its
	// fingerprint); rebuild the indexes to avoid stale keys.
	s.reindex()
	s.recomputeVersionFP()
	return r, nil, nil
}

func validateDecision(in DecisionInput) error {
	switch in.Relation {
	case Exact:
		if len(in.Targets) != 1 {
			return errors.New("exact mapping requires exactly one target")
		}
	case Broader, Narrower:
		if len(in.Targets) < 1 {
			return errors.New(in.Relation + " mapping requires at least one target")
		}
	case OneToMany:
		if len(in.Targets) < 2 && len(in.Branches) < 2 {
			return errors.New("one_to_many mapping requires at least two targets or branches")
		}
	case Deprecated:
	default:
		return errors.New("unknown relation " + in.Relation)
	}
	if in.Source == "" {
		return errors.New("source term is required")
	}
	return nil
}

// DecisionInput is the payload accepted by the decision endpoint.
type DecisionInput struct {
	TermKind  string     `json:"term_kind"`
	Relation  string     `json:"relation"`
	Source    string     `json:"source"`
	Targets   []string   `json:"targets"`
	Branches  []Branch   `json:"branches,omitempty"`
	Rationale string     `json:"rationale"`
	Evidence  []Evidence `json:"evidence"`
}

func (s *Set) acceptedForSource(src string) *Rule {
	for _, r := range s.bySrc[src] {
		if r.Status == "accepted" || r.Status == "deprecated" {
			return r
		}
	}
	return nil
}

// AcceptedRules returns the currently effective decisions.
func (s *Set) AcceptedRules() []*Rule {
	var out []*Rule
	for _, r := range s.Rules {
		if r.Status == "accepted" || r.Status == "deprecated" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

func (s *Set) Candidates() []*Rule {
	var out []*Rule
	for _, r := range s.Rules {
		if r.Status == "candidate" || r.Status == "rejected" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			return out[i].Status < out[j].Status
		}
		return out[i].Source < out[j].Source
	})
	return out
}

func (s *Set) conflictFor(r *Rule) *Conflict {
	var q []string
	q = append(q, "<"+r.Source+"> <http://gsb.local/mapping#status> \""+r.Status+"\"")
	for _, t := range r.Targets {
		q = append(q, "<"+r.Source+"> <http://gsb.local/mapping#target> <"+t+">")
	}
	return &Conflict{Rule: r, ConflictQuads: q, CurrentFP: s.VersionFP}
}

func (s *Set) recomputeVersionFP() {
	var lines []string
	for _, r := range s.AcceptedRules() {
		lines = append(lines, r.DecisionLines()...)
	}
	sort.Strings(lines)
	s.VersionFP = rdf.Fingerprint(lines)
}

// RuleID derives a stable id from rule content.
func RuleID(fp string) string { return "mr_" + fp[:12] }

// AffectedReport is returned by a new suggestion round.
type AffectedReport struct {
	Added      []*Rule `json:"added"`
	Duplicated []*Rule `json:"duplicated"`
	Affected   []*Rule `json:"affected"`
	Version    int     `json:"version"`
}

var _ = spec.OWLClass

// Reindex rebuilds fingerprint/source lookup indexes after persistence
// reconstruction.
func (s *Set) Reindex() {
	if s.byFP == nil {
		s.byFP = map[string]*Rule{}
	}
	if s.bySrc == nil {
		s.bySrc = map[string][]*Rule{}
	}
	s.reindex()
	if s.VersionFP == "" {
		s.recomputeVersionFP()
	}
}

// SetOntologyFPS records the ontology fingerprints the decisions bind to.
func (s *Set) SetOntologyFPS(oldFP, newFP string) {
	s.OldOntologyFP, s.NewOntologyFP = oldFP, newFP
}
