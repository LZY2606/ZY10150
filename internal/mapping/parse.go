package mapping

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/example/gsbmig/internal/rdf"
)

// ParseCandidates reads explicit map:Candidate records from the mapping graph.
func ParseCandidates(quads []rdf.Quad) ([]*Candidate, error) {
	v := rdf.NewView(quads)
	var out []*Candidate
	for _, subj := range v.TypedSubjects() {
		if !subj.IsIRI() {
			continue
		}
		types := v.Types(subj.Value)
		isCand := false
		for _, ty := range types {
			if ty.Value == PCandidate {
				isCand = true
			}
		}
		if !isCand {
			continue
		}
		c := &Candidate{ID: subj.Value, Origin: "explicit"}
		c.Source = firstIRI(v, subj.Value, Source)
		c.Target = firstIRI(v, subj.Value, Target)
		c.Relation = firstIRI(v, subj.Value, Relation)
		c.SourceTermType = firstIRI(v, subj.Value, SourceTermType)
		c.SourceVersion = firstString(v, subj.Value, SourceVersion)
		c.TargetVersion = firstString(v, subj.Value, TargetVersion)
		c.Rationale = firstString(v, subj.Value, Rationale)
		c.InitialStatus = firstIRI(v, subj.Value, InitialStatus)
		for _, ev := range v.Objects(subj.Value, PEvidence) {
			c.Evidence = append(c.Evidence, readEvidence(v, ev.Value))
		}
		for _, br := range v.Objects(subj.Value, PBranch) {
			c.Branches = append(c.Branches, readBranch(v, br.Value))
		}
		out = append(out, c)
	}
	// Deterministic order by source then relation.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].Relation < out[j].Relation
	})
	return out, nil
}

func readEvidence(v *rdf.View, node string) Evidence {
	e := Evidence{SourceGraph: node}
	e.Basis = firstString(v, node, Basis)
	e.Detail = firstString(v, node, Detail)
	e.SourceGraph = firstIRI(v, node, SourceGraph)
	e.SourcePath = firstString(v, node, SourcePath)
	return e
}

func readBranch(v *rdf.View, node string) Branch {
	b := Branch{Target: firstIRI(v, node, Target)}
	for _, co := range v.Objects(node, PCondition) {
		b.Conditions = append(b.Conditions, Condition{
			Property: firstIRI(v, co.Value, CondProperty),
			Operator: firstIRI(v, co.Value, CondOperator),
		})
	}
	return b
}

func firstIRI(v *rdf.View, s, p string) string {
	for _, o := range v.Objects(s, p) {
		if o.IsIRI() {
			return o.Value
		}
	}
	return ""
}

func firstString(v *rdf.View, s, p string) string {
	for _, o := range v.Objects(s, p) {
		if o.IsLiteral() {
			return o.Value
		}
		if o.IsIRI() {
			return o.Value
		}
	}
	return ""
}

// ContentID derives a stable candidate id from its semantic payload so that the
// same candidate parsed twice (or suggested twice) deduplicates regardless of
// the record IRI used in the mapping graph.
func ContentID(c *Candidate) string {
	var b strings.Builder
	b.WriteString(c.Source)
	b.WriteString("|")
	b.WriteString(c.Target)
	b.WriteString("|")
	b.WriteString(c.Relation)
	b.WriteString("|")
	b.WriteString(c.SourceTermType)
	for _, br := range c.Branches {
		b.WriteString("|B")
		b.WriteString(br.Target)
		for _, c := range br.Conditions {
			b.WriteString(":")
			b.WriteString(c.Property)
			b.WriteString("=")
			b.WriteString(c.Operator)
		}
	}
	h := sha256.Sum256([]byte(b.String()))
	return "map:" + hex.EncodeToString(h[:12])
}
