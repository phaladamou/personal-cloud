package api

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTerminalPage(t *testing.T) {
	env := newTestEnv(t)
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	resp, err := http.Get(env.srv.URL + "/terminal/" + id)
	if err != nil {
		t.Fatalf("GET terminal: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET terminal: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type: got %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"xterm", "/v1/workspaces/", "/exec?argv=", "Phase 2"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("terminal page lacks %q", want)
		}
	}
}

func TestTerminalMissingWorkspace(t *testing.T) {
	env := newTestEnv(t)
	resp, err := http.Get(env.srv.URL + "/terminal/nope")
	if err != nil {
		t.Fatalf("GET terminal: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET terminal missing: got %d, want 404", resp.StatusCode)
	}
}
