package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gsb/internal/mapping"
	"gsb/internal/service"
	"gsb/internal/store"
)

func testService(t *testing.T) (*service.Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	db := filepath.Join(dir, "test.db")
	ctx := context.Background()
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return service.New(st), st
}

func importFixture(t *testing.T, svc *service.Service, kind, format, name, path string, graph ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	g := ""
	if len(graph) > 0 {
		g = graph[0]
	}
	res, err := svc.ParseDocument(context.Background(), service.ParseInput{
		Kind: kind, Name: name, Format: format, Body: string(body), GraphIRI: g,
	})
	if err != nil {
		t.Fatalf("import %s: %v", path, err)
	}
	if !res.Imported {
		t.Fatalf("fixture %s was not imported", path)
	}
}

func fixturesRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "fixtures")
}

func seedAll(t *testing.T, svc *service.Service) {
	root := fixturesRoot(t)
	importFixture(t, svc, "ontology_old", "turtle", "old", filepath.Join(root, "ontology_old.ttl"))
	importFixture(t, svc, "ontology_new", "turtle", "new", filepath.Join(root, "ontology_new.ttl"))
	importFixture(t, svc, "candidates", "turtle", "cand", filepath.Join(root, "mappings.ttl"))
	importFixture(t, svc, "instances", "jsonld", "inst", filepath.Join(root, "instances.jsonld"),
		"http://gsb.local/graph/instances")
	importFixture(t, svc, "instances", "turtle", "inst-more", filepath.Join(root, "instances_more.ttl"),
		"http://gsb.local/graph/instances-more")
	importFixture(t, svc, "shapes", "turtle", "shapes", filepath.Join(root, "shapes_new.ttl"))
}

func TestEndToEndPreviewAndPublish(t *testing.T) {
	svc, st := testService(t)
	ctx := context.Background()
	seedAll(t, svc)

	st0, err := svc.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st0.Candidates) < 8 {
		t.Fatalf("expected explicit candidates, got %d", len(st0.Candidates))
	}

	// Accept every non-deprecated candidate; deprecate the flagged ones.
	version := 0
	for _, cand := range st0.Candidates {
		rel := cand.Relation
		in := mapping.DecisionInput{
			TermKind: cand.TermKind, Relation: rel, Source: cand.Source,
			Targets:  append([]string{}, cand.Targets...),
			Branches: append([]mapping.Branch{}, cand.Branches...),
			Evidence: cand.Evidence, Rationale: "fixture review",
		}
		r, _, ver, err := svc.Decide(ctx, service.DecideRequest{
			RuleFP: cand.RuleFingerprint, Version: version, Input: in,
		})
		if err != nil {
			t.Fatalf("decide %s: %v", cand.Source, err)
		}
		version = ver
		_ = r
	}

	pv, err := svc.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pv.StepCount == 0 {
		t.Fatal("preview produced no steps")
	}
	if pv.PendingCount == 0 {
		t.Fatal("expected at least one pending one-to-many case (frank INTERN / erin no condition)")
	}
	if pv.Categories["datatype_mismatch"] == 0 {
		t.Fatalf("expected a datatype violation, got %+v", pv.Categories)
	}
	if pv.Categories["missing_data"] == 0 {
		t.Fatalf("expected a missing-data violation, got %+v", pv.Categories)
	}
	if pv.Categories["closed_extra_property"] == 0 {
		t.Fatalf("expected a closed-extra violation, got %+v", pv.Categories)
	}

	id, fp, inserted, err := svc.Publish(ctx)
	if err != nil || !inserted {
		t.Fatalf("publish: id=%d inserted=%v err=%v", id, inserted, err)
	}
	// Idempotent publish of identical content+rules fingerprint.
	_, fp2, inserted2, err := svc.Publish(ctx)
	if err != nil || inserted2 || fp != fp2 {
		t.Fatalf("republish should dedupe: inserted=%v fp equal=%v err=%v", inserted2, fp == fp2, err)
	}
	pubFP, qs, err := st.LatestPublished(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pubFP != fp || len(qs) == 0 {
		t.Fatalf("published graph missing: fp=%s q=%d", pubFP, len(qs))
	}
}

func TestImportDedupe(t *testing.T) {
	svc, _ := testService(t)
	root := fixturesRoot(t)
	seedAll(t, svc)
	body, err := os.ReadFile(filepath.Join(root, "ontology_old.ttl"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := svc.ParseDocument(context.Background(), service.ParseInput{
		Kind: "ontology_old", Name: "old2", Format: "turtle", Body: string(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("identical content must be deduped by fingerprint")
	}
}

func TestStaleDecisionReturnsConflict(t *testing.T) {
	svc, _ := testService(t)
	seedAll(t, svc)
	st0, _ := svc.Load(context.Background())
	var personFP string
	for _, c := range st0.Candidates {
		if c.Source == "http://example.com/hr/v1#Person" {
			personFP = c.RuleFingerprint
		}
	}
	in := mapping.DecisionInput{TermKind: "class", Relation: mapping.Exact,
		Source:  "http://example.com/hr/v1#Person",
		Targets: []string{"http://example.com/hr/v2#Human"}, Evidence: []mapping.Evidence{{Type: "review"}}}
	if _, _, _, err := svc.Decide(context.Background(),
		service.DecideRequest{RuleFP: personFP, Version: 0, Input: in}); err != nil {
		t.Fatal(err)
	}
	// stale version 0 again
	_, _, _, err := svc.Decide(context.Background(),
		service.DecideRequest{RuleFP: personFP, Version: 0, Input: in})
	if err == nil {
		t.Fatal("expected conflict error on stale version")
	}
	if ce, ok := err.(*service.ConflictError); !ok || ce.Conflict == nil {
		t.Fatalf("expected *ConflictError, got %T %v", err, err)
	}
}
