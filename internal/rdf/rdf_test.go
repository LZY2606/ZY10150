package rdf_test

import (
	"strings"
	"testing"

	"gsb/internal/rdf"
)

func TestTurtleRoundTrip(t *testing.T) {
	src := `
	@prefix ex: <http://example.org/> .
	@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .
	ex:a a ex:Thing ; ex:label "Hi"@en ; ex:n 3 ; ex:ref ex:b ;
	     ex:list ( ex:x ex:y ) ; ex:bn [ ex:q "v"^^xsd:string ] .
	GRAPH <http://example.org/g> { ex:c ex:p "z" . }
`
	qs, err := rdf.ParseTurtle(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(qs) < 8 {
		t.Fatalf("expected >=8 quads, got %d: %v", len(qs), qs)
	}
	var hasNamed bool
	for _, q := range qs {
		if q.Graph.Kind == rdf.KindIRI && q.Graph.Value == "http://example.org/g" {
			hasNamed = true
		}
	}
	if !hasNamed {
		t.Fatalf("named graph quad missing")
	}
}

func TestListAndBlankProperties(t *testing.T) {
	src := `@prefix ex:<http://example.org/> .
	ex:s ex:items ( ex:a ex:b ) .`
	qs, err := rdf.ParseTurtle(src)
	if err != nil {
		t.Fatal(err)
	}
	// 2 firsts, 2 rests + nil; list structure preserved with bnodes
	blanks := 0
	for _, q := range qs {
		if q.Subject.Kind == rdf.KindBlank {
			blanks++
		}
	}
	if blanks < 2 {
		t.Fatalf("expected list bnodes, quads=%v", qs)
	}
}

func TestCanonicalBlankStability(t *testing.T) {
	// Same structure, different temporary blank labels and statement order.
	a := `@prefix ex:<http://example.org/> .
	ex:root ex:p [ ex:label "same" ; ex:n 1 ] , [ ex:label "other" ; ex:n 2 ] .`
	b := `@prefix ex:<http://example.org/> .
	ex:root ex:p [ ex:n 2 ; ex:label "other" ] , [ ex:n 1 ; ex:label "same" ] .`
	qa, err := rdf.ParseTurtle(a)
	if err != nil {
		t.Fatal(err)
	}
	qb, err := rdf.ParseTurtle(b)
	if err != nil {
		t.Fatal(err)
	}
	ca := rdf.CanonicalNQuads(qa)
	cb := rdf.CanonicalNQuads(qb)
	if strings.Join(ca, "\n") != strings.Join(cb, "\n") {
		t.Fatalf("canonical forms differ:\n%s\n---\n%s", ca, cb)
	}
	if rdf.GraphFingerprint(qa) != rdf.GraphFingerprint(qb) {
		t.Fatalf("fingerprints of isomorphic graphs differ")
	}
}

func TestLiteralEquality(t *testing.T) {
	if !rdf.String("x").Equal(rdf.Typed("x", rdf.XSDString)) {
		t.Fatal("plain and xsd:string should be equal")
	}
	if rdf.LangString("x", "en").Equal(rdf.String("x")) {
		t.Fatal("lang tagged literal must not equal plain literal")
	}
	if ok, _ := rdf.DatatypeValid(rdf.Typed("12", rdf.XSDInteger)); !ok {
		t.Fatal("12 should be valid integer")
	}
	if ok, _ := rdf.DatatypeValid(rdf.Typed("1.2", rdf.XSDInteger)); ok {
		t.Fatal("1.2 must not be valid integer")
	}
}

func TestJSONLDNamedGraph(t *testing.T) {
	src := `{
	  "@context": {"@vocab":"http://example.org/","xsd":"http://www.w3.org/2001/XMLSchema#"},
	  "@id":"http://example.org/alice",
	  "@type":"http://example.org/Person",
	  "name":{"@value":"Alice","@language":"en"},
	  "age":{"@value":"30","@type":"xsd:integer"}
	}`
	qs, err := rdf.ParseJSONLD(src, "http://example.org/g")
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 3 {
		t.Fatalf("want 3 quads, got %d", len(qs))
	}
	for _, q := range qs {
		if q.Graph.Value != "http://example.org/g" {
			t.Fatalf("quad missing named graph provenance: %v", q)
		}
	}
}
