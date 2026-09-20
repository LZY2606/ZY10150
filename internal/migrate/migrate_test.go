package migrate_test

import (
	"testing"

	"gsb/internal/mapping"
	"gsb/internal/migrate"
	"gsb/internal/rdf"
)

func mustParse(t *testing.T, src string) []rdf.Quad {
	t.Helper()
	qs, err := rdf.ParseTurtle(src)
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

func rules() []*mapping.Rule {
	exact := func(kind, src, tgt, rel string) *mapping.Rule {
		r := &mapping.Rule{
			TermKind: kind, Relation: rel, Source: src, Targets: []string{tgt},
			Status: "accepted", Evidence: []mapping.Evidence{{Type: "explicit"}},
			AcceptedVersion: 1,
		}
		r.RuleFingerprint = r.Fingerprint()
		r.ID = mapping.RuleID(r.RuleFingerprint)
		return r
	}
	dep := func(kind, src string) *mapping.Rule {
		r := &mapping.Rule{
			TermKind: kind, Relation: mapping.Deprecated, Source: src,
			Status: "deprecated", Evidence: []mapping.Evidence{{Type: "explicit"}},
			AcceptedVersion: 1,
		}
		r.RuleFingerprint = r.Fingerprint()
		r.ID = mapping.RuleID(r.RuleFingerprint)
		return r
	}
	otm := &mapping.Rule{
		TermKind: "class", Relation: mapping.OneToMany,
		Source: "http://e/C", Status: "accepted", AcceptedVersion: 1,
		Evidence: []mapping.Evidence{{Type: "explicit"}},
		Branches: []mapping.Branch{
			{Target: "http://n/A", Condition: mapping.Condition{Predicate: "http://e/kind", Value: "A"}},
			{Target: "http://n/B", Condition: mapping.Condition{Predicate: "http://e/kind", Value: "B"}},
		},
	}
	otm.RuleFingerprint = otm.Fingerprint()
	otm.ID = mapping.RuleID(otm.RuleFingerprint)
	return []*mapping.Rule{
		exact("class", "http://e/P", "http://n/H", mapping.Exact),
		exact("property", "http://e/name", "http://n/fullName", mapping.Exact),
		dep("property", "http://e/staffCode"),
		otm,
	}
}

func TestPreviewPreservesEvidence(t *testing.T) {
	src := mustParse(t, `
	@prefix e:<http://e/> .
	<http://e/x> a e:P ; e:name "Alice" ; e:staffCode "S1" .
	`)
	rep := migrate.Preview(src, rules(), 3, "vfp", migrate.Options{})
	var resolved, deprecated int
	for _, st := range rep.Steps {
		if st.Kind == migrate.StepResolved {
			resolved++
			if st.Derived == nil {
				t.Fatalf("resolved step missing derived triple")
			}
			if st.Original.Subject.Value != "http://e/x" {
				t.Fatalf("original triple lost: %v", st.Original)
			}
		}
		if st.Kind == migrate.StepDeprecated {
			deprecated++
		}
	}
	if resolved != 2 || deprecated != 1 {
		t.Fatalf("want 2 resolved/1 deprecated, got %d/%d; %+v", resolved, deprecated, rep.Steps)
	}
	if rep.ContentFP == "" || rep.RuleFP == "" {
		t.Fatal("fingerprints must be populated")
	}
}

func TestOneToManyCondition(t *testing.T) {
	src := mustParse(t, `
	@prefix e:<http://e/> .
	<http://e/match> a e:C ; e:kind "A" .
	<http://e/none>  a e:C ; e:kind "Z" .
	<http://e/miss>  a e:C .
	`)
	rep := migrate.Preview(src, rules(), 1, "f", migrate.Options{})
	counts := map[string]int{}
	targets := map[string]string{}
	for _, st := range rep.Steps {
		counts[st.Kind]++
		if st.Kind == migrate.StepResolved {
			targets[st.Original.Subject.Value] = st.Derived.Object.Value
		}
	}
	if counts[migrate.StepResolved] != 1 || counts[migrate.StepPending] != 2 {
		t.Fatalf("want 1 resolved/2 pending, got %+v (%+v)", counts, rep.Steps)
	}
	if targets["http://e/match"] != "http://n/A" {
		t.Fatalf("matching branch should select A, got %q", targets["http://e/match"])
	}
}
