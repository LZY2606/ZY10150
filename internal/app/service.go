// Package app orchestrates loading fixtures, mapping decisions, migration
// previews, SHACL validation and atomic publication.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/example/gsbmig/internal/fixtures"
	"github.com/example/gsbmig/internal/mapping"
	"github.com/example/gsbmig/internal/migrate"
	"github.com/example/gsbmig/internal/rdf"
	"github.com/example/gsbmig/internal/shacl"
	"github.com/example/gsbmig/internal/store"
)

type Service struct {
	mu sync.RWMutex
	db *store.Store

	Bundle *fixtures.Bundle

	rules  *mapping.Ruleset
	shapes []*shacl.NodeShape
	ontV2  *shacl.Ontology

	affected []mapping.Affected
	pubHook  store.BeforeCommit
}

// snapshot is the persisted mapping state.
type snapshot struct {
	Version    int                  `json:"version"`
	Candidates []*mapping.Candidate `json:"candidates"`
	Decisions  []*mapping.Decision  `json:"decisions"`
}

func New(ctx context.Context, db *store.Store) (*Service, error) {
	b, err := fixtures.Load()
	if err != nil {
		return nil, err
	}
	shapes, err := shacl.ParseShapes(b.ShapesV2)
	if err != nil {
		return nil, err
	}
	s := &Service{
		db:     db,
		Bundle: b,
		rules:  mapping.NewRuleset(),
		shapes: shapes,
		ontV2:  shacl.ParseOntology(b.OntologyV2),
	}
	if err := s.bootstrap(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// SetPublishHook installs a pre-commit hook (used by tests to prove failure
// atomicity).
func (s *Service) SetPublishHook(h store.BeforeCommit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pubHook = h
}

func (s *Service) bootstrap(ctx context.Context) error {
	// Import each fixture source with content-fingerprint dedup.
	sources := []struct {
		kind  string
		graph string
		quads []rdf.Quad
	}{
		{"ontology", fixtures.GraphOntologyV1, s.Bundle.OntologyV1},
		{"ontology", fixtures.GraphOntologyV2, s.Bundle.OntologyV2},
		{"candidates", fixtures.GraphCandidates, s.Bundle.Candidates},
		{"instances", fixtures.GraphInstancesV1, s.Bundle.InstancesV1},
		{"instances", fixtures.GraphExistingV2, s.Bundle.ExistingV2},
		{"shapes", fixtures.GraphShapesV2, s.Bundle.ShapesV2},
	}
	for _, src := range sources {
		fp := rdf.CanonicalizeDataset(rdf.NewDataset(src.quads...)).Fingerprint
		seen, err := dbHasImport(ctx, s.db, src.kind, src.graph, fp)
		if err != nil {
			return err
		}
		if !seen {
			if err := s.db.RecordImport(ctx, src.kind, src.graph, fp); err != nil {
				return err
			}
		}
	}

	// Load mapping state, or seed explicit candidates.
	version, payload, err := s.db.LoadMappingState(ctx)
	if err != nil {
		return err
	}
	if payload == "" {
		cands, err := mapping.ParseCandidates(s.Bundle.Candidates)
		if err != nil {
			return err
		}
		for _, c := range cands {
			c.ID = mapping.ContentID(c)
		}
		s.rules.Seed(cands)
		return s.persistRules(ctx)
	}
	var snap snapshot
	if err := json.Unmarshal([]byte(payload), &snap); err != nil {
		return err
	}
	s.rules.Version = version
	for _, c := range snap.Candidates {
		s.rules.Candidates[c.ID] = c
	}
	for _, d := range snap.Decisions {
		s.rules.Decisions[d.CandidateID] = d
	}
	return nil
}

func dbHasImport(ctx context.Context, db *store.Store, kind, graph, fp string) (bool, error) {
	return db.HasImport(ctx, kind, graph, fp)
}

func (s *Service) persistRules(ctx context.Context) error {
	snap := snapshot{Version: s.rules.Version}
	for _, c := range s.rules.Candidates {
		snap.Candidates = append(snap.Candidates, c)
	}
	for _, d := range s.rules.Decisions {
		snap.Decisions = append(snap.Decisions, d)
	}
	sort.Slice(snap.Candidates, func(i, j int) bool { return snap.Candidates[i].ID < snap.Candidates[j].ID })
	sort.Slice(snap.Decisions, func(i, j int) bool { return snap.Decisions[i].CandidateID < snap.Decisions[j].CandidateID })
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return s.db.SaveMappingState(ctx, s.rules.Version, string(raw))
}

// ---- read models ----

func (s *Service) Ruleset() *mapping.Ruleset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rules
}

// CandidateView pairs a candidate with its current decision.
type CandidateView struct {
	Candidate *mapping.Candidate `json:"candidate"`
	Decision  *mapping.Decision  `json:"decision,omitempty"`
}

func (s *Service) Candidates() []CandidateView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ids []string
	for id := range s.rules.Candidates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]CandidateView, 0, len(ids))
	for _, id := range ids {
		out = append(out, CandidateView{
			Candidate: s.rules.Candidates[id],
			Decision:  s.rules.Decisions[id],
		})
	}
	return out
}

