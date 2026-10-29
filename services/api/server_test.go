package api

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/packages/types"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api/internal/ws"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// testEnv wires the API over a temp file store and a fake runtime.
type testEnv struct {
	srv     *httptest.Server
	backend *controller.FakeBackend
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &controller.FakeBackend{}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus())
	srv := httptest.NewServer(NewServer(ctrl))
	t.Cleanup(srv.Close)
	return &testEnv{srv: srv, backend: backend}
}

// doJSON sends a JSON request and decodes a JSON response body (if any).
func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
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

func createWorkspace(t *testing.T, env *testEnv, userID, name string) map[string]any {
	t.Helper()
	code, body := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces",
		map[string]string{"user_id": userID, "name": name})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d (%v), want 201", code, body)
	}
	return body
}

func action(t *testing.T, env *testEnv, id, op string) (int, map[string]any) {
	t.Helper()
	return doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces/"+id+"/"+op, nil)
}

func TestHealthz(t *testing.T) {
	env := newTestEnv(t)
	code, body := doJSON(t, http.MethodGet, env.srv.URL+"/healthz", nil)
	if code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz: got %d %v, want 200 {status:ok}", code, body)
	}
}

func TestCreateValidation(t *testing.T) {
	env := newTestEnv(t)
	for name, body := range map[string]any{
		"empty":       map[string]string{},
		"blank name":  map[string]string{"user_id": "u", "name": "  "},
		"blank user":  map[string]string{"user_id": " ", "name": "n"},
		"wrong shape": "just a string",
	} {
		if code, _ := doJSON(t, http.MethodPost, env.srv.URL+"/v1/workspaces", body); code != http.StatusBadRequest {
			t.Fatalf("create %s: got %d, want 400", name, code)
		}
	}
}

func TestGetAndList(t *testing.T) {
	env := newTestEnv(t)
	created := createWorkspace(t, env, "user-1", "dev")
	id := created["id"].(string)
	if created["status"] != string(types.WorkspaceCreated) {
		t.Fatalf("create status: got %v, want CREATED", created["status"])
	}

	code, body := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id, nil)
	if code != http.StatusOK || body["id"] != id {
		t.Fatalf("get: got %d %v", code, body)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/nope", nil); code != http.StatusNotFound {
		t.Fatalf("get missing: got %d, want 404", code)
	}

	code, body = doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces", nil)
	if code != http.StatusOK {
		t.Fatalf("list: got %d", code)
	}
	items, _ := body["workspaces"].([]any)
	if len(items) != 1 {
		t.Fatalf("list: got %v, want 1 workspace", body)
	}
}

func TestTransitionActions(t *testing.T) {
	env := newTestEnv(t)
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	steps := []struct{ op, want string }{
		{"start", "RUNNING"},
		{"sleep", "SLEEPING"},
		{"wakeup", "RUNNING"},
		{"stop", "STOPPED"},
		{"restore", "RUNNING"},
		{"stop", "STOPPED"},
	}
	for _, s := range steps {
		code, body := action(t, env, id, s.op)
		if code != http.StatusOK || body["status"] != s.want {
			t.Fatalf("%s: got %d %v, want 200 %s", s.op, code, body, s.want)
		}
	}

	// Illegal transitions surface as 409 with unchanged state.
	if code, _ := action(t, env, id, "start"); code != http.StatusOK {
		t.Fatalf("restart from STOPPED: got %d, want 200", code)
	}
	if code, body := action(t, env, id, "start"); code != http.StatusConflict {
		t.Fatalf("start while RUNNING: got %d %v, want 409", code, body)
	}
	if code, _ := action(t, env, id, "restore"); code != http.StatusConflict {
		t.Fatalf("restore while RUNNING: got %d, want 409", code)
	}
	if code, _ := action(t, env, "nope", "start"); code != http.StatusNotFound {
		t.Fatalf("start missing: got %d, want 404", code)
	}
}

func TestDelete(t *testing.T) {
	env := newTestEnv(t)
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	if code, _ := action(t, env, id, "start"); code != http.StatusOK {
		t.Fatalf("start: got %d", code)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete RUNNING: got %d, want 409", resp.StatusCode)
	}

	if code, _ := action(t, env, id, "stop"); code != http.StatusOK {
		t.Fatalf("stop: got %d", code)
	}
	req, _ = http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete STOPPED: got %d, want 204", resp.StatusCode)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete: got %d, want 404", code)
	}
}

// --- Minimal WebSocket test client ---

type wsClient struct {
	conn net.Conn
	r    *bufio.Reader
}

func dialExec(t *testing.T, baseURL, path string) *wsClient {
	t.Helper()
	addr := strings.TrimPrefix(baseURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := &wsClient{conn: conn, r: bufio.NewReader(conn)}
	t.Cleanup(func() { conn.Close() })
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	status, err := c.r.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("handshake status: got %q, want 101", strings.TrimSpace(status))
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" {
			return c
		}
	}
}

