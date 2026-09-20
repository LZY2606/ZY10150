package rdf

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// Canonicalization replaces unstable blank-node labels with content-derived
// canonical labels (c0, c1, ...):
//
//  1. ground signature per blank node (edges touching named terms),
//  2. Weisfeiler-Lehman color refinement over blank-node adjacency,
//  3. connected components; small components resolve remaining symmetry by
//     bounded full-permutation search for the lexicographically smallest
//     rendering, larger components fall back to refined-color order.
//
// Isomorphic graphs hash identically even when parser blank-node ids differ.
type Canonical struct {
	Lines       []string
	Fingerprint string
	Labels      map[string]map[string]string // graph -> old label -> canonical label
}

const maxBruteForce = 7 // 7! = 5040 permutations

func CanonicalizeDataset(d *Dataset) *Canonical {
	var blocks []string
	labels := map[string]map[string]string{}
	for _, g := range d.GraphNames() {
		lines, mapLabels := canonicalizeGraph(d.Graph(g))
		if g == "" {
			blocks = append(blocks, "# GRAPH DEFAULT")
		} else {
			blocks = append(blocks, "# GRAPH <"+g+">")
		}
		blocks = append(blocks, lines...)
		labels[g] = mapLabels
	}
	sum := sha256.Sum256([]byte(strings.Join(blocks, "\n")))
	return &Canonical{Lines: blocks, Fingerprint: hex.EncodeToString(sum[:]), Labels: labels}
}

func canonicalizeGraph(quads []Quad) ([]string, map[string]string) {
	var ground []Quad
	bnodes := map[string]bool{}
	for _, q := range quads {
		if q.Subject.IsBlank() {
			bnodes[q.Subject.Value] = true
		}
		if q.Object.IsBlank() {
			bnodes[q.Object.Value] = true
		}
		if !q.Subject.IsBlank() && !q.Object.IsBlank() {
			ground = append(ground, q)
		}
	}
	if len(bnodes) == 0 {
		return renderLines(ground, nil), map[string]string{}
	}

	colors := refineColors(quads, bnodes)
	comps := bnodeComponents(quads, bnodes)

	type rendered struct {
		text  string
		local map[string]string // bnode -> x-slot
	}
	var rs []rendered
	for _, comp := range comps {
		text, local := bestComponentNaming(quads, comp, colors)
		rs = append(rs, rendered{text, local})
	}
	// deterministic component order: canonical text, then member set
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].text != rs[j].text {
			return rs[i].text < rs[j].text
		}
		return len(rs[i].local) < len(rs[j].local)
	})

	assign := map[string]string{}
	offset := 0
	for _, r := range rs {
		size := len(r.local)
		// map x-slot k to global c{offset+k}; discover x slots in use
		for bn, x := range r.local {
			k, _ := strconv.Atoi(strings.TrimPrefix(x, "x"))
			assign[bn] = "c" + strconv.Itoa(offset+k)
		}
		offset += size
	}

	all := append([]Quad{}, ground...)
	for _, q := range quads {
		if q.Subject.IsBlank() || q.Object.IsBlank() {
			all = append(all, q)
		}
	}
	return renderLines(all, assign), assign
}

// bestComponentNaming returns the lex-smallest component rendering using local
// labels x0..x(n-1), and the bnode->x label map that produced it.
func bestComponentNaming(quads []Quad, comp map[string]bool, colors map[string]string) (string, map[string]string) {
	members := make([]string, 0, len(comp))
	for b := range comp {
		members = append(members, b)
	}
	sort.Strings(members)

	label := func(mapping map[string]string) string {
		var ls []string
		for _, q := range quads {
			if compBnode(comp, q) {
				ls = append(ls, renderLine(q, func(b string) string { return mapping[b] }))
			}
		}
		sort.Strings(ls)
		return strings.Join(ls, "\n")
	}

	if len(members) <= maxBruteForce {
		best := ""
		var bestMap map[string]string
		perm := make([]string, len(members))
		copy(perm, members)
		enumeratePerms(perm, 0, func(p []string) bool {
			m := map[string]string{}
			for i, bn := range p {
				m[bn] = "x" + strconv.Itoa(i)
			}
			t := label(m)
			if bestMap == nil || t < best {
				best = t
				bestMap = map[string]string{}
				for k, v := range m {
					bestMap[k] = v
				}
			}
			return true
		})
		return best, bestMap
	}

	// Fallback for unexpectedly large components: refined-color ordering.
	// Nodes sharing a color get a stable tie-break by parser label.
	order := make([]string, len(members))
	copy(order, members)
	sort.SliceStable(order, func(i, j int) bool {
		if colors[order[i]] != colors[order[j]] {
			return colors[order[i]] < colors[order[j]]
		}
		return order[i] < order[j]
	})
	m := map[string]string{}
	for i, bn := range order {
		m[bn] = "x" + strconv.Itoa(i)
	}
	return label(m), m
}

func enumeratePerms(a []string, k int, visit func([]string) bool) bool {
	if k == len(a) {
		return visit(a)
	}
	used := map[string]bool{}
	for i := k; i < len(a); i++ {
		if used[a[i]] {
			continue
		}
		used[a[i]] = true
		a[k], a[i] = a[i], a[k]
		if !enumeratePerms(a, k+1, visit) {
			return false
		}
		a[k], a[i] = a[i], a[k]
	}
	return true
}

func compBnode(comp map[string]bool, q Quad) bool {
	return (q.Subject.IsBlank() && comp[q.Subject.Value]) ||
		(q.Object.IsBlank() && comp[q.Object.Value])
}

// ---- color refinement ----

