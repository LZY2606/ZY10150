package service

import (
	"gsb/internal/migrate"
	"gsb/internal/rdf"
	"gsb/internal/shacl"
	"gsb/internal/spec"
)

// buildValidateInput turns preview steps into validator attribution:
// derived triple -> migration cause / original triple, and deprecated
// property removals -> subject/predicate map for missing-data upgrades.
func buildValidateInput(rep *migrate.Report, src []rdf.Quad, shapes []shacl.Shape, disjoint map[string][]string) shacl.ValidateInput {
	in := shacl.ValidateInput{
		Quads:             rep.MigratedQuads,
		Shapes:            shapes,
		Disjoint:          disjoint,
		CauseByTriple:     map[string]shacl.Cause{},
		OriginalByDerived: map[string]string{},
		DroppedBySubject:  map[string]map[string]string{},
	}
	// requiredPaths is the set of property paths any focus node must
	// carry; when a deprecated source property is removed for a node that
	// is missing a required value, the violation is attributed to the
	// mapping (a mapping contradiction) rather than plain missing data.
	required := map[string]bool{}
	for _, shp := range shapes {
		for _, ps := range shp.Properties {
			if ps.MinCount > 0 {
				required[ps.Path] = true
			}
		}
	}
	for _, st := range rep.Steps {
		if st.Derived != nil {
			key := tripleNoGraph(*st.Derived)
			in.CauseByTriple[key] = shacl.Cause{
				DerivedKey: key,
				StepKind:   st.Kind,
				MappingID:  st.MappingID,
				Relation:   st.Relation,
				SourceTerm: st.Source,
			}
			in.OriginalByDerived[key] = st.Original.String()
		}
		if st.Kind == migrate.StepDeprecated &&
			st.Original.Predicate.Kind == rdf.KindIRI &&
			st.Original.Predicate.Value != rdf.RDFType {
			subj := st.Original.Subject.String()
			if in.DroppedBySubject[subj] == nil {
				in.DroppedBySubject[subj] = map[string]string{}
			}
			// Record the removal against every required target path for
			// this subject; the validator narrows it to paths that are
			// actually absent.
			for path := range required {
				in.DroppedBySubject[subj][path] = st.MappingID
			}
			in.DroppedBySubject[subj][st.Original.Predicate.Value] = st.MappingID
		}
	}
	return in
}

func tripleNoGraph(q rdf.Quad) string {
	return q.Subject.String() + " " + q.Predicate.String() + " " + q.Object.String()
}

// loadDisjoint reads owl:disjointWith declarations that may live in the
// shapes fixture as well as an ontology document.
func loadDisjoint(qs []rdf.Quad) map[string][]string {
	out := map[string][]string{}
	for _, q := range qs {
		if q.Predicate.Value == spec.OWLDisjointWith &&
			q.Subject.Kind == rdf.KindIRI && q.Object.Kind == rdf.KindIRI {
			out[q.Subject.Value] = append(out[q.Subject.Value], q.Object.Value)
			out[q.Object.Value] = append(out[q.Object.Value], q.Subject.Value)
		}
	}
	return out
}
