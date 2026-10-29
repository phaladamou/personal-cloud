// Package wsclient is a minimal stdlib-only WebSocket (RFC 6455) client side.
//
// It mirrors services/api/internal/ws (the server side): the opening
// handshake over a raw TCP connection, masked text/binary messages with
// continuation reassembly, and ping/pong/close control frames. Only the
// ws scheme (plain TCP) is supported; TLS is out of scope for Phase 1.
package wsclient

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
// rejected with a protocol error so a peer cannot grow client memory
// without limit.
const MaxMessageBytes = 1 << 20

// ErrClosed is returned by ReadMessage once the closing handshake ran.
var ErrClosed = errors.New("wsclient: connection closed")

// expectedAccept derives the Sec-WebSocket-Accept value the server must
// echo for the given client key.
func expectedAccept(key string) string {
	sum := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// Dial opens a WebSocket client connection to rawURL, which must use the
// ws or http scheme (plain TCP). The path, query, and extra request
// headers (e.g. Authorization) are sent with the handshake.
func Dial(rawURL string, headers http.Header) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("wsclient: parse url: %w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "http" {
		return nil, fmt.Errorf("wsclient: unsupported scheme %q (want ws or http)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("wsclient: missing host in url %q", rawURL)
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "80")
	}
	conn, err := net.Dial("tcp", host)
	if err != nil {
		return nil, fmt.Errorf("wsclient: dial: %w", err)
	}
	closeOnErr := func(err error) (*Conn, error) {
		_ = conn.Close()
		return nil, err
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return closeOnErr(fmt.Errorf("wsclient: nonce: %w", err))
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])

	target := u.RequestURI()
	var req strings.Builder
	req.WriteString("GET " + target + " HTTP/1.1\r\n")
	req.WriteString("Host: " + u.Host + "\r\n")
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	req.WriteString("Sec-WebSocket-Key: " + key + "\r\n")
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	for name, values := range headers {
		for _, v := range values {
			req.WriteString(name + ": " + v + "\r\n")
		}
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(conn, req.String()); err != nil {
		return closeOnErr(fmt.Errorf("wsclient: write handshake: %w", err))
	}

	r := bufio.NewReader(conn)
	status, err := r.ReadString('\n')
	if err != nil {
		return closeOnErr(fmt.Errorf("wsclient: read status: %w", err))
	}
	var code int
	if _, err := fmt.Sscanf(status, "HTTP/1.1 %d ", &code); err != nil || code != http.StatusSwitchingProtocols {
		return closeOnErr(fmt.Errorf("wsclient: handshake status: got %q, want 101", strings.TrimSpace(status)))
	}
	accept := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return closeOnErr(fmt.Errorf("wsclient: read header: %w", err))
		}
		if line == "\r\n" {
			break
		}
		name, value, _ := strings.Cut(line, ":")
		if strings.EqualFold(strings.TrimSpace(name), "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(value)
		}
	}
	if accept != expectedAccept(key) {
		return closeOnErr(fmt.Errorf("wsclient: bad Sec-WebSocket-Accept from server"))
	}
	return &Conn{conn: conn, r: r}, nil
}

// Conn is one WebSocket client connection. Reads and writes may run on
// different goroutines; concurrent writes are serialized.
type Conn struct {
	conn net.Conn
	r    *bufio.Reader

	wmu    sync.Mutex
	closed bool
}

// frameHeader is a parsed frame prefix.
type frameHeader struct {
	fin    bool
	op     Opcode
	masked bool
	length uint64
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
		return h, fmt.Errorf("wsclient: extensions not supported")
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
			return h, fmt.Errorf("wsclient: oversized frame length")
		}
	}
	return h, nil
}

// protocolError sends a close frame and reports a protocol violation.
func (c *Conn) protocolError(msg string) error {
	_ = c.WriteMessage(OpClose, []byte(msg))
	return fmt.Errorf("wsclient: protocol error: %s", msg)
}

// ReadMessage returns the next complete data message, reassembling
// continuations. Ping frames are answered automatically, pong frames
// ignored. When the peer closes, ReadMessage replies (best-effort) and
// returns ErrClosed.
func (c *Conn) ReadMessage() (Opcode, []byte, error) {
	var msg []byte
	var msgOp Opcode
	started := false
	for {
		h, err := c.readHeader()
		if err != nil {
			return 0, nil, err
		}
		if h.masked {
			return 0, nil, c.protocolError("server frames must not be masked")
		}
		payload := make([]byte, h.length)
		if _, err := io.ReadFull(c.r, payload); err != nil {
			return 0, nil, err
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
			_ = c.WriteMessage(OpClose, payload)
			return 0, nil, ErrClosed
		default:
			return 0, nil, c.protocolError("unknown opcode")
		}
	}
}

// WriteMessage sends one unfragmented masked message frame with the
// given opcode (OpText, OpBinary, or a control opcode).
func (c *Conn) WriteMessage(op Opcode, p []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return fmt.Errorf("wsclient: write on closed connection")
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return fmt.Errorf("wsclient: mask: %w", err)
	}
	hdr := []byte{0x80 | byte(op)}
	switch {
	case len(p) <= 125:
		hdr = append(hdr, 0x80|byte(len(p)))
	case len(p) <= 0xffff:
		hdr = append(hdr, 0x80|126, byte(len(p)>>8), byte(len(p)))
	default:
		hdr = append(hdr, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(p)))
		hdr = append(hdr, ext[:]...)
	}
	hdr = append(hdr, mask[:]...)
	if _, err := c.conn.Write(hdr); err != nil {
		return err
	}
	masked := make([]byte, len(p))
	for i := range p {
		masked[i] = p[i] ^ mask[i%4]
	}
	_, err := c.conn.Write(masked)
	return err
}

// Close runs the closing handshake (best-effort) and closes the
// underlying connection.
func (c *Conn) Close() error {
	_ = c.WriteMessage(OpClose, nil)
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	return c.conn.Close()
}
