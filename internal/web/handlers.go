package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"gsb/internal/service"
)

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var in service.ParseInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.svc.ParseDocument(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	rep, err := s.svc.ReSuggest(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	var req service.DecideRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, _, version, err := s.svc.Decide(r.Context(), req)
	if err != nil {
		if ce, ok := err.(*service.ConflictError); ok {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":           ce.Error(),
				"conflict":        ce.Conflict,
				"current_version": version,
			})
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": rule, "version": version})
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	pv, err := s.svc.Preview(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

func (s *Server) handlePublish(w http.ResponseWriter, r *http.Request) {
	id, fp, inserted, err := s.svc.Publish(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	code := http.StatusCreated
	if !inserted {
		code = http.StatusOK
	}
	writeJSON(w, code, map[string]any{
		"publish_id": id, "content_fp": fp, "inserted": inserted,
	})
}

func (s *Server) handlePublished(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.Load(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st.Published)
}

// handleFixture returns a bundled fixture body for the demo one-click load.
func (s *Server) handleFixture(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !safeFixture(name) {
		writeErr(w, http.StatusBadRequest, "unknown fixture")
		return
	}
	for _, base := range fixtureDirs() {
		p := filepath.Join(base, name)
		if b, err := os.ReadFile(p); err == nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(b)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "fixture not found")
}
