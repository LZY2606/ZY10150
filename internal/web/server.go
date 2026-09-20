package web

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"

	"gsb/internal/service"
)

//go:embed static/*
var staticFS embed.FS

// Server exposes the review API and single-page UI.
type Server struct {
	svc *service.Service
	mux *http.ServeMux
}

func New(svc *service.Service) *Server {
	s := &Server{svc: svc, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	fileSrv := http.FileServer(http.FS(sub))
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", fileSrv))
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("POST /api/import", s.handleImport)
	s.mux.HandleFunc("POST /api/suggest", s.handleSuggest)
	s.mux.HandleFunc("POST /api/decide", s.handleDecide)
	s.mux.HandleFunc("POST /api/preview", s.handlePreview)
	s.mux.HandleFunc("POST /api/publish", s.handlePublish)
	s.mux.HandleFunc("GET /api/published", s.handlePublished)
	s.mux.HandleFunc("GET /api/fixture/{name}", s.handleFixture)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
