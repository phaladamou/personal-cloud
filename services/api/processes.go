package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
)

// processRoutes registers the workspace process endpoints.
func (s *Server) processRoutes() {
	s.mux.HandleFunc("GET /v1/workspaces/{id}/processes", s.listProcesses)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/processes/{pid}/logs", s.processLogs)
}

// writeProcessError maps process errors to status codes, reporting
// whether err was handled.
func writeProcessError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, container.ErrProcessNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return true
	}
	return false
}

func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request) {
	list, err := s.ctrl.ListProcesses(r.Context(), r.PathValue("id"))
	if err != nil {
		writeControllerError(w, err)
		return
	}
	if list == nil {
		list = []types.Process{}
	}
	writeJSON(w, http.StatusOK, map[string][]types.Process{"processes": list})
}

func (s *Server) processLogs(w http.ResponseWriter, r *http.Request) {
	follow := false
	if v := r.URL.Query().Get("follow"); v != "" {
		var err error
		follow, err = strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid follow value")
			return
		}
	}
	rc, err := s.ctrl.ProcessLogs(r.Context(), r.PathValue("id"), r.PathValue("pid"), follow)
	if err != nil {
		if !writeProcessError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// Chunked copy with flushing so follow mode pushes output
	// promptly; a disconnected client cancels the request context and
	// ends the backend stream.
	if flusher, ok := w.(http.Flusher); ok {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := rc.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				flusher.Flush()
			}
			if rerr != nil {
				return
			}
		}
	}
	_, _ = io.Copy(w, rc)
}
