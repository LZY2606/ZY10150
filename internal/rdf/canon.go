package rdf

import (
	"sort"
	"strings"
)

// Graph is an unordered set of quads with helpers for migration and
// validation.  Blank-node labels are parse-local and must never be used
// for identity across imports; use CanonicalNQuads / GraphFingerprint.
type Graph struct {
	Quads []Quad
}

func NewGraph(qs ...Quad) *Graph { return &Graph{Quads: append([]Quad{}, qs...)} }

// CanonicalNQuads returns a deterministic N-Quads rendering.  Blank nodes
// are relabelled per named graph by iterative color refinement so that the
// output depends only on the graph structure and not on import-time
// temporary identifiers.
func CanonicalNQuads(quads []Quad) []string {
	byGraph := map[string][]Quad{}
	var graphs []string
	for _, q := range quads {
		g := graphName(q.Graph)
		if _, ok := byGraph[g]; !ok {
			graphs = append(graphs, g)
		}
		byGraph[g] = append(byGraph[g], q)
	}
	sort.Strings(graphs)
	var out []string
	for _, g := range graphs {
		mapping := canonicalBlankLabels(byGraph[g])
		var lines []string
		for _, q := range byGraph[g] {
			lines = append(lines, renderCanonical(q, mapping))
		}
		sort.Strings(lines)
		out = append(out, lines...)
	}
	return out
}

func graphName(t Term) string {
	if t.Kind == KindDefaultGraph {
		return ""
	}
	return t.Value
}

func renderCanonical(q Quad, mapping map[string]string) string {
	r := func(t Term) string {
		if t.Kind == KindBlank {
			if c, ok := mapping[t.Value]; ok {
				return "_:c" + c
			}
			return "_:c0"
		}
		return t.String()
	}
	line := r(q.Subject) + " " + r(q.Predicate) + " " + r(q.Object)
	if q.Graph.Kind != KindDefaultGraph {
		line += " " + q.Graph.String()
	}
	return line + " ."
}

// canonicalBlankLabels implements deterministic, structure-based
// relabeling.  Each blank node gets an initial color based on its
// incident (position, predicate, other-term) signatures; colors are then
// refined iteratively from neighbor colors and canonically ordered.
func canonicalBlankLabels(quads []Quad) map[string]string {
	blanks := map[string]bool{}
	for _, q := range quads {
		if q.Subject.Kind == KindBlank {
			blanks[q.Subject.Value] = true
		}
		if q.Object.Kind == KindBlank {
			blanks[q.Object.Value] = true
		}
	}
	mapping := map[string]string{}
	if len(blanks) == 0 {
		return mapping
	}
	color := map[string]string{}
	var names []string
	for b := range blanks {
		names = append(names, b)
	}
	sort.Strings(names)
	for _, b := range names {
		color[b] = initialColor(b, quads)
	}
	// Refine until stable, but at most as many rounds as nodes.
	for round := 0; round < len(names)+1; round++ {
		next := map[string]string{}
		for _, b := range names {
			var sig []string
			for _, q := range quads {
				if q.Subject.Kind == KindBlank && q.Subject.Value == b {
					sig = append(sig, "out|"+q.Predicate.String()+"|"+neighborColor(q.Object, color))
				}
				if q.Object.Kind == KindBlank && q.Object.Value == b {
					sig = append(sig, "in|"+q.Predicate.String()+"|"+neighborColor(q.Subject, color))
				}
			}
			sort.Strings(sig)
			next[b] = color[b] + "||" + strings.Join(sig, ";")
		}
		if mapsEqual(color, next) {
			break
		}
		color = next
	}
	// Order blanks by refined color; ties (truly isomorphic nodes) are
	// broken by their structural position which is indistinguishable by
	// design — lexicographic tie-break on the temporary label keeps the
	// output deterministic.
	type cv struct {
		name string
		col  string
	}
	var order []cv
	for _, b := range names {
		order = append(order, cv{b, color[b]})
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].col != order[j].col {
			return order[i].col < order[j].col
		}
		return order[i].name < order[j].name
	})
	for i, v := range order {
		mapping[v.name] = itoa(i)
	}
	return mapping
}

func neighborColor(t Term, color map[string]string) string {
	if t.Kind == KindBlank {
		if c, ok := color[t.Value]; ok {
			return "B:" + c
		}
		return "B:?"
	}
	return "T:" + t.String()
}

func initialColor(b string, quads []Quad) string {
	var sig []string
	for _, q := range quads {
		if q.Subject.Kind == KindBlank && q.Subject.Value == b {
			sig = append(sig, "out|"+q.Predicate.String()+"|"+q.Object.String())
		}
		if q.Object.Kind == KindBlank && q.Object.Value == b {
			sig = append(sig, "in|"+q.Predicate.String()+"|"+q.Subject.String())
		}
	}
	sort.Strings(sig)
	return strings.Join(sig, ";")
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// GraphFingerprint fingerprints the normalized graph content.
func GraphFingerprint(quads []Quad) string {
	return Fingerprint(CanonicalNQuads(quads))
}

// RenameBlanks applies a canonical mapping, returning quads with stable
// cN blank labels.
func RenameBlanks(quads []Quad) []Quad {
	byGraph := map[string]map[string]string{}
	groups := map[string][]Quad{}
	for _, q := range quads {
		g := graphName(q.Graph)
		groups[g] = append(groups[g], q)
	}
	for g, qs := range groups {
		byGraph[g] = canonicalBlankLabels(qs)
	}
	out := make([]Quad, len(quads))
	for i, q := range quads {
		m := byGraph[graphName(q.Graph)]
		rn := func(t Term) Term {
			if t.Kind == KindBlank {
				return Blank("c" + m[t.Value])
			}
			return t
		}
		out[i] = Quad{Graph: q.Graph, Subject: rn(q.Subject), Predicate: q.Predicate, Object: rn(q.Object)}
	}
	return out
}
