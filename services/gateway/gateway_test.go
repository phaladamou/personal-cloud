package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/internal/wsclient"
	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api"
	"github.com/phaladamou/personal-cloud/services/auth"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// gatewayEnv wires a gateway over a stub backend, guarded by a fresh
// auth service with one issued token.
type gatewayEnv struct {
	srv   *httptest.Server
	reg   *Registry
	token string
}

func newGatewayEnv(t *testing.T, backend http.Handler) *gatewayEnv {
	t.Helper()
	svc, err := auth.NewService(auth.NewFakeStore(), []byte("gateway-test-secret-0123456789-01"))
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}
	token, _, err := svc.Issue(context.Background(), "test-user", "test-device")
	if err != nil {
		t.Fatalf("Issue test token: %v", err)
	}
	reg := NewRegistry()
	srv := httptest.NewServer(New(reg, backend, WithAuth(svc)))
	t.Cleanup(srv.Close)
	return &gatewayEnv{srv: srv, reg: reg, token: token}
}

// stubBackend marks delegated requests so tests can tell delegation
// from gateway handling.
func stubBackend() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"backend":true,"path":%q}`, r.URL.Path)
	})
}

// doJSON sends a JSON request with an optional Bearer [REDACTED] decoding
// the JSON response body (if any).
func doJSON(t *testing.T, token, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if resp.ContentLength != 0 {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && resp.StatusCode != http.StatusNoContent {
			t.Fatalf("decode %s %s: %v", method, url, err)
		}
	}
	return resp.StatusCode, out
}

// doRaw sends a request with an optional Bearer [REDACTED] returning
// the raw body.
func doRaw(t *testing.T, token, method, url string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw), resp.Header
}

func TestPortsEndpoints(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	base := env.srv.URL + "/v1/workspaces/ws-1/ports"

	code, body := doJSON(t, env.token, http.MethodPost, base,
		map[string]any{"port": 3000, "target": "127.0.0.1:4000"})
	if code != http.StatusCreated {
		t.Fatalf("register: got %d (%v), want 201", code, body)
	}
	if body["workspace_id"] != "ws-1" || body["port"] != float64(3000) ||
		body["target"] != "127.0.0.1:4000" || body["protocol"] != "tcp" {
		t.Fatalf("register body: got %v", body)
	}

	// Duplicate port conflicts.
	if code, _ := doJSON(t, env.token, http.MethodPost, base,
		map[string]any{"port": 3000, "target": "127.0.0.1:4000"}); code != http.StatusConflict {
		t.Fatalf("register duplicate: got %d, want 409", code)
	}

	code, body = doJSON(t, env.token, http.MethodPost, base,
		map[string]any{"port": 3001, "target": "127.0.0.1:4001", "protocol": "udp"})
	if code != http.StatusCreated || body["protocol"] != "udp" {
		t.Fatalf("register udp: got %d %v", code, body)
	}

	// List ordered by port.
	code, body = doJSON(t, env.token, http.MethodGet, base, nil)
	if code != http.StatusOK {
		t.Fatalf("list: got %d", code)
	}
	ports, _ := body["ports"].([]any)
	if len(ports) != 2 {
		t.Fatalf("list: got %v, want 2 forwards", body)
	}
	first, _ := ports[0].(map[string]any)
	if first["port"] != float64(3000) {
		t.Fatalf("list order: got %v, want 3000 first", body)
	}

	// Remove.
	req, _ := http.NewRequest(http.MethodDelete, base+"/3000", nil)
	req.Header.Set("Authorization", "Bearer "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove: got %d, want 204", resp.StatusCode)
	}
	if code, _ := doJSON(t, env.token, http.MethodDelete, base+"/3000", nil); code != http.StatusNotFound {
		t.Fatalf("remove twice: got %d, want 404", code)
	}
	code, body = doJSON(t, env.token, http.MethodGet, base, nil)
	if code != http.StatusOK {
		t.Fatalf("list after remove: got %d", code)
	}
	if ports, _ := body["ports"].([]any); len(ports) != 1 {
		t.Fatalf("list after remove: got %v, want 1 left", body)
	}

	// Unknown workspace lists empty, like the storage managers.
	code, body = doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/ports", nil)
	if code != http.StatusOK {
		t.Fatalf("list unknown workspace: got %d", code)
	}
	if ports, _ := body["ports"].([]any); len(ports) != 0 {
		t.Fatalf("list unknown workspace: got %v, want empty", body)
	}
}

func TestPortsValidation(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	base := env.srv.URL + "/v1/workspaces/ws-1/ports"
	bad := []any{
		"just a string",
		map[string]any{},
		map[string]any{"port": 0, "target": "127.0.0.1:4000"},
		map[string]any{"port": 70000, "target": "127.0.0.1:4000"},
		map[string]any{"port": 3000},
		map[string]any{"port": 3000, "target": "127.0.0.1"},
		map[string]any{"port": 3000, "target": "127.0.0.1:4000", "protocol": "sctp"},
	}
	for i, body := range bad {
		if code, _ := doJSON(t, env.token, http.MethodPost, base, body); code != http.StatusBadRequest {
			t.Fatalf("register bad[%d]: got %d, want 400", i, code)
		}
	}
	if code, _ := doJSON(t, env.token, http.MethodDelete, base+"/abc", nil); code != http.StatusBadRequest {
		t.Fatalf("remove non-numeric port: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, base+"/abc", nil); code != http.StatusBadRequest {
		t.Fatalf("proxy non-numeric port: got %d, want 400", code)
	}
}

func TestGatewayAuthRequired(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	paths := []struct{ method, path string }{
		{http.MethodPost, "/v1/workspaces/ws-1/ports"},
		{http.MethodGet, "/v1/workspaces/ws-1/ports"},
		{http.MethodDelete, "/v1/workspaces/ws-1/ports/3000"},
		{http.MethodGet, "/v1/workspaces/ws-1/ports/3000/a"},
		{http.MethodGet, "/v1/workspaces/ws-1/ports/3000"},
		{http.MethodPost, "/v1/workspaces/ws-1/connections"},
		{http.MethodGet, "/v1/workspaces/ws-1/connections"},
		{http.MethodDelete, "/v1/workspaces/ws-1/connections/conn-1"},
		// Delegated routes are guarded too, including the terminal.
		{http.MethodGet, "/v1/workspaces"},
		{http.MethodGet, "/terminal/ws-1"},
	}
	for _, p := range paths {
		if code, _ := doJSON(t, "", p.method, env.srv.URL+p.path, nil); code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: got %d, want 401", p.method, p.path, code)
		}
		if code, _ := doJSON(t, "not-a-token", p.method, env.srv.URL+p.path, nil); code != http.StatusUnauthorized {
			t.Fatalf("%s %s bogus token: got %d, want 401", p.method, p.path, code)
		}
	}

	// Login and health stay open and reach the backend.
	if code, body := doJSON(t, "", http.MethodPost, env.srv.URL+"/v1/auth/login",
		map[string]string{"device": "probe"}); code != http.StatusOK || body["backend"] != true {
		t.Fatalf("open login: got %d %v, want backend 200", code, body)
	}
	if code, body := doJSON(t, "", http.MethodGet, env.srv.URL+"/healthz", nil); code != http.StatusOK || body["backend"] != true {
		t.Fatalf("open healthz: got %d %v, want backend 200", code, body)
	}
}

func TestGatewayDelegates(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	for _, path := range []string{"/v1/workspaces", "/terminal/ws-1", "/v1/workspaces/ws-1/exec?argv=cat"} {
		code, body := doJSON(t, env.token, http.MethodGet, env.srv.URL+path, nil)
		if code != http.StatusOK || body["backend"] != true {
			t.Fatalf("delegated %s: got %d %v, want backend marker", path, code, body)
		}
	}
}

// upstreamStub serves a fixed body while recording what the gateway
// forwarded: path, query and whether the client token leaked.
func upstreamStub(t *testing.T) (*httptest.Server, *string, *string, *string) {
	t.Helper()
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("upstream:" + r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath, &gotQuery, &gotAuth
}

func upstreamAddr(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	addr := strings.TrimPrefix(srv.URL, "http://")
	if addr == srv.URL {
		t.Fatalf("upstream url %q is not http", srv.URL)
	}
	return addr
}

func TestProxySuccess(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	up, gotPath, gotQuery, gotAuth := upstreamStub(t)
	if _, err := env.reg.Register(context.Background(), Forward{
		WorkspaceID: "ws-1", Port: 3000, Target: upstreamAddr(t, up),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	code, body, _ := doRaw(t, env.token, http.MethodGet,
		env.srv.URL+"/v1/workspaces/ws-1/ports/3000/a/b?x=1&y=2")
	if code != http.StatusOK || body != "upstream:/a/b" {
		t.Fatalf("proxy: got %d %q, want 200 upstream:/a/b", code, body)
	}
	if *gotPath != "/a/b" || *gotQuery != "x=1&y=2" {
		t.Fatalf("upstream saw path %q query %q, want /a/b x=1&y=2", *gotPath, *gotQuery)
	}
	if *gotAuth != "" {
		t.Fatalf("upstream Authorization: got %q, want stripped", *gotAuth)
	}

	// The bare forward root proxies "/".
	code, body, _ = doRaw(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/ws-1/ports/3000")
	if code != http.StatusOK || body != "upstream:/" {
		t.Fatalf("proxy root: got %d %q, want 200 upstream:/", code, body)
	}
	if *gotPath != "/" {
		t.Fatalf("upstream root path: got %q, want /", *gotPath)
	}
}

func TestProxyDefaultTarget(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	up, gotPath, _, _ := upstreamStub(t)
	if err := env.reg.SetTarget(context.Background(), "ws-1", upstreamAddr(t, up)); err != nil {
		t.Fatalf("SetTarget: %v", err)
	}
	// No per-port forward: the workspace target serves any port.
	code, body, _ := doRaw(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/ws-1/ports/9999/x")
	if code != http.StatusOK || body != "upstream:/x" {
		t.Fatalf("proxy fallback: got %d %q, want 200 upstream:/x", code, body)
	}
	if *gotPath != "/x" {
		t.Fatalf("upstream path: got %q, want /x", *gotPath)
	}
}

// closedAddr returns a localhost address nothing listens on.
func closedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

func TestProxyDeadTarget(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	if _, err := env.reg.Register(context.Background(), Forward{
		WorkspaceID: "ws-1", Port: 3000, Target: closedAddr(t),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	code, body, _ := doRaw(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/ws-1/ports/3000/")
	if code != http.StatusBadGateway {
		t.Fatalf("proxy dead target: got %d (%q), want 502", code, body)
	}
	if !strings.Contains(body, "bad gateway") {
		t.Fatalf("proxy dead target body: got %q, want JSON bad gateway", body)
	}
}

func TestProxyUnknown(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	if code, _, _ := doRaw(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/ws-1/ports/3000/"); code != http.StatusNotFound {
		t.Fatalf("proxy unknown: got %d, want 404", code)
	}
}

func TestConnectionsEndpoints(t *testing.T) {
	env := newGatewayEnv(t, stubBackend())
	base := env.srv.URL + "/v1/workspaces/ws-1/connections"

	code, body := doJSON(t, env.token, http.MethodPost, base, nil)
	if code != http.StatusCreated {
		t.Fatalf("open: got %d (%v), want 201", code, body)
	}
	first, _ := body["connection_id"].(string)
	if first == "" || body["workspace_id"] != "ws-1" || body["count"] != float64(1) {
		t.Fatalf("open body: got %v", body)
	}
	code, body = doJSON(t, env.token, http.MethodPost, base, nil)
	if code != http.StatusCreated || body["count"] != float64(2) {
		t.Fatalf("open twice: got %d %v", code, body)
	}
	second, _ := body["connection_id"].(string)

	code, body = doJSON(t, env.token, http.MethodGet, base, nil)
	if code != http.StatusOK || body["count"] != float64(2) {
		t.Fatalf("list: got %d %v, want count 2", code, body)
	}
	ids, _ := body["connections"].([]any)
	if len(ids) != 2 || ids[0] != first || ids[1] != second {
		t.Fatalf("list ids: got %v, want [%s %s]", ids, first, second)
	}

	if code, _ := doJSON(t, env.token, http.MethodDelete, base+"/"+first, nil); code != http.StatusNoContent {
		t.Fatalf("close: got %d, want 204", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodDelete, base+"/"+first, nil); code != http.StatusNotFound {
		t.Fatalf("close twice: got %d, want 404", code)
	}
	code, body = doJSON(t, env.token, http.MethodGet, base, nil)
	if code != http.StatusOK || body["count"] != float64(1) {
		t.Fatalf("list after close: got %d %v, want count 1", code, body)
	}

	// Unknown workspace counts zero.
	code, body = doJSON(t, env.token, http.MethodGet, env.srv.URL+"/v1/workspaces/nope/connections", nil)
	if code != http.StatusOK || body["count"] != float64(0) {
		t.Fatalf("list unknown: got %d %v, want count 0", code, body)
	}
}

// TestExecStreamsThroughGateway proves the exec WebSocket upgrades
// through the gateway to the wrapped API server.
func TestExecStreamsThroughGateway(t *testing.T) {
	svc, err := auth.NewService(auth.NewFakeStore(), []byte("gateway-test-secret-0123456789-01"))
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}
	token, _, err := svc.Issue(context.Background(), "test-user", "test-device")
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	backend := &controller.FakeBackend{
		ExecFunc: func(_ context.Context, _ string, opts container.ExecOptions) error {
			_, err := opts.Stdout.Write([]byte("via-gateway"))
			return err
		},
	}
	store, err := controller.NewFileStore(t.TempDir() + "/ws.json")
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus())
	backendAPI := api.NewServer(ctrl, api.WithAuth(svc))
	srv := httptest.NewServer(New(NewRegistry(), backendAPI, WithAuth(svc)))
	t.Cleanup(srv.Close)

	code, body := doJSON(t, token, http.MethodPost, srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "u", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create through gateway: got %d", code)
	}
	id, _ := body["id"].(string)
	if code, _ := doJSON(t, token, http.MethodPost, srv.URL+"/v1/workspaces/"+id+"/start", nil); code != http.StatusOK {
		t.Fatalf("start through gateway: got %d", code)
	}

	conn, err := wsclient.Dial("ws://"+strings.TrimPrefix(srv.URL, "http://")+
		"/v1/workspaces/"+id+"/exec?argv=echo",
		http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatalf("dial exec through gateway: %v", err)
	}
	defer conn.Close()
	op, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("exec read through gateway: %v", err)
	}
	if op != wsclient.OpBinary || string(msg) != "via-gateway" {
		t.Fatalf("exec output: got op=%d %q, want binary via-gateway", op, msg)
	}

	// Without a token the handshake is rejected.
	_, err = wsclient.Dial("ws://"+strings.TrimPrefix(srv.URL, "http://")+
		"/v1/workspaces/"+id+"/exec?argv=echo", nil)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("dial without token: got %v, want 401", err)
	}
}

func TestGatewayUnconfigured(t *testing.T) {
	srv := httptest.NewServer(New(NewRegistry(), stubBackend())) // No WithAuth.
	t.Cleanup(srv.Close)
	if code, _ := doJSON(t, "", http.MethodGet, srv.URL+"/v1/workspaces/ws-1/ports", nil); code != http.StatusUnauthorized {
		t.Fatalf("unconfigured ports: got %d, want 401", code)
	}
	if code, _ := doJSON(t, "", http.MethodGet, srv.URL+"/v1/workspaces", nil); code != http.StatusUnauthorized {
		t.Fatalf("unconfigured delegated: got %d, want 401", code)
	}
	if code, _ := doJSON(t, "", http.MethodGet, srv.URL+"/healthz", nil); code != http.StatusOK {
		t.Fatalf("unconfigured healthz: got %d, want backend 200", code)
	}
}

func TestBearerTokenShapes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		want   string
	}{
		{"missing", "", ""},
		{"bearer", "Bearer abc", "abc"},
		{"bearer padded", "Bearer   abc  ", "abc"},
		{"wrong scheme", "Token abc", ""},
		{"bare", "abc", ""},
		{"empty token", "Bearer   ", ""},
	} {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		if tc.header != "" {
			r.Header.Set("Authorization", tc.header)
		}
		if got := bearerToken(r); got != tc.want {
			t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
