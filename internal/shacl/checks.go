package shacl

import (
	"sort"

	"gsb/internal/migrate"
	"gsb/internal/rdf"
)

func checkClosed(shape Shape, node string, g graphIndex, in ValidateInput) []Violation {
	if !shape.Closed {
		return nil
	}
	allowed := map[string]bool{rdf.RDFType: true}
	for _, ps := range shape.Properties {
		allowed[ps.Path] = true
	}
	for _, ig := range shape.Ignored {
		allowed[ig] = true
	}
	var out []Violation
	preds := append([]string{}, keysOf(g.bySubject[node])...)
	sort.Strings(preds)
	for _, p := range preds {
		if allowed[p] {
			continue
		}
		for _, val := range g.bySubject[node][p] {
			v := Violation{
				Category: CatClosedExtra,
				Shape:    shapeName(shape),
				Path:     p,
				Node:     node,
				Value:    val.String(),
				Message:  "closed shape allows no extra property <" + p + ">",
				Evidence: evidencePath(g.subjTerms[node], p, val, true, in, nil),
			}
			v.Fingerprint = violFP(v)
			out = append(out, v)
		}
	}
	return out
}

func checkProperty(shape Shape, ps PropertyShape, node string, g graphIndex, in ValidateInput) []Violation {
	vals := g.bySubject[node][ps.Path]
	shapeIRI := shapeName(shape)

	// minCount — base category is missing data; reclassified by the
	// post-pass when the property disappeared due to a deprecated mapping.
	if ps.MinCount > 0 && len(vals) < ps.MinCount {
		v := Violation{
			Category: CatMissing,
			Shape:    shapeIRI,
			Path:     ps.Path,
			Node:     node,
			Message:  "expected at least " + itoa(ps.MinCount) + " value(s) of <" + ps.Path + ">",
			Evidence: evidencePath(g.subjTerms[node], ps.Path, rdf.Term{}, false, in, nil),
		}
		v.Fingerprint = violFP(v)
		return []Violation{v}
	}
	if ps.MaxCount > 0 && len(vals) > ps.MaxCount {
		v := Violation{
			Category: CatDatatype,
			Shape:    shapeIRI,
			Path:     ps.Path,
			Node:     node,
			Message:  "expected at most " + itoa(ps.MaxCount) + " value(s) of <" + ps.Path + ">",
		}
		v.Fingerprint = violFP(v)
		return []Violation{v}
	}

	var out []Violation
	for _, val := range vals {
		var v *Violation
		switch {
		case ps.Datatype != "":
			if val.Kind != rdf.KindLiteral {
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "value is not a literal of datatype <" + ps.Datatype + ">"}
			} else if ok, reason := rdf.DatatypeValid(withDatatype(val, ps.Datatype)); !ok {
				_ = reason
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "value " + val.String() + " is not a valid <" + ps.Datatype + "> lexical form"}
			} else if !datatypeCompatible(val, ps.Datatype) {
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "literal has datatype <" + val.Datatype + ">, expected <" + ps.Datatype + ">"}
			}
		case ps.NodeKind == sh+"IRI":
			if val.Kind != rdf.KindIRI {
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "expected an IRI value"}
			}
		case len(ps.InIRIs) > 0 || len(ps.InLiterals) > 0:
			if !inAllowed(val, ps) {
				// Closed-set violation.  When the offending value came
				// from a non-exact mapping it is a mapping contradiction;
				// plain source data that violates the set is datatype-ish
				// and is reported as a closed-set content violation.
				cat := CatDatatype
				cause := findCause(node, ps.Path, val, in)
				if cause.StepKind == migrate.StepResolved && cause.MappingID != "" {
					cat = CatMappingContradiction
				}
				msg := "value " + val.String() + " is not in the sh:in closed set"
				viol := Violation{
					Category: cat, Shape: shapeIRI, Path: ps.Path, Node: node,
					Value: val.String(), Message: msg,
					Evidence: evidencePath(g.subjTerms[node], ps.Path, val, true, in, &cause),
				}
				if cat == CatMappingContradiction {
					viol.MappingCause = cause.MappingID
				}
				viol.Fingerprint = violFP(viol)
				out = append(out, viol)
				continue
			}
		case ps.Class != "":
			if val.Kind != rdf.KindIRI || !g.types[val.String()][ps.Class] {
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "value is not an instance of <" + ps.Class + ">"}
			}
		case ps.HasValue != nil:
			if !val.Equal(*ps.HasValue) {
				v = &Violation{Category: CatDatatype, Value: val.String(),
					Message: "expected specific value " + ps.HasValue.String()}
			}
		}
		if v != nil {
			v.Shape = shapeIRI
			v.Path = ps.Path
			v.Node = node
			v.Evidence = evidencePath(g.subjTerms[node], ps.Path, val, true, in, nil)
			v.Fingerprint = violFP(*v)
			out = append(out, *v)
		}
	}
	return out
}

func checkDisjoint(shape Shape, node string, g graphIndex, in ValidateInput) []Violation {
	var out []Violation
	classes := g.types[node]
	for c1 := range classes {
		for _, c2 := range in.Disjoint[c1] {
			if classes[c2] {
				cause := findTypeCause(node, c1, in)
				cat := CatMappingContradiction
				if cause.MappingID == "" {
					cat = CatDatatype
				}
				v := Violation{
					Category: cat,
					Shape:    shapeName(shape),
					Node:     node,
					Path:     rdf.RDFType,
					Value:    "<" + c1 + ">",
					Message:  "node belongs to disjoint classes <" + c1 + "> and <" + c2 + ">",
					Evidence: evidencePath(g.subjTerms[node], rdf.RDFType, rdf.IRI(c1), true, in, &cause),
				}
				if cause.MappingID != "" {
					v.MappingCause = cause.MappingID
				}
				v.Fingerprint = violFP(v)
				out = append(out, v)
			}
		}
	}
	return out
}

func keysOf(m map[string][]rdf.Term) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func shapeName(s Shape) string {
	if s.IRI != "" {
		return s.IRI
	}
	return s.TargetClass
}

func withDatatype(t rdf.Term, dt string) rdf.Term {
	return rdf.Term{Kind: rdf.KindLiteral, Value: t.Value, Datatype: dt, Lang: t.Lang}
}

func datatypeCompatible(t rdf.Term, expected string) bool {
	if t.Lang != "" {
		return expected == rdf.RDFLangString
	}
	got := t.Datatype
	if got == "" {
		got = rdf.XSDString
	}
	if expected == rdf.XSDString && got == rdf.XSDString {
		return true
	}
	return got == expected
}

func inAllowed(t rdf.Term, ps PropertyShape) bool {
	if t.Kind == rdf.KindIRI {
		for _, x := range ps.InIRIs {
			if x == t.Value {
				return true
			}
		}
		return false
	}
	if t.Kind == rdf.KindLiteral {
		for _, x := range ps.InLiterals {
			if x == t.Value {
				return true
			}
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	b := []byte{}
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
