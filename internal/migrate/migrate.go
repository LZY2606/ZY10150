package migrate

import (
	"sort"

	"gsb/internal/mapping"
	"gsb/internal/rdf"
)

const (
	StepResolved   = "resolved"
	StepUnmapped   = "unmapped"
	StepDeprecated = "deprecated"
	StepPending    = "pending_condition"
)

// Step preserves the evidence chain for one derived triple:
// the original triple, the applied mapping rule and the resulting triple.
type Step struct {
	Kind        string    `json:"kind"`
	Original    rdf.Quad  `json:"original"`
	MappingID   string    `json:"mapping_id,omitempty"`
	Relation    string    `json:"relation,omitempty"`
	Source      string    `json:"source,omitempty"`
	Targets     []string  `json:"targets,omitempty"`
	Condition   string    `json:"condition,omitempty"`
	Derived     *rdf.Quad `json:"derived,omitempty"`
	Explanation string    `json:"explanation"`
}

// Report is the migration preview output.
type Report struct {
	MappingVersion int        `json:"mapping_version"`
	VersionFP      string     `json:"version_fp"`
	SourceFP       string     `json:"source_fp"`
	ContentFP      string     `json:"content_fp"`
	RuleFP         string     `json:"rule_fp"`
	Steps          []Step     `json:"steps"`
	MigratedGraph  string     `json:"migrated_graph"`
	MigratedQuads  []rdf.Quad `json:"migrated_quads,omitempty"`
	CanonicalLines []string   `json:"canonical_lines,omitempty"`
}

type Options struct {
	MigratedGraph string
}

type index struct {
	byClass map[string]*mapping.Rule
	byProp  map[string]*mapping.Rule
	byInd   map[string]*mapping.Rule
}

// Preview derives the target graph.  It never drops a triple silently:
// unmapped terms, deprecated terms and unsatisfied conditions remain
// visible as explicit steps, each carrying its original triple.
func Preview(source []rdf.Quad, rules []*mapping.Rule, version int, versionFP string, opts Options) *Report {
	if opts.MigratedGraph == "" {
		opts.MigratedGraph = "http://gsb.local/graph/migrated"
	}
	ix := index{
		byClass: map[string]*mapping.Rule{},
		byProp:  map[string]*mapping.Rule{},
		byInd:   map[string]*mapping.Rule{},
	}
	for _, r := range rules {
		switch r.TermKind {
		case "class":
			ix.byClass[r.Source] = r
		case "property":
			ix.byProp[r.Source] = r
		case "individual":
			ix.byInd[r.Source] = r
		}
	}

	rep := &Report{
		MappingVersion: version,
		VersionFP:      versionFP,
		SourceFP:       rdf.GraphFingerprint(source),
		MigratedGraph:  opts.MigratedGraph,
	}
	graph := rdf.IRI(opts.MigratedGraph)
	// subject -> predicate -> values, used to evaluate branch conditions
	// against the full instance rather than one isolated triple.
	desc := describe(source)

	for _, q := range source {
		switch {
		case q.Predicate.Kind == rdf.KindIRI && q.Predicate.Value == rdf.RDFType && q.Object.Kind == rdf.KindIRI:
			if r, ok := ix.byClass[q.Object.Value]; ok {
				applyClass(q, r, graph, desc, ix, &rep.Steps)
			} else {
				rep.Steps = append(rep.Steps, Step{
					Kind: StepUnmapped, Original: q, Source: q.Object.Value,
					Explanation: "no accepted class mapping for <" + q.Object.Value + ">",
				})
			}
		case q.Predicate.Kind == rdf.KindIRI:
			if r, ok := ix.byProp[q.Predicate.Value]; ok {
				applyProperty(q, r, graph, desc, ix, &rep.Steps)
			} else {
				rep.Steps = append(rep.Steps, Step{
					Kind: StepUnmapped, Original: q, Source: q.Predicate.Value,
					Explanation: "no accepted property mapping for <" + q.Predicate.Value + ">",
				})
			}
		default:
			rep.Steps = append(rep.Steps, Step{
				Kind: StepUnmapped, Original: q,
				Explanation: "triple predicate is not an IRI; unsupported by this engine",
			})
		}
	}

	var derived []rdf.Quad
	for i := range rep.Steps {
		if rep.Steps[i].Derived != nil {
			derived = append(derived, *rep.Steps[i].Derived)
		}
	}
	rep.MigratedQuads = dedupeQuads(derived)
	rep.CanonicalLines = rdf.CanonicalNQuads(rep.MigratedQuads)
	rep.ContentFP = rdf.Fingerprint(rep.CanonicalLines)
	rep.RuleFP = DecisionsFingerprint(rules)
	return rep
}

