package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gsb/internal/mapping"
	"gsb/internal/migrate"
	"gsb/internal/rdf"
	"gsb/internal/shacl"
	"gsb/internal/spec"
	"gsb/internal/store"
)

// ParseInput is a document upload/import payload.
type ParseInput struct {
	Kind     string `json:"kind"` // ontology_old|ontology_new|instances|shapes|candidates
	Name     string `json:"name"`
	Format   string `json:"format"`
	Body     string `json:"body"`
	GraphIRI string `json:"graph_iri"`
}

// ParseResult reports whether content was newly stored.
type ParseResult struct {
	Imported bool   `json:"imported"`
	Reason   string `json:"reason,omitempty"`
	Quads    int    `json:"quads"`
	FP       string `json:"fp"`
}

// ParseDocument parses and persists a fixture document.  RDF quads are
// parsed up front so malformed content is rejected before any write, and
// content fingerprint dedupe prevents duplicate imports.
func (s *Service) ParseDocument(ctx context.Context, in ParseInput) (*ParseResult, error) {
	quads, err := parseBody(in.Format, in.Body, in.GraphIRI)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", in.Kind, err)
	}
	// Explicit candidate fixture may also be TriG/Turtle and is parsed
	// here; only add its rules after document insert succeeds.
	fp := rdf.GraphFingerprint(quads)

	s.mu.Lock()
	defer s.mu.Unlock()

	doc := &store.Document{
		Kind: in.Kind, Name: in.Name, Format: in.Format,
		GraphIRI: in.GraphIRI, ContentFP: fp, Body: in.Body, Quads: quads,
	}
	inserted, err := s.st.ImportDocument(ctx, doc)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return &ParseResult{Imported: false, Reason: "identical content already imported (fingerprint dedupe)", Quads: len(quads), FP: fp}, nil
	}

	if in.Kind == "candidates" {
		rules, perr := mapping.ParseCandidates(quads)
		if perr != nil {
			return nil, perr
		}
		ms, err := s.st.LoadMapping(ctx)
		if err != nil {
			return nil, err
		}
		var n int
		for _, r := range rules {
			if ms.AddCandidate(r) {
				n++
			}
		}
		if err := s.st.SaveMapping(ctx, ms); err != nil {
			return nil, err
		}
		_ = n
	}
	if in.Kind == "ontology_old" || in.Kind == "ontology_new" {
		ms, err := s.st.LoadMapping(ctx)
		if err != nil {
			return nil, err
		}
		oldO, _ := specOntology(ctx, s.st, "ontology_old")
		newO, _ := specOntology(ctx, s.st, "ontology_new")
		if oldO != nil {
			ms.SetOntologyFPS(oldO.Fingerprint, fpOr(newO))
		}
		if newO != nil {
			ms.SetOntologyFPS(fpOr(oldO), newO.Fingerprint)
		}
		_ = oldO
		_ = newO
		if err := s.st.SaveMapping(ctx, ms); err != nil {
			return nil, err
		}
	}
	return &ParseResult{Imported: true, Quads: len(quads), FP: fp}, nil
}

func fpOr(o *spec.Ontology) string {
	if o == nil {
		return ""
	}
	return o.Fingerprint
}

func specOntology(ctx context.Context, st *store.Store, kind string) (*spec.Ontology, error) {
	qs, _, err := st.QuadsByKind(ctx, kind)
	if err != nil || len(qs) == 0 {
		return nil, err
	}
	name := "old"
	if kind == "ontology_new" {
		name = "new"
	}
	return spec.Parse(name, qs), nil
}

func parseBody(format, body, graphIRI string) ([]rdf.Quad, error) {
	switch strings.ToLower(format) {
	case "turtle", "trig", "ttl":
		return rdf.ParseTurtle(body)
	case "jsonld", "json-ld":
		return rdf.ParseJSONLD(body, graphIRI)
	default:
		return nil, fmt.Errorf("unsupported format %q", format)
	}
}

// ConflictError carries a stale-version conflict to the HTTP layer.
type ConflictError struct {
	Conflict *mapping.Conflict
}

func (e *ConflictError) Error() string { return "stale mapping version" }

