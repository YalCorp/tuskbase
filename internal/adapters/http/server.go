package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/app"
)

type Server struct {
	service *app.Service
	mux     *http.ServeMux
}

func NewServer(service *app.Service) *Server {
	s := &Server{service: service, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /v1/workspaces/attach", s.attach)
	s.mux.HandleFunc("POST /v1/decisions", s.remember)
	s.mux.HandleFunc("POST /v1/lookup", s.lookup)
	s.mux.HandleFunc("POST /v1/preflight", s.preflight)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/decisions/recent", s.recent)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/conflicts", s.conflicts)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	var in app.AttachInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.Attach(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) remember(w http.ResponseWriter, r *http.Request) {
	var in app.RememberInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.Remember(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	var in app.LookupInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.Lookup(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	var in app.PreflightInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.Preflight(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) recent(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	decisions, err := s.service.Recent(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

func (s *Server) conflicts(w http.ResponseWriter, r *http.Request) {
	conflicts, err := s.service.Conflicts(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

func readJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func queryLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil {
		return 0, errors.New("limit must be an integer")
	}
	if limit < 0 {
		return 0, errors.New("limit must be non-negative")
	}
	return limit, nil
}
