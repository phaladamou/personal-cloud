// Package ws is a minimal stdlib-only WebSocket (RFC 6455) server side.
//
// It exists so the exec endpoint can stream stdio without third-party
// dependencies. Only what the API needs is implemented: the opening
// handshake over http.Hijacker, text/binary messages with continuation
// reassembly, and ping/pong/close control frames. There is deliberately
// no extension, subprotocol, or fragmentation support on writes.
package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Opcode is a WebSocket frame opcode.
type Opcode byte

// Frame opcodes handled by this package.
const (
	OpText   Opcode = 0x1
	OpBinary Opcode = 0x2
	OpClose  Opcode = 0x8
	OpPing   Opcode = 0x9
	OpPong   Opcode = 0xA
)

// guid is the RFC 6455 magic suffix for the accept key.
const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// MaxMessageBytes bounds a reassembled message. Larger messages are
// rejected with a protocol error so a peer cannot grow server memory
// without limit.
const MaxMessageBytes = 1 << 20

// ErrClosed is returned by ReadMessage once the closing handshake ran.
var ErrClosed = errors.New("ws: connection closed")

// acceptKey derives the Sec-WebSocket-Accept value for key.
func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// hasToken reports whether the comma-separated header value holds token
// (case-insensitive, per RFC 7230).
func hasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Upgrade runs the server side of the opening handshake and returns the
// connection. On failure it writes the HTTP error itself and returns a
// non-nil error.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	fail := func(code int, msg string) (*Conn, error) {
		http.Error(w, msg, code)
		return nil, fmt.Errorf("ws: upgrade: %s", msg)
	}
	if r.Method != http.MethodGet {
		return fail(http.StatusMethodNotAllowed, "method must be GET")
	}
	if !hasToken(r.Header.Get("Connection"), "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return fail(http.StatusBadRequest, "not a websocket upgrade")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		return fail(http.StatusUpgradeRequired, "unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return fail(http.StatusBadRequest, "invalid Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return fail(http.StatusInternalServerError, "server does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("ws: hijack: %w", err)
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n" +
		"\r\n"
	if _, err := io.WriteString(rw, resp); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ws: write handshake: %w", err)
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ws: flush handshake: %w", err)
	}
	return &Conn{conn: conn, r: rw.Reader}, nil
}

// Conn is one hijacked WebSocket connection. Reads and writes may run
// on different goroutines; concurrent writes are serialized.
type Conn struct {
	conn net.Conn
	r    *bufio.Reader

	wmu    sync.Mutex
	closed bool
}

// frameHeader is a parsed frame prefix.
type frameHeader struct {
	fin     bool
	op      Opcode
	masked  bool
	length  uint64
	maskKey [4]byte
}

// readHeader parses one frame header from the peer.
func (c *Conn) readHeader() (frameHeader, error) {
	var h frameHeader
	b := make([]byte, 2)
	if _, err := io.ReadFull(c.r, b); err != nil {
		return h, err
	}
	h.fin = b[0]&0x80 != 0
	if b[0]&0x70 != 0 {
		return h, fmt.Errorf("ws: extensions not supported")
	}
	h.op = Opcode(b[0] & 0x0f)
	h.masked = b[1]&0x80 != 0
	h.length = uint64(b[1] & 0x7f)
	switch h.length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return h, err
		}
		h.length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return h, err
		}
		h.length = binary.BigEndian.Uint64(ext[:])
		if h.length>>63 != 0 {
			return h, fmt.Errorf("ws: oversized frame length")
		}
	}
	if h.masked {
		if _, err := io.ReadFull(c.r, h.maskKey[:]); err != nil {
			return h, err
		}
	}
	return h, nil
}

// protocolError sends a close frame and reports a protocol violation.
func (c *Conn) protocolError(msg string) error {
	_ = c.writeFrame(OpClose, []byte(msg))
	return fmt.Errorf("ws: protocol error: %s", msg)
}

// ReadMessage returns the next complete data message, reassembling
// continuations. Ping frames are answered automatically, pong frames
// ignored. When the peer closes, ReadMessage replies and returns
// ErrClosed.
func (c *Conn) ReadMessage() (Opcode, []byte, error) {
	var msg []byte
	var msgOp Opcode
	started := false
	for {
		h, err := c.readHeader()
		if err != nil {
			return 0, nil, err
		}
		if !h.masked {
			return 0, nil, c.protocolError("client frames must be masked")
		}
		payload := make([]byte, h.length)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return 0, nil, err
		}
		for i := range payload {
			payload[i] ^= h.maskKey[i%4]
		}
		switch h.op {
		case OpText, OpBinary:
			if started {
				return 0, nil, c.protocolError("new message before previous finished")
			}
			if h.length+uint64(len(msg)) > MaxMessageBytes {
				return 0, nil, c.protocolError("message too large")
			}
			msgOp, msg, started = h.op, payload, true
			if h.fin {
				return msgOp, msg, nil
			}
		case 0x0: // continuation
			if !started {
				return 0, nil, c.protocolError("orphan continuation")
			}
			if uint64(len(msg))+h.length > MaxMessageBytes {
				return 0, nil, c.protocolError("message too large")
			}
			msg = append(msg, payload...)
			if h.fin {
				return msgOp, msg, nil
			}
		case OpPing:
			if !h.fin || h.length > 125 {
				return 0, nil, c.protocolError("invalid ping")
			}
			if err := c.WriteMessage(OpPong, payload); err != nil {
				return 0, nil, err
			}
		case OpPong:
			if !h.fin || h.length > 125 {
				return 0, nil, c.protocolError("invalid pong")
			}
		case OpClose:
			if !h.fin || h.length > 125 {
				return 0, nil, c.protocolError("invalid close")
			}
			_ = c.WriteMessage(OpClose, payload)
			return 0, nil, ErrClosed
		default:
			return 0, nil, c.protocolError("unknown opcode")
		}
	}
}

// writeFrame sends one unfragmented server frame.
func (c *Conn) writeFrame(op Opcode, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return fmt.Errorf("ws: write on closed connection")
	}
	var hdr []byte
	hdr = append(hdr, 0x80|byte(op))
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
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	_, err := c.conn.Write(p)
	return err
}

// WriteMessage sends one unfragmented message frame with the given
// opcode (OpText, OpBinary, or a control opcode).
func (c *Conn) WriteMessage(op Opcode, p []byte) error {
	return c.writeFrame(op, p)
}

// Close runs the closing handshake (best-effort) and closes the
// underlying connection.
func (c *Conn) Close() error {
	_ = c.writeFrame(OpClose, nil)
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	return c.conn.Close()
}
