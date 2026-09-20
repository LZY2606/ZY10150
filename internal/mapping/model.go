// Package mapping models correspondence candidates between the v1 and v2
// vocabularies and the reviewer decisions taken on them.
package mapping

// Mapping vocabulary.
const (
	NS = "http://example.org/mapping#"

	PCandidate     = NS + "Candidate"
	Source         = NS + "source"
	Target         = NS + "target"
	SourceTermType = NS + "sourceTermType"
	Relation       = NS + "relation"
	SourceVersion  = NS + "sourceVersion"
	TargetVersion  = NS + "targetVersion"
	Rationale      = NS + "rationale"
	PEvidence      = NS + "evidence"
	PBranch        = NS + "branch"
	PCondition     = NS + "condition"
	CondProperty   = NS + "property"
	CondOperator   = NS + "operator"
	InitialStatus  = NS + "initialStatus"

	Basis       = NS + "basis"
	Detail      = NS + "detail"
	SourceGraph = NS + "sourceGraph"
	SourcePath  = NS + "sourcePath"

	RelExact      = NS + "exact"
	RelBroader    = NS + "broader"
	RelNarrower   = NS + "narrower"
	RelOneToMany  = NS + "oneToMany"
	RelDeprecated = NS + "deprecated"

	TermClass    = NS + "Class"
	TermProperty = NS + "Property"

	OpPresent = NS + "present"
	OpAbsent  = NS + "absent"

	StatusAccepted = NS + "accepted"
	StatusPending  = NS + "pending"
	StatusRejected = NS + "rejected"
)

// Evidence ties a suggestion to concrete source-graph material, never a bare
// string claim.
type Evidence struct {
	Basis       string `json:"basis"`
	Detail      string `json:"detail,omitempty"`
	SourceGraph string `json:"sourceGraph,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
}

type Condition struct {
	Property string `json:"property"`
	Operator string `json:"operator"` // present | absent
}

// Branch is one alternative target of a one-to-many mapping. Conditions are
// a conjunction (AND): every clause must hold.
type Branch struct {
	Target     string      `json:"target"`
	Conditions []Condition `json:"conditions,omitempty"`
}

type Candidate struct {
	ID             string     `json:"id"` // content-derived, stable
	Source         string     `json:"source"`
	Target         string     `json:"target,omitempty"` // empty for deprecated
	SourceTermType string     `json:"sourceTermType"`
	Relation       string     `json:"relation"`
	SourceVersion  string     `json:"sourceVersion"`
	TargetVersion  string     `json:"targetVersion"`
	Rationale      string     `json:"rationale,omitempty"`
	InitialStatus  string     `json:"initialStatus,omitempty"`
	Evidence       []Evidence `json:"evidence,omitempty"`
	Branches       []Branch   `json:"branches,omitempty"`
	Origin         string     `json:"origin"` // "explicit" | "auto"
}

// IsDeprecated reports whether the candidate retires a term without replacement.
func (c *Candidate) IsDeprecated() bool { return c.Relation == RelDeprecated }

func (c *Candidate) IsOneToMany() bool { return c.Relation == RelOneToMany }

// DecisionStatus values.
const (
	DecisionAccepted   = "accepted"
	DecisionDeprecated = "deprecated"
	DecisionRejected   = "rejected"
	DecisionPending    = "pending"
)

// Decision is a reviewer resolution bound to a candidate, the mapping version it
// was taken against, and a rationale.
type Decision struct {
	CandidateID string `json:"candidateId"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Relation    string `json:"relation"`
	Target      string `json:"target,omitempty"`
	Version     int    `json:"version"` // mapping-set version when decided
	Rationale   string `json:"rationale,omitempty"`
	DecidedAt   string `json:"decidedAt"`
}

// Effective flattens accepted decisions into lookup tables consumed by the
// migration engine.
type Effective struct {
	Classes    map[string]ClassRule
	Properties map[string]string // source pred -> target pred
	Deprecated map[string]bool
}

// ClassRule captures the accepted class correspondence for a source class.
type ClassRule struct {
	Relation string
	Target   string   // for exact/broader/narrower
	Branches []Branch // for oneToMany
}
