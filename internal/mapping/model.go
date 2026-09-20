package mapping

import (
	"sort"
	"strings"

	"gsb/internal/rdf"
)

// Relations supported by the review workflow.
const (
	Exact      = "exact"
	Broader    = "broader"
	Narrower   = "narrower"
	OneToMany  = "one_to_many"
	Deprecated = "deprecated"
)

// Condition is a discriminating condition for a one-to-many target.  A
// target applies when the instance has Predicate with the required value
// (literal value-space or IRI).  When no branch matches the instance the
// mapping remains unresolved instead of choosing a target randomly.
type Condition struct {
	Predicate string `json:"predicate,omitempty"`
	Value     string `json:"value,omitempty"`
	ValueIRI  bool   `json:"value_iri,omitempty"`
}

func (c Condition) Present() bool { return c.Predicate != "" || c.Value != "" }

func (c Condition) String() string {
	if !c.Present() {
		return "(always)"
	}
	if c.ValueIRI {
		return c.Predicate + " == <" + c.Value + ">"
	}
	return c.Predicate + " == \"" + c.Value + "\""
}

// Branch binds one one-to-many target to its discriminating condition.
type Branch struct {
	Target    string    `json:"target"`
	Condition Condition `json:"condition"`
}

// Evidence records why a candidate was proposed.  Decisions bind this
// evidence so a review is never just "user clicked accept".
type Evidence struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
	Quad   string `json:"quad,omitempty"`
}

// Rule is a mapping candidate or accepted decision.
type Rule struct {
	ID        string     `json:"id"`
	TermKind  string     `json:"term_kind"` // class|property|individual
	Relation  string     `json:"relation"`
	Source    string     `json:"source"`
	Targets   []string   `json:"targets,omitempty"`
	Branches  []Branch   `json:"branches,omitempty"`
	Evidence  []Evidence `json:"evidence"`
	Status    string     `json:"status"` // candidate|accepted|rejected|deprecated|superseded
	Rationale string     `json:"rationale,omitempty"`
	// Decision metadata
	AcceptedVersion int    `json:"accepted_version,omitempty"`
	RuleFingerprint string `json:"rule_fingerprint"`
}

// Fingerprint hashes the full rule content including candidate evidence
// and branch conditions, independent of generated ID/status metadata.
func (r Rule) Fingerprint() string {
	l := []string{
		"kind=" + r.TermKind,
		"rel=" + r.Relation,
		"src=" + r.Source,
	}
	tg := append([]string{}, r.Targets...)
	sort.Strings(tg)
	for _, t := range tg {
		l = append(l, "tgt="+t)
	}
	for _, b := range r.Branches {
		l = append(l, "branch="+b.Target+"|"+b.Condition.Predicate+"|"+
			boolStr(b.Condition.ValueIRI)+"|"+b.Condition.Value)
	}
	var ev []string
	for _, e := range r.Evidence {
		ev = append(ev, e.Type+"|"+e.Detail+"|"+e.Quad)
	}
	sort.Strings(ev)
	l = append(l, ev...)
	return rdf.Fingerprint(l)
}

// DecisionLines renders the accepted-decision set contribution used for
// the mapping-set version fingerprint.
func (r Rule) DecisionLines() []string {
	l := []string{
		"id=" + r.ID,
		"kind=" + r.TermKind,
		"rel=" + r.Relation,
		"src=" + r.Source,
		"v=" + itoa(r.AcceptedVersion),
	}
	tg := append([]string{}, r.Targets...)
	sort.Strings(tg)
	for _, t := range tg {
		l = append(l, "tgt="+t)
	}
	for _, b := range r.Branches {
		l = append(l, "branch="+b.Target+"|"+b.Condition.Predicate+"|"+
			boolStr(b.Condition.ValueIRI)+"|"+b.Condition.Value)
	}
	l = append(l, "why="+normalizeSpace(r.Rationale))
	return l
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
