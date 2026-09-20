package shacl

import (
	"fmt"
	"sort"

	"github.com/example/gsbmig/internal/rdf"
)

// Severity / category codes. These stay distinct so the review page can
// separate missing data, datatype problems, closed-set extras and mapping-borne
// contradictions.
const (
	ViolationMissingData  = "missing_data"
	ViolationDatatype     = "datatype_mismatch"
	ViolationClosedExtra  = "closed_extra_predicate"
	ViolationClosedValue  = "closed_set_extra_value"
	ViolationCardinality  = "cardinality"
	ViolationClass        = "class_range"
	ViolationDisjoint     = "mapping_contradiction_disjoint"
	ViolationUnresolved   = "mapping_unresolved"
	ViolationMappingBorne = "mapping_contradiction"
)

// EvidenceStep is one hop in the diagnostic evidence path.
type EvidenceStep struct {
	Quad  string `json:"quad"`
	Graph string `json:"graph,omitempty"`
	Note  string `json:"note,omitempty"`
}

type Violation struct {
	Code        string         `json:"code"`
	FocusNode   string         `json:"focusNode"`
	Path        string         `json:"path,omitempty"`
	Value       *rdf.Term      `json:"value,omitempty"`
	Message     string         `json:"message"`
	Shape       string         `json:"shape,omitempty"`
	SourceGraph string         `json:"sourceGraph,omitempty"`
	Evidence    []EvidenceStep `json:"evidence"`
	// MappingBorne marks violations that exist only because of how source data
	// mapped (contradictions/unresolved), versus pre-existing bad data.
	MappingBorne bool `json:"mappingBorne"`
}

// Report is the full validation result.
type Report struct {
	Violations []Violation `json:"violations"`
}

// Validator needs the shapes, ontology and a lookup that annotates migrated
// quads with their origin (for mapping-borne classification + evidence).
type OriginInfo struct {
	MappingBorne map[string]bool // quad.String() of migrated quad
	DerivedBy    map[string][]string
	Unresolved   map[string]string // focus node -> unresolved detail
	SourceQuad   map[string]string // migrated quad.String() -> source quad string
}

type sourceFn func(q rdf.Quad) string

type Validator struct {
	Shapes []*NodeShape
	Ont    *Ontology
}

func NewValidator(shapes []*NodeShape, ont *Ontology) *Validator {
	return &Validator{Shapes: shapes, Ont: ont}
}

// Validate checks the union of migrated + pre-existing quads.
func (v *Validator) Validate(allQuads []rdf.Quad, info *OriginInfo) *Report {
	if info == nil {
		info = &OriginInfo{}
	}
	view := rdf.NewView(allQuads)
	rep := &Report{}

	// index provenance

	termKey := func(q rdf.Quad) string {
		return rdf.TermString(q.Subject) + " " + rdf.TermString(q.Predicate) + " " + rdf.TermString(q.Object)
	}
	borneOf := func(q rdf.Quad) bool {
		if info.MappingBorne[q.String()] {
			return true
		}
		// also match term content when the probed quad lacks graph provenance
		for k := range info.MappingBorne {
			if stripGraph(k) == termKey(q) {
				return info.MappingBorne[k]
			}
		}
		return false
	}
	sourceOf := func(q rdf.Quad) string {
		if src := info.SourceQuad[q.String()]; src != "" {
			return src
		}
		tk := termKey(q)
		for k, src := range info.SourceQuad {
			if stripGraph(k) == tk {
				return src
			}
		}
		return ""
	}
	graphOf := func(q rdf.Quad) string { return q.Graph }
	_ = sourceOf

	targets := map[string][]*NodeShape{}
	for _, ns := range v.Shapes {
		for _, tc := range ns.TargetClasses {
			targets[tc] = append(targets[tc], ns)
		}
	}

	// evaluate each focus node that is typed (including subclasses)
	focusSeen := map[string]bool{}
	for _, subj := range view.TypedSubjects() {
		for _, ty := range view.Types(subj.Value) {
			for tc, shapes := range targets {
				if ty.Value == tc || v.Ont.OntIdx.IsA(ty.Value, tc) {
					for _, ns := range shapes {
						if !focusSeen[subj.Value+ns.ID] {
							focusSeen[subj.Value+ns.ID] = true
							v.checkShape(view, ns, subj.Value, borneOf, graphOf, sourceOf, rep)
						}
					}
				}
			}
		}
	}

	// targetNode shapes
	for _, ns := range v.Shapes {
		for _, node := range ns.TargetNodes {
			v.checkShape(view, ns, node, borneOf, graphOf, sourceOf, rep)
		}
	}

	// disjointness contradictions (mapping-borne when both types migrated)
	v.checkDisjoint(view, sourceOf, rep)

	// unresolved one-to-many instances
	foci := make([]string, 0, len(info.Unresolved))
	for k := range info.Unresolved {
		foci = append(foci, k)
	}
	sort.Strings(foci)
	for _, f := range foci {
		rep.Violations = append(rep.Violations, Violation{
			Code:         ViolationUnresolved,
			FocusNode:    f,
			Message:      info.Unresolved[f],
			MappingBorne: true,
			Evidence:     typeEvidence(view, f, borneOf),
		})
	}

	sort.SliceStable(rep.Violations, func(i, j int) bool {
		if rep.Violations[i].Code != rep.Violations[j].Code {
			return rep.Violations[i].Code < rep.Violations[j].Code
		}
		return rep.Violations[i].FocusNode < rep.Violations[j].FocusNode
	})
	return rep
}