// DecisionsFingerprint hashes the accepted rule set content used for a run.
func DecisionsFingerprint(rules []*mapping.Rule) string {
	var lines []string
	for _, r := range rules {
		lines = append(lines, r.DecisionLines()...)
	}
	sort.Strings(lines)
	return rdf.Fingerprint(lines)
}

func applyClass(q rdf.Quad, r *mapping.Rule, graph rdf.Term, desc description, ix index, steps *[]Step) {
	target, cond, status := resolveTarget(r, q.Subject, desc)
	switch status {
	case "deprecated":
		*steps = append(*steps, Step{
			Kind: StepDeprecated, Original: q, MappingID: r.ID,
			Relation: mapping.Deprecated, Source: r.Source,
			Explanation: "source class <" + r.Source + "> is deprecated with no replacement; triple dropped",
		})
	case "unaccepted":
		*steps = append(*steps, Step{
			Kind: StepUnmapped, Original: q, Source: r.Source, MappingID: r.ID,
			Explanation: "mapping " + r.ID + " is not in an accepted state",
		})
	case "pending":
		*steps = append(*steps, Step{
			Kind: StepPending, Original: q, MappingID: r.ID,
			Relation: mapping.OneToMany, Source: r.Source,
			Targets: branchTargets(r), Condition: cond,
			Explanation: "one-to-many discriminating condition not satisfied; left unresolved instead of guessing",
		})
	case "resolved":
		subj := rewriteSubject(q.Subject, ix)
		d := rdf.Quad{Graph: graph, Subject: subj, Predicate: q.Predicate, Object: rdf.IRI(target)}
		*steps = append(*steps, Step{
			Kind: StepResolved, Original: q, MappingID: r.ID,
			Relation: r.Relation, Source: r.Source, Targets: branchTargets(r),
			Condition: cond, Derived: &d,
			Explanation: "type mapped via accepted " + r.Relation + " mapping " + r.ID,
		})
	}
}

func applyProperty(q rdf.Quad, r *mapping.Rule, graph rdf.Term, desc description, ix index, steps *[]Step) {
	target, cond, status := resolveTarget(r, q.Subject, desc)
	switch status {
	case "deprecated":
		*steps = append(*steps, Step{
			Kind: StepDeprecated, Original: q, MappingID: r.ID,
			Relation: mapping.Deprecated, Source: r.Source,
			Explanation: "source property <" + r.Source + "> is deprecated with no replacement; triple dropped",
		})
		return
	case "unaccepted":
		*steps = append(*steps, Step{
			Kind: StepUnmapped, Original: q, Source: r.Source, MappingID: r.ID,
			Explanation: "mapping " + r.ID + " is not in an accepted state",
		})
		return
	case "pending":
		*steps = append(*steps, Step{
			Kind: StepPending, Original: q, MappingID: r.ID,
			Relation: mapping.OneToMany, Source: r.Source,
			Targets: branchTargets(r), Condition: cond,
			Explanation: "one-to-many discriminating condition not satisfied; left unresolved",
		})
		return
	}
	obj := q.Object
	// Rewrite object IRIs through accepted individual exact mappings.
	if q.Object.Kind == rdf.KindIRI {
		if im, ok := ix.byInd[q.Object.Value]; ok && im.Status == "accepted" &&
			im.Relation == mapping.Exact && len(im.Targets) == 1 {
			obj = rdf.IRI(im.Targets[0])
		}
	}
	d := rdf.Quad{Graph: graph, Subject: rewriteSubject(q.Subject, ix), Predicate: rdf.IRI(target), Object: obj}
	*steps = append(*steps, Step{
		Kind: StepResolved, Original: q, MappingID: r.ID,
		Relation: r.Relation, Source: r.Source, Targets: branchTargets(r),
		Condition: cond, Derived: &d,
		Explanation: "property mapped via accepted " + r.Relation + " mapping " + r.ID,
	})
}

