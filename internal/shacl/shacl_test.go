package shacl_test

import (
	"testing"

	"gsb/internal/mapping"
	"gsb/internal/migrate"
	"gsb/internal/rdf"
	"gsb/internal/shacl"
)

const xsd = "http://www.w3.org/2001/XMLSchema#"
const n = "http://n/"
const e = "http://e/"

func TestViolationCategories(t *testing.T) {
	shapeQ, err := rdf.ParseTurtle(`
	@prefix sh:<http://www.w3.org/ns/shacl#> .
	@prefix xsd:<http://www.w3.org/2001/XMLSchema#> .
	@prefix n:<http://n/> .
	n:HS a sh:NodeShape ; sh:targetClass n:Human ;
	  sh:property [ sh:path n:fullName ; sh:minCount 1 ; sh:datatype xsd:string ] ;
	  sh:property [ sh:path n:ageYears ; sh:datatype xsd:integer ] ;
	  sh:property [ sh:path n:memberOf ; sh:in ( n:eng n:sales ) ] .
	n:DS a sh:NodeShape ; sh:targetClass n:Department ; sh:closed true ;
	  sh:ignoredProperties ( <http://www.w3.org/1999/02/22-rdf-syntax-ns#type> ) ;
	  sh:property [ sh:path n:departmentCode ; sh:minCount 1 ; sh:datatype xsd:string ] .
	`)
	if err != nil {
		t.Fatal(err)
	}
	src, err := rdf.ParseTurtle(`
	@prefix e:<http://e/> .
	e:carol a e:Human ; e:ageYears 30 .
	e:bob   a e:Human ; e:fullName "Bob" ; e:ageYears "thirty" .
	e:alice a e:Human ; e:fullName "Alice" ; e:ageYears 40 ; e:memberOf e:ghost_dept .
	e:dx    a e:Department ; e:departmentCode "X1" ; e:ageYears 9 .
	`)
	if err != nil {
		t.Fatal(err)
	}
	r := func(id, kind, rel, src, tgt string) *mapping.Rule {
		rule := &mapping.Rule{ID: id, TermKind: kind, Relation: rel, Source: src,
			Targets: []string{tgt}, Status: "accepted", AcceptedVersion: 1,
			Evidence: []mapping.Evidence{{Type: "explicit"}}}
		rule.RuleFingerprint = rule.Fingerprint()
		rule.ID = mapping.RuleID(rule.RuleFingerprint)
		return rule
	}
	dep := &mapping.Rule{TermKind: "property", Relation: mapping.Deprecated,
		Source: e + "staffCode", Status: "deprecated", AcceptedVersion: 1,
		Evidence: []mapping.Evidence{{Type: "explicit"}}}
	dep.RuleFingerprint = dep.Fingerprint()
	dep.ID = mapping.RuleID(dep.RuleFingerprint)
	rules := []*mapping.Rule{
		r("c", "class", mapping.Exact, e+"Human", n+"Human"),
		r("d", "class", mapping.Broader, e+"Department", n+"Department"),
		r("p1", "property", mapping.Exact, e+"fullName", n+"fullName"),
		r("p2", "property", mapping.Exact, e+"ageYears", n+"ageYears"),
		r("p3", "property", mapping.Narrower, e+"memberOf", n+"memberOf"),
		r("p4", "property", mapping.Exact, e+"departmentCode", n+"departmentCode"),
		dep,
	}
	rep := migrate.Preview(src, rules, 1, "fp", migrate.Options{})
	in := buildInput(rep, shapeQ)
	viols := shacl.Validate(in)
	got := map[string]int{}
	detail := map[string]string{}
	for _, v := range viols {
		got[v.Category]++
		detail[v.Category+"|"+v.Node] = v.Message
	}
	if got[shacl.CatMissing] == 0 {
		t.Fatalf("expected missing_data, got %+v %+v", got, detail)
	}
	if got[shacl.CatDatatype] == 0 {
		t.Fatalf("expected datatype_mismatch, got %+v", got)
	}
	if got[shacl.CatClosedExtra] == 0 {
		t.Fatalf("expected closed_extra_property, got %+v %+v", got, detail)
	}
	if got[shacl.CatMappingContradiction] == 0 {
		t.Fatalf("expected mapping_contradiction (sh:in via narrower mapping), got %+v %+v", got, detail)
	}
}

func buildInput(rep *migrate.Report, shapeQ []rdf.Quad) shacl.ValidateInput {
	in := shacl.ValidateInput{
		Quads:             rep.MigratedQuads,
		Shapes:            shacl.Shapes(shapeQ),
		CauseByTriple:     map[string]shacl.Cause{},
		OriginalByDerived: map[string]string{},
		DroppedBySubject:  map[string]map[string]string{},
	}
	for _, st := range rep.Steps {
		if st.Derived != nil {
			key := st.Derived.Subject.String() + " " + st.Derived.Predicate.String() + " " + st.Derived.Object.String()
			in.CauseByTriple[key] = shacl.Cause{
				StepKind: st.Kind, MappingID: st.MappingID,
				Relation: st.Relation, SourceTerm: st.Source,
			}
			in.OriginalByDerived[key] = st.Original.String()
		}
	}
	return in
}

var _ = xsd
