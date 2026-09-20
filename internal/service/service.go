package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gsb/internal/mapping"
	"gsb/internal/migrate"
	"gsb/internal/rdf"
	"gsb/internal/shacl"
	"gsb/internal/spec"
	"gsb/internal/store"
)

// Service serializes reviews with an RWMutex: concurrent readers work
// against a consistent mapping version, while decisions/previews/publish
// take the write lock.  Stale decisions return a version conflict.
type Service struct {
	st *store.Store
	mu sync.RWMutex
}

func New(st *store.Store) *Service { return &Service{st: st} }

// State is the page payload.
type State struct {
	Old        *spec.Ontology   `json:"old"`
	New        *spec.Ontology   `json:"new"`
	Mapping    *mapping.Set     `json:"mapping"`
	Candidates []*mapping.Rule  `json:"candidates"`
	Accepted   []*mapping.Rule  `json:"accepted"`
	Instances  []InstanceView   `json:"instances"`
	Preview    *PreviewView     `json:"preview,omitempty"`
	Published  *PublishedView   `json:"published,omitempty"`
	Documents  []store.Document `json:"documents"`
	Note       string           `json:"note,omitempty"`
}

type InstanceView struct {
	Subject string   `json:"subject"`
	Types   []string `json:"types"`
	Quads   []string `json:"quads"`
	Graph   string   `json:"graph"`
}

type PreviewView struct {
	Report       *migrate.Report   `json:"report"`
	Diagnostics  []shacl.Violation `json:"diagnostics"`
	Categories   map[string]int    `json:"categories"`
	StepCount    int               `json:"step_count"`
	PendingCount int               `json:"pending_count"`
}

type PublishedView struct {
	Fingerprint string   `json:"fingerprint"`
	Quads       []string `json:"quads"`
	Canonical   []string `json:"canonical"`
}

// Load assembles the current review state.
func (s *Service) Load(ctx context.Context) (*State, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.loadLocked(ctx)
}

func (s *Service) loadLocked(ctx context.Context) (*State, error) {
	st := &State{Mapping: mapping.NewSet()}
	docs, err := s.st.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	st.Documents = docs

	oldQ, oldFP, err := s.st.QuadsByKind(ctx, "ontology_old")
	if err != nil {
		return nil, err
	}
	newQ, newFP, err := s.st.QuadsByKind(ctx, "ontology_new")
	if err != nil {
		return nil, err
	}
	if len(oldQ) > 0 {
		st.Old = spec.Parse("old", oldQ)
		_ = oldFP
	}
	if len(newQ) > 0 {
		st.New = spec.Parse("new", newQ)
		_ = newFP
	}

	ms, err := s.st.LoadMapping(ctx)
	if err != nil {
		return nil, err
	}
	st.Mapping = ms
	st.Candidates = ms.Candidates()
	st.Accepted = ms.AcceptedRules()

	instQ, err := s.st.QuadsByKindAll(ctx, "instances")
	if err != nil {
		return nil, err
	}
	st.Instances = instanceViews(instQ)

	fp, pq, err := s.st.LatestPublished(ctx)
	if err != nil {
		return nil, err
	}
	if pq != nil {
		st.Published = &PublishedView{
			Fingerprint: fp,
			Quads:       quadStrings(pq),
			Canonical:   rdf.CanonicalNQuads(pq),
		}
	}
	return st, nil
}

func instanceViews(qs []rdf.Quad) []InstanceView {
	bySubj := map[string]*InstanceView{}
	var order []string
	for _, q := range qs {
		key := q.Subject.String()
		v := bySubj[key]
		if v == nil {
			g := ""
			if q.Graph.Kind == rdf.KindIRI {
				g = q.Graph.Value
			}
			v = &InstanceView{Subject: key, Graph: g}
			bySubj[key] = v
			order = append(order, key)
		}
		v.Quads = append(v.Quads, q.String())
		if q.Predicate.Value == rdf.RDFType && q.Object.Kind == rdf.KindIRI {
			v.Types = append(v.Types, q.Object.Value)
		}
	}
	out := make([]InstanceView, 0, len(order))
	for _, k := range order {
		v := bySubj[k]
		v.Quads = unique(v.Quads)
		v.Types = unique(v.Types)
		out = append(out, *v)
	}
	return out
}

func quadStrings(qs []rdf.Quad) []string {
	out := make([]string, len(qs))
	for i, q := range qs {
		out[i] = q.String()
	}
	return out
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

var _ = json.Marshal
var _ = errors.New
var _ = fmt.Sprint
var _ = strings.TrimSpace
