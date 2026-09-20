package mapping

import (
	"sort"

	"gsb/internal/rdf"
	"gsb/internal/spec"
)

const mapNS = "http://gsb.local/mapping#"

// ParseCandidates reads the explicit mapping-candidate Turtle fixture.
// Vocabulary (prefix m: <http://gsb.local/mapping#>):
//
//	<src> a m:ClassMapping / m:PropertyMapping / m:IndividualMapping ;
//	    m:relation m:exact|m:broader|m:narrower|m:oneToMany|m:deprecated ;
//	    m:target <t1>, <t2> ;
//	    m:evidence [ m:type "explicit" ; m:detail "..." ] ;
//	    m:branch [ m:target <t> ; m:whenProperty <p> ; m:whenValue "v" ;
//	               m:whenIRI <i> ] .
//
// One or more branches with discriminating conditions become a single
// one-to-many rule; branches with targets and no condition become plain
// targets of the same rule.
func ParseCandidates(quads []rdf.Quad) ([]*Rule, error) {
	type branch struct {
		target    string
		predicate string
		value     string
		valueIRI  bool
	}
	meta := map[string]struct {
		kind     string
		rel      string
		targets  []string
		ev       []Evidence
		branches []branch
	}{}
	get := func(s string) *struct {
		kind     string
		rel      string
		targets  []string
		ev       []Evidence
		branches []branch
	} {
		v := meta[s]
		return &v
	}
	// helper without pointer-to-temporary
	store := map[string]map[string]any{}
	_ = get
	ensure := func(s string) map[string]any {
		if store[s] == nil {
			store[s] = map[string]any{
				"kind":     "",
				"rel":      "",
				"targets":  []string{},
				"ev":       []Evidence{},
				"branches": []branch{},
			}
		}
		return store[s]
	}

	// Blank-node helpers keyed by bnode label.
	bnode := map[string]*branch{}
	ensureBranch := func(b string) *branch {
		if bnode[b] == nil {
			bnode[b] = &branch{}
		}
		return bnode[b]
	}
	evNode := map[string]*Evidence{}
	ensureEv := func(b string) *Evidence {
		if evNode[b] == nil {
			evNode[b] = &Evidence{Type: "explicit"}
		}
		return evNode[b]
	}

	relationOf := map[string]string{
		mapNS + "exact":      Exact,
		mapNS + "broader":    Broader,
		mapNS + "narrower":   Narrower,
		mapNS + "oneToMany":  OneToMany,
		mapNS + "deprecated": Deprecated,
	}
	kindOf := map[string]string{
		mapNS + "ClassMapping":      "class",
		mapNS + "PropertyMapping":   "property",
		mapNS + "IndividualMapping": "individual",
	}

	for _, q := range quads {
		if q.Subject.Kind != rdf.KindIRI {
			// blank-node property
			if q.Subject.Kind == rdf.KindBlank && q.Object.Kind != rdf.KindDefaultGraph {
				b := q.Subject.Value
				switch q.Predicate.Value {
				case mapNS + "target":
					if q.Object.Kind == rdf.KindIRI {
						ensureBranch(b).target = q.Object.Value
					}
				case mapNS + "whenProperty":
					if q.Object.Kind == rdf.KindIRI {
						ensureBranch(b).predicate = q.Object.Value
					}
				case mapNS + "whenValue":
					if q.Object.Kind == rdf.KindLiteral {
						ensureBranch(b).value = q.Object.Value
					}
				case mapNS + "whenIRI":
					if q.Object.Kind == rdf.KindIRI {
						br := ensureBranch(b)
						br.value = q.Object.Value
						br.valueIRI = true
					}
				case mapNS + "type":
					if q.Object.Kind == rdf.KindLiteral {
						ensureEv(b).Type = q.Object.Value
					}
				case mapNS + "detail":
					if q.Object.Kind == rdf.KindLiteral {
						ensureEv(b).Detail = q.Object.Value
					}
				}
			}
			continue
		}
		s := q.Subject.Value
		m := ensure(s)
		switch q.Predicate.Value {
		case rdf.RDFType:
			if q.Object.Kind == rdf.KindIRI {
				if k, ok := kindOf[q.Object.Value]; ok {
					m["kind"] = k
				}
			}
		case mapNS + "relation":
			if q.Object.Kind == rdf.KindIRI {
				if rel, ok := relationOf[q.Object.Value]; ok {
					m["rel"] = rel
				}
			}
		case mapNS + "target":
			if q.Object.Kind == rdf.KindIRI {
				m["targets"] = append(m["targets"].([]string), q.Object.Value)
			}
		case mapNS + "branch":
			if q.Object.Kind == rdf.KindBlank {
				m["branches"] = append(m["branches"].([]branch), *ensureBranch(q.Object.Value))
			}
		case mapNS + "evidence":
			if q.Object.Kind == rdf.KindBlank {
				e := *ensureEv(q.Object.Value)
				m["ev"] = append(m["ev"].([]Evidence), e)
			} else if q.Object.Kind == rdf.KindLiteral {
				m["ev"] = append(m["ev"].([]Evidence), Evidence{Type: "explicit", Detail: q.Object.Value})
			}
		}
	}

	var keys []string
	for k := range store {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rules []*Rule
	for _, src := range keys {
		m := store[src]
		rel := m["rel"].(string)
		targets := append([]string{}, m["targets"].([]string)...)
		branches := m["branches"].([]branch)
		var cond *Condition
		for _, br := range branches {
			if br.target != "" {
				targets = append(targets, br.target)
			}
			if br.predicate != "" {
				cond = &Condition{Predicate: br.predicate, Value: br.value, ValueIRI: br.valueIRI}
			}
		}
		if len(branches) > 0 && rel == "" {
			rel = OneToMany
		}
		if rel == "" {
			rel = Exact
		}
		if rel == OneToMany && cond == nil && len(targets) >= 2 {
			// multi target without condition branches stays one-to-many
			// with no condition — migration will report it unresolved
			// unless a condition is supplied at decision time.
		}
		ev := m["ev"].([]Evidence)
		if len(ev) == 0 {
			ev = []Evidence{{Type: "explicit", Detail: "listed in mapping candidates fixture"}}
		}
		var brs []Branch
		for _, br := range branches {
			if br.target != "" {
				b := Branch{Target: br.target}
				if br.predicate != "" {
					b.Condition = Condition{Predicate: br.predicate, Value: br.value, ValueIRI: br.valueIRI}
				}
				brs = append(brs, b)
			}
		}
		if rel == OneToMany && len(brs) >= 2 {
			targets = nil
		}
		r := &Rule{
			TermKind: m["kind"].(string),
			Relation: rel,
			Source:   src,
			Targets:  uniqueSorted(targets),
			Branches: brs,
			Evidence: ev,
			Status:   "candidate",
		}
		_ = cond
		if r.TermKind == "" {
			r.TermKind = "class"
		}
		r.RuleFingerprint = r.Fingerprint()
		r.ID = RuleID(r.RuleFingerprint)
		rules = append(rules, r)
	}
	return rules, nil
}

func uniqueSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

var _ = spec.OWLClass
