package httpapi

import (
	"net/http"
	"strings"

	"github.com/priyavratuniyal/tuskbase/internal/app"
)

type ControlServer struct {
	service *app.Service
	mux     *http.ServeMux
}

func NewControlServer(service *app.Service) *ControlServer {
	s := &ControlServer{service: service, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *ControlServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *ControlServer) routes() {
	s.mux.HandleFunc("POST /control/v1/import/scan", s.importScan)
	s.mux.HandleFunc("GET /control/v1/import/candidates", s.importList)
	s.mux.HandleFunc("GET /control/v1/import/candidates/{id}", s.importShow)
	s.mux.HandleFunc("POST /control/v1/import/candidates/{id}/accept", s.importAccept)
	s.mux.HandleFunc("POST /control/v1/import/candidates/{id}/reject", s.importReject)
}

func (s *ControlServer) importScan(w http.ResponseWriter, r *http.Request) {
	var in app.ImportScanInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.ImportScan(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *ControlServer) importList(w http.ResponseWriter, r *http.Request) {
	limit, err := queryLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	out, err := s.service.ImportList(r.Context(), app.ImportListInput{
		WorkspaceID: strings.TrimSpace(r.URL.Query().Get("workspace_id")),
		Status:      strings.TrimSpace(r.URL.Query().Get("status")),
		Limit:       limit,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *ControlServer) importShow(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.ImportShow(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *ControlServer) importAccept(w http.ResponseWriter, r *http.Request) {
	out, err := s.service.ImportAccept(r.Context(), r.PathValue("id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *ControlServer) importReject(w http.ResponseWriter, r *http.Request) {
	var in app.ImportRejectInput
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	in.CandidateID = r.PathValue("id")
	out, err := s.service.ImportReject(r.Context(), in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
