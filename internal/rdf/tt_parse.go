package rdf

import (
	"fmt"
	"strings"
)

func formatErr(line, col int, format string, args ...any) error {
	return fmt.Errorf("turtle:%d:%d: %s", line, col, fmt.Sprintf(format, args...))
}

func (p *ttParser) peek() *tok {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *ttParser) next() *tok {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

func (p *ttParser) isWord(w string) bool {
	t := p.peek()
	return t != nil && t.kind == tkWord && t.val == w
}

func (p *ttParser) parseDoc() error {
	for p.pos < len(p.toks) {
		if err := p.parseStatement(DefaultGraph()); err != nil {
			return err
		}
	}
	return nil
}

// parseStatement parses directives, triples and TriG graph blocks.
// outer is the graph inherited from an enclosing block.
func (p *ttParser) parseStatement(outer Term) error {
	t := p.peek()
	if t == nil {
		return nil
	}
	// Directives
	if t.kind == tkAt && t.val == "prefix" || (t.kind == tkWord && strings.EqualFold(t.val, "PREFIX")) {
		p.next()
		return p.parsePrefixDecl()
	}
	if t.kind == tkAt && t.val == "base" || (t.kind == tkWord && strings.EqualFold(t.val, "BASE")) {
		p.next()
		return p.parseBaseDecl()
	}
	// Named graph block: [GRAPH] <g> { ... }
	if (t.kind == tkWord && strings.EqualFold(t.val, "GRAPH")) || t.kind == tkIRI {
		var graph Term
		if t.kind == tkWord && strings.EqualFold(t.val, "GRAPH") {
			p.next()
			gt := p.next()
			if gt == nil || gt.kind != tkIRI {
				return p.errf("GRAPH requires an IRI")
			}
			graph = IRI(p.resolveIRI(gt.val))
		} else {
			graph = IRI(p.resolveIRI(t.val))
		}
		// Must be followed by '{' to be a graph block, otherwise it is
		// a subject IRI.
		if p.peek() != nil && p.peek().kind == tkLC {
			p.next()
			old := p.graph
			p.graph = graph
			if err := p.parseGraphBody(); err != nil {
				return err
			}
			p.graph = old
			return nil
		}
	}
	// Regular triples under current graph.
	p.graph = outer
	if err := p.parseTriples(outer); err != nil {
		return err
	}
	t = p.peek()
	if t != nil && t.kind == tkDot {
		p.next()
	}
	return nil
}

func (p *ttParser) parseGraphBody() error {
	for {
		t := p.peek()
		if t == nil {
			return p.errf("unterminated graph block")
		}
		if t.kind == tkRC {
			p.next()
			if d := p.peek(); d != nil && d.kind == tkDot {
				p.next()
			}
			return nil
		}
		if err := p.parseStatement(p.graph); err != nil {
			return err
		}
	}
}

func (p *ttParser) parsePrefixDecl() error {
	nt := p.next()
	if nt == nil || (nt.kind != tkWord && nt.kind != tkPName) {
		return p.errf("expected prefix name")
	}
	name := nt.val
	if !strings.HasSuffix(name, ":") {
		name += ":"
	}
	ut := p.next()
	if ut == nil || ut.kind != tkIRI {
		return p.errf("prefix %s requires an IRI", name)
	}
	p.prefixes[strings.ToLower(name)] = p.resolveIRI(ut.val)
	if d := p.peek(); d != nil && d.kind == tkDot {
		p.next()
	}
	return nil
}

func (p *ttParser) parseBaseDecl() error {
	ut := p.next()
	if ut == nil || ut.kind != tkIRI {
		return p.errf("BASE requires an IRI")
	}
	p.base = ut.val
	if d := p.peek(); d != nil && d.kind == tkDot {
		p.next()
	}
	return nil
}

func (p *ttParser) parseTriples(graph Term) error {
	subj, err := p.parseSubject(graph)
	if err != nil {
		return err
	}
	return p.parsePredicateObjectList(subj, graph)
}

func (p *ttParser) parseSubject(graph Term) (Term, error) {
	t := p.peek()
	if t == nil {
		return Term{}, p.errf("expected subject")
	}
	switch {
	case t.kind == tkWord && t.val == "a":
		return Term{}, p.errf("a cannot be a subject")
	case t.kind == tkWord && t.val == "[]":
		p.next()
		b := p.freshBlank()
		return b, nil
	case t.kind == tkLB:
		return p.parseBlankNode(graph)
	case t.kind == tkLP:
		return p.parseCollection(graph)
	case t.kind == tkBlank:
		p.next()
		return Blank(t.val), nil
	case t.kind == tkIRI:
		p.next()
		return IRI(p.resolveIRI(t.val)), nil
	case t.kind == tkWord:
		// bare word treated as prefixed name
		p.next()
		iri, err := p.expandPName(t.val)
		if err != nil {
			return Term{}, err
		}
		return iri, nil
	}
	return Term{}, p.errf("unexpected token %q as subject", t.val)
}

func (p *ttParser) parsePredicateObjectList(subj Term, graph Term) error {
	for {
		pred, err := p.parsePredicate()
		if err != nil {
			return err
		}
		for {
			obj, err := p.parseObject(graph)
			if err != nil {
				return err
			}
			p.emit(subj, pred, obj)
			t := p.peek()
			if t != nil && t.kind == tkComma {
				p.next()
				continue
			}
			break
		}
		t := p.peek()
		if t != nil && t.kind == tkSemi {
			p.next()
			// allow trailing ';' before '.' or ']'
			if n := p.peek(); n != nil && (n.kind == tkDot || n.kind == tkRB || n.kind == tkRC) {
				return nil
			}
			continue
		}
		return nil
	}
}

func (p *ttParser) parsePredicate() (Term, error) {
	t := p.peek()
	if t == nil {
		return Term{}, p.errf("expected predicate")
	}
	if t.kind == tkWord && t.val == "a" {
		p.next()
		return IRI(RDFType), nil
	}
	if t.kind == tkIRI {
		p.next()
		return IRI(p.resolveIRI(t.val)), nil
	}
	if t.kind == tkWord {
		p.next()
		return p.expandPName(t.val)
	}
	return Term{}, p.errf("unexpected token %q as predicate", t.val)
}

func (p *ttParser) parseObject(graph Term) (Term, error) {
	t := p.peek()
	if t == nil {
		return Term{}, p.errf("expected object")
	}
	switch t.kind {
	case tkIRI:
		p.next()
		return IRI(p.resolveIRI(t.val)), nil
	case tkBlank:
		p.next()
		return Blank(t.val), nil
	case tkLB:
		return p.parseBlankNode(graph)
	case tkLP:
		return p.parseCollection(graph)
	case tkString:
		return p.parseLiteralObject()
	case tkNumber:
		p.next()
		dt := XSDInteger
		if strings.ContainsAny(t.val, ".eE") {
			dt = XSDDecimal
		}
		return Typed(t.val, dt), nil
	case tkWord:
		switch t.val {
		case "true", "false":
			p.next()
			return Typed(t.val, XSDBoolean), nil
		default:
			p.next()
			return p.expandPName(t.val)
		}
	}
	return Term{}, p.errf("unexpected token %q as object", t.val)
}

func (p *ttParser) parseLiteralObject() (Term, error) {
	st := p.next()
	t := p.peek()
	if t != nil && t.kind == tkAt {
		p.next()
		return LangString(st.val, t.val), nil
	}
	if t != nil && t.kind == tkCaret {
		p.next()
		// expect second caret
		if c := p.next(); c == nil || c.kind != tkCaret {
			return Term{}, p.errf("expected ^^ before datatype")
		}
		dt := p.next()
		if dt == nil {
			return Term{}, p.errf("missing datatype")
		}
		var dtIRI string
		if dt.kind == tkIRI {
			dtIRI = p.resolveIRI(dt.val)
		} else {
			iri, err := p.expandPName(dt.val)
			if err != nil {
				return Term{}, err
			}
			dtIRI = iri.Value
		}
		return Typed(st.val, dtIRI), nil
	}
	return String(st.val), nil
}

func (p *ttParser) parseBlankNode(graph Term) (Term, error) {
	p.next() // consume '['
	b := p.freshBlank()
	t := p.peek()
	if t != nil && t.kind == tkRB {
		p.next()
		return b, nil
	}
	if err := p.parsePredicateObjectList(b, graph); err != nil {
		return Term{}, err
	}
	if c := p.next(); c == nil || c.kind != tkRB {
		return Term{}, p.errf("expected ']' closing blank node")
	}
	return b, nil
}

func (p *ttParser) parseCollection(graph Term) (Term, error) {
	p.next() // consume '('
	var items []Term
	for {
		t := p.peek()
		if t == nil {
			return Term{}, p.errf("unterminated collection")
		}
		if t.kind == tkRP {
			p.next()
			break
		}
		o, err := p.parseObject(graph)
		if err != nil {
			return Term{}, err
		}
		items = append(items, o)
	}
	// Build the list backwards so labels remain deterministic.
	head := IRI("http://www.w3.org/1999/02/22-rdf-syntax-ns#nil")
	first := "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"
	rest := "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"
	for i := len(items) - 1; i >= 0; i-- {
		cell := p.freshBlank()
		p.emit(cell, IRI(first), items[i])
		p.emit(cell, IRI(rest), head)
		head = cell
	}
	return head, nil
}

func (p *ttParser) resolveIRI(v string) string {
	if p.base == "" || strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "urn:") {
		return v
	}
	return p.base + v
}

func (p *ttParser) expandPName(word string) (Term, error) {
	if word == "a" {
		return IRI(RDFType), nil
	}
	idx := strings.Index(word, ":")
	if idx < 0 {
		return Term{}, p.errf("unknown bare token %q (missing prefix declaration?)", word)
	}
	pfx := strings.ToLower(word[:idx+1])
	ns, ok := p.prefixes[pfx]
	if !ok {
		return Term{}, p.errf("undefined prefix %q", pfx)
	}
	return IRI(ns + word[idx+1:]), nil
}