// DecideRequest binds a decision to candidate evidence and a base version.
type DecideRequest struct {
	RuleFP  string                `json:"rule_fp"`
	Version int                   `json:"version"`
	Input   mapping.DecisionInput `json:"decision"`
}

// Decide persists an accepted/broader/narrower/one-to-many/deprecated
// decision with optimistic version checking.
func (s *Service) Decide(ctx context.Context, req DecideRequest) (*mapping.Rule, *mapping.Conflict, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms, err := s.st.LoadMapping(ctx)
	if err != nil {
		return nil, nil, 0, err
	}
	if req.Input.Evidence == nil {
		req.Input.Evidence = []mapping.Evidence{{Type: "review", Detail: "manual review decision"}}
	}
	r, conflict, err := ms.Decide(req.RuleFP, req.Input, req.Version)
	if err != nil {
		if errors.Is(err, mapping.ErrConflict) {
			return nil, conflict, ms.Version, &ConflictError{Conflict: conflict}
		}
		return nil, nil, ms.Version, err
	}
	if err := s.st.SaveMapping(ctx, ms); err != nil {
		return nil, nil, ms.Version, err
	}
	return r, nil, ms.Version, nil
}

// ReSuggest runs a fresh non-destructive suggestion round.
func (s *Service) ReSuggest(ctx context.Context) (*mapping.AffectedReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldO, err := specOntology(ctx, s.st, "ontology_old")
	if err != nil || oldO == nil {
		return nil, errors.New("old ontology not imported")
	}
	newO, err := specOntology(ctx, s.st, "ontology_new")
	if err != nil || newO == nil {
		return nil, errors.New("new ontology not imported")
	}
	ms, err := s.st.LoadMapping(ctx)
	if err != nil {
		return nil, err
	}
	report := ms.Suggest(oldO, newO)
	if err := s.st.SaveMapping(ctx, ms); err != nil {
		return nil, err
	}
	return &report, nil
}

// Preview builds the migration preview and validates it.
func (s *Service) Preview(ctx context.Context) (*PreviewView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.st.QuadsByKindAll(ctx, "instances")
	if err != nil || len(src) == 0 {
		return nil, errors.New("instance graph not imported")
	}
	shapeQ, _, err := s.st.QuadsByKind(ctx, "shapes")
	if err != nil {
		return nil, err
	}
	ms, err := s.st.LoadMapping(ctx)
	if err != nil {
		return nil, err
	}
	accepted := ms.AcceptedRules()
	rep := migrate.Preview(src, accepted, ms.Version, ms.VersionFP, migrate.Options{})

	shapes := shacl.Shapes(shapeQ)
	newQ, _, _ := s.st.QuadsByKind(ctx, "ontology_new")
	disjoint := loadDisjoint(append(append([]rdf.Quad{}, shapeQ...), newQ...))
	vi := buildValidateInput(rep, src, shapes, disjoint)
	viols := shacl.Validate(vi)

	if err := s.st.SavePreview(ctx, rep, viols); err != nil {
		return nil, err
	}
	cats := map[string]int{}
	for _, v := range viols {
		cats[v.Category]++
	}
	pending := 0
	for _, st := range rep.Steps {
		if st.Kind == migrate.StepPending {
			pending++
		}
	}
	return &PreviewView{
		Report: rep, Diagnostics: viols, Categories: cats,
		StepCount: len(rep.Steps), PendingCount: pending,
	}, nil
}

// Publish atomically commits the current preview.
func (s *Service) Publish(ctx context.Context) (int64, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, err := s.st.QuadsByKindAll(ctx, "instances")
	if err != nil || len(src) == 0 {
		return 0, "", false, errors.New("instance graph not imported")
	}
	ms, err := s.st.LoadMapping(ctx)
	if err != nil {
		return 0, "", false, err
	}
	rep := migrate.Preview(src, ms.AcceptedRules(), ms.Version, ms.VersionFP, migrate.Options{})
	id, inserted, err := s.st.Publish(ctx, rep)
	if err != nil {
		return 0, "", false, err
	}
	return id, rep.ContentFP, inserted, nil
}
