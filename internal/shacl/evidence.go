package shacl

import (
	"strings"

	"gsb/internal/migrate"
	"gsb/internal/rdf"
)

// evidencePath renders the typed evidence chain for a diagnostic:
//   - the target graph triple that violates the shape,
//   - when attribution exists, the migration step kind and mapping id,
//   - the original source triple,
//   - the defining shape evidence quad.
func evidencePath(node rdf.Term, pred string, val rdf.Term, hasVal bool, in ValidateInput, cause *Cause) []string {
	var path []string
	if hasVal {
		path = append(path, "violating triple: "+node.String()+" <"+pred+"> "+val.String())
	} else {
		path = append(path, "focus node: "+node.String()+" (missing <"+pred+">)")
	}
	var valp *rdf.Term
	if hasVal {
		vv := val
		valp = &vv
	}
	key := tripleKey(node, pred, valp)
	c := cause
	if c == nil {
		if cc, ok := in.CauseByTriple[key]; ok {
			c = &cc
		}
	}
	if c != nil && c.StepKind != "" {
		path = append(path, "migration step: "+c.StepKind+
			" via mapping "+c.MappingID+" (source term <"+c.SourceTerm+">)")
	}
	if c != nil {
		// original triple is embedded in the derived-key index via Step
		// evidence which ValidateInput exposes through OriginalByDerived.
		if orig, ok := in.OriginalByDerived[key]; ok {
			path = append(path, "original triple: "+orig)
		}
	}
	return path
}

func tripleKey(s rdf.Term, p string, o *rdf.Term) string {
	if o == nil {
		return s.String() + " <" + p + ">"
	}
	return s.String() + " <" + p + "> " + o.String()
}

func findCause(node, pred string, val rdf.Term, in ValidateInput) Cause {
	return in.CauseByTriple[tripleKey(rdfNode(node), pred, &val)]
}

func findTypeCause(node, class string, in ValidateInput) Cause {
	v := rdf.IRI(class)
	return in.CauseByTriple[tripleKey(rdfNode(node), rdf.RDFType, &v)]
}

func rdfNode(s string) rdf.Term {
	if strings.HasPrefix(s, "_:") {
		return rdf.Blank(strings.TrimPrefix(s, "_:"))
	}
	if len(s) > 2 && s[0] == '<' && s[len(s)-1] == '>' {
		return rdf.IRI(s[1 : len(s)-1])
	}
	return rdf.IRI(s)
}

func violFP(v Violation) string {
	lines := []string{
		"cat=" + v.Category,
		"shape=" + v.Shape,
		"node=" + v.Node,
		"path=" + v.Path,
		"value=" + v.Value,
		"msg=" + v.Message,
		"cause=" + v.MappingCause,
	}
	return rdf.Fingerprint(lines)
}

// nonExact reports whether the given mapping is a broader/narrower/
// one-to-many rule (and therefore can introduce set membership the exact
// source data did not have).
func nonExact(mappingID string, in ValidateInput) bool {
	for _, c := range in.CauseByTriple {
		if c.MappingID == mappingID {
			return c.Relation != "exact"
		}
	}
	return false
}

var _ = migrate.StepResolved
