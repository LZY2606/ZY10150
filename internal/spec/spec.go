package spec

import (
	"sort"

	"gsb/internal/rdf"
)

const (
	RDFSSubClassOf  = "http://www.w3.org/2000/01/rdf-schema#subClassOf"
	RDFSLabel       = "http://www.w3.org/2000/01/rdf-schema#label"
	RDFSComment     = "http://www.w3.org/2000/01/rdf-schema#comment"
	RDFSDomain      = "http://www.w3.org/2000/01/rdf-schema#domain"
	RDFSRange       = "http://www.w3.org/2000/01/rdf-schema#range"
	OWLClass        = "http://www.w3.org/2002/07/owl#Class"
	OWLObjectProp   = "http://www.w3.org/2002/07/owl#ObjectProperty"
	OWLDatatypeProp = "http://www.w3.org/2002/07/owl#DatatypeProperty"
	OWLNamedInd     = "http://www.w3.org/2002/07/owl#NamedIndividual"
	OWLDeprecated   = "http://www.w3.org/2002/07/owl#deprecated"
	OWLVersionInfo  = "http://www.w3.org/2002/07/owl#versionInfo"
	OWLOntology     = "http://www.w3.org/2002/07/owl#Ontology"
	OWLDisjointWith = "http://www.w3.org/2002/07/owl#disjointWith"
)

// Term is a vocabulary element extracted from an ontology document.
type Term struct {
	IRI          string     `json:"iri"`
	Kind         string     `json:"kind"` // class|object_property|datatype_property|individual
	Labels       []Text     `json:"labels"`
	Comments     []Text     `json:"comments,omitempty"`
	Parents      []string   `json:"parents,omitempty"`
	Disjoint     []string   `json:"disjoint,omitempty"`
	Domain       []string   `json:"domain,omitempty"`
	Range        []string   `json:"range,omitempty"`
	Deprecated   bool       `json:"deprecated"`
	Types        []string   `json:"types,omitempty"`
	Evidence     []rdf.Quad `json:"-"`
	EvidenceKeys []string   `json:"evidence"`
}

// Text keeps language tags instead of flattening to bare strings.
type Text struct {
	Value string `json:"value"`
	Lang  string `json:"lang,omitempty"`
}

// Ontology is a parsed versioned vocabulary.
type Ontology struct {
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	IRI         string           `json:"iri,omitempty"`
	Terms       map[string]*Term `json:"terms"`
	Order       []string         `json:"order"`
	Quads       []rdf.Quad       `json:"-"`
	Fingerprint string           `json:"fingerprint"`
}

func (o *Ontology) SortedTerms() []*Term {
	out := make([]*Term, 0, len(o.Order))
	for _, iri := range o.Order {
		out = append(out, o.Terms[iri])
	}
	return out
}

// Parse extracts an ontology from quads.  name identifies the vocabulary
// (old/new) for provenance display.
func Parse(name string, quads []rdf.Quad) *Ontology {
	o := &Ontology{Name: name, Terms: map[string]*Term{}, Quads: quads}
	o.Fingerprint = rdf.GraphFingerprint(quads)

	index := map[string]*Term{}
	get := func(iri string) *Term {
		t := index[iri]
		if t == nil {
			t = &Term{IRI: iri}
			index[iri] = t
		}
		return t
	}
	var types = map[string]map[string]bool{}
	addType := func(s, t string) {
		if types[s] == nil {
			types[s] = map[string]bool{}
		}
		types[s][t] = true
	}

	for _, q := range quads {
		if q.Subject.Kind != rdf.KindIRI {
			continue
		}
		s := q.Subject.Value
		switch q.Predicate.Value {
		case rdf.RDFType:
			if q.Object.Kind == rdf.KindIRI {
				addType(s, q.Object.Value)
				switch q.Object.Value {
				case OWLClass, "http://www.w3.org/2000/01/rdf-schema#Class":
					get(s).Kind = "class"
				case OWLObjectProp:
					get(s).Kind = "object_property"
				case OWLDatatypeProp:
					get(s).Kind = "datatype_property"
				case OWLNamedInd:
					get(s).Kind = "individual"
				case OWLOntology:
					o.IRI = s
				}
			}
		case RDFSSubClassOf:
			if q.Object.Kind == rdf.KindIRI {
				get(s).Parents = append(get(s).Parents, q.Object.Value)
			}
		case OWLDisjointWith:
			if q.Object.Kind == rdf.KindIRI {
				get(s).Disjoint = append(get(s).Disjoint, q.Object.Value)
			}
		case RDFSLabel:
			if q.Object.Kind == rdf.KindLiteral {
				get(s).Labels = append(get(s).Labels, Text{q.Object.Value, q.Object.Lang})
			}
		case RDFSComment:
			if q.Object.Kind == rdf.KindLiteral {
				get(s).Comments = append(get(s).Comments, Text{q.Object.Value, q.Object.Lang})
			}
		case RDFSDomain:
			if q.Object.Kind == rdf.KindIRI {
				get(s).Domain = append(get(s).Domain, q.Object.Value)
			}
		case RDFSRange:
			if q.Object.Kind == rdf.KindIRI {
				get(s).Range = append(get(s).Range, q.Object.Value)
			}
		case OWLDeprecated:
			if q.Object.Value == "true" {
				get(s).Deprecated = true
			}
		case OWLVersionInfo:
			if q.Object.Kind == rdf.KindLiteral {
				o.Version = q.Object.Value
			}
		}
	}
	// Individuals can also appear as plain class instances without an
	// explicit owl:NamedIndividual typing.
	for s, ts := range types {
		t := get(s)
		for ty := range ts {
			t.Types = append(t.Types, ty)
		}
		if t.Kind == "" && len(ts) > 0 && !ts[OWLOntology] {
			if _, isClass := ts[OWLClass]; isClass {
				t.Kind = "class"
			} else {
				t.Kind = "individual"
			}
		}
	}
	var order []string
	for iri, t := range index {
		if t.Kind == "" {
			continue
		}
		sort.Strings(t.Parents)
		sort.Strings(t.Disjoint)
		sort.Strings(t.Domain)
		sort.Strings(t.Range)
		sort.Strings(t.Types)
		// Evidence: quads defining this term.
		for _, q := range quads {
			if q.Subject.Kind == rdf.KindIRI && q.Subject.Value == iri {
				t.Evidence = append(t.Evidence, q)
				t.EvidenceKeys = append(t.EvidenceKeys, q.String())
			}
		}
		o.Terms[iri] = t
		order = append(order, iri)
	}
	sort.Strings(order)
	o.Order = order
	if o.Version == "" {
		o.Version = "unknown"
	}
	return o
}

// Subgraph returns the defining quads for a single term plus its
// neighborhood (one hop), suitable for rendering an evidence subgraph.
func Subgraph(o *Ontology, iri string) []rdf.Quad {
	var out []rdf.Quad
	want := map[string]bool{iri: true}
	if t := o.Terms[iri]; t != nil {
		for _, x := range t.Parents {
			want[x] = true
		}
		for _, x := range t.Disjoint {
			want[x] = true
		}
		for _, x := range t.Domain {
			want[x] = true
		}
		for _, x := range t.Range {
			want[x] = true
		}
	}
	for _, q := range o.Quads {
		if q.Subject.Kind == rdf.KindIRI && want[q.Subject.Value] {
			out = append(out, q)
		}
	}
	return rdf.RenameBlanks(out)
}