func (v *Validator) checkShape(
	view *rdf.View, ns *NodeShape, focus string,
	borneOf func(rdf.Quad) bool,
	graphOf func(rdf.Quad) string,
	sourceOf sourceFn, rep *Report,
) {
	add := func(vi Violation) {
		vi.FocusNode = focus
		vi.Shape = ns.ID
		vi.SourceGraph = firstGraph(view, focus, graphOf)
		rep.Violations = append(rep.Violations, vi)
	}

	// closed: any predicate not explicitly allowed
	if ns.Closed {
		allowed := map[string]bool{rdf.RDFType: true}
		for _, ps := range ns.Properties {
			allowed[ps.Path] = true
		}
		for p := range predicatesOf(view, focus) {
			if allowed[p] || ns.Ignored[p] {
				continue
			}
			extraBorne := anyQuadBorne(view, focus, p, borneOf)
			add(Violation{
				Code:         ViolationClosedExtra,
				Path:         p,
				Message:      fmt.Sprintf("closed shape: predicate %s is not allowed", short(p)),
				MappingBorne: extraBorne,
				Evidence:     pathEvidenceSrc(view, focus, p, sourceOf),
			})
		}
	}

	for _, ps := range ns.Properties {
		values := view.Objects(focus, ps.Path)

		// cardinality
		if ps.MinCount >= 0 && len(values) < ps.MinCount {
			add(Violation{
				Code:         ViolationMissingData,
				Path:         ps.Path,
				Message:      fmt.Sprintf("needs at least %d value(s) of %s, found %d", ps.MinCount, short(ps.Path), len(values)),
				MappingBorne: anyTypeBorne(view, focus, borneOf),
				Evidence:     typeEvidence(view, focus, borneOf),
			})
		}
		if ps.MaxCount >= 0 && len(values) > ps.MaxCount {
			add(Violation{
				Code: ViolationCardinality, Path: ps.Path,
				Message:  fmt.Sprintf("allows at most %d value(s) of %s, found %d", ps.MaxCount, short(ps.Path), len(values)),
				Evidence: typeEvidenceSrc(view, focus, sourceOf),
			})
		}

		for _, val := range values {
			ev := pathEvidenceSrc(view, focus, ps.Path, sourceOf)
			borne := borneOf(quadFor(focus, ps.Path, val))

			if ps.Datatype != "" {
				if val.IsLiteral() && !compatibleDatatype(val.Datatype, ps.Datatype) {
					add(Violation{
						Code: ViolationDatatype, Path: ps.Path, Value: &val,
						Message: fmt.Sprintf("value %q has datatype %s, expected %s",
							val.Value, short(val.Datatype), short(ps.Datatype)),
						MappingBorne: borne, Evidence: ev,
					})
				}
			}

			if len(ps.In) > 0 && !inList(val, ps.In) {
				add(Violation{
					Code: ViolationClosedValue, Path: ps.Path, Value: &val,
					Message:      fmt.Sprintf("value %q is not in the closed value set", val.Value),
					MappingBorne: borne, Evidence: ev,
				})
			}

			if ps.Class != "" && val.IsIRI() {
				if !v.hasRequiredClass(view, val.Value, ps.Class) {
					code := ViolationClass
					msg := fmt.Sprintf("value %s is not a %s", short(val.Value), short(ps.Class))
					if borne {
						code = ViolationMappingBorne
						msg = "mapped edge points to a node whose mapped class contradicts the required " + short(ps.Class)
					}
					add(Violation{
						Code: code, Path: ps.Path, Value: &val,
						Message:      msg,
						MappingBorne: borne, Evidence: ev,
					})
				}
			}
		}
	}
}

