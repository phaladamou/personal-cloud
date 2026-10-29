package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/filesystem"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// maxFileBytes caps file downloads and uploads (8 MiB).
const maxFileBytes = 8 << 20

// fileRoutes registers the workspace file endpoints.
func (s *Server) fileRoutes() {
	s.mux.HandleFunc("GET /v1/workspaces/{id}/files", s.listFiles)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/files/content", s.readFile)
	s.mux.HandleFunc("PUT /v1/workspaces/{id}/files/content", s.writeFile)
	s.mux.HandleFunc("POST /v1/workspaces/{id}/files/mkdir", s.mkdir)
	s.mux.HandleFunc("DELETE /v1/workspaces/{id}/files", s.deleteFile)
}

// writeFileError maps file errors to status codes, reporting whether
// err was handled.
func writeFileError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, filesystem.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return true
	case errors.Is(err, filesystem.ErrPathEscape),
		errors.Is(err, controller.ErrNotDirectory),
		errors.Is(err, controller.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
		return true
	case errors.Is(err, controller.ErrFileTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		return true
	case errors.Is(err, controller.ErrFilesystemsUnavailable):
		writeError(w, http.StatusNotImplemented, err.Error())
		return true
	default:
		return false
	}
}

func (s *Server) listFiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.ctrl.ListFiles(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		if !writeFileError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	if list == nil {
		list = []types.FileEntry{}
	}
	writeJSON(w, http.StatusOK, map[string][]types.FileEntry{"files": list})
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	data, err := s.ctrl.ReadFile(r.Context(), r.PathValue("id"), path, maxFileBytes)
	if err != nil {
		if !writeFileError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFileBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
		} else {
			writeError(w, http.StatusBadRequest, "cannot read request body")
		}
		return
	}
	if err := s.ctrl.WriteFile(r.Context(), r.PathValue("id"), path, raw); err != nil {
		if !writeFileError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

// mkdirRequest is the POST .../files/mkdir body.
type mkdirRequest struct {
	Path string `json:"path"`
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := s.ctrl.Mkdir(r.Context(), r.PathValue("id"), req.Path); err != nil {
		if !writeFileError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": req.Path})
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path query parameter is required")
		return
	}
	if err := s.ctrl.DeleteFile(r.Context(), r.PathValue("id"), path); err != nil {
		if !writeFileError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
