package api

import (
	_ "embed"
	"net/http"
)

// terminalPage is the Phase-1 minimal web terminal (single HTML+JS
// page, xterm.js via CDN). The full Next.js app comes in Phase 2.
//
//go:embed terminal.html
var terminalPage []byte

// terminalWorkspace serves the web terminal page for a workspace. The
// page reads its workspace id from the URL path and dials the exec
// WebSocket endpoint itself, so the bytes are identical for every id.
func (s *Server) terminalWorkspace(w http.ResponseWriter, r *http.Request) {
	if _, err := s.ctrl.Get(r.Context(), r.PathValue("id")); err != nil {
		writeControllerError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(terminalPage)
}
