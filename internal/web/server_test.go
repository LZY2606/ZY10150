package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/example/gsbmig/internal/app"
	"github.com/example/gsbmig/internal/mapping"
	"github.com/example/gsbmig/internal/store"
)

func newTestServer(t *testing.T) (*Server, *app.Service) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc, err := app.New(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return New(svc), svc
}

func do(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestStateAndPreview(t *testing.T) {
	s, _ := newTestServer(t)
	code, st := do(t, s.Handler(), "GET", "/api/state", nil)
	if code != 200 {
		t.Fatalf("state %d", code)
	}
	if st["version"].(float64) < 1 {
		t.Fatal("seeded version expected")
	}
	code, pv := do(t, s.Handler(), "GET", "/api/preview", nil)
	if code != 200 {
		t.Fatalf("preview %d", code)
	}
	if pv["previewFingerprint"] == nil || pv["previewFingerprint"] == "" {
		t.Fatal("missing preview fingerprint")
	}
}

func TestDecideStaleConflictHTTP(t *testing.T) {
	s, svc := newTestServer(t)
	h := s.Handler()

	// discover pending broader candidate
	_, st := do(t, h, "GET", "/api/state", nil)
	var candID string
	for _, raw := range st["candidates"].([]any) {
		cv := raw.(map[string]any)
		c := cv["candidate"].(map[string]any)
		if cv["decision"] == nil && c["relation"] == mapping.RelBroader {
			candID = c["id"].(string)
		}
	}
	if candID == "" {
		t.Fatal("no pending broader candidate")
	}
	// first accepted at v1 bumps to v2
	code, _ := do(t, h, "POST", "/api/decide", map[string]any{
		"candidateId": candID, "status": "accepted", "rationale": "a", "version": 1,
	})
	if code != 200 {
		t.Fatalf("first decide %d", code)
	}
	// stale decision at v1 -> 409 with conflict subgraph
	code, body := do(t, h, "POST", "/api/decide", map[string]any{
		"candidateId": candID, "status": "deprecated", "rationale": "b", "version": 1,
	})
	if code != 409 {
		t.Fatalf("expected 409, got %d", code)
	}
	if body["conflict"] == nil {
		t.Fatal("409 must carry conflict subgraph")
	}
	_ = svc
}

func TestPublishDedupHTTP(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	_, pv := do(t, h, "GET", "/api/preview", nil)
	fp := pv["previewFingerprint"].(string)
	c1, r1 := do(t, h, "POST", "/api/publish", map[string]any{"previewFingerprint": fp})
	c2, r2 := do(t, h, "POST", "/api/publish", map[string]any{"previewFingerprint": fp})
	if c1 != 200 || c2 != 200 {
		t.Fatalf("publish codes %d %d", c1, c2)
	}
	if r1["id"].(float64) != r2["id"].(float64) || r2["after"].(float64) != 1 {
		t.Fatalf("publish not deduplicated: %v %v", r1, r2)
	}
}

func TestIndexServed(t *testing.T) {
	s, _ := newTestServer(t)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("RDF 词汇迁移审阅台")) {
		t.Fatalf("index not served: %d", rec.Code)
	}
}
