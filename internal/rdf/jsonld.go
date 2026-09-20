package rdf

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ParseJSONLD parses a reduced JSON-LD dialect:
// {"@context": {"pfx":"ns", "name":"full-iri" or {"@id":...,"@type":...}},
//
//	"@graph": [ node, ... ] } or a single node at top level.
//
// Node objects use "@id", "@type", "@value"/"@language"/"@type" value
// objects, nested node objects/arrays and {"@id": "..."} references.
// graphID optionally places every statement into a named graph.
func ParseJSONLD(src, graphID string) ([]Quad, error) {
	var doc any
	dec := json.NewDecoder(strings.NewReader(src))
	dec.UseNumber()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("jsonld: %w", err)
	}
	jp := &jldParser{bns: 1, graph: DefaultGraph()}
	if graphID != "" {
		jp.graph = IRI(graphID)
	}
	obj, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("jsonld: top level must be an object")
	}
	if ctx, ok := obj["@context"]; ok {
		if err := jp.parseContext(ctx); err != nil {
			return nil, err
		}
	}
	var nodes []any
	if g, ok := obj["@graph"]; ok {
		arr, ok := g.([]any)
		if !ok {
			return nil, fmt.Errorf("jsonld: @graph must be an array")
		}
		nodes = arr
	} else {
		nodes = []any{obj}
	}
	for _, n := range nodes {
		no, ok := n.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("jsonld: graph entries must be objects")
		}
		if _, err := jp.parseNode(no); err != nil {
			return nil, err
		}
	}
	return jp.quads, nil
}

type jldParser struct {
	quads      []Quad
	terms      map[string]string
	vocab      string
	graph      Term
	lang       string
	bns        int
	typeCoerce map[string]string
}

func (p *jldParser) blank() Term {
	p.bns++
	return Blank("j" + strconv.Itoa(p.bns))
}

func (p *jldParser) emit(s, pr, o Term) {
	p.quads = append(p.quads, Quad{Graph: p.graph, Subject: s, Predicate: pr, Object: o})
}

func (p *jldParser) parseContext(ctx any) error {
	p.terms = map[string]string{}
	p.typeCoerce = map[string]string{}
	m, ok := ctx.(map[string]any)
	if !ok {
		return fmt.Errorf("jsonld: @context must be an object")
	}
	for k, v := range m {
		switch vv := v.(type) {
		case string:
			if k == "@vocab" {
				p.vocab = vv
			} else if strings.HasSuffix(vv, "/") || strings.HasSuffix(vv, "#") || strings.HasPrefix(vv, "http") {
				p.terms[k] = vv
			} else {
				p.terms[k] = vv // prefix-style
			}
		case map[string]any:
			if id, ok := vv["@id"].(string); ok {
				p.terms[k] = id
			}
			if t, ok := vv["@type"].(string); ok {
				p.typeCoerce[k] = p.expandCtxType(t)
			}
			if l, ok := vv["@language"].(string); ok && k == "@language" {
				p.lang = l
			}
		}
	}
	if l, ok := m["@language"].(string); ok {
		p.lang = l
	}
	if v, ok := m["@vocab"].(string); ok {
		p.vocab = v
	}
	return nil
}

func (p *jldParser) expandCtxType(t string) string {
	switch t {
	case "xsd:string", "string":
		return XSDString
	case "xsd:integer", "integer":
		return XSDInteger
	case "xsd:boolean", "boolean":
		return XSDBoolean
	case "xsd:decimal", "decimal":
		return XSDDecimal
	case "xsd:double", "double":
		return XSDDouble
	}
	if strings.Contains(t, ":") {
		if ex, err := p.expandTerm(t); err == nil {
			return ex.Value
		}
	}
	return t
}

