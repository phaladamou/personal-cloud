package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/services/auth"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// newTestAuth builds an auth service over a fake store and issues one
// token for test-user/test-device.
func newTestAuth(t *testing.T, opts ...auth.Option) (*auth.Service, string) {
	t.Helper()
	svc, err := auth.NewService(auth.NewFakeStore(), []byte("api-test-secret-0123456789-0123"), opts...)
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}
	token, _, err := svc.Issue(context.Background(), "test-user", "test-device")
	if err != nil {
		t.Fatalf("Issue test token: %v", err)
	}
	return svc, token
}

func TestLoginFlow(t *testing.T) {
	env := newTestEnv(t)

	// Login stays open: no token needed.
	code, body := doJSON(t, "", http.MethodPost, env.srv.URL+"/v1/auth/login",
		map[string]string{"device": "laptop"})
	if code != http.StatusCreated {
		t.Fatalf("login: got %d (%v), want 201", code, body)
	}
	token, _ := body["token"].(string)
	if token == "" || !strings.HasPrefix(token, "pc1.") {
		t.Fatalf("login token: got %v", body)
	}
	if body["user_id"] != "local-user" || body["device"] != "laptop" {
		t.Fatalf("login identity: got %v, want local-user/laptop", body)
	}
	if body["token_id"] == "" || body["expires_at"] == "" {
		t.Fatalf("login fields: got %v", body)
	}

	// The minted token authenticates /v1/* calls.
	code, list := doJSON(t, token, http.MethodGet, env.srv.URL+"/v1/workspaces", nil)
	if code != http.StatusOK {
		t.Fatalf("authed list: got %d (%v), want 200", code, list)
	}

	// An explicit user_id is honored.
	code, body = doJSON(t, "", http.MethodPost, env.srv.URL+"/v1/auth/login",
		map[string]string{"device": "phone", "user_id": "user-9"})
	if code != http.StatusCreated || body["user_id"] != "user-9" || body["device"] != "phone" {
		t.Fatalf("login custom user: got %d %v", code, body)
	}
}

func TestLoginValidation(t *testing.T) {
	env := newTestEnv(t)
	for name, body := range map[string]any{
		"missing device": map[string]string{},
		"blank device":   map[string]string{"device": "  "},
		"wrong shape":    "just a string",
	} {
		if code, _ := doJSON(t, "", http.MethodPost, env.srv.URL+"/v1/auth/login", body); code != http.StatusBadRequest {
			t.Fatalf("login %s: got %d, want 400", name, code)
		}
	}
}

func TestAuthRequired(t *testing.T) {
	env := newTestEnv(t)
	// Tamper with the signature: flip the first char of the signature
	// part to a different valid char, which always changes the decoded
	// bytes. (Flipping the last char is unsound: with RawURLEncoding
	// the trailing char of a 32-byte signature only carries 4
	// significant bits, so some flips decode identically and the test
	// flakes ~6% of runs.)
	parts := strings.Split(env.token, ".")
	sigFirst := byte('A')
	if parts[2][0] == sigFirst {
		sigFirst = 'B'
	}
	tampered := parts[0] + "." + parts[1] + "." + string(sigFirst) + parts[2][1:]

	paths := []struct{ method, path string }{
		{http.MethodGet, "/v1/workspaces"},
		{http.MethodPost, "/v1/workspaces"},
		{http.MethodGet, "/v1/workspaces/nope"},
		{http.MethodPost, "/v1/auth/revoke"},
	}
	for _, p := range paths {
		// No header at all.
		if code, _ := doJSON(t, "", p.method, env.srv.URL+p.path, nil); code != http.StatusUnauthorized {
			t.Fatalf("%s %s without token: got %d, want 401", p.method, p.path, code)
		}
		// Garbage and tampered tokens.
		for name, bad := range map[string]string{"garbage": "not-a-token", "tampered": tampered} {
			if code, _ := doJSON(t, bad, p.method, env.srv.URL+p.path, nil); code != http.StatusUnauthorized {
				t.Fatalf("%s %s %s token: got %d, want 401", p.method, p.path, name, code)
			}
		}
	}

	// A non-Bearer scheme is rejected too.
	req, _ := http.NewRequest(http.MethodGet, env.srv.URL+"/v1/workspaces", nil)
	req.Header.Set("Authorization", "Token "+env.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("wrong scheme: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong scheme: got %d, want 401", resp.StatusCode)
	}
}