func (c *wsClient) send(t *testing.T, op ws.Opcode, payload []byte) {
	t.Helper()
	mask := [4]byte{0xaa, 0xbb, 0xcc, 0xdd}
	hdr := []byte{0x80 | byte(op), 0x80 | byte(len(payload))}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.conn.Write(append(hdr, masked...)); err != nil {
		t.Fatalf("send frame: %v", err)
	}
}

func (c *wsClient) recv(t *testing.T) (ws.Opcode, []byte) {
	t.Helper()
	b := make([]byte, 2)
	if _, err := io.ReadFull(c.r, b); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	length := uint64(b[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		_, _ = io.ReadFull(c.r, ext[:])
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		_, _ = io.ReadFull(c.r, ext[:])
		length = binary.BigEndian.Uint64(ext[:])
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return ws.Opcode(b[0] & 0x0f), payload
}

// TestIntegration_FullFlow drives create -> start -> exec -> stop ->
// delete against the fake runtime through real HTTP.
func TestIntegration_FullFlow(t *testing.T) {
	env := newTestEnv(t)
	env.backend.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) error {
		want := []string{"cat"}
		if len(opts.Cmd) != 1 || opts.Cmd[0] != want[0] {
			return fmt.Errorf("unexpected cmd %v", opts.Cmd)
		}
		in := make([]byte, 4)
		if _, err := io.ReadFull(opts.Stdin, in); err != nil {
			return err
		}
		_, err := opts.Stdout.Write(append([]byte("got:"), in...))
		return err
	}

	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	if code, body := action(t, env, id, "start"); code != http.StatusOK || body["status"] != "RUNNING" {
		t.Fatalf("start: got %d %v", code, body)
	}

	cli := dialExec(t, env.srv.URL, "/v1/workspaces/"+id+"/exec?argv=cat")
	cli.send(t, ws.OpText, []byte("ping"))
	op, payload := cli.recv(t)
	if op != ws.OpBinary || string(payload) != "got:ping" {
		t.Fatalf("exec output: got op=%d %q, want binary got:ping", op, payload)
	}
	if op, _ := cli.recv(t); op != ws.OpClose {
		t.Fatalf("exec end: got op=%d, want close", op)
	}
	if len(env.backend.Execs) != 1 {
		t.Fatalf("recorded execs: got %d, want 1", len(env.backend.Execs))
	}

	if code, body := action(t, env, id, "stop"); code != http.StatusOK || body["status"] != "STOPPED" {
		t.Fatalf("stop: got %d %v", code, body)
	}
	req, _ := http.NewRequest(http.MethodDelete, env.srv.URL+"/v1/workspaces/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d, want 204", resp.StatusCode)
	}
	if code, _ := doJSON(t, http.MethodGet, env.srv.URL+"/v1/workspaces/"+id, nil); code != http.StatusNotFound {
		t.Fatalf("get after delete: got %d, want 404", code)
	}
}

func TestExecErrors(t *testing.T) {
	env := newTestEnv(t)
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)

	// Missing argv.
	resp, err := http.Get(env.srv.URL + "/v1/workspaces/" + id + "/exec")
	if err != nil {
		t.Fatalf("exec without argv: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("exec without argv: got %d, want 400", resp.StatusCode)
	}

	// Unknown workspace (plain GET, no upgrade headers: the 404 wins
	// before the handshake is attempted).
	resp, err = http.Get(env.srv.URL + "/v1/workspaces/nope/exec?argv=cat")
	if err != nil {
		t.Fatalf("exec missing: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("exec missing: got %d, want 404", resp.StatusCode)
	}

	// Not running.
	resp, err = http.Get(env.srv.URL + "/v1/workspaces/" + id + "/exec?argv=cat")
	if err != nil {
		t.Fatalf("exec created: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("exec on CREATED: got %d, want 409", resp.StatusCode)
	}
}

func TestExecFailureSendsErrorMessage(t *testing.T) {
	env := newTestEnv(t)
	env.backend.ExecFunc = func(ctx context.Context, id string, opts container.ExecOptions) error {
		return fmt.Errorf("kaboom")
	}
	id := createWorkspace(t, env, "user-1", "dev")["id"].(string)
	if code, _ := action(t, env, id, "start"); code != http.StatusOK {
		t.Fatalf("start: got %d", code)
	}

	cli := dialExec(t, env.srv.URL, "/v1/workspaces/"+id+"/exec?argv=false")
	op, payload := cli.recv(t)
	if op != ws.OpText || !strings.HasPrefix(string(payload), "error: ") {
		t.Fatalf("exec failure: got op=%d %q, want error text", op, payload)
	}
	if op, _ := cli.recv(t); op != ws.OpClose {
		t.Fatalf("exec end: got op=%d, want close", op)
	}
}
