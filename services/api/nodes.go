package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/phaladamou/personal-cloud/compute/node"
	"github.com/phaladamou/personal-cloud/packages/types"
)

// nodeRoutes registers the compute node endpoints.
func (s *Server) nodeRoutes() {
	s.mux.HandleFunc("POST /v1/nodes", s.registerNode)
	s.mux.HandleFunc("GET /v1/nodes", s.listNodes)
	s.mux.HandleFunc("GET /v1/nodes/{nid}", s.getNode)
	s.mux.HandleFunc("DELETE /v1/nodes/{nid}", s.unregisterNode)
	s.mux.HandleFunc("POST /v1/nodes/{nid}/heartbeat", s.heartbeatNode)
	s.mux.HandleFunc("POST /v1/nodes/{nid}/allocate", s.allocateNode)
	s.mux.HandleFunc("POST /v1/nodes/{nid}/release", s.releaseNode)
}

// requireNodes reports whether the node registry is configured,
// answering 501 when it is not.
func (s *Server) requireNodes(w http.ResponseWriter) bool {
	if s.nodes == nil {
		writeError(w, http.StatusNotImplemented, "node registry not configured")
		return false
	}
	return true
}

// writeNodeError maps registry errors to status codes.
func writeNodeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, node.ErrNodeNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, node.ErrInsufficientCapacity), errors.Is(err, node.ErrNodeNotReady):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// registerNodeRequest is the POST /v1/nodes body.
type registerNodeRequest struct {
	Name     string            `json:"name"`
	CPU      int               `json:"cpu"`
	MemoryMB int64             `json:"memory_mb"`
	DiskGB   int64             `json:"disk_gb"`
	GPUs     int               `json:"gpus"`
	Labels   map[string]string `json:"labels"`
}

func (s *Server) registerNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	var req registerNodeRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	n, err := s.nodes.Register(r.Context(), node.Spec{
		Name:     req.Name,
		CPU:      req.CPU,
		MemoryMB: req.MemoryMB,
		DiskGB:   req.DiskGB,
		GPUs:     req.GPUs,
		Labels:   req.Labels,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, n)
}

func (s *Server) listNodes(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	list, err := s.nodes.List(r.Context())
	if err != nil {
		writeNodeError(w, err)
		return
	}
	if list == nil {
		list = []types.Node{}
	}
	writeJSON(w, http.StatusOK, map[string][]types.Node{"nodes": list})
}

func (s *Server) getNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	n, err := s.nodes.Get(r.Context(), r.PathValue("nid"))
	if err != nil {
		writeNodeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) unregisterNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	if err := s.nodes.Unregister(r.Context(), r.PathValue("nid")); err != nil {
		writeNodeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) heartbeatNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	n, err := s.nodes.Heartbeat(r.Context(), r.PathValue("nid"))
	if err != nil {
		writeNodeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// capacityRequest is the POST .../allocate and .../release body.
type capacityRequest struct {
	CPU      int   `json:"cpu"`
	MemoryMB int64 `json:"memory_mb"`
	DiskGB   int64 `json:"disk_gb"`
	GPUs     int   `json:"gpus"`
}

func (s *Server) allocateNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	var req capacityRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	want := types.Resource{CPU: req.CPU, MemoryMB: req.MemoryMB, DiskGB: req.DiskGB, GPUs: req.GPUs}
	if want.Negative() {
		writeError(w, http.StatusBadRequest, "resource request must not be negative")
		return
	}
	n, err := s.nodes.Allocate(r.Context(), r.PathValue("nid"), want)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) releaseNode(w http.ResponseWriter, r *http.Request) {
	if !s.requireNodes(w) {
		return
	}
	var req capacityRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	want := types.Resource{CPU: req.CPU, MemoryMB: req.MemoryMB, DiskGB: req.DiskGB, GPUs: req.GPUs}
	if want.Negative() {
		writeError(w, http.StatusBadRequest, "resource release must not be negative")
		return
	}
	n, err := s.nodes.Release(r.Context(), r.PathValue("nid"), want)
	if err != nil {
		writeNodeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}
