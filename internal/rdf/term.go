package rdf

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// TermKind discriminates the four RDF term kinds without flattening them
// into bare strings.
type TermKind int

const (
	KindIRI TermKind = iota
	KindBlank
	KindLiteral
	KindDefaultGraph
)

// Term is a concrete RDF term.  Literals keep their datatype IRI and
// language tag; IRIs keep their absolute form; blank nodes keep the local
// label which is scoped to a single document parse.
type Term struct {
	Kind     TermKind `json:"kind"`
	Value    string   `json:"value"`
	Lang     string   `json:"lang,omitempty"`
	Datatype string   `json:"datatype,omitempty"`
}

func IRI(v string) Term       { return Term{Kind: KindIRI, Value: v} }
func Blank(v string) Term     { return Term{Kind: KindBlank, Value: v} }
func DefaultGraph() Term      { return Term{Kind: KindDefaultGraph, Value: ""} }
func Plain(v string) Term     { return String(v) }
func String(v string) Term    { return Term{Kind: KindLiteral, Value: v, Datatype: XSDString} }
func Typed(v, dt string) Term { return Term{Kind: KindLiteral, Value: v, Datatype: dt} }
func LangString(v, l string) Term {
	return Term{Kind: KindLiteral, Value: v, Lang: strings.ToLower(l), Datatype: RDFLangString}
}

const (
	RDFType       = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
	RDFLangString = "http://www.w3.org/1999/02/22-rdf-syntax-ns#langString"
	XSDString     = "http://www.w3.org/2001/XMLSchema#string"
	XSDBoolean    = "http://www.w3.org/2001/XMLSchema#boolean"
	XSDInteger    = "http://www.w3.org/2001/XMLSchema#integer"
	XSDDecimal    = "http://www.w3.org/2001/XMLSchema#decimal"
	XSDDouble     = "http://www.w3.org/2001/XMLSchema#double"
)

// Equal implements literal value-space identity:
//   - lang-tagged literals match only same-language rdf:langStrings
//   - untyped literals and xsd:string literals are treated as equal
//     (RDF 1.1 semantics: "x" == "x"^^xsd:string)
func (t Term) Equal(o Term) bool {
	if t.Kind != o.Kind {
		return false
	}
	switch t.Kind {
	case KindIRI, KindBlank, KindDefaultGraph:
		return t.Value == o.Value
	case KindLiteral:
		td := t.Datatype
		od := o.Datatype
		if td == "" || td == XSDString {
			td = XSDString
		}
		if od == "" || od == XSDString {
			od = XSDString
		}
		return t.Value == o.Value && strings.EqualFold(t.Lang, o.Lang) && td == od
	}
	return false
}

// String renders the term in N-Triples/N-Quads compatible notation.
func (t Term) String() string {
	switch t.Kind {
	case KindIRI:
		return "<" + escapeIRI(t.Value) + ">"
	case KindBlank:
		return "_:" + t.Value
	case KindDefaultGraph:
		return "(default graph)"
	case KindLiteral:
		if t.Lang != "" {
			return quote(t.Value) + "@" + t.Lang
		}
		if t.Datatype == "" || t.Datatype == XSDString {
			return quote(t.Value)
		}
		return quote(t.Value) + "^^<" + escapeIRI(t.Datatype) + ">"
	}
	return ""
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func escapeIRI(s string) string {
	return strings.NewReplacer("<", `\u003c`, ">", `\u003e`, "\\", `\\`, " ", "%20").Replace(s)
}

// Quad is a single statement.  Graph is DefaultGraph() for the default
// graph; named graphs carry their own IRI so that provenance is never
// collapsed into a predicate value.
type Quad struct {
	Graph     Term `json:"g"`
	Subject   Term `json:"s"`
	Predicate Term `json:"p"`
	Object    Term `json:"o"`
}

func (q Quad) TripleKey() string {
	return q.Subject.String() + " " + q.Predicate.String() + " " + q.Object.String()
}
func (q Quad) String() string {
	if q.Graph.Kind == KindDefaultGraph {
		return q.TripleKey() + " ."
	}
	return q.TripleKey() + " " + q.Graph.String() + " ."
}

// Fingerprint is a stable content fingerprint over a set of quads.
func Fingerprint(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)[:16])
}

// ParseDatatype validates the lexical/value space of common xsd datatypes.
func DatatypeValid(t Term) (ok bool, reason string) {
	if t.Kind != KindLiteral {
		return false, "not a literal"
	}
	switch t.Datatype {
	case "", XSDString, RDFLangString:
		return true, ""
	case XSDInteger:
		s := strings.TrimSpace(t.Value)
		if s == "" {
			return false, "empty integer"
		}
		if s[0] == '+' || s[0] == '-' {
			s = s[1:]
		}
		if s == "" {
			return false, "malformed integer"
		}
		for _, r := range s {
			if r < '0' || r > '9' {
				return false, fmt.Sprintf("malformed integer %q", t.Value)
			}
		}
		return true, ""
	case XSDBoolean:
		switch t.Value {
		case "true", "false", "0", "1":
			return true, ""
		}
		return false, fmt.Sprintf("malformed boolean %q", t.Value)
	case XSDDecimal, XSDDouble:
		s := strings.TrimSpace(t.Value)
		if s == "" {
			return false, "empty number"
		}
		dot := 0
		digit := 0
		for i, r := range s {
			if (r == '+' || r == '-') && i != 0 {
				return false, "malformed number"
			}
			switch {
			case r >= '0' && r <= '9':
				digit++
			case r == '.':
				dot++
			case r == 'e' || r == 'E' || r == '+' || r == '-':
				// tolerated in exponent positions loosely
			default:
				return false, fmt.Sprintf("malformed number %q", t.Value)
			}
		}
		if dot > 1 || digit == 0 {
			return false, fmt.Sprintf("malformed number %q", t.Value)
		}
		return true, ""
	}
	return true, "" // unknown datatypes are not rejected by this minimal engine
}
