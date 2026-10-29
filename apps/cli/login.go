package main

import (
	"fmt"
	"io"
	"strings"
)

// cmdLogin implements "pc login": a Phase 1 stub that stores a token
// file for future authenticated calls. The token comes from --token or
// a positional argument, defaulting to a dev stub value.
func cmdLogin(args []string, stdout io.Writer, getenv func(string) string) error {
	token := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--token" && i+1 < len(args):
			i++
			token = args[i]
		case strings.HasPrefix(a, "--token="):
			token = strings.TrimPrefix(a, "--token=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("login: unknown flag %q", a)
		default:
			rest = append(rest, a)
		}
	}
	if token == "" && len(rest) > 0 {
		token = rest[0]
		rest = rest[1:]
	}
	if len(rest) > 0 {
		return fmt.Errorf("login: unexpected argument %q", rest[0])
	}
	if token == "" {
		token = defaultToken
	}
	if err := saveToken(getenv, token); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "logged in (token saved to %s)\n", tokenPath(getenv))
	return nil
}
