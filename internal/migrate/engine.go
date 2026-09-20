// Package migrate rewrites v1 instance triples into the v2 vocabulary using an
// effective mapping set. Every derived triple carries its source triple and the
// exact mapping path that produced it.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/example/gsbmig/internal/mapping"
	"github.com/example/gsbmig/internal/rdf"
)

// Origin values for derived triples.
const (
	OriginMapped     = "mapped"
	OriginDeprecated = "deprecated-dropped"
	OriginUnmapped   = "unmapped-dropped"
	OriginUnresolved = "one-to-many-unresolved"
)

// Derived links one produced v2 quad back to its source quad and mapping path.
type Derived struct {
	Source      rdf.Quad  `json:"source"`
	Derived     *rdf.Quad `json:"derived,omitempty"` // nil when dropped/unresolved
	Origin      string    `json:"origin"`
	MappingPath []Step    `json:"mappingPath"`
	Detail      string    `json:"detail,omitempty"`
}

// Step names one mapping rule applied along the path.
type Step struct {
	Source   string `json:"source"`
	Target   string `json:"target,omitempty"`
	Relation string `json:"relation"`
	Branch   string `json:"branch,omitempty"` // chosen one-to-many target
}

// Dropped records a source triple that did not migrate (deprecated or unmapped).
type Dropped struct {
	Source rdf.Quad `json:"source"`
	Reason string   `json:"reason"`
}

// Result is a migration preview.
type Result struct {
	Derived     []Derived  `json:"derived"`
	Migrated    []rdf.Quad `json:"migrated"`
	Dropped     []Dropped  `json:"dropped"`
	Warnings    []string   `json:"warnings"`
	RuleVersion int        `json:"ruleVersion"`
	ContentFP   string     `json:"contentFingerprint"`
	RuleFP      string     `json:"ruleFingerprint"`
	PreviewFP   string     `json:"previewFingerprint"`
}

type Engine struct {
	Eff   *mapping.Effective
	Rules *mapping.Ruleset
	OntV1 *rdf.View
}

func NewEngine(rs *mapping.Ruleset, v1Ont []rdf.Quad) *Engine {
	return &Engine{Eff: rs.Effective(), Rules: rs, OntV1: rdf.NewView(v1Ont)}
}

// Run rewrites instance quads (v1 graph) into the target graph IRI.
func (e *Engine) Run(instances []rdf.Quad, targetGraph string) *Result {
	res := &Result{RuleVersion: e.Rules.Version, RuleFP: e.Rules.Fingerprint()}
	instView := rdf.NewView(instances)

	// First pass: resolve every (subject, source-class) pair. Doing this before
	// rewriting any property triple lets discriminator conditions see the whole
	// instance (including nodes declared after they are referenced) and ensures
	// one-to-many instances never get a random branch.
	type key struct{ subj, cls string }
	resolved := map[key]classResolution{}
	resolveClass := func(subj, cls string) classResolution {
		k := key{subj, cls}
		if r, ok := resolved[k]; ok {
			return r
		}
		r := e.resolveOneClass(subj, cls, instView)
		resolved[k] = r
		return r
	}

	seen := map[string]bool{}
	addDerived := func(d Derived) {
		if d.Derived != nil {
			d.Derived.Graph = targetGraph
			k := d.Derived.String()
			if !seen[k] {
				seen[k] = true
				res.Migrated = append(res.Migrated, *d.Derived)
			}
		}
		res.Derived = append(res.Derived, d)
	}

	// Emit type triples in subject/class order for determinism.
	var typedSubjects []string
	typeSeen := map[string]bool{}
	for _, q := range instances {
		if q.Predicate.Value == rdf.RDFType {
			if !typeSeen[q.Subject.Value] {
				typeSeen[q.Subject.Value] = true
				typedSubjects = append(typedSubjects, q.Subject.Value)
			}
		}
	}
	sort.Strings(typedSubjects)
	for _, subj := range typedSubjects {
		classes := instView.Types(subj)
		sortTerms(classes)
		for _, ty := range classes {
			sq := findQuad(instances, subj, rdf.RDFType, ty)
			r := resolveClass(subj, ty.Value)
			switch {
			case r.target != "":
				d := rdf.Quad{Subject: sq.Subject, Predicate: rdf.NewIRI(rdf.RDFType),
					Object: rdf.NewIRI(r.target)}
				addDerived(Derived{
					Source:  sq,
					Derived: &d,
					Origin:  OriginMapped,
					MappingPath: []Step{{
						Source: ty.Value, Target: r.target,
						Relation: r.relation, Branch: r.branch,
					}},
					Detail: r.detail,
				})
			case r.deprecated:
				addDerived(drop(sq, OriginDeprecated, "source class deprecated"))
				res.Dropped = append(res.Dropped, Dropped{Source: sq, Reason: "deprecated"})
			case r.unresolved:
				addDerived(unresolved(sq, r.detail))
				res.Warnings = append(res.Warnings, subj+": "+r.detail)
			default:
				addDerived(drop(sq, OriginUnmapped, "no class mapping for "+ty.Value))
				res.Dropped = append(res.Dropped, Dropped{Source: sq, Reason: "unmapped"})
			}
		}
	}

	// Second pass: non-type property triples.
	for _, q := range instances {
		if q.Predicate.Value == rdf.RDFType {
			continue
		}
		out, status := e.mapProperty(q)
		switch status {
		case OriginMapped:
			addDerived(out)
		case OriginDeprecated:
			addDerived(out)
			res.Dropped = append(res.Dropped, Dropped{Source: q, Reason: "deprecated"})
		case OriginUnmapped:
			addDerived(out)
			res.Dropped = append(res.Dropped, Dropped{Source: q, Reason: "unmapped"})
		}
	}

	sort.SliceStable(res.Derived, func(i, j int) bool {
		return res.Derived[i].Source.String() < res.Derived[j].Source.String()
	})

	res.ContentFP = fingerprintQuads(instances)
	res.RuleFP = e.Rules.Fingerprint()
	res.PreviewFP = combineFP(res.ContentFP, res.RuleFP, targetGraph)
	return res
}
func sortTerms(ts []rdf.Term) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Value < ts[j].Value })
}

