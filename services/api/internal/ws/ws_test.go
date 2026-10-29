package ws

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// echoServer upgrades and echoes every message back with the same opcode.
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := Upgrade(w, r)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			op, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(op, msg); err != nil {
				return
			}
		}
	}))
}

// rawDial opens a TCP connection to srv and runs the client handshake,
// returning the connection and its buffered reader.
func rawDial(t *testing.T, srv *httptest.Server, key string) (net.Conn, *bufio.Reader) {
	t.Helper()
	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	req := "GET /socket HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		_ = conn.Close()
		t.Fatalf("write handshake: %v", err)
	}
	return conn, bufio.NewReader(conn)
}

// readHandshake parses the 101 response headers.
func readHandshake(t *testing.T, r *bufio.Reader) (int, http.Header) {
	t.Helper()
	status, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var code int
	if _, err := fmt.Sscanf(status, "HTTP/1.1 %d ", &code); err != nil {
		t.Fatalf("parse status %q: %v", status, err)
	}
	hdr := make(http.Header)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read header: %v", err)
		}
		if line == "\r\n" {
			return code, hdr
		}
		name, value, _ := strings.Cut(line, ":")
		hdr.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
}

// writeMasked sends one masked client frame.
func writeMasked(t *testing.T, w io.Writer, op Opcode, fin bool, payload []byte) {
	t.Helper()
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	b0 := byte(op)
	if fin {
		b0 |= 0x80
	}
	hdr := []byte{b0}
	switch {
	case len(payload) <= 125:
		hdr = append(hdr, 0x80|byte(len(payload)))
	case len(payload) <= 0xffff:
		hdr = append(hdr, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	default:
		hdr = append(hdr, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(payload)))
		hdr = append(hdr, ext[:]...)
	}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := w.Write(append(hdr, masked...)); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

// readServerFrame parses one unmasked server frame.
func readServerFrame(t *testing.T, r *bufio.Reader) (Opcode, []byte) {
	t.Helper()
	b := make([]byte, 2)
	if _, err := io.ReadFull(r, b); err != nil {
		t.Fatalf("read frame header: %v", err)
	}
	if b[0]&0x80 == 0 {
		t.Fatal("server frame is fragmented")
	}
	if b[1]&0x80 != 0 {
		t.Fatal("server frame is masked")
	}
	length := uint64(b[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			t.Fatalf("read ext16: %v", err)
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			t.Fatalf("read ext64: %v", err)
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return Opcode(b[0] & 0x0f), payload
}

func TestHandshakeAcceptVector(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()

	// RFC 6455 section 1.3 example vector.
	conn, r := rawDial(t, srv, "dGhlIHNhbXBsZSBub25jZQ==")
	defer conn.Close()
	code, hdr := readHandshake(t, r)
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("status: got %d, want 101", code)
	}
	if got := hdr.Get("Sec-WebSocket-Accept"); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key: got %q", got)
	}
	if got := hdr.Get("Upgrade"); !strings.EqualFold(got, "websocket") {
		t.Fatalf("upgrade header: got %q", got)
	}
}

func TestEchoAndFragmentation(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()
	conn, r := rawDial(t, srv, "dGhlIHNhbXBsZSBub25jZQ==")
	defer conn.Close()
	if code, _ := readHandshake(t, r); code != 101 {
		t.Fatalf("status: got %d, want 101", code)
	}

	writeMasked(t, conn, OpText, true, []byte("hello"))
	op, payload := readServerFrame(t, r)
	if op != OpText || string(payload) != "hello" {
		t.Fatalf("echo: got op=%d %q, want text hello", op, payload)
	}

	// Two fragments reassemble into one message.
	writeMasked(t, conn, OpBinary, false, []byte("ab"))
	writeMasked(t, conn, 0x0, true, []byte("cd"))
	op, payload = readServerFrame(t, r)
	if op != OpBinary || string(payload) != "abcd" {
		t.Fatalf("fragmented echo: got op=%d %q, want binary abcd", op, payload)
	}
}

func TestPingPong(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()
	conn, r := rawDial(t, srv, "dGhlIHNhbXBsZSBub25jZQ==")
	defer conn.Close()
	if code, _ := readHandshake(t, r); code != 101 {
		t.Fatalf("status: got %d, want 101", code)
	}

	writeMasked(t, conn, OpPing, true, []byte("are you there"))
	op, payload := readServerFrame(t, r)
	if op != OpPong || string(payload) != "are you there" {
		t.Fatalf("ping reply: got op=%d %q, want pong", op, payload)
	}
}

func TestCloseHandshake(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()
	conn, r := rawDial(t, srv, "dGhlIHNhbXBsZSBub25jZQ==")
	defer conn.Close()
	if code, _ := readHandshake(t, r); code != 101 {
		t.Fatalf("status: got %d, want 101", code)
	}

	writeMasked(t, conn, OpClose, true, []byte{0x03, 0xe8})
	op, _ := readServerFrame(t, r)
	if op != OpClose {
		t.Fatalf("close reply: got op=%d, want close", op)
	}
}

func TestUpgradeRejects(t *testing.T) {
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/socket", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		return req
	}
	cases := []struct {
		name string
		mut  func(*http.Request)
		want int
	}{
		{"method", func(r *http.Request) { r.Method = http.MethodPost }, http.StatusMethodNotAllowed},
		{"connection", func(r *http.Request) { r.Header.Set("Connection", "keep-alive") }, http.StatusBadRequest},
		{"upgrade", func(r *http.Request) { r.Header.Del("Upgrade") }, http.StatusBadRequest},
		{"version", func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "12") }, http.StatusUpgradeRequired},
		{"key", func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "bogus") }, http.StatusBadRequest},
		{"no-hijack", func(r *http.Request) {}, http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := newReq()
			tc.mut(req)
			rec := httptest.NewRecorder()
			if _, err := Upgrade(rec, req); err == nil {
				t.Fatal("Upgrade: got nil, want error")
			}
			if rec.Code != tc.want {
				t.Fatalf("status: got %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestServerRejectsUnmaskedFrames(t *testing.T) {
	srv := echoServer(t)
	defer srv.Close()
	conn, r := rawDial(t, srv, "dGhlIHNhbXBsZSBub25jZQ==")
	defer conn.Close()
	if code, _ := readHandshake(t, r); code != 101 {
		t.Fatalf("status: got %d, want 101", code)
	}

	// Unmasked text frame "hi": protocol violation, server must close.
	if _, err := conn.Write([]byte{0x81, 0x02, 'h', 'i'}); err != nil {
		t.Fatalf("write: %v", err)
	}
	op, _ := readServerFrame(t, r)
	if op != OpClose {
		t.Fatalf("violation reply: got op=%d, want close", op)
	}
}

func TestErrClosedExported(t *testing.T) {
	if errors.Is(errors.New("x"), ErrClosed) {
		t.Fatal("unrelated error matches ErrClosed")
	}
}
