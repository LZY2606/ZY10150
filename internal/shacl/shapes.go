// Package shacl implements the small SHACL subset needed for migration review:
// targetClass/targetNode, minCount/maxCount, datatype, in, class, nodeKind,
// closed+ignoredProperties and owl:disjointWith contradictions.
package shacl

import (
	"github.com/example/gsbmig/internal/rdf"
)

type PropertyShape struct {
	Path     string
	MinCount int // -1 = unset
	MaxCount int // -1 = unset
	Datatype string
	In       []rdf.Term
	Class    string
	NodeKind string
}

type NodeShape struct {
	ID            string
	TargetClasses []string
	TargetNodes   []string
	Closed        bool
	Ignored       map[string]bool
	Properties    []*PropertyShape
}

type Ontology struct {
	Disjoint map[string][]string // class -> disjoint classes (v2 ontology)
	OntIdx   *rdf.OntologyIndex
}

// ParseShapes reads node shapes from the shapes graph.
func ParseShapes(quads []rdf.Quad) ([]*NodeShape, error) {
	v := rdf.NewView(quads)
	var out []*NodeShape
	for _, subj := range v.TypedSubjects() {
		isShape := false
		for _, ty := range v.Types(subj.Value) {
			if ty.Value == rdf.SHNodeShape {
				isShape = true
			}
		}
		if !isShape {
			continue
		}
		ns := &NodeShape{ID: subj.Value, Ignored: map[string]bool{}}
		for _, t := range v.Objects(subj.Value, rdf.SHTargetClass) {
			ns.TargetClasses = append(ns.TargetClasses, t.Value)
		}
		for _, t := range v.Objects(subj.Value, rdf.SHTargetNode) {
			ns.TargetNodes = append(ns.TargetNodes, t.Value)
		}
		for _, t := range v.Objects(subj.Value, rdf.SHClosed) {
			ns.Closed = t.Value == "true"
		}
		// ignoredProperties is an rdf:List of predicates
		for _, head := range v.Objects(subj.Value, "http://www.w3.org/ns/shacl#ignoredProperties") {
			collectList(v, head, func(t rdf.Term) {
				ns.Ignored[t.Value] = true
			})
		}
		for _, psNode := range v.Objects(subj.Value, rdf.SHProperty) {
			ns.Properties = append(ns.Properties, parseProperty(v, psNode.Value))
		}
		out = append(out, ns)
	}
	return out, nil
}

func parseProperty(v *rdf.View, node string) *PropertyShape {
	ps := &PropertyShape{MinCount: -1, MaxCount: -1}
	if t := v.Objects(node, rdf.SHPath); len(t) > 0 {
		ps.Path = t[0].Value
	}
	ps.MinCount = intLit(v, node, rdf.SHMinCount, -1)
	ps.MaxCount = intLit(v, node, rdf.SHMaxCount, -1)
	ps.Datatype = iriVal(v, node, rdf.SHDataType)
	ps.Class = iriVal(v, node, rdf.SHClass)
	ps.NodeKind = iriVal(v, node, rdf.SHNodeKind)
	for _, head := range v.Objects(node, rdf.SHIn) {
		collectList(v, head, func(t rdf.Term) { ps.In = append(ps.In, t) })
	}
	return ps
}

func intLit(v *rdf.View, s, p string, def int) int {
	for _, o := range v.Objects(s, p) {
		var n int
		for _, c := range o.Value {
			if c < '0' || c > '9' {
				return def
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	return def
}

func iriVal(v *rdf.View, s, p string) string {
	for _, o := range v.Objects(s, p) {
		if o.IsIRI() {
			return o.Value
		}
	}
	return ""
}

func collectList(v *rdf.View, head rdf.Term, fn func(rdf.Term)) {
	cur := head.Value
	visited := map[string]bool{}
	for cur != rdf.RDFNil && !visited[cur] {
		visited[cur] = true
		if first := v.Objects(cur, rdf.RDFFirst); len(first) > 0 {
			fn(first[0])
		}
		rest := v.Objects(cur, rdf.RDFRest)
		if len(rest) == 0 {
			return
		}
		cur = rest[0].Value
	}
}

// ParseOntology builds disjointness and subclass indexes from the v2 ontology.
func ParseOntology(quads []rdf.Quad) *Ontology {
	o := &Ontology{
		Disjoint: map[string][]string{},
		OntIdx:   rdf.BuildOntologyIndex(quads),
	}
	for _, q := range quads {
		if q.Predicate.Value == rdf.OWLDisjointWith && q.Object.IsIRI() {
			o.Disjoint[q.Subject.Value] = append(o.Disjoint[q.Subject.Value], q.Object.Value)
			o.Disjoint[q.Object.Value] = append(o.Disjoint[q.Object.Value], q.Subject.Value)
		}
	}
	return o
}
