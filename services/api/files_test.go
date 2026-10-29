package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// filesEnv wires the API over a dir-backed filesystem provider.
type filesEnv struct {
	srv   *httptest.Server
	ctrl  *controller.Controller
	bus   *protocol.EventBus
	token string
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	bus := protocol.NewEventBus()
	ctrl := controller.NewController(store, &controller.FakeBackend{}, bus,
		controller.WithFilesystems(controller.DirFSProvider{Root: t.TempDir()}))
	authSvc, token := newTestAuth(t)
	srv := httptest.NewServer(NewServer(ctrl, WithAuth(authSvc)))
	t.Cleanup(srv.Close)
	return &filesEnv{srv: srv, ctrl: ctrl, bus: bus, token: token}
}

func filesURL(base, id, op, path string) string {
	u := base + "/v1/workspaces/" + id + "/files" + op
	if path != "" {
		u += "?path=" + url.QueryEscape(path)
	}
	return u
}

func doRaw(t *testing.T, token, method, url string, body []byte) (int, []byte, http.Header) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
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
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, raw, resp.Header
}

func TestFileFlow(t *testing.T) {
	env := newFilesEnv(t)
	var events []protocol.Event
	env.bus.Subscribe(protocol.EventFileChanged, func(e protocol.Event) { events = append(events, e) })

	code, created := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d (%v), want 201", code, created)
	}
	id := created["id"].(string)

	// Upload a file.
	code, _, _ = doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, id, "/content", "a/b.txt"), []byte("hello"))
	if code != http.StatusOK {
		t.Fatalf("put: got %d, want 200", code)
	}
	// List the root and the directory.
	code, list := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", ""), nil)
	if code != http.StatusOK {
		t.Fatalf("list root: got %d", code)
	}
	items, _ := list["files"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "a" {
		t.Fatalf("list root: got %v, want [a/]", list)
	}
	code, list = doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", "a"), nil)
	if code != http.StatusOK {
		t.Fatalf("list dir: got %d", code)
	}
	items, _ = list["files"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["path"] != "a/b.txt" {
		t.Fatalf("list dir: got %v, want [a/b.txt]", list)
	}
	// Download the file.
	code, raw, hdr := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "a/b.txt"), nil)
	if code != http.StatusOK || string(raw) != "hello" {
		t.Fatalf("get content: got %d %q, want 200 hello", code, raw)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("content type = %q, want application/octet-stream", ct)
	}
	// Create a directory.
	code, _ = doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/files/mkdir",
		map[string]string{"path": "a/c"})
	if code != http.StatusCreated {
		t.Fatalf("mkdir: got %d, want 201", code)
	}
	// Delete the file.
	code, _, _ = doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", "a/b.txt"), nil)
	if code != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", code)
	}
	code, _, _ = doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "a/b.txt"), nil)
	if code != http.StatusNotFound {
		t.Fatalf("get deleted: got %d, want 404", code)
	}

	if len(events) != 3 {
		t.Fatalf("file.changed events: got %d, want 3", len(events))
	}
	for i, want := range []string{"write", "mkdir", "delete"} {
		var payload struct {
			Path string `json:"path"`
			Op   string `json:"op"`
		}
		if err := events[i].DecodePayload(&payload); err != nil {
			t.Fatalf("event %d payload: %v", i, err)
		}
		if events[i].WorkspaceID != id || payload.Op != want {
			t.Fatalf("event %d = %+v / %+v, want op %s", i, events[i], payload, want)
		}
	}
}

