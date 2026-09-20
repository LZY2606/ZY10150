package shacl

import (
	"sort"

	"gsb/internal/rdf"
)

// Cause describes how a migrated triple was produced, enabling the
// validator to separate mapping contradictions from data defects.
type Cause struct {
	DerivedKey string
	StepKind   string
	MappingID  string
	Relation   string
	SourceTerm string
}

// ValidateInput carries migrated data plus attribution.
type ValidateInput struct {
	Quads    []rdf.Quad
	Shapes   []Shape
	Disjoint map[string][]string // class -> disjoint classes
	// CauseByTriple maps migrated triple key (s p o) to its migration step.
	CauseByTriple map[string]Cause
	// OriginalByDerived maps migrated triple key to its original N-Quads.
	OriginalByDerived map[string]string
	// DroppedBySubject records source terms removed by deprecated
	// mappings: subject -> predicate IRI -> mapping id.
	DroppedBySubject map[string]map[string]string
}

// Validate runs all shapes and returns diagnostics with categories.
func Validate(in ValidateInput) []Violation {
	index := buildIndex(in.Quads)
	var viols []Violation

	for _, shape := range in.Shapes {
		nodes := targetNodes(shape, index)
		for _, node := range nodes {
			viols = append(viols, checkClosed(shape, node, index, in)...)
			for _, ps := range shape.Properties {
				viols = append(viols, checkProperty(shape, ps, node, index, in)...)
			}
			viols = append(viols, checkDisjoint(shape, node, index, in)...)
		}
	}

	// Post-classify missing data: if a required path was removed for this
	// node by a deprecated mapping, the violation is a mapping
	// contradiction rather than absent source data.
	for i := range viols {
		v := &viols[i]
		if v.Category == CatMissing {
			if m, ok := in.DroppedBySubject[v.Node]; ok {
				if id, ok2 := m[v.Path]; ok2 {
					v.Category = CatMappingContradiction
					v.MappingCause = id
					v.Message = "required property removed by deprecated mapping " + id
				}
			}
		}
	}
	sort.SliceStable(viols, func(i, j int) bool {
		if viols[i].Category != viols[j].Category {
			return viols[i].Category < viols[j].Category
		}
		if viols[i].Node != viols[j].Node {
			return viols[i].Node < viols[j].Node
		}
		return viols[i].Path < viols[j].Path
	})
	// Deduplicate diagnostics by fingerprint while keeping stable order.
	seen := map[string]bool{}
	out := viols[:0]
	for _, v := range viols {
		if seen[v.Fingerprint] {
			continue
		}
		seen[v.Fingerprint] = true
		out = append(out, v)
	}
	return out
}

type graphIndex struct {
	bySubject map[string]map[string][]rdf.Term
	subjTerms map[string]rdf.Term
	types     map[string]map[string]bool
}

func buildIndex(qs []rdf.Quad) graphIndex {
	g := graphIndex{
		bySubject: map[string]map[string][]rdf.Term{},
		subjTerms: map[string]rdf.Term{},
		types:     map[string]map[string]bool{},
	}
	for _, q := range qs {
		key := q.Subject.String()
		g.subjTerms[key] = q.Subject
		if g.bySubject[key] == nil {
			g.bySubject[key] = map[string][]rdf.Term{}
		}
		g.bySubject[key][q.Predicate.Value] = append(g.bySubject[key][q.Predicate.Value], q.Object)
		if q.Predicate.Value == rdf.RDFType && q.Object.Kind == rdf.KindIRI {
			if g.types[key] == nil {
				g.types[key] = map[string]bool{}
			}
			g.types[key][q.Object.Value] = true
		}
	}
	return g
}

func targetNodes(shape Shape, g graphIndex) []string {
	set := map[string]bool{}
	if shape.TargetClass != "" {
		for k, ts := range g.types {
			if ts[shape.TargetClass] {
				set[k] = true
			}
		}
	}
	for _, t := range shape.Targets {
		set[t] = true
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
