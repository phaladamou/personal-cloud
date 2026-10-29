// Package api exposes the Personal Cloud control-plane HTTP API.
//
// It is stdlib-only (net/http) and thin by design: every route decodes
// its input, delegates to the workspace controller, and maps errors to
// status codes (400 validation, 404 unknown workspace, 409 illegal
// transition, 500 internal). The exec endpoint upgrades to a WebSocket
// (see internal/ws) and streams the command stdio as binary messages;
// a final `error: ...` text message marks a failed command.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api/internal/ws"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// maxBodyBytes bounds JSON request bodies.
const maxBodyBytes = 1 << 20

// Server is the HTTP control-plane API.
type Server struct {
	ctrl *controller.Controller
	mux  *http.ServeMux
}

// NewServer builds a Server over ctrl and registers its routes.
func NewServer(ctrl *controller.Controller) *Server {
	s := &Server{ctrl: ctrl, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.HandleFunc("POST /v1/workspaces", s.createWorkspace)
	s.mux.HandleFunc("GET /v1/workspaces", s.listWorkspaces)
	s.mux.HandleFunc("GET /v1/workspaces/{id}", s.getWorkspace)
	s.mux.HandleFunc("DELETE /v1/workspaces/{id}", s.deleteWorkspace)
	s.mux.HandleFunc("POST /v1/workspaces/{id}/start", s.transition((*controller.Controller).Start))
	s.mux.HandleFunc("POST /v1/workspaces/{id}/stop", s.transition((*controller.Controller).Stop))
	s.mux.HandleFunc("POST /v1/workspaces/{id}/sleep", s.transition((*controller.Controller).Sleep))
	s.mux.HandleFunc("POST /v1/workspaces/{id}/wakeup", s.transition((*controller.Controller).Wakeup))
	s.mux.HandleFunc("POST /v1/workspaces/{id}/restore", s.transition((*controller.Controller).Restore))
	s.mux.HandleFunc("GET /v1/workspaces/{id}/exec", s.execWorkspace)
	s.mux.HandleFunc("GET /terminal/{id}", s.terminalWorkspace)
	s.snapshotRoutes()
	s.fileRoutes()
	s.processRoutes()
	return s
}

// ServeHTTP dispatches to the registered routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// writeJSON encodes v as a JSON response.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends a JSON error response.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// writeControllerError maps controller/store errors to status codes.
func writeControllerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, controller.ErrWorkspaceNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, controller.ErrIllegalTransition):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// createRequest is the POST /v1/workspaces body.
type createRequest struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "user_id and name are required")
		return
	}
	ws, err := s.ctrl.Create(r.Context(), req.UserID, req.Name)
	if err != nil {
		writeControllerError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	list, err := s.ctrl.List(r.Context())
	if err != nil {
		writeControllerError(w, err)
		return
	}
	if list == nil {
		list = []types.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string][]types.Workspace{"workspaces": list})
}

func (s *Server) getWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.ctrl.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeControllerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ws)
}

func (s *Server) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
	if err := s.ctrl.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeControllerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// transition builds a POST action handler over a controller method.
func (s *Server) transition(fn func(*controller.Controller, context.Context, string) (types.Workspace, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, err := fn(s.ctrl, r.Context(), r.PathValue("id"))
		if err != nil {
			writeControllerError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ws)
	}
}

// wsBinaryWriter adapts a *ws.Conn to io.Writer for command output.
type wsBinaryWriter struct {
	conn *ws.Conn
}

func (w wsBinaryWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := w.conn.WriteMessage(ws.OpBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// execWorkspace runs a command in a RUNNING workspace over WebSocket.
//
// The command comes from the repeated `argv` query parameter
// (/exec?argv=echo&argv=hello). Client text/binary messages feed the
// command stdin; stdout and stderr stream back as binary messages.
func (s *Server) execWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	argv := r.URL.Query()["argv"]
	if len(argv) == 0 {
		writeError(w, http.StatusBadRequest, "argv query parameter is required")
		return
	}
	workspace, err := s.ctrl.Get(r.Context(), id)
	if err != nil {
		writeControllerError(w, err)
		return
	}
	if workspace.Status != types.WorkspaceRunning {
		writeError(w, http.StatusConflict, "workspace is not running")
		return
	}
	conn, err := ws.Upgrade(w, r)
	if err != nil {
		return // Upgrade wrote the error.
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stdinR, stdinW := io.Pipe()
	defer stdinR.Close()

	execDone := make(chan error, 1)
	go func() {
		out := wsBinaryWriter{conn: conn}
		execDone <- s.ctrl.Exec(ctx, id, container.ExecOptions{
			Cmd:    argv,
			Stdin:  stdinR,
			Stdout: out,
			Stderr: out,
		})
	}()

	// Client messages feed command stdin. A clean close frame means
	// stdin EOF: the pipe is closed and the command drains and exits
	// on its own. Any other read failure (the client going away)
	// cancels the command.
	go func() {
		defer stdinW.Close()
		for {
			op, msg, err := conn.ReadMessage()
			if err != nil {
				if !errors.Is(err, ws.ErrClosed) {
					cancel()
				}
				return
			}
			if op != ws.OpText && op != ws.OpBinary {
				continue
			}
			if _, err := stdinW.Write(msg); err != nil {
				return
			}
		}
	}()

	if err := <-execDone; err != nil {
		_ = conn.WriteMessage(ws.OpText, []byte("error: "+err.Error()))
	}
}