// rewriteSubject maps an individual IRI used as a triple subject through
// an accepted exact individual mapping; blank nodes are left intact.
func rewriteSubject(t rdf.Term, ix index) rdf.Term {
	if t.Kind != rdf.KindIRI {
		return t
	}
	if im, ok := ix.byInd[t.Value]; ok && im.Status == "accepted" &&
		im.Relation == mapping.Exact && len(im.Targets) == 1 {
		return rdf.IRI(im.Targets[0])
	}
	return t
}

// resolveTarget returns the chosen target IRI, a human readable condition
// description, and one of resolved/pending/deprecated/unaccepted.
func resolveTarget(r *mapping.Rule, subj rdf.Term, desc description) (target, condText, status string) {
	switch r.Status {
	case "deprecated":
		return "", "", "deprecated"
	case "accepted":
	default:
		return "", "", "unaccepted"
	}
	switch r.Relation {
	case mapping.Deprecated:
		return "", "", "deprecated"
	case mapping.OneToMany:
		if len(r.Branches) > 0 {
			for _, b := range r.Branches {
				if matches(desc, subj, b.Condition) {
					return b.Target, b.Condition.String(), "resolved"
				}
			}
			return "", summarizeConditions(r.Branches), "pending"
		}
		// targets without per-branch conditions: cannot decide
		return "", "no discriminating condition provided", "pending"
	default:
		if len(r.Targets) == 0 {
			return "", "", "pending"
		}
		return r.Targets[0], "", "resolved"
	}
}

func branchTargets(r *mapping.Rule) []string {
	if len(r.Branches) > 0 {
		out := make([]string, len(r.Branches))
		for i, b := range r.Branches {
			out[i] = b.Target
		}
		sort.Strings(out)
		return out
	}
	return append([]string{}, r.Targets...)
}

func summarizeConditions(bs []mapping.Branch) string {
	out := "any of: "
	for i, b := range bs {
		if i > 0 {
			out += "; "
		}
		out += b.Target + " when " + b.Condition.String()
	}
	return out
}

// description indexes a source graph for condition evaluation.
type description map[string]map[string][]rdf.Term

func describe(qs []rdf.Quad) description {
	d := description{}
	for _, q := range qs {
		if q.Subject.Kind != rdf.KindIRI && q.Subject.Kind != rdf.KindBlank {
			continue
		}
		if q.Predicate.Kind != rdf.KindIRI {
			continue
		}
		sk := q.Subject.String()
		if d[sk] == nil {
			d[sk] = map[string][]rdf.Term{}
		}
		d[sk][q.Predicate.Value] = append(d[sk][q.Predicate.Value], q.Object)
	}
	return d
}

func matches(d description, subj rdf.Term, c mapping.Condition) bool {
	if !c.Present() {
		return true
	}
	vals, ok := d[subj.String()][c.Predicate]
	if !ok {
		return false
	}
	for _, v := range vals {
		if c.ValueIRI {
			if v.Kind == rdf.KindIRI && v.Value == c.Value {
				return true
			}
			continue
		}
		if v.Kind == rdf.KindLiteral && literalEquals(v, c.Value) {
			return true
		}
	}
	return false
}

func literalEquals(t rdf.Term, lex string) bool {
	if t.Value == lex {
		return true
	}
	// support "true"/"false" vs 0/1 boolean lexical forms loosely
	if t.Datatype == rdf.XSDBoolean {
		return boolLex(t.Value) == boolLex(lex)
	}
	return false
}

func boolLex(s string) string {
	switch s {
	case "1", "true":
		return "true"
	case "0", "false":
		return "false"
	}
	return s
}

func dedupeQuads(qs []rdf.Quad) []rdf.Quad {
	seen := map[string]bool{}
	var out []rdf.Quad
	for _, q := range qs {
		k := q.String()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, q)
	}
	return out
}