func refineColors(quads []Quad, bnodes map[string]bool) map[string]string {
	color := map[string]string{}
	for b := range bnodes {
		color[b] = groundSignature(b, quads)
	}
	color = normalize(color)
	for {
		next := map[string]string{}
		for b := range bnodes {
			var desc []string
			for _, q := range quads {
				if q.Subject.IsBlank() && q.Subject.Value == b {
					if q.Object.IsBlank() {
						desc = append(desc, "out:"+q.Predicate.Value+":"+color[q.Object.Value])
					} else {
						desc = append(desc, "out:"+q.Predicate.Value+":"+termKey(q.Object))
					}
				}
				if q.Object.IsBlank() && q.Object.Value == b {
					if q.Subject.IsBlank() {
						desc = append(desc, "in:"+color[q.Subject.Value]+":"+q.Predicate.Value)
					} else {
						desc = append(desc, "in:"+termKey(q.Subject)+":"+q.Predicate.Value)
					}
				}
			}
			sort.Strings(desc)
			h := sha256.Sum256([]byte(color[b] + "|" + strings.Join(desc, ";")))
			next[b] = hex.EncodeToString(h[:])
		}
		// Convergence is reached when the PARTITION (which nodes share a color)
		// stops changing. Labels themselves are re-ranked by hash each round and
		// must not be compared, otherwise the ranks oscillate forever.
		if samePartition(color, next) {
			return normalize(next)
		}
		color = normalize(next)
	}
}

// samePartition ignores color labels and compares the equivalence classes.
func samePartition(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	groups := func(m map[string]string) map[string]map[string]bool {
		g := map[string]map[string]bool{}
		for k, v := range m {
			if g[v] == nil {
				g[v] = map[string]bool{}
			}
			g[v][k] = true
		}
		return g
	}
	ga, gb := groups(a), groups(b)
	if len(ga) != len(gb) {
		return false
	}
	// map each node in a to its group signature, compare membership in b
	sig := func(g map[string]map[string]bool, node string) []string {
		for color, members := range g {
			if members[node] {
				var ms []string
				for m := range members {
					ms = append(ms, m)
				}
				sort.Strings(ms)
				return append(ms, "@"+color)
			}
		}
		return nil
	}
	for k := range a {
		x, y := sig(ga, k), sig(gb, k)
		if len(x) != len(y) {
			return false
		}
		// compare member sets only (drop the @color label)
		xm, ym := x[:len(x)-1], y[:len(y)-1]
		for i := range xm {
			if xm[i] != ym[i] {
				return false
			}
		}
	}
	return true
}

func normalize(m map[string]string) map[string]string {
	uniq := map[string]bool{}
	var vals []string
	for _, v := range m {
		if !uniq[v] {
			uniq[v] = true
			vals = append(vals, v)
		}
	}
	sort.Strings(vals)
	rank := map[string]string{}
	for i, v := range vals {
		rank[v] = strconv.Itoa(i)
	}
	out := map[string]string{}
	for k, v := range m {
		out[k] = rank[v]
	}
	return out
}

func groundSignature(bn string, quads []Quad) string {
	var desc []string
	for _, q := range quads {
		if q.Subject.IsBlank() && q.Subject.Value == bn && !q.Object.IsBlank() {
			desc = append(desc, "out:"+q.Predicate.Value+":"+termKey(q.Object))
		}
		if q.Object.IsBlank() && q.Object.Value == bn && !q.Subject.IsBlank() {
			desc = append(desc, "in:"+termKey(q.Subject)+":"+q.Predicate.Value)
		}
	}
	sort.Strings(desc)
	h := sha256.Sum256([]byte(strings.Join(desc, ";")))
	return hex.EncodeToString(h[:])
}

func bnodeComponents(quads []Quad, bnodes map[string]bool) []map[string]bool {
	adj := map[string]map[string]bool{}
	for b := range bnodes {
		adj[b] = map[string]bool{}
	}
	for _, q := range quads {
		if q.Subject.IsBlank() && q.Object.IsBlank() {
			adj[q.Subject.Value][q.Object.Value] = true
			adj[q.Object.Value][q.Subject.Value] = true
		}
	}
	seen := map[string]bool{}
	var comps []map[string]bool
	for start := range bnodes {
		if seen[start] {
			continue
		}
		comp := map[string]bool{}
		stack := []string{start}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			comp[cur] = true
			for n := range adj[cur] {
				if !seen[n] {
					stack = append(stack, n)
				}
			}
		}
		comps = append(comps, comp)
	}
	return comps
}

// ---- rendering ----

func termKey(t Term) string {
	switch t.Kind {
	case KindIRI:
		return "I<" + t.Value + ">"
	case KindBlank:
		return "B"
	default:
		if t.Lang != "" {
			return `L"` + escape(t.Value) + `"@` + t.Lang
		}
		return `L"` + escape(t.Value) + `"^^<` + t.Datatype + `>`
	}
}

func renderLines(quads []Quad, assign map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, q := range quads {
		line := renderLine(q, func(b string) string {
			if v, ok := assign[b]; ok {
				return v
			}
			return b
		})
		if !seen[line] {
			seen[line] = true
			out = append(out, line)
		}
	}
	sort.Strings(out)
	return out
}

func renderLine(q Quad, bmap func(string) string) string {
	return encodeTerm(q.Subject, bmap) + " " + encodeTerm(q.Predicate, bmap) + " " +
		encodeTerm(q.Object, bmap) + " ."
}

func encodeTerm(t Term, bmap func(string) string) string {
	switch t.Kind {
	case KindIRI:
		return "<" + t.Value + ">"
	case KindBlank:
		return "_:" + bmap(t.Value)
	default:
		if t.Lang != "" {
			return `"` + escape(t.Value) + `"@` + t.Lang
		}
		return `"` + escape(t.Value) + `"^^<` + t.Datatype + `>`
	}
}

func escape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return r.Replace(s)
}
