package wsclient

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phaladamou/personal-cloud/packages/protocol"
	"github.com/phaladamou/personal-cloud/runtime/container"
	"github.com/phaladamou/personal-cloud/services/api"
	controller "github.com/phaladamou/personal-cloud/services/workspace-controller"
)

// testGUID is the RFC 6455 magic suffix, repeated here so the raw test
// server below stays independent of both wsclient and the stage-3
// server implementation.
const testGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// rawEchoServer is a minimal independent WebSocket echo server
// (handshake + frame echo over a hijacked connection) used to verify
// client framing without importing the stage-3 server package.
func rawEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not a websocket upgrade", http.StatusBadRequest)
			return
		}
		key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		sum := sha1.Sum([]byte(key + testGUID))
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n" +
			"\r\n"
		if _, err := io.WriteString(rw, resp); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		writeFrame := func(op byte, p []byte) error {
			hdr := []byte{0x80 | op}
			switch {
			case len(p) <= 125:
				hdr = append(hdr, byte(len(p)))
			case len(p) <= 0xffff:
				hdr = append(hdr, 126, byte(len(p)>>8), byte(len(p)))
			default:
				hdr = append(hdr, 127)
				var ext [8]byte
				binary.BigEndian.PutUint64(ext[:], uint64(len(p)))
				hdr = append(hdr, ext[:]...)
			}
			if _, err := conn.Write(hdr); err != nil {
				return err
			}
			_, err := conn.Write(p)
			return err
		}
		rd := rw.Reader
		for {
			b := make([]byte, 2)
			if _, err := io.ReadFull(rd, b); err != nil {
				return
			}
			op := b[0] & 0x0f
			masked := b[1]&0x80 != 0
			length := uint64(b[1] & 0x7f)
			switch length {
			case 126:
				var ext [2]byte
				if _, err := io.ReadFull(rd, ext[:]); err != nil {
					return
				}
				length = uint64(binary.BigEndian.Uint16(ext[:]))
			case 127:
				var ext [8]byte
				if _, err := io.ReadFull(rd, ext[:]); err != nil {
					return
				}
				length = binary.BigEndian.Uint64(ext[:])
			}
			var mask [4]byte
			if masked {
				if _, err := io.ReadFull(rd, mask[:]); err != nil {
					return
				}
			}
			payload := make([]byte, length)
			if _, err := io.ReadFull(rd, payload); err != nil {
				return
			}
			if masked {
				for i := range payload {
					payload[i] ^= mask[i%4]
				}
			} else {
				// Clients must mask: fail the test visibly.
				t.Error("rawEchoServer: got unmasked client frame")
				return
			}
			switch op {
			case 0x1, 0x2: // text, binary
				if err := writeFrame(op, payload); err != nil {
					return
				}
			case 0x9: // ping
				if err := writeFrame(0xA, payload); err != nil {
					return
				}
			case 0x8: // close
				_ = writeFrame(0x8, payload)
				return
			}
		}
	}))
}

