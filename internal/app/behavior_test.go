package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/example/gsbmig/internal/mapping"
	"github.com/example/gsbmig/internal/store"
)

func openSvc(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func TestResuggestDoesNotOverwriteAccepted(t *testing.T) {
	s, _ := openSvc(t)
	// organization has an accepted exact decision; auto-suggest produces the
	// same candidate (identical local name) and must not change it.
	var org string
	for _, cv := range s.Candidates() {
		if cv.Candidate.Source == "http://example.org/vocab/v1#Organization" {
			org = cv.Candidate.ID
			if cv.Decision == nil || cv.Decision.Status != "accepted" {
				t.Fatal("organization should seed as accepted")
			}
		}
	}
	affected, err := s.ReSuggest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range affected {
		if a.Source == "http://example.org/vocab/v1#Organization" {
			t.Fatal("identical suggestion must not flag accepted decision")
		}
	}
	d := s.Ruleset().Decisions[org]
	if d == nil || d.Status != "accepted" {
		t.Fatal("accepted decision was overwritten")
	}
}

func TestStaleVersionConflict(t *testing.T) {
	s, _ := openSvc(t)
	// choose a pending candidate whose acceptance CHANGES the effective set
	// (the broader PostalAddress->Location mapping) so the version bumps.
	var pending string
	for _, cv := range s.Candidates() {
		if cv.Decision == nil && cv.Candidate.Relation == mapping.RelBroader {
			pending = cv.Candidate.ID
		}
	}
	if pending == "" {
		t.Fatal("no pending broader candidate")
	}
	v, err := s.Decide(context.Background(), pending, "accepted", "first reviewer", 1)
	if err != nil {
		t.Fatal(err)
	}
	if v != 2 {
		t.Fatalf("expected version bump to 2, got %d", v)
	}
	// a second reviewer still on version 1 must be told about the conflict
	_, err = s.Decide(context.Background(), pending, "deprecated", "late reviewer", 1)
	if err != mapping.ErrStaleVersion {
		t.Fatalf("expected stale conflict, got %v", err)
	}
	sub := s.ConflictSubgraph(pending)
	if len(sub) == 0 {
		t.Fatal("conflict subgraph empty")
	}
}

func TestPreviewAndPublishDeduplication(t *testing.T) {
	s, _ := openSvc(t)
	ctx := context.Background()
	p1, err := s.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.Preview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p1.PreviewFingerprint != p2.PreviewFingerprint {
		t.Fatal("same content+rules must produce identical preview fingerprint")
	}
	r1, err := s.Publish(ctx, p1.PreviewFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.Publish(ctx, p2.PreviewFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ID != r2.ID || r2.After != 1 {
		t.Fatalf("identical publish should dedupe to same record: %+v %+v", r1, r2)
	}
}

func TestBnodeFingerprintIndependentOfTempIDs(t *testing.T) {
	// canonicalization guarantees export fingerprints ignore parser bnode ids;
	// this is also covered in the rdf package, but assert end-to-end stability
	// across two fresh service builds (re-parse identical content).
	s1, _ := openSvc(t)
	s2, _ := openSvc(t)
	p1, _ := s1.Preview(context.Background())
	p2, _ := s2.Preview(context.Background())
	if p1.ContentFingerprint != p2.ContentFingerprint {
		t.Fatal("content fingerprint differs across re-imports")
	}
	if p1.PreviewFingerprint != p2.PreviewFingerprint {
		t.Fatal("preview fingerprint differs across re-imports")
	}
}
