package mapping_test

import (
	"errors"
	"testing"

	"gsb/internal/mapping"
	"gsb/internal/rdf"
	"gsb/internal/spec"
)

func newSet() *mapping.Set {
	s := mapping.NewSet()
	s.AddCandidate(&mapping.Rule{
		TermKind: "class", Relation: mapping.Exact, Source: "http://e/P",
		Targets:  []string{"http://n/H"},
		Evidence: []mapping.Evidence{{Type: "explicit", Detail: "d"}},
	})
	return s
}

func TestDecideAndVersion(t *testing.T) {
	s := newSet()
	var fp string
	for _, r := range s.Candidates() {
		fp = r.RuleFingerprint
	}
	r, _, err := s.Decide(fp, mapping.DecisionInput{
		TermKind: "class", Relation: mapping.Exact, Source: "http://e/P",
		Targets:   []string{"http://n/H"},
		Rationale: "agreed",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 1 || r.AcceptedVersion != 1 || s.VersionFP == "" {
		t.Fatalf("version state wrong: %+v", s)
	}
}

func TestStaleVersionConflict(t *testing.T) {
	s := newSet()
	var fp string
	for _, r := range s.Candidates() {
		fp = r.RuleFingerprint
	}
	if _, _, err := s.Decide(fp, mapping.DecisionInput{
		TermKind: "class", Relation: mapping.Exact, Source: "http://e/P",
		Targets: []string{"http://n/H"},
	}, 0); err != nil {
		t.Fatal(err)
	}
	// Another reviewer still on version 0.
	_, conflict, err := s.Decide("nonexistent-fp", mapping.DecisionInput{
		TermKind: "class", Relation: mapping.Broader, Source: "http://e/P",
		Targets: []string{"http://n/H2"},
	}, 0)
	if !errors.Is(err, mapping.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if conflict == nil || conflict.Rule == nil || len(conflict.ConflictQuads) == 0 {
		t.Fatalf("conflict subgraph missing: %+v", conflict)
	}
}

func TestSuggestDoesNotOverwrite(t *testing.T) {
	oldQ, _ := rdf.ParseTurtle(`
	@prefix owl:<http://www.w3.org/2002/07/owl#> .
	@prefix rdfs:<http://www.w3.org/2000/01/rdf-schema#> .
	<http://e/P> a owl:Class ; rdfs:label "Person"@en .`)
	newQ, _ := rdf.ParseTurtle(`
	@prefix owl:<http://www.w3.org/2002/07/owl#> .
	@prefix rdfs:<http://www.w3.org/2000/01/rdf-schema#> .
	<http://n/P> a owl:Class ; rdfs:label "Person"@en .`)
	oldO := spec.Parse("old", oldQ)
	newO := spec.Parse("new", newQ)
	s := mapping.NewSet()
	// accept a broader mapping that disagrees with the label hint
	_, _, err := s.Decide("", mapping.DecisionInput{
		TermKind: "class", Relation: mapping.Broader, Source: "http://e/P",
		Targets:  []string{"http://n/Other"},
		Evidence: []mapping.Evidence{{Type: "review"}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	rep := s.Suggest(oldO, newO)
	if len(rep.Added) != 0 {
		t.Fatalf("suggest must not overwrite accepted rule, added=%v", rep.Added)
	}
	if len(rep.Affected) != 1 {
		t.Fatalf("divergent hint must list 1 affected decision, got %d", len(rep.Affected))
	}
}

func TestDeprecatedRequiresNoTarget(t *testing.T) {
	s := newSet()
	r, _, err := s.Decide("", mapping.DecisionInput{
		TermKind: "class", Relation: mapping.Deprecated,
		Source:   "http://e/Legacy",
		Evidence: []mapping.Evidence{{Type: "review"}},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "deprecated" || len(r.Targets) != 0 {
		t.Fatalf("deprecated rule wrong: %+v", r)
	}
}
