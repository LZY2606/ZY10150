package shacl

import (
	"fmt"
	"sort"

	"gsb/internal/rdf"
)

const sh = "http://www.w3.org/ns/shacl#"
const rdfFirst = "http://www.w3.org/1999/02/22-rdf-syntax-ns#first"
const rdfRest = "http://www.w3.org/1999/02/22-rdf-syntax-ns#rest"
const rdfNil = "http://www.w3.org/1999/02/22-rdf-syntax-ns#nil"

// Violation categories.  "mapping_contradiction" is the class for problems
// introduced by the mapping itself (mapped-away required data, mapped into
// a closed set, or mapped into a disjoint class); the other three
// describe source-data problems that survive migration.
const (
	CatMissing              = "missing_data"
	CatDatatype             = "datatype_mismatch"
	CatClosedExtra          = "closed_extra_property"
	CatMappingContradiction = "mapping_contradiction"
)

// PropertyShape is a reduced sh:PropertyShape.
type PropertyShape struct {
	Path       string    `json:"path"`
	MinCount   int       `json:"min_count,omitempty"`
	MaxCount   int       `json:"max_count,omitempty"`
	Datatype   string    `json:"datatype,omitempty"`
	NodeKind   string    `json:"node_kind,omitempty"`
	Class      string    `json:"class,omitempty"`
	InIRIs     []string  `json:"in_iris,omitempty"`
	InLiterals []string  `json:"in_literals,omitempty"`
	HasValue   *rdf.Term `json:"has_value,omitempty"`
	Evidence   []string  `json:"evidence,omitempty"`
}

// Shape is a reduced sh:NodeShape.
type Shape struct {
	IRI         string          `json:"iri,omitempty"`
	TargetClass string          `json:"target_class,omitempty"`
	Targets     []string        `json:"target_node,omitempty"`
	Closed      bool            `json:"closed"`
	Ignored     []string        `json:"ignored_properties,omitempty"`
	Properties  []PropertyShape `json:"properties"`
	Disjoint    []string        `json:"disjoint_classes,omitempty"`
}

// Violation is one SHACL diagnostic with a typed evidence path.
type Violation struct {
	Category    string   `json:"category"`
	Shape       string   `json:"shape"`
	Path        string   `json:"path,omitempty"`
	Node        string   `json:"node"`
	Message     string   `json:"message"`
	Value       string   `json:"value,omitempty"`
	Evidence    []string `json:"evidence"`
	Fingerprint string   `json:"fingerprint"`
	// MappingCause identifies the migration step responsible when the
	// category is mapping_contradiction (e.g. deprecated property dropped).
	MappingCause string `json:"mapping_cause,omitempty"`
}

// Shapes parses reduced SHACL Turtle quads.
func Shapes(quads []rdf.Quad) []Shape {
	subjects := map[string][]rdf.Quad{}
	var order []string
	for _, q := range quads {
		if q.Subject.Kind != rdf.KindIRI && q.Subject.Kind != rdf.KindBlank {
			continue
		}
		key := q.Subject.String()
		if _, ok := subjects[key]; !ok {
			order = append(order, key)
		}
		subjects[key] = append(subjects[key], q)
	}
	sort.Strings(order)

	// Resolve RDF lists for sh:in etc.
	listMembers := func(node rdf.Term) []rdf.Term {
		if node.Kind == rdf.KindIRI && node.Value == rdfNil {
			return nil
		}
		if node.Kind != rdf.KindBlank {
			return nil
		}
		var out []rdf.Term
		cur := node.Value
		seen := map[string]bool{}
		for cur != "" && !seen[cur] {
			seen[cur] = true
			qs := subjects["_:"+cur]
			var first, next *rdf.Term
			for i := range qs {
				switch qs[i].Predicate.Value {
				case rdfFirst:
					v := qs[i].Object
					first = &v
				case rdfRest:
					v := qs[i].Object
					next = &v
				}
			}
			if first != nil {
				out = append(out, *first)
			}
			if next == nil || (next.Kind == rdf.KindIRI && next.Value == rdfNil) {
				break
			}
			if next.Kind != rdf.KindBlank {
				break
			}
			cur = next.Value
		}
		return out
	}

	var shapes []Shape
	for _, key := range order {
		qs := subjects[key]
		isShape := false
		for _, q := range qs {
			if q.Predicate.Value == rdf.RDFType && q.Object.Value == sh+"NodeShape" {
				isShape = true
			}
		}
		if !isShape {
			continue
		}
		sp := Shape{}
		if t, err := termFromKey(key); err == nil && t.Kind == rdf.KindIRI {
			sp.IRI = t.Value
		}
		var propLinks []rdf.Term
		var ignored []rdf.Term
		for _, q := range qs {
			switch q.Predicate.Value {
			case sh + "targetClass":
				if q.Object.Kind == rdf.KindIRI {
					sp.TargetClass = q.Object.Value
				}
			case sh + "targetNode":
				sp.Targets = append(sp.Targets, q.Object.String())
			case sh + "closed":
				sp.Closed = q.Object.Value == "true"
			case sh + "ignoredProperties":
				ignored = append(ignored, listMembers(q.Object)...)
			case sh + "property":
				if q.Object.Kind == rdf.KindBlank || q.Object.Kind == rdf.KindIRI {
					propLinks = append(propLinks, q.Object)
				}
			}
		}
		for _, m := range ignored {
			sp.Ignored = append(sp.Ignored, m.String())
		}
		for _, pl := range propLinks {
			pqs := subjects[pl.String()]
			ps := PropertyShape{}
			for _, q := range pqs {
				switch q.Predicate.Value {
				case sh + "path":
					if q.Object.Kind == rdf.KindIRI {
						ps.Path = q.Object.Value
					}
				case sh + "minCount":
					ps.MinCount = atoiSafe(q.Object.Value)
				case sh + "maxCount":
					ps.MaxCount = atoiSafe(q.Object.Value)
				case sh + "datatype":
					ps.Datatype = q.Object.Value
				case sh + "nodeKind":
					ps.NodeKind = q.Object.Value
				case sh + "class":
					ps.Class = q.Object.Value
				case sh + "in":
					for _, m := range listMembers(q.Object) {
						if m.Kind == rdf.KindIRI {
							ps.InIRIs = append(ps.InIRIs, m.Value)
						} else if m.Kind == rdf.KindLiteral {
							ps.InLiterals = append(ps.InLiterals, m.Value)
						}
					}
				case sh + "hasValue":
					v := q.Object
					ps.HasValue = &v
				}
				ps.Evidence = append(ps.Evidence, q.String())
			}
			if ps.Path != "" {
				sp.Properties = append(sp.Properties, ps)
			}
		}
		// disjoint classes are declared on the class itself in the
		// ontology; validation loads them separately via SetDisjoint.
		shapes = append(shapes, sp)
	}
	return shapes
}

func termFromKey(key string) (rdf.Term, error) {
	if len(key) > 2 && key[:2] == "_:" {
		return rdf.Blank(key[2:]), nil
	}
	if len(key) > 2 && key[0] == '<' && key[len(key)-1] == '>' {
		return rdf.IRI(key[1 : len(key)-1]), nil
	}
	return rdf.Term{}, fmt.Errorf("not a term key")
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
