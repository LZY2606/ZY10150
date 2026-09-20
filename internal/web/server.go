// Package web serves the single-page graph review UI and its JSON API.
package web

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/example/gsbmig/internal/app"
	"github.com/example/gsbmig/internal/mapping"
	"github.com/example/gsbmig/internal/rdf"
)

type Server struct {
	app *app.Service
	mux *http.ServeMux
}

func New(svc *app.Service) *Server {
	s := &Server{app: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) handleAppJS(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("app.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /app.js", s.handleAppJS)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("GET /api/ontology", s.handleOntology)
	s.mux.HandleFunc("GET /api/instances", s.handleInstances)
	s.mux.HandleFunc("POST /api/decide", s.handleDecide)
	s.mux.HandleFunc("POST /api/suggest", s.handleSuggest)
	s.mux.HandleFunc("GET /api/preview", s.handlePreview)
	s.mux.HandleFunc("POST /api/publish", s.handlePublish)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	rs := s.app.Ruleset()
	count, publishedFP, _ := s.app.PublishedState(r.Context())
	writeJSON(w, 200, map[string]any{
		"version":            rs.Version,
		"ruleFingerprint":    rs.Fingerprint(),
		"candidates":         s.app.Candidates(),
		"affected":           s.app.Affected(),
		"publicationCount":   count,
		"publishedPreviewFp": publishedFP,
	})
}

// graphPayload renders quads plus canonical fingerprint and bnode label map.
func graphPayload(name string, quads []rdf.Quad) map[string]any {
	can := rdf.CanonicalizeDataset(rdf.NewDataset(quads...))
	return map[string]any{
		"name":        name,
		"quads":       quads,
		"fingerprint": can.Fingerprint,
		"canonical":   can.Lines,
	}
}

func (s *Server) handleOntology(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"v1": graphPayload("ontology-v1", s.app.OntologyV1()),
		"v2": graphPayload("ontology-v2", s.app.OntologyV2()),
	})
}

func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, graphPayload("instances-v1", s.app.InstancesV1()))
}

type decideReq struct {
	CandidateID string `json:"candidateId"`
	Status      string `json:"status"` // accepted | deprecated | rejected | pending
	Rationale   string `json:"rationale"`
	Version     int    `json:"version"` // mapping version the reviewer saw
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var req decideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	version, err := s.app.Decide(r.Context(), req.CandidateID, req.Status, req.Rationale, req.Version)
	if err != nil {
		if errors.Is(err, mapping.ErrStaleVersion) {
			writeJSON(w, 409, map[string]any{
				"error":          err.Error(),
				"currentVersion": version,
				"conflict":       s.app.ConflictSubgraph(req.CandidateID),
			})
			return
		}
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": version})
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	affected, err := s.app.ReSuggest(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"affected": affected, "version": s.app.Ruleset().Version})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	pv, err := s.app.Preview(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, pv)
}

type publishReq struct {
	PreviewFingerprint string `json:"previewFingerprint"`
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	var req publishReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	res, err := s.app.Publish(r.Context(), req.PreviewFingerprint)
	if err != nil {
		writeJSON(w, 409, res)
		return
	}
	writeJSON(w, 200, res)
}
