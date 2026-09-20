package rdf

import (
	"fmt"
	"strings"
)

// ParseTurtle parses the simplified Turtle / TriG dialect used by this
// project. Supported constructs:
//
//	@prefix / PREFIX, @base / BASE declarations
//	IRIs (<...>), prefixed names (pfx:local), blank nodes (_:b1, [])
//	literals ("..." with @lang or ^^datatype, integers, decimals, booleans)
//	; and , predicate/object lists, [ p o ] blank node property lists
//	( a b c ) RDF collections
//	named graphs in TriG: GRAPH <g> { ... } and "<g> { ... }"
//
// Line comments starting with # are skipped.
func ParseTurtle(src string) ([]Quad, error) {
	p := &ttParser{
		src: src,
		prefixes: map[string]string{
			"rdf:":  "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
			"rdfs:": "http://www.w3.org/2000/01/rdf-schema#",
			"xsd:":  "http://www.w3.org/2001/XMLSchema#",
			"sh":    "http://www.w3.org/ns/shacl#",
			"owl":   "http://www.w3.org/2002/07/owl#",
			":":     "",
		},
		bnSeq: 1,
	}
	if err := p.tokenize(); err != nil {
		return nil, err
	}
	if err := p.parseDoc(); err != nil {
		return nil, err
	}
	return p.quads, nil
}

type ttParser struct {
	src      string
	toks     []tok
	pos      int
	prefixes map[string]string
	base     string
	graph    Term
	quads    []Quad
	bnSeq    int
}

func (p *ttParser) freshBlank() Term {
	id := fmt.Sprintf("b%d", p.bnSeq)
	p.bnSeq++
	return Blank(id)
}

func (p *ttParser) emit(s, pred, o Term) {
	p.quads = append(p.quads, Quad{Graph: p.graph, Subject: s, Predicate: pred, Object: o})
}

func (p *ttParser) errf(format string, args ...any) error {
	line, col := 1, 1
	if p.pos < len(p.toks) {
		off := p.toks[p.pos].off
		head := p.src[:off]
		line = strings.Count(head, "\n") + 1
		if i := strings.LastIndex(head, "\n"); i >= 0 {
			col = off - i
		} else {
			col = off + 1
		}
	}
	return fmt.Errorf("turtle:%d:%d: %s", line, col, fmt.Sprintf(format, args...))
}