func (s *Service) Affected() []mapping.Affected {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]mapping.Affected(nil), s.affected...)
}

// ---- decisions ----

var ErrStale = errors.New("stale mapping version")

// Decide applies a reviewer decision with optimistic concurrency on version.
func (s *Service) Decide(ctx context.Context, candidateID, status, rationale string, baseVersion int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rules.CheckStale(baseVersion); err != nil {
		return s.rules.Version, err
	}
	if _, ok := s.rules.Candidates[candidateID]; !ok {
		return s.rules.Version, mapping.ErrUnknownCandidate
	}
	if err := s.rules.Decide(candidateID, status, rationale, baseVersion); err != nil {
		return s.rules.Version, err
	}
	if err := s.persistRules(ctx); err != nil {
		return s.rules.Version, err
	}
	return s.rules.Version, nil
}

// ConflictSubgraph returns the candidates/decisions touching the same source
// terms as the stale action, so the UI can show what changed concurrently.
func (s *Service) ConflictSubgraph(candidateID string) []CandidateView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target := s.rules.Candidates[candidateID]
	if target == nil {
		return nil
	}
	var out []CandidateView
	for id, c := range s.rules.Candidates {
		if c.Source == target.Source || c.Target == target.Target {
			out = append(out, CandidateView{Candidate: c, Decision: s.rules.Decisions[id]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Candidate.ID < out[j].Candidate.ID })
	return out
}

// ---- suggestions ----

// ReSuggest runs a new automatic suggestion round. Accepted decisions are never
// overwritten; newly affected accepted decisions are listed for re-review.
func (s *Service) ReSuggest(ctx context.Context) ([]mapping.Affected, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	auto := mapping.Suggest(s.Bundle.OntologyV1, s.Bundle.OntologyV2)
	affected := s.rules.ReSuggest(auto)
	s.affected = affected
	if err := s.persistRules(ctx); err != nil {
		return nil, err
	}
	return affected, nil
}

// ---- preview ----

// Preview computes migration + SHACL validation and persists it keyed by the
// content+rule fingerprint (dedup).
func (s *Service) Preview(ctx context.Context) (*Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildPreview(ctx)
}

func (s *Service) buildPreview(ctx context.Context) (*Preview, error) {
	eng := migrate.NewEngine(s.rules, s.Bundle.OntologyV1)
	mig := eng.Run(s.Bundle.InstancesV1, fixtures.GraphMigrated)

	// validation union: migrated + pre-existing v2
	union := append([]rdf.Quad{}, mig.Migrated...)
	union = append(union, s.Bundle.ExistingV2...)

	info := buildOriginInfo(mig)
	validator := shacl.NewValidator(s.shapes, s.ontV2)
	report := validator.Validate(union, info)

	pv := &Preview{
		Result:             mig,
		Report:             report,
		Migrated:           mig.Migrated,
		ExistingV2:         s.Bundle.ExistingV2,
		RuleVersion:        s.rules.Version,
		RuleFingerprint:    s.rules.Fingerprint(),
		ContentFingerprint: mig.ContentFP,
		PreviewFingerprint: mig.PreviewFP,
	}

	raw, err := json.Marshal(pv)
	if err != nil {
		return nil, err
	}
	if err := s.db.SavePreview(ctx, store.PreviewRow{
		PreviewFP:   pv.PreviewFingerprint,
		ContentFP:   pv.ContentFingerprint,
		RuleFP:      pv.RuleFingerprint,
		RuleVersion: pv.RuleVersion,
		Payload:     string(raw),
	}); err != nil {
		return nil, err
	}
	return pv, nil
}

func buildOriginInfo(mig *migrate.Result) *shacl.OriginInfo {
	info := &shacl.OriginInfo{
		MappingBorne: map[string]bool{},
		DerivedBy:    map[string][]string{},
		Unresolved:   map[string]string{},
		SourceQuad:   map[string]string{},
	}
	for _, d := range mig.Derived {
		if d.Derived != nil {
			info.MappingBorne[d.Derived.String()] = true
			info.SourceQuad[d.Derived.String()] = d.Source.String()
		}
		if d.Origin == migrate.OriginUnresolved {
			info.Unresolved[d.Source.Subject.Value] = d.Detail
		}
	}
	return info
}

// Preview is the externally returned preview payload.
type Preview struct {
	Result             *migrate.Result `json:"result"`
	Report             *shacl.Report   `json:"report"`
	Migrated           []rdf.Quad      `json:"migrated"`
	ExistingV2         []rdf.Quad      `json:"existingV2"`
	RuleVersion        int             `json:"ruleVersion"`
	RuleFingerprint    string          `json:"ruleFingerprint"`
	ContentFingerprint string          `json:"contentFingerprint"`
	PreviewFingerprint string          `json:"previewFingerprint"`
}

// ---- publish ----

func (s *Service) Publish(ctx context.Context, previewFP string) (*PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pv, err := s.buildPreview(ctx)
	if err != nil {
		return nil, err
	}
	if previewFP != "" && previewFP != pv.PreviewFingerprint {
		return nil, errors.New("preview fingerprint is stale; regenerate before publishing")
	}

	raw, err := json.Marshal(pv.Migrated)
	if err != nil {
		return nil, err
	}
	before, _ := s.db.CountPublications(ctx)
	id, err := s.db.Publish(ctx, store.Publication{
		PreviewFP:   pv.PreviewFingerprint,
		ContentFP:   pv.ContentFingerprint,
		RuleFP:      pv.RuleFingerprint,
		RuleVersion: pv.RuleVersion,
		GraphIRI:    fixtures.GraphMigrated,
		QuadsJSON:   string(raw),
	}, s.pubHook)
	if err != nil {
		after, _ := s.db.CountPublications(ctx)
		return &PublishResult{Failed: true, Before: before, After: after, Error: err.Error()}, err
	}
	after, _ := s.db.CountPublications(ctx)
	return &PublishResult{ID: id, Before: before, After: after, PreviewFingerprint: pv.PreviewFingerprint}, nil
}

type PublishResult struct {
	ID                 int64  `json:"id,omitempty"`
	Before             int    `json:"before"`
	After              int    `json:"after"`
	PreviewFingerprint string `json:"previewFingerprint,omitempty"`
	Failed             bool   `json:"failed,omitempty"`
	Error              string `json:"error,omitempty"`
}

func (s *Service) PublishedState(ctx context.Context) (int, string, error) {
	p, err := s.db.LatestPublication(ctx)
	if err != nil {
		return 0, "", err
	}
	n, err := s.db.CountPublications(ctx)
	if err != nil {
		return 0, "", err
	}
	if p == nil {
		return n, "", nil
	}
	return n, p.PreviewFP, nil
}

// Ontology subgraph helpers for the review page.
func (s *Service) OntologyV1() []rdf.Quad  { return s.Bundle.OntologyV1 }
func (s *Service) OntologyV2() []rdf.Quad  { return s.Bundle.OntologyV2 }
func (s *Service) InstancesV1() []rdf.Quad { return s.Bundle.InstancesV1 }