func (v *Validator) checkDisjoint(view *rdf.View, sourceOf sourceFn, rep *Report) {
	checked := map[string]bool{}
	for _, subj := range view.TypedSubjects() {
		types := view.Types(subj.Value)
		for i := 0; i < len(types); i++ {
			for j := i + 1; j < len(types); j++ {
				a, b := types[i].Value, types[j].Value
				key := subj.Value + "|" + minStr(a, b) + "|" + maxStr(a, b)
				if checked[key] {
					continue
				}
				checked[key] = true
				if !disjoint(v.Ont, a, b) {
					continue
				}
				// both type quads carry migration provenance => mapping-borne
				srcA := anySource(view, subj.Value, rdf.RDFType, types[i], sourceOf)
				srcB := anySource(view, subj.Value, rdf.RDFType, types[j], sourceOf)
				bothBorne := srcA != "" && srcB != ""
				rep.Violations = append(rep.Violations, Violation{
					Code: ViolationDisjoint, FocusNode: subj.Value,
					Message: fmt.Sprintf("%s is typed as both disjoint classes %s and %s",
						short(subj.Value), short(a), short(b)),
					MappingBorne: bothBorne,
					Evidence: append(
						stepListSrc(view, subj.Value, rdf.RDFType, types[i], sourceOf),
						stepListSrc(view, subj.Value, rdf.RDFType, types[j], sourceOf)...),
				})
			}
		}
	}
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}
func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}
func anySource(view *rdf.View, s, p string, o rdf.Term, sourceOf sourceFn) string {
	for _, q := range view.Quads() {
		if q.Subject.Value == s && q.Predicate.Value == p && q.Object.Equals(o) {
			if src := sourceOf(q); src != "" {
				return src
			}
		}
	}
	return ""
}

func disjoint(o *Ontology, a, b string) bool {
	for _, d := range o.Disjoint[a] {
		if d == b || o.OntIdx.IsA(b, d) {
			return true
		}
	}
	return false
}

func (v *Validator) hasRequiredClass(view *rdf.View, node, class string) bool {
	for _, ty := range view.Types(node) {
		if ty.Value == class || v.Ont.OntIdx.IsA(ty.Value, class) {
			return true
		}
	}
	return false
}

// ---- evidence helpers ----

func predicatesOf(view *rdf.View, focus string) map[string]bool {
	out := map[string]bool{}
	for _, q := range view.Quads() {
		if q.Subject.Value == focus {
			out[q.Predicate.Value] = true
		}
	}
	return out
}

func anyQuadBorne(view *rdf.View, focus, pred string, borneOf func(rdf.Quad) bool) bool {
	for _, q := range view.Quads() {
		if q.Subject.Value == focus && q.Predicate.Value == pred && borneOf(q) {
			return true
		}
	}
	return false
}

func anyTypeBorne(view *rdf.View, focus string, borneOf func(rdf.Quad) bool) bool {
	for _, q := range view.Quads() {
		if q.Subject.Value == focus && q.Predicate.Value == rdf.RDFType && borneOf(q) {
			return true
		}
	}
	return false
}

func firstGraph(view *rdf.View, focus string, graphOf func(rdf.Quad) string) string {
	for _, q := range view.Quads() {
		if q.Subject.Value == focus {
			return graphOf(q)
		}
	}
	return ""
}

func quadFor(s, p string, o rdf.Term) rdf.Quad {
	return rdf.Quad{Subject: rdf.NewIRI(s), Predicate: rdf.NewIRI(p), Object: o}
}

