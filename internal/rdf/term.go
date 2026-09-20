package rdf

import (
	"fmt"
	"strings"
)

// Term kinds.
const (
	KindIRI     = "iri"
	KindLiteral = "literal"
	KindBlank   = "bnode"
	KindDefault = "default" // default graph term
)

// Term is an RDF term: IRI, literal (lexical form + datatype/lang) or blank node.
type Term struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`              // IRI, lexical form, or blank-node label
	Datatype string `json:"datatype,omitempty"` // IRI for typed literals
	Lang     string `json:"lang,omitempty"`     // BCP-47 language tag
}

// Convenience constructors.
func NewIRI(iri string) Term  { return Term{Kind: KindIRI, Value: iri} }
func NewBlank(id string) Term { return Term{Kind: KindBlank, Value: id} }

// NewPlainLiteral builds an xsd:string literal (datatype kept explicit so that
// plain and typed strings are never silently collapsed).
func NewPlainLiteral(s string) Term {
	return Term{Kind: KindLiteral, Value: s, Datatype: XSDString}
}

// NewLangLiteral keeps the language tag distinct from the lexical form.
func NewLangLiteral(s, lang string) Term {
	return Term{Kind: KindLiteral, Value: s, Lang: strings.ToLower(lang)}
}

func NewTypedLiteral(lex, datatype string) Term {
	return Term{Kind: KindLiteral, Value: lex, Datatype: datatype}
}

func (t Term) IsIRI() bool     { return t.Kind == KindIRI }
func (t Term) IsBlank() bool   { return t.Kind == KindBlank }
func (t Term) IsLiteral() bool { return t.Kind == KindLiteral }

// Equals compares all literal facets (datatype and language tag matter).
func (t Term) Equals(o Term) bool {
	return t.Kind == o.Kind && t.Value == o.Value &&
		t.Datatype == o.Datatype && t.Lang == o.Lang
}

// Canonical datatypes.
const (
	XSDString  = "http://www.w3.org/2001/XMLSchema#string"
	XSDInteger = "http://www.w3.org/2001/XMLSchema#integer"
	XSDDecimal = "http://www.w3.org/2001/XMLSchema#decimal"
	XSDBoolean = "http://www.w3.org/2001/XMLSchema#boolean"
	XSDDouble  = "http://www.w3.org/2001/XMLSchema#double"
	XSDFloat   = "http://www.w3.org/2001/XMLSchema#float"
	XSDAnyURI  = "http://www.w3.org/2001/XMLSchema#anyURI"
	RDFLangStr = "http://www.w3.org/1999/02/22-rdf-syntax-ns#langString"
)

// Quad is a single statement in a named (or default) graph.
type Quad struct {
	Subject   Term   `json:"subject"`
	Predicate Term   `json:"predicate"`
	Object    Term   `json:"object"`
	Graph     string `json:"graph"` // "" means the default graph
}

func (q Quad) GraphTerm() Term {
	if q.Graph == "" {
		return Term{Kind: KindDefault, Value: ""}
	}
	return NewIRI(q.Graph)
}

// String renders the quad in a Turtle-like, lossless form (datatype/lang shown).
func (q Quad) String() string {
	g := "default"
	if q.Graph != "" {
		g = q.Graph
	}
	return fmt.Sprintf("%s %s %s [%s]", TermString(q.Subject), TermString(q.Predicate), TermString(q.Object), g)
}

func TermString(t Term) string {
	switch t.Kind {
	case KindIRI:
		return "<" + t.Value + ">"
	case KindBlank:
		return "_:" + t.Value
	default:
		var d string
		if t.Lang != "" {
			d = "@" + t.Lang
		} else if t.Datatype != "" && t.Datatype != XSDString {
			d = "^^<" + t.Datatype + ">"
		}
		return fmt.Sprintf("%q%s", t.Value, d)
	}
}