func (p *jldParser) expandTerm(name string) (Term, error) {
	if strings.HasPrefix(name, "_:") {
		return Blank(strings.TrimPrefix(name, "_:")), nil
	}
	if name == "@type" {
		return IRI(RDFType), nil
	}
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		return IRI(name), nil
	}
	// JSON-LD compact IRI using a context prefix alias (e.g. "old:Foo",
	// "xsd:integer").  A context value ending in a name separator is a
	// namespace; otherwise it is a full-IRI alias usable only bare.
	if idx := strings.Index(name, ":"); idx >= 0 {
		if ns, ok := p.terms[name[:idx]]; ok {
			if name[:idx] == "@vocab" {
				return Term{}, fmt.Errorf("jsonld: cannot resolve %q", name)
			}
			return IRI(ns + name[idx+1:]), nil
		}
	}
	if t, ok := p.terms[name]; ok {
		return IRI(t), nil
	}
	if idx := strings.Index(name, ":"); idx >= 0 {
		ns, ok := p.terms[name[:idx]]
		if !ok {
			return Term{}, fmt.Errorf("jsonld: undefined context prefix %q", name[:idx])
		}
		if !strings.HasSuffix(ns, "#") && !strings.HasSuffix(ns, "/") {
			return IRI(ns), nil
		}
		return IRI(ns + name[idx+1:]), nil
	}
	if p.vocab != "" {
		return IRI(p.vocab + name), nil
	}
	return Term{}, fmt.Errorf("jsonld: cannot expand term %q", name)
}

func (p *jldParser) parseNode(node map[string]any) (Term, error) {
	subj := p.blank()
	if id, ok := node["@id"].(string); ok {
		t, err := p.expandTerm(id)
		if err != nil {
			return Term{}, err
		}
		subj = t
	}
	for k, v := range node {
		if k == "@id" || k == "@context" || k == "@graph" {
			continue
		}
		if k == "@type" {
			for _, t := range toList(v) {
				ts, ok := t.(string)
				if !ok {
					return Term{}, fmt.Errorf("jsonld: @type must be a string")
				}
				tt, err := p.expandTerm(ts)
				if err != nil {
					return Term{}, err
				}
				p.emit(subj, IRI(RDFType), tt)
			}
			continue
		}
		pred, err := p.expandTerm(k)
		if err != nil {
			return Term{}, err
		}
		for _, item := range toList(v) {
			if err := p.parseValue(subj, pred, item, k); err != nil {
				return Term{}, err
			}
		}
	}
	return subj, nil
}

func (p *jldParser) parseValue(subj, pred Term, v any, key string) error {
	switch vv := v.(type) {
	case map[string]any:
		if _, isVal := vv["@value"]; isVal {
			lt, err := p.parseValueObject(vv, key)
			if err != nil {
				return err
			}
			p.emit(subj, pred, lt)
			return nil
		}
		if id, ok := vv["@id"].(string); ok && len(vv) == 1 {
			t, err := p.expandTerm(id)
			if err != nil {
				return err
			}
			p.emit(subj, pred, t)
			return nil
		}
		nested, err := p.parseNode(vv)
		if err != nil {
			return err
		}
		p.emit(subj, pred, nested)
		return nil
	case string:
		if dt, ok := p.typeCoerce[key]; ok {
			p.emit(subj, pred, Typed(vv, dt))
		} else if p.lang != "" {
			p.emit(subj, pred, LangString(vv, p.lang))
		} else {
			p.emit(subj, pred, String(vv))
		}
	case json.Number:
		dt := XSDInteger
		s := vv.String()
		if strings.ContainsAny(s, ".eE") {
			dt = XSDDecimal
		}
		if coerced, ok := p.typeCoerce[key]; ok {
			dt = coerced
		}
		p.emit(subj, pred, Typed(s, dt))
	case bool:
		p.emit(subj, pred, Typed(strconv.FormatBool(vv), XSDBoolean))
	case nil:
		return nil
	default:
		return fmt.Errorf("jsonld: unsupported value type for %s", key)
	}
	return nil
}

func (p *jldParser) parseValueObject(vo map[string]any, key string) (Term, error) {
	raw := vo["@value"]
	var s string
	switch v := raw.(type) {
	case string:
		s = v
	case json.Number:
		s = v.String()
	case bool:
		s = strconv.FormatBool(v)
	default:
		return Term{}, fmt.Errorf("jsonld: @value must be scalar")
	}
	if l, ok := vo["@language"].(string); ok {
		return LangString(s, l), nil
	}
	if t, ok := vo["@type"].(string); ok {
		return Typed(s, p.expandCtxType(t)), nil
	}
	if dt, ok := p.typeCoerce[key]; ok {
		return Typed(s, dt), nil
	}
	switch raw.(type) {
	case json.Number:
		if strings.ContainsAny(s, ".eE") {
			return Typed(s, XSDDecimal), nil
		}
		return Typed(s, XSDInteger), nil
	case bool:
		return Typed(s, XSDBoolean), nil
	}
	if p.lang != "" {
		return LangString(s, p.lang), nil
	}
	return String(s), nil
}

func toList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return []any{v}
}
