package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"

	"github.com/phaladamou/personal-cloud/services/auth"
)

// maxBodyBytes bounds JSON request bodies.
const maxBodyBytes = 1 << 20

// Gateway fronts an API server: it guards every route with Bearer [REDACTED]
// serves the workspace ports proxy and the connection registry, and
// delegates everything else (workspaces, exec streaming, terminal,
// snapshots, files, processes, nodes) to the wrapped backend.
//
// Only two routes stay open: POST /v1/auth/login (it mints the token)
// and GET /healthz (ops check). Every other route, including
// /terminal/* and the exec WebSocket, requires a valid Bearer [REDACTED]
type Gateway struct {
	reg     *Registry
	auth    *auth.Service
	backend http.Handler
	mux     *http.ServeMux
	proxy   *httputil.ReverseProxy
}

// Option configures a Gateway.
type Option func(*Gateway)

// WithAuth attaches the token service guarding every non-open route.
// Without it, every guarded route answers 401.
func WithAuth(a *auth.Service) Option {
	return func(g *Gateway) { g.auth = a }
}

// New builds a Gateway over reg, delegating non-gateway routes to
// backend. A nil registry means an empty one; a nil backend answers
// 404 to every delegated route.
func New(reg *Registry, backend http.Handler, opts ...Option) *Gateway {
	if reg == nil {
		reg = NewRegistry()
	}
	if backend == nil {
		backend = http.NotFoundHandler()
	}
	g := &Gateway{reg: reg, backend: backend, mux: http.NewServeMux()}
	for _, opt := range opts {
		opt(g)
	}
	g.proxy = &httputil.ReverseProxy{
		Rewrite:      g.rewriteUpstream,
		ErrorHandler: proxyError,
	}
	g.mux.HandleFunc("POST /v1/workspaces/{id}/ports", g.registerForward)
	g.mux.HandleFunc("GET /v1/workspaces/{id}/ports", g.listForwards)
	g.mux.HandleFunc("DELETE /v1/workspaces/{id}/ports/{port}", g.removeForward)
	g.mux.HandleFunc("GET /v1/workspaces/{id}/ports/{port}", g.proxyService)
	g.mux.HandleFunc("GET /v1/workspaces/{id}/ports/{port}/", g.proxyService)
	g.mux.HandleFunc("POST /v1/workspaces/{id}/connections", g.openConnection)
	g.mux.HandleFunc("GET /v1/workspaces/{id}/connections", g.listConnections)
	g.mux.HandleFunc("DELETE /v1/workspaces/{id}/connections/{conn}", g.closeConnection)
	return g
}

// Registry returns the connection registry backing the gateway.
func (g *Gateway) Registry() *Registry { return g.reg }

// openRoute reports the two routes that bypass authentication: login
// (it mints the token) and the ops health check.
func openRoute(r *http.Request) bool {
	if r.URL.Path == "/healthz" {
		return true
	}
	return r.Method == http.MethodPost && r.URL.Path == "/v1/auth/login"
}

// gatewayPath reports whether p belongs to the gateway namespace:
// /v1/workspaces/{id}/ports... or /v1/workspaces/{id}/connections...
func gatewayPath(p string) bool {
	rest, ok := strings.CutPrefix(p, "/v1/workspaces/")
	if !ok {
		return false
	}
	segs := strings.SplitN(rest, "/", 3)
	if len(segs) < 2 || segs[0] == "" {
		return false
	}
	return segs[1] == "ports" || segs[1] == "connections"
}

// ServeHTTP authenticates every non-open route, serves the gateway
// namespace itself, and delegates the rest (including the /terminal
// page and the exec WebSocket upgrade) to the backend on the same
// connection. Auth failures answer a uniform 401.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !openRoute(r) {
		if g.auth == nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id, err := g.auth.Validate(r.Context(), bearerToken(r))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		r = r.WithContext(auth.ContextWithIdentity(r.Context(), id))
	}
	if gatewayPath(r.URL.Path) {
		g.mux.ServeHTTP(w, r)
		return
	}
	g.backend.ServeHTTP(w, r)
}

// bearerToken extracts the Bearer [REDACTED] the Authorization header,
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

// registerForwardRequest is the POST .../ports body.
type registerForwardRequest struct {
	Port     int    `json:"port"`
	Target   string `json:"target"`
	Protocol string `json:"protocol"`
}