func findQuad(qs []rdf.Quad, subj, pred string, obj rdf.Term) rdf.Quad {
	for _, q := range qs {
		if q.Subject.Value == subj && q.Predicate.Value == pred && q.Object.Equals(obj) {
			return q
		}
	}
	return rdf.Quad{Subject: rdf.NewIRI(subj), Predicate: rdf.NewIRI(pred), Object: obj}
}

type classResolution struct {
	subj       string
	target     string
	relation   string
	branch     string
	detail     string
	deprecated bool
	unresolved bool
}

// resolveSubjectClass picks the mapped target class for a subject. For
// one-to-many mappings the discriminator conditions are evaluated against the
// subject's own properties; if zero or several branches match the instance
// stays UNRESOLVED (never a random pick).
func (e *Engine) resolveOneClass(subj, sourceClass string, inst *rdf.View) classResolution {
	res := classResolution{subj: subj}
	rule, ok := e.Eff.Classes[sourceClass]
	if !ok {
		if e.Eff.Deprecated[sourceClass] {
			res.deprecated = true
		}
		return res
	}
	if rule.Relation == mapping.RelOneToMany {
		matched := evaluateBranches(rule.Branches, subj, inst)
		switch len(matched) {
		case 1:
			res.target = matched[0].Target
			res.relation = mapping.RelOneToMany
			res.branch = matched[0].Target
			res.detail = fmt.Sprintf("branch %s selected by discriminator", short(matched[0].Target))
		case 0:
			res.unresolved = true
			res.detail = fmt.Sprintf("no one-to-many branch of %s satisfied its discriminator", short(sourceClass))
		default:
			res.unresolved = true
			res.detail = fmt.Sprintf("%d one-to-many branches of %s matched; ambiguous", len(matched), short(sourceClass))
		}
		return res
	}
	res.target = rule.Target
	res.relation = rule.Relation
	return res
}

// evaluateBranches returns branches whose conditions all hold for the subject.
func evaluateBranches(branches []mapping.Branch, subj string, inst *rdf.View) []mapping.Branch {
	var out []mapping.Branch
	for _, b := range branches {
		if len(b.Conditions) == 0 {
			out = append(out, b)
			continue
		}
		all := true
		for _, c := range b.Conditions {
			present := len(inst.Objects(subj, c.Property)) > 0
			ok := (c.Operator == mapping.OpPresent && present) ||
				(c.Operator == mapping.OpAbsent && !present)
			if !ok {
				all = false
				break
			}
		}
		if all {
			out = append(out, b)
		}
	}
	return out
}

// mapProperty rewrites a non-type quad.
func (e *Engine) mapProperty(q rdf.Quad) (Derived, string) {
	if e.Eff.Deprecated[q.Predicate.Value] {
		return Derived{Source: q}, OriginDeprecated
	}
	targetPred, ok := e.Eff.Properties[q.Predicate.Value]
	if !ok {
		return Derived{Source: q}, OriginUnmapped
	}
	d := rdf.Quad{
		Subject:   q.Subject,
		Predicate: rdf.NewIRI(targetPred),
		Object:    q.Object, // literal facets (datatype/lang) preserved verbatim
	}
	return Derived{
		Source:      q,
		Derived:     &d,
		Origin:      OriginMapped,
		MappingPath: []Step{{Source: q.Predicate.Value, Target: targetPred, Relation: mapping.RelExact}},
	}, OriginMapped
}

func drop(q rdf.Quad, origin, detail string) Derived {
	return Derived{Source: q, Origin: origin, Detail: detail}
}
func unresolved(q rdf.Quad, detail string) Derived {
	return Derived{Source: q, Origin: OriginUnresolved, Detail: detail}
}

func short(iri string) string {
	for i := len(iri) - 1; i >= 0; i-- {
		if iri[i] == '#' || iri[i] == '/' {
			return iri[i+1:]
		}
	}
	return iri
}

func fingerprintQuads(qs []rdf.Quad) string {
	can := rdf.CanonicalizeDataset(rdf.NewDataset(qs...))
	return can.Fingerprint
}

func combineFP(content, rules, graph string) string {
	h := sha256.Sum256([]byte(content + "|" + rules + "|" + graph))
	return hex.EncodeToString(h[:])
}