func dialRaw(t *testing.T, srv *httptest.Server) *Conn {
	t.Helper()
	c, err := Dial("ws://"+strings.TrimPrefix(srv.URL, "http://")+"/socket", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestEchoTextAndBinary(t *testing.T) {
	srv := rawEchoServer(t)
	defer srv.Close()
	c := dialRaw(t, srv)

	for _, tc := range []struct {
		op   Opcode
		body string
	}{
		{OpText, "hello"},
		{OpBinary, "raw bytes \x00\xff"},
		{OpText, ""},
	} {
		if err := c.WriteMessage(tc.op, []byte(tc.body)); err != nil {
			t.Fatalf("WriteMessage(%q): %v", tc.body, err)
		}
		op, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if op != tc.op || string(msg) != tc.body {
			t.Fatalf("echo: got op=%d %q, want op=%d %q", op, msg, tc.op, tc.body)
		}
	}
}

func TestEchoLargePayloads(t *testing.T) {
	srv := rawEchoServer(t)
	defer srv.Close()
	c := dialRaw(t, srv)

	// Cover all three frame length encodings (7-bit, 16-bit, 64-bit).
	for _, size := range []int{125, 126, 1024, 0xffff, 0x10000} {
		body := bytes.Repeat([]byte{0xab}, size)
		if err := c.WriteMessage(OpBinary, body); err != nil {
			t.Fatalf("WriteMessage(%d bytes): %v", size, err)
		}
		op, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage(%d bytes): %v", size, err)
		}
		if op != OpBinary || !bytes.Equal(msg, body) {
			t.Fatalf("echo %d bytes: got op=%d len=%d, want binary len=%d", size, op, len(msg), size)
		}
	}
}

func TestPingPong(t *testing.T) {
	srv := rawEchoServer(t)
	defer srv.Close()
	c := dialRaw(t, srv)

	if err := c.WriteMessage(OpPing, []byte("ping")); err != nil {
		t.Fatalf("WriteMessage ping: %v", err)
	}
	// The pong reply is skipped by ReadMessage; a following echo
	// proves the connection is still healthy.
	if err := c.WriteMessage(OpText, []byte("after-ping")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	op, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpText || string(msg) != "after-ping" {
		t.Fatalf("after ping: got op=%d %q", op, msg)
	}
}

func TestCloseHandshake(t *testing.T) {
	srv := rawEchoServer(t)
	defer srv.Close()
	c := dialRaw(t, srv)

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.WriteMessage(OpText, []byte("x")); err == nil {
		t.Fatal("WriteMessage after Close: got nil, want error")
	}
}

func TestDialErrors(t *testing.T) {
	srv := rawEchoServer(t)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	for name, rawURL := range map[string]string{
		"bad scheme": "wss://" + host + "/socket",
		"no host":    "ws:///socket",
		"bad url":    "ws://[::1]:x/socket",
	} {
		if _, err := Dial(rawURL, nil); err == nil {
			t.Fatalf("Dial %s: got nil, want error", name)
		}
	}

	// Plain HTTP endpoint (no upgrade): handshake must fail, not hang.
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a websocket"))
	}))
	defer plain.Close()
	if _, err := Dial("ws://"+strings.TrimPrefix(plain.URL, "http://"), nil); err == nil {
		t.Fatal("Dial plain HTTP: got nil, want error")
	}

	// Unreachable host.
	if _, err := Dial("ws://127.0.0.1:1/socket", nil); err == nil {
		t.Fatal("Dial refused: got nil, want error")
	}
}

func TestServerCloseSurfacesErrClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj := w.(http.Hijacker)
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
		sum := sha1.Sum([]byte(key + testGUID))
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n" +
			"\r\n"
		if _, err := io.WriteString(rw, resp); err != nil {
			return
		}
		if err := rw.Flush(); err != nil {
			return
		}
		// One close frame, then TCP close.
		_, _ = conn.Write([]byte{0x88, 0x03, 0x03, 0xe8, 0x00})
	}))
	defer srv.Close()

	c, err := Dial("ws://"+strings.TrimPrefix(srv.URL, "http://")+"/socket", nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	if _, _, err := c.ReadMessage(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ReadMessage: got %v, want ErrClosed", err)
	}
}

// TestExecAgainstStage3Server drives the wsclient against the real
// stage-3 WebSocket exec endpoint (httptest API + fake runtime).
func TestExecAgainstStage3Server(t *testing.T) {
	store, err := controller.NewFileStore(filepath.Join(t.TempDir(), "ws.json"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	backend := &controller.FakeBackend{
		ExecFunc: func(ctx context.Context, id string, opts container.ExecOptions) error {
			in := make([]byte, 4)
			if _, err := io.ReadFull(opts.Stdin, in); err != nil {
				return err
			}
			_, err := opts.Stdout.Write(append([]byte("got:"), in...))
			return err
		},
	}
	ctrl := controller.NewController(store, backend, protocol.NewEventBus())
	srv := httptest.NewServer(api.NewServer(ctrl))
	defer srv.Close()

	post := func(path string, body any) (int, map[string]any) {
		var rdr io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rdr = bytes.NewReader(raw)
		}
		resp, err := http.Post(srv.URL+path, "application/json", rdr)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}
	code, created := post("/v1/workspaces", map[string]string{"user_id": "u", "name": "dev"})
	if code != http.StatusCreated {
		t.Fatalf("create: got %d (%v), want 201", code, created)
	}
	id := created["id"].(string)
	if code, _ := post("/v1/workspaces/"+id+"/start", nil); code != http.StatusOK {
		t.Fatalf("start: got %d, want 200", code)
	}

	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://") +
		"/v1/workspaces/" + id + "/exec?argv=cat"
	c, err := Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial exec: %v", err)
	}
	defer c.Close()
	if err := c.WriteMessage(OpText, []byte("ping")); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}
	op, msg, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpBinary || string(msg) != "got:ping" {
		t.Fatalf("exec output: got op=%d %q, want binary got:ping", op, msg)
	}
	if _, _, err := c.ReadMessage(); !errors.Is(err, ErrClosed) {
		t.Fatalf("exec end: got %v, want ErrClosed", err)
	}
	if len(backend.Execs) != 1 {
		t.Fatalf("recorded execs: got %d, want 1", len(backend.Execs))
	}
}