func TestRevokeFlow(t *testing.T) {
	env := newTestEnv(t)
	code, body := doJSON(t, "", http.MethodPost, env.srv.URL+"/v1/auth/login",
		map[string]string{"device": "laptop"})
	if code != http.StatusCreated {
		t.Fatalf("login: got %d", code)
	}
	token := body["token"].(string)

	if code, _ := doJSON(t, token, http.MethodPost, env.srv.URL+"/v1/auth/revoke", nil); code != http.StatusNoContent {
		t.Fatalf("revoke: got %d, want 204", code)
	}
	// The token is dead, with the same 401 as an unknown one.
	if code, _ := doJSON(t, token, http.MethodGet, env.srv.URL+"/v1/workspaces", nil); code != http.StatusUnauthorized {
		t.Fatalf("use after revoke: got %d, want 401", code)
	}
	// Revoking again answers 401: the token no longer authenticates.
	if code, _ := doJSON(t, token, http.MethodPost, env.srv.URL+"/v1/auth/revoke", nil); code != http.StatusUnauthorized {
		t.Fatalf("revoke twice: got %d, want 401", code)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	current := now
	svc, err := auth.NewService(auth.NewFakeStore(), []byte("api-test-secret-0123456789-0123"),
		auth.WithTTL(time.Hour), auth.WithNow(func() time.Time { return current }))
	if err != nil {
		t.Fatalf("NewService auth: %v", err)
	}
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus())
	srv := httptest.NewServer(NewServer(ctrl, WithAuth(svc)))
	t.Cleanup(srv.Close)

	code, body := doJSON(t, "", http.MethodPost, srv.URL+"/v1/auth/login",
		map[string]string{"device": "laptop"})
	if code != http.StatusCreated {
		t.Fatalf("login: got %d", code)
	}
	token := body["token"].(string)
	if code, _ := doJSON(t, token, http.MethodGet, srv.URL+"/v1/workspaces", nil); code != http.StatusOK {
		t.Fatalf("use fresh token: got %d, want 200", code)
	}
	current = now.Add(2 * time.Hour)
	if code, _ := doJSON(t, token, http.MethodGet, srv.URL+"/v1/workspaces", nil); code != http.StatusUnauthorized {
		t.Fatalf("use expired token: got %d, want 401", code)
	}
}

func TestIdentityInContext(t *testing.T) {
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	svc, token := newTestAuth(t)
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus())
	s := NewServer(ctrl, WithAuth(svc))
	// In-package probe: the middleware must have stored the identity.
	s.mux.HandleFunc("GET /v1/probe", func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFrom(r.Context())
		if !ok {
			writeError(w, http.StatusInternalServerError, "no identity")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"user_id": id.UserID, "device": id.Device})
	})
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	code, body := doJSON(t, token, http.MethodGet, srv.URL+"/v1/probe", nil)
	if code != http.StatusOK || body["user_id"] != "test-user" || body["device"] != "test-device" {
		t.Fatalf("probe: got %d %v, want test-user/test-device", code, body)
	}
}

func TestAuthUnconfigured(t *testing.T) {
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctrl := controller.NewController(store, &controller.FakeBackend{}, protocol.NewEventBus())
	srv := httptest.NewServer(NewServer(ctrl)) // No WithAuth.
	t.Cleanup(srv.Close)

	if code, _ := doJSON(t, "", http.MethodGet, srv.URL+"/v1/workspaces", nil); code != http.StatusUnauthorized {
		t.Fatalf("unconfigured /v1: got %d, want 401", code)
	}
	if code, _ := doJSON(t, "", http.MethodPost, srv.URL+"/v1/auth/login",
		map[string]string{"device": "laptop"}); code != http.StatusNotImplemented {
		t.Fatalf("unconfigured login: got %d, want 501", code)
	}
	if code, _ := doJSON(t, "", http.MethodGet, srv.URL+"/healthz", nil); code != http.StatusOK {
		t.Fatalf("unconfigured healthz: got %d, want 200", code)
	}
}