func TestFileErrors(t *testing.T) {
	env := newFilesEnv(t)
	code, created := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d", code)
	}
	id := created["id"].(string)
	if err := env.ctrl.WriteFile(context.Background(), id, "f.txt", []byte("hello")); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	// Unknown workspace on every endpoint.
	if code, _ := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, "nope", "", ""), nil); code != http.StatusNotFound {
		t.Fatalf("list on missing workspace: got %d, want 404", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, "nope", "/content", "f"), nil); code != http.StatusNotFound {
		t.Fatalf("get on missing workspace: got %d, want 404", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, "nope", "/content", "f"), []byte("x")); code != http.StatusNotFound {
		t.Fatalf("put on missing workspace: got %d, want 404", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/nope/files/mkdir", map[string]string{"path": "d"}); code != http.StatusNotFound {
		t.Fatalf("mkdir on missing workspace: got %d, want 404", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, "nope", "", "f"), nil); code != http.StatusNotFound {
		t.Fatalf("delete on missing workspace: got %d, want 404", code)
	}

	// Traversal is rejected everywhere.
	if code, _ := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", "../x"), nil); code != http.StatusBadRequest {
		t.Fatalf("list traversal: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "a/../../x"), nil); code != http.StatusBadRequest {
		t.Fatalf("get traversal: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, id, "/content", ".."), []byte("x")); code != http.StatusBadRequest {
		t.Fatalf("put traversal: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/files/mkdir", map[string]string{"path": "../d"}); code != http.StatusBadRequest {
		t.Fatalf("mkdir traversal: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", "../f"), nil); code != http.StatusBadRequest {
		t.Fatalf("delete traversal: got %d, want 400", code)
	}

	// Missing path parameter.
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", ""), nil); code != http.StatusBadRequest {
		t.Fatalf("get without path: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, id, "/content", ""), []byte("x")); code != http.StatusBadRequest {
		t.Fatalf("put without path: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", ""), nil); code != http.StatusBadRequest {
		t.Fatalf("delete without path: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/files/mkdir", map[string]string{}); code != http.StatusBadRequest {
		t.Fatalf("mkdir without path: got %d, want 400", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/files/mkdir", "bogus"); code != http.StatusBadRequest {
		t.Fatalf("mkdir invalid body: got %d, want 400", code)
	}

	// Missing paths, files vs directories, root deletion.
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "nope"), nil); code != http.StatusNotFound {
		t.Fatalf("get missing: got %d, want 404", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", "nope"), nil); code != http.StatusNotFound {
		t.Fatalf("list missing: got %d, want 404", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", "nope"), nil); code != http.StatusNotFound {
		t.Fatalf("delete missing: got %d, want 404", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "f.txt"), nil); code != http.StatusOK {
		t.Fatalf("get file: got %d, want 200", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", "f.txt"), nil); code != http.StatusBadRequest {
		t.Fatalf("list on file: got %d, want 400", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", "."), nil); code != http.StatusBadRequest {
		t.Fatalf("delete root: got %d, want 400", code)
	}
}

func TestFileSizeCaps(t *testing.T) {
	env := newFilesEnv(t)
	code, created := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": "user-1", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create workspace: got %d", code)
	}
	id := created["id"].(string)

	// Oversized upload is rejected.
	big := bytes.Repeat([]byte("x"), maxFileBytes+1)
	code, _, _ = doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, id, "/content", "big.bin"), big)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("put oversized: got %d, want 413", code)
	}
	// Oversized download is rejected without reading: seed past the
	// cap through the controller, which enforces no cap on writes.
	if err := env.ctrl.WriteFile(context.Background(), id, "big.bin", big); err != nil {
		t.Fatalf("seed big file: %v", err)
	}
	code, _, _ = doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "big.bin"), nil)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("get oversized: got %d, want 413", code)
	}
}

func TestFilesUnavailable(t *testing.T) {
	env := newTestEnv(t) // No filesystem provider.
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	if code, _ := doJSON(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "", ""), nil); code != http.StatusNotImplemented {
		t.Fatalf("list without provider: got %d, want 501", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodGet, filesURL(env.srv.URL, id, "/content", "f"), nil); code != http.StatusNotImplemented {
		t.Fatalf("get without provider: got %d, want 501", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodPut, filesURL(env.srv.URL, id, "/content", "f"), []byte("x")); code != http.StatusNotImplemented {
		t.Fatalf("put without provider: got %d, want 501", code)
	}
	if code, _ := doJSON(t, env.token, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/files/mkdir", map[string]string{"path": "d"}); code != http.StatusNotImplemented {
		t.Fatalf("mkdir without provider: got %d, want 501", code)
	}
	if code, _, _ := doRaw(t, env.token, http.MethodDelete, filesURL(env.srv.URL, id, "", "f"), nil); code != http.StatusNotImplemented {
		t.Fatalf("delete without provider: got %d, want 501", code)
	}
}
