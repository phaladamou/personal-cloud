package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// cmdLogin implements "pc login": it exchanges a device name for a bearer
// token with the server and saves the token for future calls.
//
// Usage: pc login [device] [--device NAME] [--user ID]
func cmdLogin(c *Client, args []string, stdout io.Writer, getenv func(string) string) error {
	device := ""
	user := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--device" && i+1 < len(args):
			i++
			device = args[i]
		case strings.HasPrefix(a, "--device="):
			device = strings.TrimPrefix(a, "--device=")
		case a == "--user" && i+1 < len(args):
			i++
			user = args[i]
		case strings.HasPrefix(a, "--user="):
			user = strings.TrimPrefix(a, "--user=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("login: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) > 1 {
		return fmt.Errorf("login: unexpected argument %q", rest[1])
	}
	if len(rest) == 1 {
		if device != "" && device != rest[0] {
			return fmt.Errorf("login: device given twice (%q and %q)", device, rest[0])
		}
		device = rest[0]
	}
	if device == "" {
		device = defaultDevice()
	}
	res, err := c.Login(device, user)
	if err != nil {
		return err
	}
	if err := saveToken(getenv, res.Token); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "logged in as %s on %s (token saved to %s)\n", res.UserID, res.Device, tokenPath(getenv))
	return nil
}

// defaultDevice names the calling device after the local hostname,
// falling back to "cli" when the hostname is unavailable.
func defaultDevice() string {
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return "cli"
}
