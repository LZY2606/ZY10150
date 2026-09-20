package app

import (
	"context"
	"path/filepath"
	"testing"

	"database/sql"
	"github.com/example/gsbmig/internal/shacl"

	"github.com/example/gsbmig/internal/store"
)

func testService(t *testing.T) *Service {
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
	return s
}

func TestSeedPreviewCategories(t *testing.T) {
	s := testService(t)
	pv, err := s.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]int{}
	for _, v := range pv.Report.Violations {
		codes[v.Code]++
	}
	t.Logf("codes=%v", codes)
	if codes[shacl.ViolationMissingData] == 0 {
		t.Error("expected missing_data violation (Dave has no firstName)")
	}
	if codes[shacl.ViolationDatatype] == 0 {
		t.Error("expected datatype_mismatch violation (Bob age as string)")
	}
	if codes[shacl.ViolationClosedExtra] == 0 {
		t.Error("expected closed_extra_predicate (Erin contactEmail)")
	}
	if codes[shacl.ViolationClosedValue] == 0 {
		t.Error("expected closed_set_extra_value (Mallory/Nina status)")
	}
}

func TestOneToManyUnresolvedAndBranch(t *testing.T) {
	s := testService(t)
	// accept the one-to-many postal address candidate
	var o2m string
	for _, cv := range s.Candidates() {
		if cv.Candidate.IsOneToMany() {
			o2m = cv.Candidate.ID
		}
	}
	if o2m == "" {
		t.Fatal("no one-to-many candidate")
	}
	if _, err := s.Decide(context.Background(), o2m, "accepted", "split addresses", 1); err != nil {
		t.Fatal(err)
	}
	pv, err := s.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	foundUnresolved := map[string]bool{}
	foundPostal := false
	for _, v := range pv.Report.Violations {
		if v.Code == shacl.ViolationUnresolved {
			foundUnresolved[v.FocusNode] = true
		}
	}
	for _, q := range pv.Migrated {
		if q.Predicate.Value == "http://www.w3.org/1999/02/22-rdf-syntax-ns#type" &&
			q.Object.Value == "http://example.org/vocab/v2#PostalAddress" {
			foundPostal = true
		}
	}
	if !foundPostal {
		t.Error("expected DE address branch -> PostalAddress")
	}
	if len(foundUnresolved) == 0 {
		t.Error("expected free-text address to stay unresolved")
	}
}

func TestDisjointMappingContradiction(t *testing.T) {
	s := testService(t)
	pv, err := s.Preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	saw := false
	for _, v := range pv.Report.Violations {
		if v.Code == shacl.ViolationDisjoint {
			saw = true
			if len(v.Evidence) < 2 {
				t.Error("disjoint evidence must show both type paths")
			}
		}
	}
	if !saw {
		t.Error("Mallory Person+Organization should produce disjoint contradiction")
	}
}

func TestPublishSuccessAndFailureAtomic(t *testing.T) {
	s := testService(t)
	ctx := context.Background()

	// successful publish
	res, err := s.Publish(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.After != 1 {
		t.Fatalf("after publish count=%d", res.After)
	}

	// failing pre-commit hook must roll back: count unchanged
	s.SetPublishHook(func(tx *sql.Tx) error { return errBoom })
	_, err = s.Publish(ctx, "")
	if err == nil {
		t.Fatal("expected publish failure")
	}
	after, _, _ := s.PublishedState(ctx)
	if after != 1 {
		t.Fatalf("partial publication left behind: count=%d", after)
	}
}

var errBoom = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }
