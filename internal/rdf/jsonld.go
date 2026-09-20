package rdf

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseJSONLD parses a compact JSON-LD document (the small fixture dialect):
//
//   - top-level object or @graph array of node objects,
//   - "@context" object mapping terms to IRIs/CURIEs (plus a "@vocab" entry),
//   - "@id" names a node, missing id allocates a blank node,
//   - "@type" is a string or list (rdf:type),
//   - value objects {"@value": ..., "@type"|"@language"?} become typed/lang
//     literals,
//   - plain JSON numbers, booleans and strings map to their XSD datatypes,
//   - nested node objects become blank nodes.
//
// All quads are placed in the given named graph.
func ParseJSONLD(data []byte, graph string) ([]Quad, error) {
	var doc any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("jsonld: %w", err)
	}
	ctx := map[string]string{"@vocab": ""}
	var nodes []map[string]any

	root, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("jsonld: top-level must be object")
	}
	if c, ok := root["@context"].(map[string]any); ok {
		loadContext(c, ctx)
	}
	if g, ok := root["@graph"].([]any); ok {
		for _, item := range g {
			if no, ok := item.(map[string]any); ok {
				nodes = append(nodes, no)
			}
		}
	} else {
		nodes = append(nodes, root)
	}

	p := &jld{ctx: ctx, bn: 0, graph: graph}
	var out []Quad
	for _, no := range nodes {
		subj := p.subjectTerm(no)
		out = append(out, p.nodeTriples(subj, no)...)
	}
	return out, nil
}

type jld struct {
	ctx   map[string]string
	bn    int
	graph string
}

func loadContext(c map[string]any, ctx map[string]string) {
	for k, v := range c {
		switch vv := v.(type) {
		case string:
			ctx[k] = vv
		case map[string]any:
			if id, ok := vv["@id"].(string); ok {
				ctx[k] = id
			}
		}
	}
}

func (p *jld) resolve(term string) string {
	if isAbsoluteOrBlank(term) {
		return term
	}
	// prefix:local CURIE, including prefixes declared in @context
	if iri, ok := p.expandCurie(term); ok {
		return iri
	}
	if v, ok := p.ctx[term]; ok && v != "" {
		if isAbsoluteOrBlank(v) {
			return v
		}
		if iri, ok := p.expandCurie(v); ok {
			return iri
		}
		return v
	}
	if vocab := p.ctx["@vocab"]; vocab != "" {
		return vocab + term
	}
	return term
}

// expandCurie expands "prefix:local" against @context prefixes; bare ":" uses
// the empty-prefix if present.
func (p *jld) expandCurie(term string) (string, bool) {
	idx := strings.Index(term, ":")
	if idx < 0 {
		return "", false
	}
	prefix, local := term[:idx], term[idx+1:]
	ns, ok := p.ctx[prefix]
	if !ok || ns == "" {
		return "", false
	}
	if !isAbsoluteOrBlank(ns) {
		// context value may itself be a CURIE
		if iri, ok2 := p.expandCurie(ns); ok2 {
			return iri + local, true
		}
	}
	return ns + local, true
}

func isAbsoluteOrBlank(s string) bool {
	return strings.HasPrefix(s, "_:") || strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "urn:")
}

func curieExpand(curie string, ctx map[string]string) (string, error) {
	idx := strings.Index(curie, ":")
	if idx < 0 {
		return "", fmt.Errorf("bad curie")
	}
	prefix, local := curie[:idx], curie[idx+1:]
	if prefix == "_" {
		return curie, nil
	}
	ns, ok := ctx[prefix]
	if !ok {
		return "", fmt.Errorf("undefined jsonld prefix %q", prefix)
	}
	return ns + local, nil
}

func (p *jld) freshBlank() Term {
	p.bn++
	return NewBlank("jld" + strconv.Itoa(p.bn))
}

func (p *jld) subjectTerm(no map[string]any) Term {
	if id, ok := no["@id"].(string); ok {
		if strings.HasPrefix(id, "_:") {
			return NewBlank(strings.TrimPrefix(id, "_:"))
		}
		return NewIRI(p.resolve(id))
	}
	return p.freshBlank()
}

func (p *jld) nodeTriples(subj Term, no map[string]any) []Quad {
	var out []Quad
	add := func(pr Term, obj Term) {
		out = append(out, Quad{Subject: subj, Predicate: pr, Object: obj, Graph: p.graph})
	}
	for key, raw := range no {
		if key == "@id" || key == "@context" || key == "@graph" {
			continue
		}
		if key == "@type" {
			for _, t := range listify(raw) {
				if s, ok := t.(string); ok {
					add(NewIRI(RDFType), NewIRI(p.resolve(s)))
				}
			}
			continue
		}
		pred := NewIRI(p.resolve(key))
		for _, val := range listify(raw) {
			term, extra := p.value(val)
			add(pred, term)
			out = append(out, extra...)
		}
	}
	return out
}

func listify(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{v}
}

// value converts a JSON value to an RDF term; nested node objects allocate a
// blank node and return their describing triples as extra quads.
func (p *jld) value(v any) (Term, []Quad) {
	switch t := v.(type) {
	case nil:
		return NewPlainLiteral(""), nil
	case string:
		return NewPlainLiteral(t), nil
	case bool:
		return NewTypedLiteral(strconv.FormatBool(t), XSDBoolean), nil
	case json.Number:
		s := t.String()
		if strings.ContainsAny(s, ".eE") {
			return NewTypedLiteral(s, XSDDouble), nil
		}
		return NewTypedLiteral(s, XSDInteger), nil
	case map[string]any:
		if _, isValue := t["@value"]; isValue {
			return p.valueObject(t)
		}
		if id, ok := t["@id"].(string); ok {
			term := NewIRI(p.resolve(id))
			if strings.HasPrefix(id, "_:") {
				term = NewBlank(strings.TrimPrefix(id, "_:"))
			}
			// A node reference may itself carry @type/properties; emit them so
			// referenced-but-defined-inline nodes keep their own triples.
			var extra []Quad
			if hasMoreThanID(t) {
				extra = p.nodeTriples(term, t)
			}
			return term, extra
		}
		b := p.freshBlank()
		return b, p.nodeTriples(b, t)
	}
	return NewPlainLiteral(fmt.Sprint(v)), nil
}

func hasMoreThanID(no map[string]any) bool {
	for k := range no {
		if k != "@id" {
			return true
		}
	}
	return false
}

func (p *jld) valueObject(vo map[string]any) (Term, []Quad) {
	val := vo["@value"]
	if lang, ok := vo["@language"].(string); ok {
		return NewLangLiteral(fmt.Sprint(val), lang), nil
	}
	if dt, ok := vo["@type"].(string); ok {
		return NewTypedLiteral(fmt.Sprint(val), p.resolve(dt)), nil
	}
	term, extra := p.value(val)
	return term, extra
}