func stepList(view *rdf.View, s, p string, o rdf.Term, borneOf func(rdf.Quad) bool, info *OriginInfo) []EvidenceStep {
	for _, q := range view.Quads() {
		if q.Subject.Value == s && q.Predicate.Value == p && q.Object.Equals(o) {
			st := EvidenceStep{Quad: q.String(), Graph: q.Graph}
			if info != nil && info.SourceQuad != nil {
				if src, ok := info.SourceQuad[q.String()]; ok {
					st.Note = "derived from: " + src
				}
			}
			return []EvidenceStep{st}
		}
	}
	return nil
}

func pathEvidence(view *rdf.View, focus, pred string, borneOf func(rdf.Quad) bool, info *OriginInfo) []EvidenceStep {
	var out []EvidenceStep
	for _, q := range view.Quads() {
		if q.Subject.Value == focus && q.Predicate.Value == pred {
			st := EvidenceStep{Quad: q.String(), Graph: q.Graph}
			if info != nil && info.SourceQuad != nil {
				if src, ok := info.SourceQuad[q.String()]; ok {
					st.Note = "derived from: " + src
				}
			}
			out = append(out, st)
		}
	}
	return out
}

func typeEvidence(view *rdf.View, focus string, borneOf func(rdf.Quad) bool) []EvidenceStep {
	var out []EvidenceStep
	for _, ty := range view.Types(focus) {
		out = append(out, EvidenceStep{Quad: quadFor(focus, rdf.RDFType, ty).String()})
	}
	return out
}

func stepListSrc(view *rdf.View, s, p string, o rdf.Term, sourceOf sourceFn) []EvidenceStep {
	for _, q := range view.Quads() {
		if q.Subject.Value == s && q.Predicate.Value == p && q.Object.Equals(o) {
			st := EvidenceStep{Quad: q.String(), Graph: q.Graph}
			if src := sourceOf(q); src != "" {
				st.Note = "derived from: " + src
			}
			return []EvidenceStep{st}
		}
	}
	st := EvidenceStep{Quad: rdf.TermString(rdf.NewIRI(s)) + " " + rdf.TermString(rdf.NewIRI(p)) + " " + rdf.TermString(o)}
	return []EvidenceStep{st}
}

func pathEvidenceSrc(view *rdf.View, focus, pred string, sourceOf sourceFn) []EvidenceStep {
	var out []EvidenceStep
	for _, q := range view.Quads() {
		if q.Subject.Value == focus && q.Predicate.Value == pred {
			st := EvidenceStep{Quad: q.String(), Graph: q.Graph}
			if src := sourceOf(q); src != "" {
				st.Note = "derived from: " + src
			}
			out = append(out, st)
		}
	}
	return out
}

func typeEvidenceSrc(view *rdf.View, focus string, sourceOf sourceFn) []EvidenceStep {
	var out []EvidenceStep
	for _, q := range view.Quads() {
		if q.Subject.Value == focus && q.Predicate.Value == rdf.RDFType {
			st := EvidenceStep{Quad: q.String(), Graph: q.Graph}
			if src := sourceOf(q); src != "" {
				st.Note = "derived from: " + src
			}
			out = append(out, st)
		}
	}
	return out
}

func inList(v rdf.Term, list []rdf.Term) bool {
	for _, x := range list {
		if x.Equals(v) {
			return true
		}
	}
	return false
}

// compatibleDatatype understands the numeric hierarchy we actually use.
func compatibleDatatype(actual, expected string) bool {
	if actual == expected {
		return true
	}
	numeric := map[string]bool{
		rdf.XSDInteger: true, rdf.XSDDecimal: true,
		rdf.XSDDouble: true, rdf.XSDFloat: true,
	}
	return numeric[actual] && numeric[expected]
}

func stripGraph(line string) string {
	// stored quad string form is "s p o [graph]"; drop the trailing graph part
	for i := len(line) - 1; i >= 0; i-- {
		if line[i] == '[' {
			return line[:i]
		}
	}
	return line
}

func short(iri string) string {
	for i := len(iri) - 1; i >= 0; i-- {
		if iri[i] == '#' || iri[i] == '/' {
			return iri[i+1:]
		}
	}
	return iri
}
