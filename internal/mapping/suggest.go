package mapping

import (
	"sort"
	"strings"

	"github.com/example/gsbmig/internal/rdf"
)

// Suggest computes automatic candidates between two ontology graphs using two
// deliberately simple signals:
//
//  1. identical local name after the namespace '#',
//  2. equal rdfs:label literal regardless of language tag, or ja/en synonym
//     when a label exactly matches.
//
// Existing candidates (by content id) are not duplicated; the caller uses
// Reconsider to flag accepted decisions that the new round affects.
func Suggest(v1Quads, v2Quads []rdf.Quad) []*Candidate {
	v1 := rdf.NewView(v1Quads)
	v2 := rdf.NewView(v2Quads)
	old := terms(v1)
	new := terms(v2)

	var out []*Candidate
	for _, o := range old {
		for _, n := range new {
			if o.kind != n.kind {
				continue
			}

			basis := matchSignal(o, n)
			if basis == "" {
				continue
			}
			c := &Candidate{
				Origin:         "auto",
				Source:         o.iri,
				Target:         n.iri,
				SourceTermType: termType(o.kind),
				Relation:       RelExact,
				SourceVersion:  "v1",
				TargetVersion:  "v2",
				Rationale:      "Auto-suggested by " + basis + " (not reviewed).",
				Evidence: []Evidence{{
					Basis:       basis,
					SourceGraph: "urn:gsb:ontology-v1",
					Detail:      describeSignal(o, n),
				}},
			}
			c.ID = ContentID(c)
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type termInfo struct {
	iri    string
	kind   string // class | property
	labels []rdf.Term
}

func terms(view *rdf.View) []termInfo {
	var out []termInfo
	add := func(iri, kind string) {
		out = append(out, termInfo{iri: iri, kind: kind, labels: view.Objects(iri, rdf.RDFSLabel)})
	}
	for _, s := range view.TypedSubjects() {
		for _, ty := range view.Types(s.Value) {
			switch ty.Value {
			case rdf.OWLClass:
				add(s.Value, "class")
			case rdf.OWLObjectProperty, rdf.OWLDatatypeProperty:
				add(s.Value, "property")
			}
		}
	}
	// de-duplicate (a property could in theory carry multiple type triples)
	seen := map[string]bool{}
	uniq := out[:0]
	for _, t := range out {
		key := t.iri + "|" + t.kind
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, t)
	}
	return uniq
}

func termType(kind string) string {
	if kind == "class" {
		return TermClass
	}
	return TermProperty
}

func localName(iri string) string {
	idx := strings.LastIndexAny(iri, "#/")
	if idx < 0 {
		return iri
	}
	return iri[idx+1:]
}

// matchSignal returns the reason string when o and n look equivalent.
func matchSignal(o, n termInfo) string {
	if strings.EqualFold(localName(o.iri), localName(n.iri)) {
		return "identical local name"
	}
	if labelKey(o.labels) == labelKey(n.labels) {
		return "rdfs:label lexical match"
	}
	return ""
}

func describeSignal(o, n termInfo) string {
	return localName(o.iri) + " ~ " + localName(n.iri)
}

// labelKey compares label lexical values while KEEPING language tags meaningful:
// match only when the same text appears in the same language (so a Japanese and
// English label never collapse merely because of a shared string).
func labelKey(ls []rdf.Term) string {
	var parts []string
	for _, l := range ls {
		if l.IsLiteral() {
			parts = append(parts, l.Value+"@"+l.Lang)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
