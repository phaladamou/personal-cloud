package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/phaladamou/personal-cloud/services/auth"
)

// defaultLoginUser is the user_id of a login that names no user: a
// personal cloud has a single owner, and devices authenticate as them.
const defaultLoginUser = "local-user"

// WithAuth attaches the token service guarding /v1/*. Without it every
// /v1/* call answers 401 and the login endpoint answers 501.
func WithAuth(a *auth.Service) Option {
	return func(s *Server) { s.auth = a }
}

// bearerToken extracts the bearer token of the Authorization header,
// or "" when the header is missing or malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	token, ok := strings.CutPrefix(h, "Bearer ")
	if !ok || strings.TrimSpace(token) == "" {
		return ""
	}
	return strings.TrimSpace(token)
}

// authRoutes registers the login and revoke endpoints. Login stays open
// (it mints the token); revoke is guarded like every other /v1/* route.
func (s *Server) authRoutes() {
	s.mux.HandleFunc("POST /v1/auth/login", s.login)
	s.mux.HandleFunc("POST /v1/auth/revoke", s.revoke)
}

// loginRequest is the POST /v1/auth/login body: device names the calling
// device, user_id defaults to the single local owner.
type loginRequest struct {
	Device string `json:"device"`
	UserID string `json:"user_id"`
}

// loginResponse is the POST /v1/auth/login answer.
type loginResponse struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	Device    string    `json:"device"`
	TokenID   string    `json:"token_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		writeError(w, http.StatusNotImplemented, "auth not configured")
		return
	}
	var req loginRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Device) == "" {
		writeError(w, http.StatusBadRequest, "device is required")
		return
	}
	if strings.TrimSpace(req.UserID) == "" {
		req.UserID = defaultLoginUser
	}
	token, id, err := s.auth.Issue(r.Context(), req.UserID, req.Device)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, loginResponse{
		Token:     token,
		UserID:    id.UserID,
		Device:    id.Device,
		TokenID:   id.TokenID,
		ExpiresAt: id.ExpiresAt,
	})
}

// revoke kills the token carried by the request. The middleware already
// validated it, so only a store failure can still fail here.
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.Revoke(r.Context(), bearerToken(r)); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
