package mapping

import (
	"sort"
	"strings"

	"gsb/internal/rdf"
	"gsb/internal/spec"
)

// Suggest runs a new automatic suggestion round against the old/new
// ontologies.  Crucially it never overwrites accepted decisions:
//   - fresh exact-ish hints are inserted as candidates (deduped by rule
//     fingerprint),
//   - an existing accepted decision whose target disappeared from the new
//     ontology, or whose source now has a stronger exact hint, is listed
//     under Affected for re-review.
func (s *Set) Suggest(oldO, newO *spec.Ontology) AffectedReport {
	if s.byFP == nil {
		s.reindex()
	}
	report := AffectedReport{Version: s.Version}
	newByLocal := map[string][]*spec.Term{}
	for _, iri := range newO.Order {
		t := newO.Terms[iri]
		newByLocal[localName(iri)] = append(newByLocal[localName(iri)], t)
	}
	labelIndex := map[string][]*spec.Term{}
	for _, iri := range newO.Order {
		t := newO.Terms[iri]
		for _, l := range t.Labels {
			key := strings.ToLower(strings.TrimSpace(l.Value))
			if key != "" {
				labelIndex[key] = append(labelIndex[key], t)
			}
		}
	}

	oldOrder := append([]string{}, oldO.Order...)
	sort.Strings(oldOrder)
	for _, iri := range oldOrder {
		ot := oldO.Terms[iri]
		if ot.Deprecated {
			continue
		}
		hint := findHint(ot, newO, newByLocal, labelIndex)
		accepted := s.acceptedForSource(iri)
		if hint == nil {
			// If an accepted target vanished, flag for re-review.
			if accepted != nil && accepted.Relation != Deprecated {
				for _, tg := range accepted.Targets {
					if _, ok := newO.Terms[tg]; !ok {
						report.Affected = append(report.Affected, accepted)
						break
					}
				}
			}
			continue
		}
		r := &Rule{
			TermKind: hint.kind,
			Relation: Exact,
			Source:   iri,
			Targets:  []string{hint.target},
			Evidence: []Evidence{hint.evidence},
			Status:   "candidate",
		}
		r.RuleFingerprint = r.Fingerprint()
		r.ID = RuleID(r.RuleFingerprint)
		if accepted != nil {
			// Never overwrite.  Record re-review impact when the hint
			// diverges from the effective decision.
			if accepted.Relation == Deprecated ||
				len(accepted.Targets) != 1 || accepted.Targets[0] != hint.target {
				report.Affected = append(report.Affected, accepted)
			} else {
				report.Duplicated = append(report.Duplicated, accepted)
			}
			continue
		}
		if _, ok := s.byFP[r.RuleFingerprint]; ok {
			report.Duplicated = append(report.Duplicated, r)
			continue
		}
		s.Rules = append(s.Rules, r)
		s.byFP[r.RuleFingerprint] = r
		s.bySrc[iri] = append(s.bySrc[iri], r)
		report.Added = append(report.Added, r)
	}
	return report
}

type hint struct {
	target   string
	kind     string
	evidence Evidence
}

func findHint(ot *spec.Term, newO *spec.Ontology, byLocal map[string][]*spec.Term, labels map[string][]*spec.Term) *hint {
	// Same IRI strongest.
	if nt, ok := newO.Terms[ot.IRI]; ok {
		return &hint{nt.IRI, compatibleKind(ot.Kind, nt.Kind), Evidence{
			Type: "same_iri", Detail: "term keeps the same IRI across versions",
			Quad: firstQuadString(nt),
		}}
	}
	// Same local name.
	if cands := byLocal[localName(ot.IRI)]; len(cands) == 1 {
		nt := cands[0]
		return &hint{nt.IRI, compatibleKind(ot.Kind, nt.Kind), Evidence{
			Type:   "localname_match",
			Detail: "identical local name \"" + localName(ot.IRI) + "\" in new vocabulary",
			Quad:   firstQuadString(nt),
		}}
	}
	// Same language-agnostic label.
	for _, l := range ot.Labels {
		key := strings.ToLower(strings.TrimSpace(l.Value))
		if key == "" {
			continue
		}
		if cands := labels[key]; len(cands) == 1 {
			nt := cands[0]
			return &hint{nt.IRI, compatibleKind(ot.Kind, nt.Kind), Evidence{
				Type:   "label_match",
				Detail: "identical label \"" + l.Value + "\"",
				Quad:   firstQuadString(nt),
			}}
		}
	}
	return nil
}

func firstQuadString(t *spec.Term) string {
	if len(t.EvidenceKeys) > 0 {
		return t.EvidenceKeys[0]
	}
	return ""
}

func compatibleKind(a, b string) string {
	if a == b {
		return a
	}
	if strings.Contains(a, "property") && strings.Contains(b, "property") {
		return b
	}
	return a
}

func localName(iri string) string {
	i := strings.LastIndexAny(iri, "#/")
	if i < 0 {
		return iri
	}
	return iri[i+1:]
}

var _ = rdf.XSDString