func (g *Gateway) registerForward(w http.ResponseWriter, r *http.Request) {
	var req registerForwardRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	fwd, err := g.reg.Register(r.Context(), Forward{
		WorkspaceID: r.PathValue("id"),
		Port:        req.Port,
		Target:      req.Target,
		Protocol:    req.Protocol,
	})
	if err != nil {
		if errors.Is(err, ErrForwardExists) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, fwd)
}

func (g *Gateway) listForwards(w http.ResponseWriter, r *http.Request) {
	list, err := g.reg.Forwards(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string][]Forward{"ports": list})
}

func (g *Gateway) removeForward(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid port")
		return
	}
	if err := g.reg.Remove(r.Context(), r.PathValue("id"), port); err != nil {
		if errors.Is(err, ErrForwardNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// upstreamKey carries the proxy target through the request context to
// the ReverseProxy rewrite function.
type upstreamKey struct{}

// upstream is one proxied request: the backend host:port and the path
// past the /v1/workspaces/{id}/ports/{port} prefix.
type upstream struct {
	host string
	path string
}

// proxyService reverse-proxies a workspace HTTP service: the per-port
// forward wins, else the workspace default target; a dead target
// answers 502.
func (g *Gateway) proxyService(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	port, err := strconv.Atoi(r.PathValue("port"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid port")
		return
	}
	target := ""
	if fwd, err := g.reg.Lookup(r.Context(), id, port); err == nil {
		target = fwd.Target
	} else if !errors.Is(err, ErrForwardNotFound) {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	} else if addr, err := g.reg.Target(r.Context(), id); err == nil {
		target = addr
	} else if !errors.Is(err, ErrForwardNotFound) {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target == "" {
		writeError(w, http.StatusNotFound, "no such port forward")
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/workspaces/"+id+"/ports/"+r.PathValue("port"))
	if !ok || (rest != "" && rest[0] != '/') {
		writeError(w, http.StatusBadRequest, "invalid proxy path")
		return
	}
	if rest == "" {
		rest = "/"
	}
	ctx := context.WithValue(r.Context(), upstreamKey{}, upstream{host: target, path: rest})
	g.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// rewriteUpstream retargets the request at the workspace service: the
// gateway prefix is already stripped into the upstream path, the Host
// follows the backend, the client Bearer [REDACTED] is dropped (it
// authenticates the gateway, not the workspace service), and the
// client address is forwarded. The query string passes through
// untouched.
func (g *Gateway) rewriteUpstream(out *httputil.ProxyRequest) {
	up, _ := out.In.Context().Value(upstreamKey{}).(upstream)
	out.Out.URL.Scheme = "http"
	out.Out.URL.Host = up.host
	out.Out.URL.Path = up.path
	out.Out.URL.RawPath = ""
	out.Out.Host = up.host
	out.Out.Header.Del("Authorization")
	out.SetXForwarded()
}

// proxyError answers a dead workspace service with a JSON 502,
// revealing nothing about the backend address.
func proxyError(w http.ResponseWriter, _ *http.Request, _ error) {
	writeError(w, http.StatusBadGateway, "bad gateway")
}

// openResponse is the POST .../connections answer.
type openResponse struct {
	WorkspaceID  string `json:"workspace_id"`
	ConnectionID string `json:"connection_id"`
	Count        int    `json:"count"`
}

func (g *Gateway) openConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	connID, err := g.reg.Open(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := g.reg.Count(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, openResponse{WorkspaceID: id, ConnectionID: connID, Count: n})
}

// connectionsResponse is the GET .../connections answer.
type connectionsResponse struct {
	WorkspaceID string   `json:"workspace_id"`
	Count       int      `json:"count"`
	Connections []string `json:"connections"`
}

func (g *Gateway) listConnections(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ids, err := g.reg.Connections(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if ids == nil {
		ids = []string{}
	}
	writeJSON(w, http.StatusOK, connectionsResponse{WorkspaceID: id, Count: len(ids), Connections: ids})
}

func (g *Gateway) closeConnection(w http.ResponseWriter, r *http.Request) {
	if err := g.reg.Close(r.Context(), r.PathValue("id"), r.PathValue("conn")); err != nil {
		if errors.Is(err, ErrConnectionNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
