package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/phaladamou/personal-cloud/internal/wsclient"
)

// cmdExec implements "pc exec <id> -- <cmd> [args...]": it dials the
// exec WebSocket endpoint, copies stdin to the command, and streams
// command output to stdout. A server "error: ..." message fails the
// command.
func cmdExec(c *Client, args, cmd []string, stdout, stderr io.Writer, stdin io.Reader) error {
	if len(args) == 0 {
		return fmt.Errorf("exec: missing workspace id (usage: pc exec <id> -- <cmd> [args...])")
	}
	if len(args) > 1 {
		return fmt.Errorf("exec: unexpected argument %q (did you forget -- ?)", args[1])
	}
	if len(cmd) == 0 {
		return fmt.Errorf("exec: missing command (usage: pc exec <id> -- <cmd> [args...])")
	}
	wsURL, err := execURL(c.Base(), args[0], cmd)
	if err != nil {
		return err
	}
	var headers http.Header
	if c.token != "" {
		headers = http.Header{"Authorization": {"Bearer " + c.token}}
	}
	conn, err := wsclient.Dial(wsURL, headers)
	if err != nil {
		if isAuthFailure(err) {
			return &apiError{Status: http.StatusUnauthorized, Message: "authentication required"}
		}
		return err
	}
	defer conn.Close()

	// Stdin feeds the command; EOF (or a read error) sends a close
	// frame while the read loop below still drains command output.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := stdin.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(wsclient.OpBinary, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				_ = conn.WriteMessage(wsclient.OpClose, nil)
				return
			}
		}
	}()

	failed := false
	closing := false
	for {
		op, msg, err := conn.ReadMessage()
		if err != nil {
			if errors.Is(err, wsclient.ErrClosed) {
				// The server answers our stdin-EOF close
				// immediately, possibly before command
				// output; keep draining until it closes
				// the TCP connection (after the command
				// exits).
				closing = true
				continue
			}
			if closing && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				break
			}
			return fmt.Errorf("exec: read: %w", err)
		}
		switch op {
		case wsclient.OpBinary:
			if _, err := stdout.Write(msg); err != nil {
				return fmt.Errorf("exec: write stdout: %w", err)
			}
		case wsclient.OpText:
			// The API marks a failed command with a final
			// "error: ..." text message.
			if text := string(msg); strings.HasPrefix(text, "error: ") {
				failed = true
				fmt.Fprintln(stderr, text)
			} else if _, err := stdout.Write(msg); err != nil {
				return fmt.Errorf("exec: write stdout: %w", err)
			}
		}
	}
	if failed {
		return fmt.Errorf("exec: command failed")
	}
	return nil
}

// isAuthFailure reports a WebSocket handshake rejected with 401
// Unauthorized, so exec surfaces the same login hint as the REST calls.
func isAuthFailure(err error) bool {
	return err != nil && strings.Contains(err.Error(), "401 Unauthorized")
}

// execURL builds the WebSocket URL for the exec endpoint, mapping the
// API http(s) scheme to ws(s) and encoding the command as repeated
// argv query parameters.
func execURL(base, id string, cmd []string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("exec: parse api url: %w", err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("exec: unsupported api scheme %q", u.Scheme)
	}
	u.Path = "/v1/workspaces/" + id + "/exec"
	q := url.Values{}
	for _, arg := range cmd {
		q.Add("argv", arg)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
