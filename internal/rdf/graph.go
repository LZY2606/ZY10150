package rdf

// Dataset is a set of quads grouped by named graphs.
type Dataset struct {
	Quads []Quad
}

func NewDataset(quads ...Quad) *Dataset {
	d := &Dataset{}
	d.Quads = append(d.Quads, quads...)
	return d
}

func (d *Dataset) Add(q Quad)       { d.Quads = append(d.Quads, q) }
func (d *Dataset) AddAll(qs []Quad) { d.Quads = append(d.Quads, qs...) }

// GraphNames returns graph IRIs in first-seen order ("" default first if present).
func (d *Dataset) GraphNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, q := range d.Quads {
		if !seen[q.Graph] {
			seen[q.Graph] = true
			names = append(names, q.Graph)
		}
	}
	return names
}

func (d *Dataset) Graph(name string) []Quad {
	var out []Quad
	for _, q := range d.Quads {
		if q.Graph == name {
			out = append(out, q)
		}
	}
	return out
}

// Merge appends another dataset.
func (d *Dataset) Merge(o *Dataset) {
	d.Quads = append(d.Quads, o.Quads...)
}

// View indexes quads for read-only inspection.
type View struct {
	bySP map[string]map[string][]Term // subject -> predicate -> objects (single graph)
	po   []Quad
}

// NewView indexes one graph's quads (call Dataset.Graph first when needed).
func NewView(quads []Quad) *View {
	v := &View{bySP: map[string]map[string][]Term{}, po: quads}
	for _, q := range quads {
		m, ok := v.bySP[q.Subject.Value]
		if !ok {
			m = map[string][]Term{}
			v.bySP[q.Subject.Value] = m
		}
		m[q.Predicate.Value] = append(m[q.Predicate.Value], q.Object)
	}
	return v
}

func (v *View) Objects(subject, predicate string) []Term {
	return v.bySP[subject][predicate]
}

func (v *View) Has(subject, predicate string, object Term) bool {
	for _, o := range v.bySP[subject][predicate] {
		if o.Equals(object) {
			return true
		}
	}
	return false
}

// Types returns all rdf:type objects of a subject.
func (v *View) Types(subject string) []Term {
	return v.Objects(subject, RDFType)
}

func (v *View) Quads() []Quad { return v.po }

// Subjects returns subject terms that carry at least one rdf:type.
func (v *View) TypedSubjects() []Term {
	var out []Term
	seen := map[string]bool{}
	for _, q := range v.po {
		if q.Predicate.Value == RDFType && !seen[q.Subject.Value] {
			seen[q.Subject.Value] = true
			out = append(out, q.Subject)
		}
	}
	return out
}

// EntailedTypes walks rdfs:subClassOf so subclass/type reasoning is available
// to validation and mapping code.
type OntologyIndex struct {
	Parents map[string][]string
	Labels  map[string]Term
}

func BuildOntologyIndex(quads []Quad) *OntologyIndex {
	idx := &OntologyIndex{
		Parents: map[string][]string{},
		Labels:  map[string]Term{},
	}
	for _, q := range quads {
		switch q.Predicate.Value {
		case RDFSSubClassOf:
			if q.Object.IsIRI() {
				idx.Parents[q.Subject.Value] = append(idx.Parents[q.Subject.Value], q.Object.Value)
			}
		case RDFSLabel:
			idx.Labels[q.Subject.Value] = q.Object
		}
	}
	return idx
}

// IsA reports whether cls equals or is transitively subordinate to ancestor.
func (o *OntologyIndex) IsA(cls, ancestor string) bool {
	visited := map[string]bool{}
	var walk func(string) bool
	walk = func(c string) bool {
		if c == ancestor {
			return true
		}
		if visited[c] {
			return false
		}
		visited[c] = true
		for _, p := range o.Parents[c] {
			if walk(p) {
				return true
			}
		}
		return false
	}
	return walk(cls)
}
