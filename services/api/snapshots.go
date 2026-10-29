package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/phaladamou/personal-cloud/packages/types"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
	"github.com/phaladamou/personal-cloud/storage/snapshots"
	"github.com/phaladamou/personal-cloud/storage/volumes"
)

// snapshotRoutes registers the workspace snapshot endpoints.
func (s *Server) snapshotRoutes() {
	s.mux.HandleFunc("POST /v1/workspaces/{id}/snapshots", s.createSnapshot)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/snapshots", s.listSnapshots)
	s.mux.HandleFunc("GET /v1/workspaces/{id}/snapshots/{sid}", s.getSnapshot)
	s.mux.HandleFunc("POST /v1/workspaces/{id}/snapshots/{sid}/restore", s.restoreSnapshot)
	s.mux.HandleFunc("DELETE /v1/workspaces/{id}/snapshots/{sid}", s.deleteSnapshot)
}

// writeStorageError maps storage errors to status codes, reporting
// whether err was handled.
func writeStorageError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, snapshots.ErrSnapshotNotFound),
		errors.Is(err, snapshots.ErrVolumeNotFound),
		errors.Is(err, volumes.ErrVolumeNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return true
	case errors.Is(err, controller.ErrSnapshotsUnavailable),
		errors.Is(err, controller.ErrVolumesUnavailable):
		writeError(w, http.StatusNotImplemented, err.Error())
		return true
	default:
		return false
	}
}

// createSnapshotRequest is the POST .../snapshots body. Both fields are
// optional: an empty volume_id snapshots the default volume.
type createSnapshotRequest struct {
	VolumeID string `json:"volume_id"`
	Name     string `json:"name"`
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	var req createSnapshotRequest
	if r.ContentLength != 0 {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	snap, err := s.ctrl.CreateSnapshot(r.Context(), r.PathValue("id"), req.VolumeID, req.Name)
	if err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, snap)
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	list, err := s.ctrl.ListSnapshots(r.Context(), r.PathValue("id"))
	if err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	if list == nil {
		list = []types.Snapshot{}
	}
	writeJSON(w, http.StatusOK, map[string][]types.Snapshot{"snapshots": list})
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.ctrl.GetSnapshot(r.Context(), r.PathValue("id"), r.PathValue("sid"))
	if err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) restoreSnapshot(w http.ResponseWriter, r *http.Request) {
	id, sid := r.PathValue("id"), r.PathValue("sid")
	if err := s.ctrl.RestoreSnapshot(r.Context(), id, sid); err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	snap, err := s.ctrl.GetSnapshot(r.Context(), id, sid)
	if err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.DeleteSnapshot(r.Context(), r.PathValue("id"), r.PathValue("sid")); err != nil {
		if !writeStorageError(w, err) {
			writeControllerError(w, err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
